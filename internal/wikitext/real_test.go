package wikitext

import (
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// TestRealArticles renders many genuine articles when NABU_REAL_DUMPS
// names a folder of real dumps, checking for crashes, slowness and
// leftover markup. NABU_RENDER_OUT, if set, receives sample pages.
func TestRealArticles(t *testing.T) {
	dir := os.Getenv("NABU_REAL_DUMPS")
	if dir == "" {
		t.Skip("set NABU_REAL_DUMPS")
	}
	n := 1500
	eds, _ := dump.Scan(dir)
	for _, ed := range eds {
		out := filepath.Join(t.TempDir(), "x.nidx")
		if err := dump.Build(ed, out, t.TempDir(), 0, nil); err != nil {
			t.Fatal(err)
		}
		ix, _ := dump.OpenIndex(out, ed)
		rd, _ := dump.NewReader(ed, ix)
		site := rd.Site()
		ctx := func(title string) *Context {
			return &Context{Title: title, Site: site,
				PageURL:  func(t string) string { return "/w/" + url.PathEscape(t) },
				ImageURL: func(f string, w int) string { return "https://img/" + f }}
		}
		rng := rand.New(rand.NewSource(1))
		var times []time.Duration
		leftovers := 0
		var worst string
		for i := 0; i < n; i++ {
			e, err := ix.Random(rng, 0)
			if err != nil {
				continue
			}
			p, err := rd.Load(e)
			if err != nil || p.Redirect != "" {
				continue
			}
			start := time.Now()
			var o *Output
			func() {
				defer func() {
					if x := recover(); x != nil {
						t.Errorf("%s: panic rendering %q: %v", ed.Key(), p.Title, x)
					}
				}()
				o = Render(p.Text, ctx(p.Title))
			}()
			d := time.Since(start)
			times = append(times, d)
			if o == nil {
				continue
			}
			plain := stripTags(o.HTML)
			if strings.Contains(plain, "{{") || strings.Contains(plain, "[[") || strings.Contains(o.HTML, "\x01") {
				leftovers++
				if worst == "" {
					worst = p.Title
				}
				if os.Getenv("NABU_SHOW_LEFTOVER") != "" && leftovers <= 12 {
					k := strings.Index(plain, "{{")
					if k < 0 {
						k = strings.Index(plain, "[[")
					}
					if k < 0 {
						k = strings.Index(o.HTML, "\x01")
						t.Logf("   %s: placeholder leak", p.Title)
					} else {
						e := k + 120
						if e > len(plain) {
							e = len(plain)
						}
						t.Logf("   %s: %q", p.Title, plain[k:e])
					}
				}
			}
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		if len(times) == 0 {
			continue
		}
		t.Logf("%s: %d pages; median %v, p99 %v, max %v; pages with leftover markup: %d (e.g. %q)", ed.Key(), len(times),
			times[len(times)/2], times[len(times)*99/100], times[len(times)-1], leftovers, worst)
		if outDir := os.Getenv("NABU_RENDER_OUT"); outDir != "" {
			for _, title := range []string{"Babylon", "Albert Einstein", "France", "Water", site.MainPage()} {
				p, _, err := rd.Lookup(title)
				if err != nil || p.Redirect != "" {
					continue
				}
				o := Render(p.Text, ctx(p.Title))
				os.WriteFile(filepath.Join(outDir, ed.DB+"-"+strings.ReplaceAll(title, " ", "_")+".html"),
					[]byte("<meta charset=utf-8><h1>"+p.Title+"</h1>"+o.HTML), 0o644)
			}
		}
		rd.Close()
	}
}
