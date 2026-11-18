// Package update asks Wikimedia for the newest dump of a wiki and
// downloads it.
//
// Every wiki's latest "pages-articles-multistream" dump is announced in a
// small RSS file, and each dump run has a dumpstatus.json listing its
// files with sizes and SHA-1 checksums. Both live on dumps.wikimedia.org
// (or a mirror with the same layout).
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// DefaultMirror is Wikimedia's own dump server.
const DefaultMirror = "https://dumps.wikimedia.org"

// RemoteFile is one file of a dump.
type RemoteFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
	SHA1 string `json:"sha1"`
}

// Release is a complete multistream dump: the XML and its index.
type Release struct {
	Wiki  string       `json:"wiki"` // dbname, e.g. "enwiki"
	Date  string       `json:"date"` // e.g. "20261101"
	Files []RemoteFile `json:"files"`
}

// Size is the total download size.
func (r Release) Size() int64 {
	var n int64
	for _, f := range r.Files {
		n += f.Size
	}
	return n
}

// Key names the release, e.g. "enwiki-20261101".
func (r Release) Key() string { return r.Wiki + "-" + r.Date }

// UserAgent identifies NABU to Wikimedia, as their policy asks.
var UserAgent = "NABU-offline-wikipedia/1 (https://github.com/vajra-wielder/nabu)"

var (
	rssDateRe = regexp.MustCompile(`/` + `[a-z0-9_]+/(\d{8})`)
	wikiRe    = regexp.MustCompile(`^[a-z0-9_]{2,40}$`)
)

// Latest asks the mirror for the newest finished multistream dump of
// wiki (a dbname such as "enwiki" or "zh_yuewiki").
func Latest(ctx context.Context, client *http.Client, mirror, wiki string) (*Release, error) {
	if !wikiRe.MatchString(wiki) {
		return nil, fmt.Errorf("%q is not a wiki name", wiki)
	}
	mirror = strings.TrimRight(mirror, "/")
	rss, err := get(ctx, client, fmt.Sprintf("%s/%s/latest/%s-latest-pages-articles-multistream-index.txt.bz2-rss.xml", mirror, wiki, wiki), 1<<20)
	if err != nil {
		return nil, err
	}
	m := rssDateRe.FindAllStringSubmatch(string(rss), -1)
	if m == nil {
		return nil, errors.New("the dump server did not say which dump is newest")
	}
	date := m[len(m)-1][1]
	status, err := get(ctx, client, fmt.Sprintf("%s/%s/%s/dumpstatus.json", mirror, wiki, date), 8<<20)
	if err != nil {
		return nil, err
	}
	rel, err := parseStatus(status, wiki, date, mirror)
	if err != nil {
		return nil, err
	}
	return rel, nil
}

type dumpStatus struct {
	Jobs map[string]struct {
		Status string `json:"status"`
		Files  map[string]struct {
			Size int64  `json:"size"`
			SHA1 string `json:"sha1"`
			URL  string `json:"url"`
		} `json:"files"`
	} `json:"jobs"`
}

// parseStatus picks the combined multistream file and its index from a
// dumpstatus.json. (Big wikis also offer the same data in numbered
// parts; one file is simpler to fetch and NABU splits it for FAT32.)
func parseStatus(b []byte, wiki, date, mirror string) (*Release, error) {
	var ds dumpStatus
	if err := json.Unmarshal(b, &ds); err != nil {
		return nil, fmt.Errorf("unreadable dumpstatus.json: %w", err)
	}
	data := wiki + "-" + date + "-pages-articles-multistream.xml.bz2"
	index := wiki + "-" + date + "-pages-articles-multistream-index.txt.bz2"
	rel := &Release{Wiki: wiki, Date: date}
	for _, want := range []string{index, data} {
		found := false
		for _, job := range []string{"articlesmultistreamdumprecombine", "articlesmultistreamdump"} {
			j, ok := ds.Jobs[job]
			if !ok || j.Status != "done" {
				continue
			}
			if f, ok := j.Files[want]; ok && f.Size > 0 {
				u := f.URL
				if strings.HasPrefix(u, "/") {
					u = mirror + u
				}
				rel.Files = append(rel.Files, RemoteFile{Name: want, URL: u, Size: f.Size, SHA1: f.SHA1})
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("the %s dump of %s is not finished yet", date, wiki)
		}
	}
	return rel, nil
}

func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("this wiki has no dump on the server")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dump server answered %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Language is one of the Wikipedias NABU offers to download.
type Language struct {
	Code   string // Wikipedia language code
	Wiki   string // dbname
	Name   string // in its own language
	En     string // in English
	Covers string // where it is the (co-)official language, by land area
}

// Languages is the smallest set of languages whose countries cover the
// most land: the greedy set cover in docs/language_coverage.py. Ten
// languages reach about 91% of the inhabited land area of the world.
var Languages = []Language{
	{"en", "enwiki", "English", "English", "Canada, USA, Australia, India, Nigeria, South Africa and 50+ more"},
	{"ru", "ruwiki", "Русский", "Russian", "Russia, Kazakhstan, Belarus, Kyrgyzstan, Tajikistan"},
	{"ar", "arwiki", "العربية", "Arabic", "Algeria, Saudi Arabia, Sudan, Libya, Egypt and the Arab world"},
	{"es", "eswiki", "Español", "Spanish", "Argentina, Mexico, Peru, Colombia, Bolivia, Spain and more"},
	{"pt", "ptwiki", "Português", "Portuguese", "Brazil, Angola, Mozambique, Portugal"},
	{"zh", "zhwiki", "中文", "Chinese", "China, Taiwan"},
	{"fr", "frwiki", "Français", "French", "DR Congo, Niger, Mali, Chad, France and Francophone Africa"},
	{"fa", "fawiki", "فارسی", "Persian", "Iran, Afghanistan"},
	{"da", "dawiki", "Dansk", "Danish", "Greenland, Denmark"},
	{"id", "idwiki", "Bahasa Indonesia", "Indonesian", "Indonesia"},
}

// LanguageByWiki finds a language by dbname.
func LanguageByWiki(wiki string) (Language, bool) {
	for _, l := range Languages {
		if l.Wiki == wiki {
			return l, true
		}
	}
	return Language{}, false
}
