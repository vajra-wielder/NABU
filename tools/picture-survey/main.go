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
		fmt.Fprintf(os.Stderr, "%s: downloading sample pictures\n", wiki)
		r.LeadBytes, r.LeadN = sampleBytes(ctx, f, site.Host(), leads, *npics, rng)
		r.OtherBytes, r.OtherN = sampleBytes(ctx, f, site.Host(), others, *npics, rng)
		fmt.Fprintf(os.Stderr, "%s: %d articles sampled, %d with pictures, %d pictures shown (%d distinct, %d flags/icons/logos); lead %.0f B (n=%d), other %.0f B (n=%d)\n",
			wiki, r.Sampled, r.WithPic, r.Refs, r.Distinct, r.Shared, r.LeadBytes, r.LeadN, r.OtherBytes, r.OtherN)
		results = append(results, r)
	}
	out, _ := json.MarshalIndent(results, "", "  ")
	os.WriteFile("picture-survey.json", out, 0o644)
	report(results)
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
	fmt.Println("| Wikipedia | Articles | Sampled | With a picture | Pictures per article | Main picture | Other pictures |")
	fmt.Println("|---|---:|---:|---:|---:|---:|---:|")
	for _, r := range rs {
		if r.Sampled == 0 {
			continue
		}
		fmt.Printf("| %s | %d | %d | %.0f%% | %.2f | %.1f KB | %.1f KB |\n", r.Wiki, r.Articles, r.Sampled,
			100*float64(r.WithPic)/float64(r.Sampled), float64(r.Refs)/float64(r.Sampled), r.LeadBytes/1000, r.OtherBytes/1000)
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
