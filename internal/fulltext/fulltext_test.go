package fulltext

import (
	"fmt"
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

func TestStopwordsAndJunk(t *testing.T) {
	en := strings.Join(Stopwords("simple"), " ")
	for _, w := range []string{"the", "can", "they", "themselves"} {
		if !strings.Contains(" "+en+" ", " "+w+" ") {
			t.Errorf("%q should be a stopword", w)
		}
	}
	// Little words count only in lower case: in capitals they are
	// abbreviations, and a few are names with a capital first letter.
	r := NewRules(Stopwords("en"), CapitalNames("en"))
	for _, c := range []struct {
		word string
		keep bool
	}{
		{"us", false}, {"US", true}, {"Us", false},
		{"who", false}, {"WHO", true}, {"Who", false},
		{"it", false}, {"IT", true}, {"It", false},
		{"may", false}, {"May", true}, {"MAY", true},
		{"will", false}, {"Will", true},
		{"the", false}, {"The", false}, {"THE", true},
		{"Babylon", true}, {"babylon", true},
	} {
		var got bool
		TokenizeCase(c.word, func(t string, k Case) { got = r.Keep(t, k) })
		if got != c.keep {
			t.Errorf("%q indexed: %v", c.word, got)
		}
	}
	for w, want := range map[string]Case{"US": Caps, "U2": Capital, "May": Capital, "iPhone": Lower, "McDonald": Lower, "ÉTÉ": Caps, "a": Lower} {
		if got := caseOf([]rune(w)); got != want {
			t.Errorf("caseOf(%q) = %d, want %d", w, got, want)
		}
	}
	if fr := Stopwords("fr"); len(fr) < 50 || fr[0] != "le" {
		t.Errorf("french %v", fr)
	}
	for _, l := range []string{"en", "fr", "es", "pt", "ru", "ar", "fa", "id", "da"} {
		if len(Stopwords(l)) < 30 {
			t.Errorf("%s: only %d", l, len(Stopwords(l)))
		}
	}
	for term, want := range map[string]bool{"1921": false, "12345": true, "2nd": false, "b52": false, "q12345678": true, "babylon": false} {
		if junk(term) != want {
			t.Errorf("junk(%q) = %v", term, !want)
		}
	}
}

// Both formats: the current one, and format 2, which NABU still reads
// (indexes of English take hours to build).
func TestBuildSearchNearby(t *testing.T) {
	for _, v := range []int{FormatVersion, 2} {
		t.Run(fmt.Sprintf("format %d", v), func(t *testing.T) {
			buildVersion = v
			defer func() { buildVersion = FormatVersion }()
			checkBuildSearch(t, v)
		})
	}
}

func checkBuildSearch(t *testing.T, version int) {
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
	if ix.Meta.Version != version || (version >= 3) != (ix.lens != nil) {
		t.Fatalf("format %d, lengths %v", ix.Meta.Version, ix.lens != nil)
	}
	if ix.Meta.Docs < 240 || ix.Meta.Geo != 2 {
		t.Fatalf("meta %+v", ix.Meta)
	}
	// Places nearby: Babylon's own point, then Borsippa (Nabu) ~17 km away.
	near, err := ix.Nearby(32.536, 44.420, 5, "")
	if err != nil || len(near) != 2 || near[0].Title != "Babylon" || near[1].Title != "Nabu" || near[1].Km < 10 || near[1].Km > 25 {
		t.Fatalf("nearby: %+v %v", near, err)
	}
	if near, _ := ix.Nearby(32.536, 44.420, 5, "Babylon"); len(near) != 1 || near[0].Title != "Nabu" {
		t.Fatalf("nearby should skip the article itself: %+v", near)
	}
	res, err := ix.Search("god of scribes", 0, 10)
	if err != nil || res.Total != 2 || (res.Hits[0].Title != "Nabu" && res.Hits[1].Title != "Nabu") {
		t.Fatalf("search: %+v %v", res, err)
	}
	// Little words are not indexed; searching them is not an error.
	res, _ = ix.Search("The god of the scribes", 0, 10)
	if res.Total != 2 || strings.Join(res.Ignored, ",") != "the,of" {
		t.Fatalf("stopwords: %+v", res)
	}
	if res, _ = ix.Search("god of scribes us", 0, 10); strings.Join(res.Ignored, ",") != "of,us" {
		t.Fatalf("lower-case us: %+v", res)
	}
	if res, _ = ix.Search("god of scribes US", 0, 10); strings.Join(res.Ignored, ",") != "of" || res.Total != 0 {
		t.Fatalf("US in capitals is searched (and is in no article here): %+v", res)
	}
	if ix.Meta.Lang != "en" || ix.Meta.Postings == 0 {
		t.Fatalf("meta %+v", ix.Meta)
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
	// Every article is found by its stream and page; the one titled as
	// searched comes first.
	for n := uint32(0); n < uint32(ix.Meta.Docs); n += 7 {
		d, _ := ix.Doc(n)
		if got, ok := ix.DocOf(d.Stream, d.PageID); !ok || got != n {
			t.Fatalf("DocOf %d (%s): %d %v", n, d.Title, got, ok)
		}
	}
	if _, ok := ix.DocOf(1<<30, 1); ok {
		t.Fatal("DocOf found what is not there")
	}
	first := ix.TitleDoc(rd, "nabu")
	if len(first) != 1 || first[0].Title != "Nabu" {
		t.Fatalf("title doc: %+v", first)
	}
	if res, _ := ix.Search("god", 0, 3, first...); res.Hits[0].Title != "Nabu" {
		t.Fatalf("title match not first: %s", res.Hits[0].Title)
	}
	if len(ix.TitleDoc(rd, "no such article at all")) != 0 {
		t.Fatal("title doc for nothing")
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
		t.Logf("   sections %v; %d postings", ix.Sections(), ix.Meta.Postings)
		t.Logf("   dropped for being in over 40%% of articles: %v", ix.Meta.Common)
		for _, q := range []string{"US army", "us army", "WHO pandemic", "May 1968", "IT company", "hanging gardens", "river flowing through baghdad", "nobel prize physics 1921"} {
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

// Snippets come from the paragraph the words are in, not from templates,
// references, comments or tables around them; otherwise from the whole
// article.
func TestWikiSnippet(t *testing.T) {
	filler := strings.Repeat("Some running text about other things entirely. ", 150)
	src := "{{Infobox river|name=Danube boiling}}\n'''Rivers''' flow.\n\n" + filler +
		"\n\n<!-- boiling note --> A ref<ref>boiling in a ref</ref> here.\n\n" +
		"{| class=wikitable\n| boiling cell\n|}\n\n" + filler +
		"\n\nWater starts '''boiling''' at a hundred degrees.\n\n" + filler
	plainCalls := 0
	plain := func(s string) string {
		plainCalls++
		s = strings.NewReplacer("'''", "", "<!-- boiling note -->", "", "<ref>boiling in a ref</ref>", "").Replace(s)
		for {
			i := strings.Index(s, "{")
			j := strings.LastIndex(s, "}")
			if i < 0 || j < i {
				return s
			}
			s = s[:i] + s[j+1:]
		}
	}
	got := WikiSnippet(src, []string{"boiling"}, 80, plain)
	if !strings.Contains(got, "<mark>boiling</mark> at a hundred") {
		t.Fatalf("snippet: %s", got)
	}
	if plainCalls != 1 {
		t.Fatalf("turned %d pieces into text; want only the paragraph", plainCalls)
	}
	if len(paragraphsWith(src, []string{"boiling"}, 4)) != 1 {
		t.Fatal("found the word in markup")
	}
	// Words written with accents or not found in the wikitext: the whole
	// article, as before.
	plainCalls = 0
	if got := WikiSnippet(src, []string{"école"}, 80, plain); plainCalls != 1 || strings.Contains(got, "<mark>") {
		t.Fatalf("no match: %d calls, %s", plainCalls, got)
	}
	if !hasTerm("Big RIVER, small", []string{"river"}) || hasTerm("rivers", []string{"river"}) {
		t.Fatal("hasTerm")
	}
}

// Article lists of every shape come back as they were written.
func TestListRoundTrip(t *testing.T) {
	rnd := uint32(7)
	next := func() uint32 { rnd = rnd*1103515245 + 12345; return rnd >> 8 }
	for _, c := range []struct{ n, gap int }{{1, 5}, {3, 1}, {15, 900}, {16, 1}, {17, 3}, {200, 1}, {500, 40}, {3000, 2000}, {20000, 7}, {5000, 1 << 16}} {
		var ps []posting
		doc := uint32(0)
		for i := 0; i < c.n; i++ {
			if i > 0 || next()%2 == 0 {
				doc += 1 + next()%uint32(c.gap)
			}
			ps = append(ps, posting{doc, bucketTF[next()%4]})
		}
		buf := appendList([]byte{0xAA}, ps)
		got, ok := readList(buf[1:], uint32(len(ps)), nil)
		if !ok || len(got) != len(ps) {
			t.Fatalf("%d gap %d: ok %v, %d back", c.n, c.gap, ok, len(got))
		}
		for i := range ps {
			if got[i] != ps[i] {
				t.Fatalf("%d gap %d: #%d is %v, wrote %v", c.n, c.gap, i, got[i], ps[i])
			}
		}
		// Cut short: noticed, not read wrong.
		if _, ok := readList(buf[1:len(buf)-1], uint32(len(ps)), nil); ok && c.n >= shortList {
			t.Fatalf("%d gap %d: a list cut short read as whole", c.n, c.gap)
		}
	}
}
