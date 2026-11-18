// Package update discovers newer Wikipedia dumps and downloads them.
//
// Wikimedia publishes raw dumps (wikitext, no images, no rendering) at
// dumps.wikimedia.org. The Kiwix project turns each one into a ZIM file
// with the pages fully rendered, pictures included, and publishes them at
// download.kiwix.org. NABU reads those, so that is the catalogue it asks.
package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultCatalogURL is the Kiwix directory of Wikipedia ZIM files.
const DefaultCatalogURL = "https://download.kiwix.org/zim/wikipedia/"

// Edition is one downloadable dump.
type Edition struct {
	File      string `json:"file"`      // wikipedia_en_all_maxi_2026-10.zim
	URL       string `json:"url"`       // absolute download URL
	Lang      string `json:"lang"`      // en, fr, zh-classical, ...
	Selection string `json:"selection"` // all, top, 100, simple_all, medicine, ...
	Flavour   string `json:"flavour"`   // maxi, nopic, mini (or "")
	Date      string `json:"date"`      // 2026-10
	Size      int64  `json:"size"`      // approximate, from the listing
}

// Series is the edition's family name without the date, matching the
// Name_Flavour metadata of an installed book.
func (e Edition) Series() string {
	s := "wikipedia_" + e.Lang + "_" + e.Selection
	if e.Flavour != "" {
		s += "_" + e.Flavour
	}
	return s
}

// Describe is a plain-language summary of what the edition contains.
func (e Edition) Describe() string {
	var what string
	switch e.Selection {
	case "all":
		what = "Complete Wikipedia"
	case "simple_all":
		what = "Simple English Wikipedia"
	case "top":
		what = "Most-read articles"
	case "100":
		what = "Top 100 articles (sampler)"
	default:
		what = "Topic: " + strings.ReplaceAll(e.Selection, "_", " ")
	}
	switch e.Flavour {
	case "maxi":
		return what + ", full articles with pictures"
	case "nopic":
		return what + ", full articles, no pictures"
	case "mini":
		return what + ", introductions only"
	}
	return what
}

var (
	zimHrefRe = regexp.MustCompile(`href="(wikipedia_[^"/]+\.zim)"`)
	nameRe    = regexp.MustCompile(`^wikipedia_([a-z][a-z0-9-]*)_(.+)_(\d{4}-\d{2})\.zim$`)
	sizeRe    = regexp.MustCompile(`(?:^|[\s>])(\d+(?:\.\d+)?)([KMGT])(?:[\s<]|$)`)
)

// ParseName splits a Kiwix Wikipedia file name into its parts.
func ParseName(file string) (Edition, bool) {
	m := nameRe.FindStringSubmatch(file)
	if m == nil {
		return Edition{}, false
	}
	e := Edition{File: file, Lang: m[1], Selection: m[2], Date: m[3]}
	for _, f := range []string{"maxi", "nopic", "mini"} {
		if strings.HasSuffix(e.Selection, "_"+f) {
			e.Flavour = f
			e.Selection = strings.TrimSuffix(e.Selection, "_"+f)
		}
	}
	return e, true
}

// ParseListing extracts editions from an Apache/nginx style directory
// index page. Sizes such as "102G" are read from the text that follows
// each link on the same row.
func ParseListing(page, base string) []Edition {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var out []Edition
	seen := map[string]bool{}
	locs := zimHrefRe.FindAllStringSubmatchIndex(page, -1)
	for i, loc := range locs {
		file := page[loc[2]:loc[3]]
		if seen[file] {
			continue
		}
		seen[file] = true
		e, ok := ParseName(file)
		if !ok {
			continue
		}
		ref, _ := url.Parse(url.PathEscape(file))
		e.URL = baseURL.ResolveReference(ref).String()
		rowEnd := len(page)
		if i+1 < len(locs) {
			rowEnd = locs[i+1][0]
		}
		row := page[loc[1]:rowEnd]
		if nl := strings.IndexAny(row, "\n"); nl >= 0 {
			if tr := strings.Index(row, "</tr>"); tr < 0 || nl < tr {
				row = row[:nl]
			}
		}
		if m := sizeRe.FindAllStringSubmatch(row, -1); m != nil {
			last := m[len(m)-1]
			e.Size = parseSize(last[1], last[2])
		}
		out = append(out, e)
	}
	return out
}

func parseSize(num, unit string) int64 {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	mult := map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[unit]
	return int64(f * mult)
}

// Latest keeps only the newest edition of every series.
func Latest(eds []Edition) []Edition {
	best := map[string]Edition{}
	for _, e := range eds {
		if b, ok := best[e.Series()]; !ok || e.Date > b.Date {
			best[e.Series()] = e
		}
	}
	out := make([]Edition, 0, len(best))
	for _, e := range best {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		return rank(out[i]) < rank(out[j])
	})
	return out
}

// rank orders a language's editions from most to least useful.
func rank(e Edition) string {
	sel := map[string]string{"all": "0", "simple_all": "1", "top": "2", "100": "9"}[e.Selection]
	if sel == "" {
		sel = "5" + e.Selection
	}
	fl := map[string]string{"maxi": "0", "nopic": "1", "mini": "2"}[e.Flavour]
	return sel + fl
}

// FetchCatalog downloads and parses the directory listing at base.
func FetchCatalog(ctx context.Context, client *http.Client, base string) ([]Edition, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", base, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalogue returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	eds := ParseListing(string(body), base)
	if len(eds) == 0 {
		return nil, fmt.Errorf("no Wikipedia editions found at %s", base)
	}
	return eds, nil
}

// UserAgent identifies NABU to download servers.
var UserAgent = "NABU-offline-wikipedia (+https://github.com/vajra-wielder/nabu)"
