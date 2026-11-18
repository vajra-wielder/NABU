package zim

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// testEntry describes one entry for the in-memory test writer.
type testEntry struct {
	ns       byte
	url      string
	title    string
	mime     string
	content  []byte
	redirect string // "ns/url" of target
}

// buildZIM writes a minimal, spec-conforming ZIM file. Every entry with
// content goes in its own blob; blobs are split into two clusters, the
// first compressed with comp and the second stored raw, so both code
// paths are exercised. Old (minor 0) layout: namespaces as given.
func buildZIM(t *testing.T, entries []testEntry, comp byte, mainPath string) []byte {
	t.Helper()
	sort.Slice(entries, func(i, j int) bool {
		return compareKey(entries[i].ns, entries[i].url, entries[j].ns, entries[j].url) < 0
	})
	index := map[string]uint32{}
	for i, e := range entries {
		index[string(e.ns)+"/"+e.url] = uint32(i)
	}
	mimeIdx := map[string]uint16{}
	var mimes []string
	for _, e := range entries {
		if e.redirect == "" {
			if _, ok := mimeIdx[e.mime]; !ok {
				mimeIdx[e.mime] = uint16(len(mimes))
				mimes = append(mimes, e.mime)
			}
		}
	}
	// Assign blobs: first half compressed cluster 0, rest raw cluster 1.
	type loc struct{ cluster, blob uint32 }
	locs := map[int]loc{}
	var blobs [2][][]byte
	n := 0
	for i, e := range entries {
		if e.redirect != "" {
			continue
		}
		c := 0
		if n%2 == 1 {
			c = 1
		}
		n++
		locs[i] = loc{uint32(c), uint32(len(blobs[c]))}
		blobs[c] = append(blobs[c], e.content)
	}
	le := binary.LittleEndian
	var buf bytes.Buffer
	buf.Write(make([]byte, headerSize))
	mimePos := buf.Len()
	for _, m := range mimes {
		buf.WriteString(m)
		buf.WriteByte(0)
	}
	buf.WriteByte(0)
	// Dirents.
	direntPos := make([]uint64, len(entries))
	for i, e := range entries {
		direntPos[i] = uint64(buf.Len())
		var b [16]byte
		if e.redirect != "" {
			le.PutUint16(b[0:], mimeRedirect)
			b[3] = e.ns
			tgt, ok := index[e.redirect]
			if !ok {
				t.Fatalf("redirect target %q missing", e.redirect)
			}
			le.PutUint32(b[8:], tgt)
			buf.Write(b[:12])
		} else {
			le.PutUint16(b[0:], mimeIdx[e.mime])
			b[3] = e.ns
			le.PutUint32(b[8:], locs[i].cluster)
			le.PutUint32(b[12:], locs[i].blob)
			buf.Write(b[:16])
		}
		buf.WriteString(e.url)
		buf.WriteByte(0)
		buf.WriteString(e.title)
		buf.WriteByte(0)
	}
	urlPtrPos := buf.Len()
	for _, p := range direntPos {
		binary.Write(&buf, le, p)
	}
	titlePtrPos := buf.Len()
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	title := func(i int) string {
		if entries[i].title != "" {
			return entries[i].title
		}
		return entries[i].url
	}
	sort.SliceStable(order, func(a, b int) bool {
		return compareKey(entries[order[a]].ns, title(order[a]), entries[order[b]].ns, title(order[b])) < 0
	})
	for _, i := range order {
		binary.Write(&buf, le, uint32(i))
	}
	clusterPtrPos := buf.Len()
	buf.Write(make([]byte, 16))
	var clusterPos [2]uint64
	for c := 0; c < 2; c++ {
		clusterPos[c] = uint64(buf.Len())
		var data bytes.Buffer
		off := uint32(4 * (len(blobs[c]) + 1))
		for _, b := range blobs[c] {
			binary.Write(&data, le, off)
			off += uint32(len(b))
		}
		binary.Write(&data, le, off)
		for _, b := range blobs[c] {
			data.Write(b)
		}
		if c == 1 {
			buf.WriteByte(compNone)
			buf.Write(data.Bytes())
			continue
		}
		buf.WriteByte(comp)
		switch comp {
		case compZstd:
			w, _ := zstd.NewWriter(&buf)
			w.Write(data.Bytes())
			w.Close()
		case compXZ:
			w, _ := xz.NewWriter(&buf)
			w.Write(data.Bytes())
			w.Close()
		default:
			buf.Write(data.Bytes())
		}
	}
	checksumPos := buf.Len()
	buf.Write(make([]byte, 16))
	out := buf.Bytes()
	le.PutUint64(out[clusterPtrPos:], clusterPos[0])
	le.PutUint64(out[clusterPtrPos+8:], clusterPos[1])
	h := out[:headerSize]
	le.PutUint32(h[0:], Magic)
	le.PutUint16(h[4:], 5)
	le.PutUint32(h[24:], uint32(len(entries)))
	le.PutUint32(h[28:], 2)
	le.PutUint64(h[32:], uint64(urlPtrPos))
	le.PutUint64(h[40:], uint64(titlePtrPos))
	le.PutUint64(h[48:], uint64(clusterPtrPos))
	le.PutUint64(h[56:], uint64(mimePos))
	main := uint32(noMainPage)
	if i, ok := index[mainPath]; ok {
		main = i
	}
	le.PutUint32(h[64:], main)
	le.PutUint32(h[68:], noMainPage)
	le.PutUint64(h[72:], uint64(checksumPos))
	return out
}

func sampleEntries() []testEntry {
	html := func(s string) []byte {
		return []byte("<html><head><title>" + s + "</title></head><body><h1>" + s + "</h1></body></html>")
	}
	return []testEntry{
		{ns: 'A', url: "Main_Page", title: "Main Page", mime: "text/html", content: html("Main")},
		{ns: 'A', url: "Babylon", title: "Babylon", mime: "text/html", content: html("Babylon")},
		{ns: 'A', url: "Babylonia", title: "Babylonia", redirect: "A/Babylon"},
		{ns: 'A', url: "Nabu", title: "Nabu", mime: "text/html", content: html("Nabu")},
		{ns: 'A', url: "Nabopolassar", title: "Nabopolassar", mime: "text/html", content: html("Nabopolassar")},
		{ns: 'A', url: "AC/DC", title: "AC/DC", mime: "text/html", content: html("AC/DC")},
		{ns: 'A', url: "東京", title: "東京", mime: "text/html", content: html("Tokyo")},
		{ns: 'I', url: "tablet.png", mime: "image/png", content: bytes.Repeat([]byte{0x89, 'P'}, 500)},
		{ns: '-', url: "style.css", mime: "text/css", content: []byte("body{color:red}")},
		{ns: 'M', url: "Title", mime: "text/plain", content: []byte("Test Wikipedia")},
		{ns: 'M', url: "Counter", mime: "text/plain", content: []byte("image/png=1;text/html=6")},
	}
}

func TestOldSchemeArchive(t *testing.T) {
	for _, comp := range []byte{compNone, compZstd, compXZ} {
		raw := buildZIM(t, sampleEntries(), comp, "A/Main_Page")
		a, err := New(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatalf("comp %d: %v", comp, err)
		}
		if a.NewNamespaceScheme || a.ContentNamespace() != 'A' {
			t.Fatalf("expected old namespace scheme")
		}
		main, err := a.MainEntry()
		if err != nil || main.URL != "Main_Page" {
			t.Fatalf("main page: %v %v", main, err)
		}
		for _, e := range sampleEntries() {
			got, err := a.Find(e.ns, e.url)
			if err != nil {
				t.Fatalf("comp %d: find %c/%s: %v", comp, e.ns, e.url, err)
			}
			if e.redirect != "" {
				if !got.IsRedirect() {
					t.Fatalf("%s should be a redirect", e.url)
				}
				r, _ := a.Resolve(got)
				if r.Path() != e.redirect {
					t.Fatalf("redirect resolved to %s", r.Path())
				}
				continue
			}
			b, err := a.ReadAll(got)
			if err != nil || !bytes.Equal(b, e.content) {
				t.Fatalf("comp %d: content of %s: %q %v", comp, e.url, b, err)
			}
			if got.MimeType() != e.mime {
				t.Fatalf("mime of %s = %q", e.url, got.MimeType())
			}
		}
		if _, err := a.Find('A', "Nope"); err != ErrNotFound {
			t.Fatalf("missing entry: %v", err)
		}
		if a.Metadata("Title") != "Test Wikipedia" {
			t.Fatalf("metadata title %q", a.Metadata("Title"))
		}
		if a.ArticleCount() != 6 {
			t.Fatalf("article count %d", a.ArticleCount())
		}
		res, err := a.SearchTitles("nab", 10)
		if err != nil || len(res) != 2 {
			t.Fatalf("search nab: %+v %v", res, err)
		}
		res, _ = a.SearchTitles("babylonia", 10)
		if len(res) != 1 || res[0].Entry.URL != "Babylon" || res[0].Via != "Babylonia" || !res[0].Exact {
			t.Fatalf("search redirect: %+v", res)
		}
		if e, ok := a.FindTitle("ac/dc"); !ok || e.URL != "AC/DC" {
			t.Fatalf("FindTitle ac/dc: %v %v", e, ok)
		}
		if e, ok := a.FindTitle("東京"); !ok || e.URL != "東京" {
			t.Fatalf("FindTitle unicode: %v %v", e, ok)
		}
		for i := 0; i < 20; i++ {
			r, err := a.RandomArticle()
			if err != nil || !isArticle(r) {
				t.Fatalf("random: %v %v", r, err)
			}
		}
		// Uncompressed blobs must be served without buffering.
		img, _ := a.Find('I', "tablet.png")
		rs, n, err := a.BlobReader(img)
		if err != nil || n != 1000 {
			t.Fatalf("blob reader: %d %v", n, err)
		}
		if _, ok := rs.(*io.SectionReader); !ok && img.Cluster == 1 {
			t.Fatalf("raw blob not streamed: %T", rs)
		}
	}
}

func TestRejectsGarbage(t *testing.T) {
	junk := bytes.Repeat([]byte("not a zim "), 20)
	if _, err := New(bytes.NewReader(junk), int64(len(junk))); err != ErrNotZIM {
		t.Fatalf("got %v", err)
	}
	raw := buildZIM(t, sampleEntries(), compZstd, "A/Main_Page")
	cut := raw[:len(raw)/3]
	if _, err := New(bytes.NewReader(cut), int64(len(cut))); err == nil {
		// Header may still be valid; reads must then fail cleanly.
		a, _ := New(bytes.NewReader(cut), int64(len(cut)))
		if a != nil {
			if _, err := a.MainEntry(); err == nil {
				if e, err := a.MainEntry(); err == nil {
					if _, err := a.ReadAll(e); err == nil {
						t.Fatalf("truncated archive read succeeded")
					}
				}
			}
		}
	}
}

func TestCaseVariants(t *testing.T) {
	got := strings.Join(caseVariants("new york city"), "|")
	want := "new york city|New york city|New York City|NEW YORK CITY"
	if got != want {
		t.Fatalf("got %s", got)
	}
}

// TestLibzimFixture checks the reader against a file produced by the
// reference libzim writer (testdata/make_fixture.py). Point NABU_FIXTURE
// at it; the test is skipped otherwise.
func TestLibzimFixture(t *testing.T) {
	path := os.Getenv("NABU_FIXTURE")
	if path == "" {
		matches, _ := filepath.Glob("../../testdata/wikipedia_en_test_*.zim")
		if len(matches) == 0 {
			t.Skip("set NABU_FIXTURE to a ZIM made by testdata/make_fixture.py")
		}
		path = matches[0]
	}
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !a.NewNamespaceScheme {
		t.Fatalf("libzim 9 output should use the new namespace scheme")
	}
	if !a.titleIdx().articles {
		t.Fatalf("expected the front-article title listing to be used")
	}
	main, err := a.MainEntry()
	if err != nil || main.URL != "Main_Page" {
		t.Fatalf("main: %v %v", main, err)
	}
	body, err := a.ReadAll(main)
	if err != nil || !bytes.Contains(body, []byte("Welcome to the test Wikipedia")) {
		t.Fatalf("main body: %v", err)
	}
	css, err := a.Find('C', "_assets_/style.css")
	if err != nil || css.MimeType() != "text/css" {
		t.Fatalf("css: %v", err)
	}
	img, err := a.Find('C', "_assets_/tablet.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := a.ReadAll(img)
	if !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatalf("png corrupt")
	}
	for _, q := range []string{"babylon", "Ελληνικά", "москва", "nabu", "god of"} {
		res, err := a.SearchTitles(q, 10)
		if err != nil || len(res) == 0 {
			t.Fatalf("search %q: %v %v", q, res, err)
		}
	}
	if e, ok := a.FindTitle("god of scribes"); !ok || e.URL != "Nabu" {
		t.Fatalf("redirect title search: %v", e)
	}
	if a.Metadata("Name") != "wikipedia_en_test" || a.Metadata("Flavour") != "maxi" {
		t.Fatalf("metadata")
	}
	if ill, err := a.MetadataBytes("Illustration_48x48@1"); err != nil || !bytes.HasPrefix(ill, []byte("\x89PNG")) {
		t.Fatalf("illustration: %v", err)
	}
	if a.ArticleCount() < 300 {
		t.Fatalf("article count %d", a.ArticleCount())
	}
	// Read every article once to shake out cluster/offset bugs.
	lo, hi, _ := a.NamespaceRange('C')
	count := 0
	for i := lo; i < hi; i++ {
		e, _ := a.EntryAt(i)
		if e.IsRedirect() {
			continue
		}
		if _, err := a.ReadAll(e); err != nil {
			t.Fatalf("read %s: %v", e.Path(), err)
		}
		count++
	}
	if count < 300 {
		t.Fatalf("only %d content entries", count)
	}
}
