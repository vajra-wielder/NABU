// Command picture-survey estimates how much room a Wikipedia's pictures
// take, by sampling its real dump: random stretches of the compressed
// file are fetched (HTTP ranges, no full download), each article in them
// is rendered as NABU renders it to list the pictures it shows, and a
// sample of those pictures is downloaded at NABU's size (250 px) through
// NABU's own fetcher, within Wikimedia's limits.
//
//	go run ./tools/picture-survey [-samples 12] [-pics 30] [wiki ...]
//
// It prints a Markdown table and the Go lines for
// internal/pictures/estimate.go. Results: docs/PICTURES-SURVEY.md.
package main

import (
	"bytes"
	"compress/bzip2"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/update"
)

const ua = "NABU-picture-survey/1.0 (https://github.com/vajra-wielder/nabu)"

var client = &http.Client{Timeout: 90 * time.Second}

var outFile = flag.String("json", "", "where to write the results (default picture-survey.json)")

func rangeGet(url string, from, n int64) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, from+n-1))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 206 && resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, n))
}

var magic = []byte("BZh91AY&SY")

// pagesAt fetches a window of the dump at off and decodes the pages of
// the complete streams in it.
func pagesAt(url string, off, window int64) ([]*dump.Page, error) {
	b, err := rangeGet(url, off, window)
	if err != nil {
		return nil, err
	}
	i := bytes.Index(b, magic)
	if i < 0 {
		return nil, nil
	}
	var out bytes.Buffer
	io.Copy(&out, bzip2.NewReader(bytes.NewReader(b[i:]))) // stops with an error at the cut
	return dump.ParsePages(out.Bytes()), nil
}

type result struct {
	Wiki       string  `json:"wiki"`
	Articles   int64   `json:"articles"`
	Sampled    int     `json:"sampled"`
	WithPic    int     `json:"with_picture"`
	Refs       int     `json:"refs"`
	Distinct   int     `json:"distinct"`
	Shared     int     `json:"shared_kind"` // flags, coats of arms, icons, logos: used by many articles
	LeadBytes  float64 `json:"lead_bytes"`
	OtherBytes float64 `json:"other_bytes"`
	LeadN      int     `json:"lead_n"`
	OtherN     int     `json:"other_n"`
	Resolved   int     `json:"resolved"`   // sample pictures looked up
	Local      int     `json:"local"`      // of those, the wiki's own files (not on Commons)
	CheckedEn  int     `json:"checked_en"` // shared ones checked against English
	AlsoEn     int     `json:"also_en"`    // of those, also used by English articles

	leads, others, distinct []string
	host                    string
}

func sharedKind(n string) bool {
	l := strings.ToLower(n)
	for _, k := range []string{"flag_of", "flag-of", "coat_of_arms", "coat-of-arms", "icon", "logo", "symbol", "emblem", "blason", "escudo", "bandera", "drapeau", "флаг", "герб", "wappen"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return strings.HasSuffix(l, ".svg") && strings.Contains(l, "map")
}

func main() {
	samples := flag.Int("samples", 12, "random stretches of each dump")
	window := flag.Int64("window", 700_000, "bytes per stretch")
	npics := flag.Int("pics", 30, "pictures of each kind to download per wiki")
	skipOverlap := flag.Bool("skip-overlap", false, "do not look up own files and overlap with English")
	flag.Parse()
	wikis := flag.Args()
	if len(wikis) == 0 {
		for _, l := range update.Languages {
			wikis = append(wikis, l.Wiki)
		}
	}
	update.UserAgent = ua
	f := pictures.NewFetcher(client, ua)
	rng := rand.New(rand.NewSource(20261118))
	var results []result
	for _, wiki := range wikis {
		ctx := context.Background()
		rel, err := update.Latest(ctx, client, update.DefaultMirror, wiki)
		if err != nil {
			fmt.Fprintln(os.Stderr, wiki, err)
			continue
		}
		var data []update.RemoteFile
		var total int64
		for _, rf := range rel.Files {
			if !strings.Contains(rf.Name, "-index") {
				data = append(data, rf)
				total += rf.Size
			}
		}
		fmt.Fprintf(os.Stderr, "%s: dump of %s, %d MB\n", wiki, rel.Date, total>>20)
		head, err := rangeGet(data[0].URL, 0, 1<<20)
		if err != nil {
			fmt.Fprintln(os.Stderr, wiki, err)
			continue
		}
		site, err := dump.SiteinfoFromDump(bytes.NewReader(head), 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, wiki, "siteinfo:", err)
			continue
		}
		r := result{Wiki: wiki, Articles: articles(site.Host())}
		fmt.Fprintf(os.Stderr, "%s: %d articles\n", wiki, r.Articles)
		var leads, others []string
		seen := map[string]bool{}
		for k := 0; k < *samples; k++ {
			pos := rng.Int63n(total - *window)
			var rf update.RemoteFile
			for _, x := range data {
				if pos < x.Size-*window {
					rf = x
					break
				}
				pos -= x.Size
			}
			if rf.URL == "" {
				continue
			}
			t0 := time.Now()
			pages, err := pagesAt(rf.URL, pos, *window)
			fmt.Fprintf(os.Stderr, "%s: stretch %d: %d pages in %v\n", wiki, k+1, len(pages), time.Since(t0).Round(time.Millisecond))
			if err != nil {
				fmt.Fprintln(os.Stderr, wiki, err)
				continue
			}
			for _, p := range pages {
				if p.NS != dump.NSMain || p.Redirect != "" {
					continue
				}
				r.Sampled++
				names := pictures.PageNames(p.Title, p.Text, site, false)
				if len(names) > 0 {
					r.WithPic++
					leads = append(leads, names[0])
				}
				for i, n := range names {
					r.Refs++
					if sharedKind(n) {
						r.Shared++
					}
					if !seen[n] {
						seen[n] = true
						r.Distinct++
						if i > 0 {
							others = append(others, n)
						}
					}
				}
			}
		}
		r.leads, r.others, r.host = leads, others, site.Host()
		for n := range seen {
			r.distinct = append(r.distinct, n)
		}
		sort.Strings(r.distinct)
		rng.Shuffle(len(r.distinct), func(i, j int) { r.distinct[i], r.distinct[j] = r.distinct[j], r.distinct[i] })
		// Shared or local; and, for other languages, also used by English?
		check := r.distinct
		if *skipOverlap {
			check = nil
		}
		if len(check) > 300 {
			check = check[:300]
		}
		infos, _ := f.Resolve(ctx, r.host, check)
		var shared []string
		for _, n := range check {
			if info, ok := infos[pictures.Normalize(n)]; ok && !info.Missing {
				r.Resolved++
				if info.Shared {
					shared = append(shared, n)
				} else {
					r.Local++
				}
			}
		}
		if wiki != "enwiki" && !*skipOverlap {
			r.CheckedEn, r.AlsoEn = usedOnEnglish(shared)
		}
		fmt.Fprintf(os.Stderr, "%s: %d articles sampled, %d with pictures, %d pictures shown (%d distinct, %d flags/icons/logos); %d of %d looked up are its own files; %d of %d shared ones also used by English\n",
			wiki, r.Sampled, r.WithPic, r.Refs, r.Distinct, r.Shared, r.Local, r.Resolved, r.AlsoEn, r.CheckedEn)
		results = append(results, r)
		save(results)
	}
	for i := range results {
		r := &results[i]
		r.LeadBytes, r.LeadN = sampleBytes(context.Background(), f, r.host, r.leads, *npics, rng)
		r.OtherBytes, r.OtherN = sampleBytes(context.Background(), f, r.host, r.others, *npics, rng)
		fmt.Fprintf(os.Stderr, "%s: sizes: lead %.0f B (n=%d), other %.0f B (n=%d)\n", r.Wiki, r.LeadBytes, r.LeadN, r.OtherBytes, r.OtherN)
		save(results)
	}
	report(results)
}

func save(rs []result) {
	out, _ := json.MarshalIndent(rs, "", "  ")
	name := "picture-survey.json"
	if *outFile != "" {
		name = *outFile
	}
	os.WriteFile(name, out, 0o644)
}

// usedOnEnglish checks which Commons pictures English Wikipedia articles
// use too (its imagelinks include Commons files).
func usedOnEnglish(names []string) (checked, used int) {
	for len(names) > 0 {
		n := len(names)
		if n > 50 {
			n = 50
		}
		batch := names[:n]
		names = names[n:]
		titles := make([]string, len(batch))
		for i, x := range batch {
			titles[i] = "File:" + pictures.Normalize(x)
		}
		seen := map[string]bool{}
		cont := ""
		for round := 0; round < 40; round++ {
			q := url.Values{"action": {"query"}, "format": {"json"}, "formatversion": {"2"}, "prop": {"fileusage"},
				"funamespace": {"0"}, "fulimit": {"max"}, "fuprop": {"pageid"}, "titles": {strings.Join(titles, "|")}}
			if cont != "" {
				q.Set("fucontinue", cont)
				q.Set("continue", "||")
			}
			var v struct {
				Continue struct {
					Fucontinue string `json:"fucontinue"`
				} `json:"continue"`
				Query struct {
					Normalized []struct{ From, To string } `json:"normalized"`
					Pages      []struct {
						Title     string            `json:"title"`
						FileUsage []json.RawMessage `json:"fileusage"`
					} `json:"pages"`
				} `json:"query"`
			}
			if err := apiGet("https://en.wikipedia.org/w/api.php?"+q.Encode(), &v); err != nil {
				fmt.Fprintln(os.Stderr, "english usage:", err)
				return checked, used
			}
			for _, p := range v.Query.Pages {
				if len(p.FileUsage) > 0 {
					seen[pictures.Normalize(strings.TrimPrefix(p.Title, "File:"))] = true
				}
			}
			if cont = v.Continue.Fucontinue; cont == "" {
				break
			}
		}
		checked += len(batch)
		used += len(seen)
	}
	return checked, used
}

var apiLast time.Time

// apiGet: one request at a time, at most one every 0.5 s, waiting out 429s.
func apiGet(u string, v interface{}) error {
	for try := 0; try < 8; try++ {
		if d := time.Until(apiLast.Add(500 * time.Millisecond)); d > 0 {
			time.Sleep(d)
		}
		apiLast = time.Now()
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", ua)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == 429 || resp.StatusCode == 503 {
			resp.Body.Close()
			time.Sleep(time.Duration(10*(try+1)) * time.Second)
			continue
		}
		err = json.NewDecoder(resp.Body).Decode(v)
		resp.Body.Close()
		return err
	}
	return fmt.Errorf("Wikimedia kept asking to slow down")
}

// articles asks the wiki how many articles it has.
func articles(host string) int64 {
	req, _ := http.NewRequest("GET", "https://"+host+"/w/api.php?action=query&meta=siteinfo&siprop=statistics&format=json&formatversion=2", nil)
	req.Header.Set("User-Agent", ua)
	for i := 0; i < 4; i++ {
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == 200 {
			var v struct {
				Query struct{ Statistics struct{ Articles int64 } }
			}
			json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			return v.Query.Statistics.Articles
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(time.Duration(10*(i+1)) * time.Second)
	}
	return 0
}

func sampleBytes(ctx context.Context, f *pictures.Fetcher, host string, names []string, n int, rng *rand.Rand) (float64, int) {
	rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	if len(names) > n {
		names = names[:n]
	}
	infos, _ := f.Resolve(ctx, host, names)
	var sum, got int
	for _, name := range names {
		info, ok := infos[pictures.Normalize(name)]
		if !ok || info.Missing || info.Skip {
			continue
		}
		for try := 0; try < 3; try++ {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			b, err := f.Download(cctx, info.URL)
			cancel()
			if err == nil {
				sum += len(b)
				got++
				break
			}
			if err != pictures.ErrBusy {
				break
			}
		}
	}
	if got == 0 {
		return 0, 0
	}
	return float64(sum) / float64(got), got
}

func report(rs []result) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Articles > rs[j].Articles })
	fmt.Println("| Wikipedia | Articles | Sampled | With a picture | Pictures per article | Own files | Also used by English | Main picture | Other pictures |")
	fmt.Println("|---|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, r := range rs {
		if r.Sampled == 0 {
			continue
		}
		fmt.Printf("| %s | %d | %d | %.0f%% | %.2f | %.0f%% | %.0f%% | %.1f KB | %.1f KB |\n", r.Wiki, r.Articles, r.Sampled,
			100*float64(r.WithPic)/float64(r.Sampled), float64(r.Refs)/float64(r.Sampled),
			100*float64(r.Local)/float64(max(r.Resolved, 1)), 100*float64(r.AlsoEn)/float64(max(r.CheckedEn, 1)),
			r.LeadBytes/1000, r.OtherBytes/1000)
	}
	fmt.Println()
	for _, r := range rs {
		if r.Sampled == 0 {
			continue
		}
		shared := float64(r.Shared) / float64(max(r.Refs, 1))
		fmt.Printf("\t%q: {LeadShare: %.3f, PerArticle: %.3f, LeadBytes: %.0f, OtherBytes: %.0f, SharedShare: %.3f},\n", r.Wiki,
			float64(r.WithPic)/float64(r.Sampled), float64(r.Refs)/float64(r.Sampled), r.LeadBytes, r.OtherBytes, shared)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
