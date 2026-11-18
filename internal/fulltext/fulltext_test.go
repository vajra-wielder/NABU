package fulltext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
)

func openDump(t *testing.T, dir, db string) *dump.Reader {
	t.Helper()
	eds, _ := dump.Scan(dir)
	for _, f := range eds {
		if f.DB != db {
			continue
		}
		out := filepath.Join(t.TempDir(), "t.nidx")
		if err := dump.Build(f, out, t.TempDir(), 0, nil); err != nil {
			t.Fatal(err)
		}
		ix, err := dump.OpenIndex(out, f)
		if err != nil {
			t.Fatal(err)
		}
		rd, err := dump.NewReader(f, ix)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rd.Close() })
		return rd
	}
	t.Fatalf("no %s in %s", db, dir)
	return nil
}

func TestTokenize(t *testing.T) {
	var got []string
	Tokenize("Écriture of a King's 2nd CUNEIFORM-tablet, 東京都 a", func(s string) { got = append(got, s) })
	want := "ecriture|of|king|2nd|cuneiform|tablet|東京|京都"
	if strings.Join(got, "|") != want {
		t.Fatalf("got %s", strings.Join(got, "|"))
	}
}

func TestBuildSearchNearby(t *testing.T) {
	rd := openDump(t, "../../testdata", "testwiki")
	out := filepath.Join(t.TempDir(), "t.nfts")
	// A tiny memory budget forces several runs to merge.
	if err := Build(rd, out, t.TempDir(), Options{Workers: 3, MemBudget: 4000}); err != nil {
		t.Fatal(err)
	}
	ix, err := Open(out, rd.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if ix.Meta.Docs < 240 || ix.Meta.Geo != 0 {
		t.Fatalf("meta %+v", ix.Meta)
	}
	res, err := ix.Search("god of scribes", 0, 10)
	if err != nil || res.Total != 2 || (res.Hits[0].Title != "Nabu" && res.Hits[1].Title != "Nabu") {
		t.Fatalf("search: %+v %v", res, err)
	}
	res, _ = ix.Search("EUPHRATES hammurabi", 0, 10)
	if len(res.Hits) != 1 || res.Hits[0].Title != "Babylon" {
		t.Fatalf("multi-term: %+v", res)
	}
	res, _ = ix.Search("ecriture", 0, 10)
	if len(res.Hits) != 1 || res.Hits[0].Title != "Écriture" {
		t.Fatalf("accent-free: %+v", res)
	}
	if res, _ := ix.Search("zzzqqq", 0, 10); len(res.Hits) != 0 {
		t.Fatal("unknown word matched")
	}
	// (Words in more than half of all articles are dropped and reported,
	// but only in wikis of 1,000+ articles; see TestRealFullText.)
	res, _ = ix.Search("tablet", 0, 10)
	page, err := rd.Load(dump.Entry{Stream: res.Hits[0].Stream, ID: res.Hits[0].PageID})
	if err != nil || page.Title != res.Hits[0].Title {
		t.Fatalf("hit does not lead to its page: %v %v", page, err)
	}
	// Paging.
	all, _ := ix.Search("tablet", 0, 100)
	p2, _ := ix.Search("tablet", 5, 5)
	if all.Total < 10 || len(p2.Hits) != 5 || p2.Hits[0].Title != all.Hits[5].Title {
		t.Fatalf("paging: total %d", all.Total)
	}
	if s := Snippet("The Ishtar Gate of Babylon was built", []string{"babylon"}, 80); !strings.Contains(s, "<mark>Babylon</mark>") {
		t.Fatalf("snippet %q", s)
	}
	// A changed dump makes the index stale.
	if _, err := Open(out, &dump.Files{DB: "x", Pairs: []dump.Pair{{Index: "x"}}}); err != ErrStale {
		t.Fatalf("stale not detected: %v", err)
	}
}

// TestRealFullText measures a build on genuine dumps (NABU_REAL_DUMPS).
func TestRealFullText(t *testing.T) {
	dir := os.Getenv("NABU_REAL_DUMPS")
	if dir == "" {
		t.Skip("set NABU_REAL_DUMPS")
	}
	eds, _ := dump.Scan(dir)
	for _, f := range eds {
		rd := openDump(t, dir, f.DB)
		out := filepath.Join(t.TempDir(), "r.nfts")
		start := time.Now()
		if err := Build(rd, out, t.TempDir(), Options{}); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(out)
		ix, err := Open(out, rd.Files)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d articles, %d terms, %d places, %d stopwords; %.1f MB (%.1f%% of the %.0f MB dump) in %v",
			f.DB, ix.Meta.Docs, ix.Meta.Terms, ix.Meta.Geo, len(ix.Meta.Stopwords), float64(st.Size())/1e6,
			100*float64(st.Size())/float64(f.Size), float64(f.Size)/1e6, time.Since(start).Round(time.Second))
		for _, q := range []string{"hanging gardens", "river flowing through baghdad", "nobel prize physics 1921"} {
			t0 := time.Now()
			res, _ := ix.Search(q, 0, 5)
			var titles []string
			for _, h := range res.Hits {
				titles = append(titles, h.Title)
			}
			t.Logf("   %q: %d matches in %v: %v", q, res.Total, time.Since(t0).Round(time.Millisecond), titles)
		}
		if ix.Meta.Geo > 0 {
			near, _ := ix.Nearby(32.54, 44.42, 5, "")
			var titles []string
			for _, p := range near {
				titles = append(titles, p.Title)
			}
			t.Logf("   near Babylon: %v", titles)
		}
		ix.Close()
	}
}
