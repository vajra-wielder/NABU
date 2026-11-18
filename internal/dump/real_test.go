package dump

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealDumps runs against genuine Wikimedia dumps when NABU_REAL_DUMPS
// names a folder holding some (e.g. simplewiki, abwiki, yiwiki).
func TestRealDumps(t *testing.T) {
	dir := os.Getenv("NABU_REAL_DUMPS")
	if dir == "" {
		t.Skip("set NABU_REAL_DUMPS to a folder with real multistream dumps")
	}
	eds, notes := Scan(dir)
	if len(notes) > 0 {
		t.Log(notes)
	}
	for _, ed := range eds {
		out := filepath.Join(t.TempDir(), ed.Key()+".nidx")
		start := time.Now()
		if err := Build(ed, out, t.TempDir(), 0, nil); err != nil {
			t.Fatalf("%s: build: %v", ed.Key(), err)
		}
		st, _ := os.Stat(out)
		ix, err := OpenIndex(out, ed)
		if err != nil {
			t.Fatal(err)
		}
		r, err := NewReader(ed, ix)
		if err != nil {
			t.Fatal(err)
		}
		site := r.Site()
		t.Logf("%s: %d records, %d articles, %d streams, index %d KB, built in %v; site %s lang=%s rtl=%v main=%q",
			ed.Key(), ix.Meta.Records, ix.Meta.Articles, len(ix.streams), st.Size()/1024, time.Since(start).Round(time.Millisecond),
			site.Sitename, site.Lang, site.RTL(), site.MainPage())
		t0 := time.Now()
		p, _, err := r.Lookup(site.MainPage())
		if err != nil {
			t.Fatalf("main page: %v", err)
		}
		t.Logf("   main page %q (%d bytes wikitext, redirect=%q) in %v", p.Title, len(p.Text), p.Redirect, time.Since(t0))
		res, _ := ix.Prefix("ba", 5, func(ns int) bool { return ns == 0 })
		for _, e := range res {
			t.Logf("   prefix: %s", e.Title)
		}
		r.Close()
	}
}
