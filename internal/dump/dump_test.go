package dump

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const td = "../../testdata"

func open(t *testing.T, dir, db string) (*Reader, *Files) {
	t.Helper()
	eds, notes := Scan(dir)
	if len(notes) > 0 {
		t.Fatalf("notes: %v", notes)
	}
	for _, f := range eds {
		if f.DB != db {
			continue
		}
		out := filepath.Join(t.TempDir(), f.Key()+".nidx")
		// A tiny memory budget forces the external merge sort path.
		if err := Build(f, out, t.TempDir(), 2000, nil); err != nil {
			t.Fatal(err)
		}
		ix, err := OpenIndex(out, f)
		if err != nil {
			t.Fatal(err)
		}
		r, err := NewReader(f, ix)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		return r, f
	}
	t.Fatalf("%s not found in %s", db, dir)
	return nil, nil
}

func TestEnglishDump(t *testing.T) {
	r, _ := open(t, td, "testwiki")
	si := r.Site()
	if si.DBName != "testwiki" || si.Lang != "en" || si.MainPage() != "Main Page" || si.RTL() {
		t.Fatalf("siteinfo: %+v", si)
	}
	if r.Index.Meta.Records != 248 {
		t.Fatalf("records %d", r.Index.Meta.Records)
	}
	p, _, err := r.Lookup("babylon")
	if err != nil || p.Title != "Babylon" || !strings.Contains(p.Text, "Infobox settlement") {
		t.Fatalf("lookup: %v %v", p, err)
	}
	p, _, err = r.Lookup("Babylonia")
	if err != nil || p.Redirect != "Babylon" {
		t.Fatalf("redirect: %+v %v", p, err)
	}
	// A page in the third stream (pages 200+), across stream boundaries.
	for _, e := range mustPrefix(t, r, "tablet", 100) {
		pg, err := r.Load(e)
		if err != nil || pg.Title != e.Title {
			t.Fatalf("load %q: %v", e.Title, err)
		}
	}
	// Accent- and case-insensitive search.
	if res := mustPrefix(t, r, "ecri", 5); len(res) != 1 || res[0].Title != "Écriture" {
		t.Fatalf("fold search: %+v", res)
	}
	if e, exact, err := r.Index.Find("ÉCRITURE"); err != nil || exact || e.Title != "Écriture" {
		t.Fatalf("fold find: %+v %v %v", e, exact, err)
	}
	if _, _, err := r.Index.Find("Nineveh"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	// Namespaces.
	if e, _, _ := r.Index.Find("Template:Infobox settlement"); e.NS != NSTemplate {
		t.Fatalf("template ns %d", e.NS)
	}
	if si.Normalize("category:ancient_cities") != "Category:Ancient cities" {
		t.Fatalf("normalize: %q", si.Normalize("category:ancient_cities"))
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		e, err := r.Index.Random(rng, NSMain)
		if err != nil || e.NS != 0 {
			t.Fatalf("random: %v %v", e, err)
		}
	}
}

func mustPrefix(t *testing.T, r *Reader, q string, n int) []Entry {
	t.Helper()
	res, err := r.Index.Prefix(q, n, func(ns int) bool { return ns == 0 })
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPersianDump(t *testing.T) {
	r, _ := open(t, td, "fawiki")
	si := r.Site()
	if !si.RTL() || si.Lang != "fa" || si.MainPage() != "صفحهٔ اصلی" {
		t.Fatalf("siteinfo: %+v main=%q", si, si.MainPage())
	}
	if k, ok := si.NamespaceKey("پرونده"); !ok || k != NSFile {
		t.Fatalf("localized File namespace: %d %v", k, ok)
	}
	if k, ok := si.NamespaceKey("File"); !ok || k != NSFile {
		t.Fatal("English alias for File must work everywhere")
	}
	if si.Normalize("file:x.jpg") != "پرونده:X.jpg" {
		t.Fatalf("normalize: %q", si.Normalize("file:x.jpg"))
	}
	p, _, err := r.Lookup("بابل")
	if err != nil || !strings.Contains(p.Text, "عراق") {
		t.Fatalf("lookup: %v", err)
	}
}

// TestSplitAndParts: a dump split in pieces for FAT32, and Wikimedia's
// numbered-part layout, both read like the combined file.
func TestSplitAndParts(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join(td, "testwiki-20261101-pages-articles-multistream.xml.bz2"))
	idx, _ := os.ReadFile(filepath.Join(td, "testwiki-20261101-pages-articles-multistream-index.txt.bz2"))

	split := t.TempDir()
	cuts := []int{0, 100, 3000, 3001, len(data)}
	for i := 0; i+1 < len(cuts); i++ {
		os.WriteFile(filepath.Join(split, "testwiki-20261101-pages-articles-multistream.xml.bz2a"+string(rune('a'+i))), data[cuts[i]:cuts[i+1]], 0o644)
	}
	os.WriteFile(filepath.Join(split, "testwiki-20261101-pages-articles-multistream-index.txt.bz2"), idx, 0o644)

	parts := t.TempDir()
	os.WriteFile(filepath.Join(parts, "testwiki-20261101-pages-articles-multistream1.xml-p1p300.bz2"), data, 0o644)
	os.WriteFile(filepath.Join(parts, "testwiki-20261101-pages-articles-multistream-index1.txt-p1p300.bz2"), idx, 0o644)

	for _, dir := range []string{split, parts} {
		r, f := open(t, dir, "testwiki")
		if len(f.Pairs) != 1 || f.Pairs[0].Size != int64(len(data)) {
			t.Fatalf("%s: pairs %d size %d", dir, len(f.Pairs), f.Pairs[0].Size)
		}
		res := mustPrefix(t, r, "c", 300)
		if len(res) < 10 {
			t.Fatalf("prefix c: %d", len(res))
		}
		for _, e := range res {
			if _, err := r.Load(e); err != nil {
				t.Fatalf("%s: %v", e.Title, err)
			}
		}
	}
}

func TestStaleAndMissingIndex(t *testing.T) {
	dir := t.TempDir()
	data, _ := os.ReadFile(filepath.Join(td, "testwiki-20261101-pages-articles-multistream.xml.bz2"))
	os.WriteFile(filepath.Join(dir, "testwiki-20261101-pages-articles-multistream.xml.bz2"), data, 0o644)
	if eds, notes := Scan(dir); len(eds) != 0 || len(notes) != 1 {
		t.Fatalf("a data file without index must be reported: %v %v", eds, notes)
	}
	idx, _ := os.ReadFile(filepath.Join(td, "testwiki-20261101-pages-articles-multistream-index.txt.bz2"))
	os.WriteFile(filepath.Join(dir, "testwiki-20261101-pages-articles-multistream-index.txt.bz2"), idx, 0o644)
	eds, _ := Scan(dir)
	out := filepath.Join(dir, "x.nidx")
	if err := Build(eds[0], out, t.TempDir(), 0, nil); err != nil {
		t.Fatal(err)
	}
	// Grow the data file: the index no longer matches.
	f, _ := os.OpenFile(filepath.Join(dir, "testwiki-20261101-pages-articles-multistream.xml.bz2"), os.O_APPEND|os.O_WRONLY, 0)
	f.Write([]byte("more"))
	f.Close()
	eds, _ = Scan(dir)
	if _, err := OpenIndex(out, eds[0]); err != ErrStale {
		t.Fatalf("expected stale index, got %v", err)
	}
	// A half-written index is rejected too.
	b, _ := os.ReadFile(out)
	os.WriteFile(out, b[:len(b)/2], 0o644)
	if _, err := OpenIndex(out, nil); err == nil {
		t.Fatal("truncated index accepted")
	}
}

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"Écriture": "ecriture", "São_Paulo": "sao paulo", "  Straße ": "strasse",
		"Москва": "москва", "東京": "東京", "Æsir": "aesir",
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}
