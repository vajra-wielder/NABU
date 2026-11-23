package dump

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/vajra-wielder/nabu/internal/bzip2w"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func scanOne(t *testing.T, dir, db string) *Files {
	t.Helper()
	eds, _ := Scan(dir)
	for _, f := range eds {
		if f.DB == db {
			return f
		}
	}
	t.Fatalf("no %s in %s", db, dir)
	return nil
}

func TestSlim(t *testing.T) {
	for _, part := range []int64{0, 4_000} { // one file, and FAT32-style pieces
		src := scanOne(t, "../../testdata", "testwiki")
		if need, err := NeedsSlim(src); err != nil || !need {
			t.Fatalf("NeedsSlim before: %v %v", need, err)
		}
		dir := t.TempDir()
		res, err := Slim(src, dir, SlimOptions{PartSize: part, Workers: 3}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Pages != 248 || res.Kept != 247 || res.After >= res.Before {
			t.Fatalf("result %+v", res)
		}
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if !IsDumpFile(e.Name()) {
				t.Errorf("left behind: %s", e.Name())
			}
		}
		slim := scanOne(t, dir, "testwiki")
		if part > 0 && len(slim.Pairs[0].Data) < 2 {
			t.Fatalf("not written in pieces: %v", slim.Pairs[0].Data)
		}
		if need, err := NeedsSlim(slim); err != nil || need {
			t.Fatalf("NeedsSlim after: %v %v", need, err)
		}
		r, _ := open(t, dir, "testwiki")
		orig, _ := open(t, "../../testdata", "testwiki")
		// Every article, redirect and category reads as before.
		n := 0
		for s := 0; s < orig.Index.StreamCount(); s++ {
			pages, err := orig.StreamPages(uint32(s))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range pages {
				got, _, err := r.Lookup(p.Title)
				if !KeptBySlim(p.NS) {
					if err == nil && got != nil && got.Title == p.Title {
						t.Errorf("%s (ns %d) was kept", p.Title, p.NS)
					}
					continue
				}
				if err != nil || got == nil || got.Text != p.Text || got.ID != p.ID || got.Redirect != p.Redirect {
					t.Fatalf("%s: %v", p.Title, err)
				}
				n++
			}
		}
		if n != 247 {
			t.Fatalf("%d pages checked", n)
		}
		if r.Site().Sitename != orig.Site().Sitename {
			t.Fatal("site information lost")
		}
	}
}

// Slimming in place replaces the dump only once the new one is complete.
func TestSlimInPlace(t *testing.T) {
	dir := t.TempDir()
	src := scanOne(t, "../../testdata", "testwiki")
	for _, p := range append(append([]string{}, src.Pairs[0].Data...), src.Pairs[0].Index) {
		b, _ := os.ReadFile(p)
		os.WriteFile(filepath.Join(dir, filepath.Base(p)), b, 0o644)
	}
	f := scanOne(t, dir, "testwiki")
	called := false
	_, err := Slim(f, dir, SlimOptions{}, func() error {
		called = true
		for _, p := range append(append([]string{}, f.Pairs[0].Data...), f.Pairs[0].Index) {
			os.Remove(p)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal(err)
	}
	if need, _ := NeedsSlim(scanOne(t, dir, "testwiki")); need {
		t.Fatal("not slimmed")
	}
	// Stopped before it starts: no dump is made (only what lets it carry on).
	stop := make(chan struct{})
	close(stop)
	g := scanOne(t, "../../testdata", "testwiki")
	out := t.TempDir()
	if _, err := Slim(g, out, SlimOptions{Stop: stop}, nil); err == nil {
		t.Fatal("not stopped")
	}
	if eds, _ := Scan(out); len(eds) != 0 {
		t.Fatalf("a dump left after stopping: %v", eds)
	}
}

// TestRealSlim slims a genuine dump (NABU_REAL_DUMPS) and checks every
// page it keeps against the original.
func TestRealSlim(t *testing.T) {
	dir := os.Getenv("NABU_REAL_DUMPS")
	if dir == "" {
		t.Skip("set NABU_REAL_DUMPS")
	}
	eds, _ := Scan(dir)
	for _, src := range eds {
		out := t.TempDir()
		start := time.Now()
		res, err := Slim(src, out, SlimOptions{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %.1f MB to %.1f MB (%.1f%%), %d of %d pages kept, in %v", src.DB,
			float64(res.Before)/1e6, float64(res.After)/1e6, 100*float64(res.After)/float64(res.Before),
			res.Kept, res.Pages, time.Since(start).Round(time.Second))
		orig, slim := openBig(t, src), openBig(t, scanOne(t, out, src.DB))
		checked := 0
		for s := 0; s < orig.Index.StreamCount(); s += 7 {
			pages, err := orig.StreamPages(uint32(s))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range pages {
				if !KeptBySlim(p.NS) {
					continue
				}
				got, _, err := slim.Lookup(p.Title)
				if err != nil || got == nil || got.Text != p.Text || got.ID != p.ID {
					t.Fatalf("%s: %v", p.Title, err)
				}
				checked++
			}
		}
		t.Logf("   %d pages compared with the original", checked)
		timeOpen(t, orig, slim)
	}
}

func openBig(t *testing.T, f *Files) *Reader {
	t.Helper()
	out := filepath.Join(t.TempDir(), "x.nidx")
	if err := Build(f, out, t.TempDir(), 64<<20, nil); err != nil {
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
	return r
}

func timeOpen(t *testing.T, orig, slim *Reader) {
	var titles []string
	for s := 3; s < orig.Index.StreamCount(); s += 47 {
		pages, _ := orig.StreamPages(uint32(s))
		for _, p := range pages {
			if p.NS == 0 && p.Redirect == "" {
				titles = append(titles, p.Title)
				break
			}
		}
	}
	for _, r := range []*Reader{orig, slim} {
		var total time.Duration
		for _, title := range titles {
			r.streams = newStreamCache(24 << 20) // nothing cached
			t0 := time.Now()
			if _, _, err := r.Lookup(title); err != nil {
				t.Fatal(err)
			}
			total += time.Since(t0)
		}
		t.Logf("   opening an article: %.1f ms on average (%d articles)", float64(total.Microseconds())/1000/float64(len(titles)), len(titles))
	}
}

// Interrupted, a slim carries on from its last checkpoint, and the result
// is the same, byte for byte, as without the interruption.
func TestSlimResumes(t *testing.T) {
	old := checkpointEvery
	checkpointEvery = 0 // a checkpoint after every stream
	defer func() { checkpointEvery = old }()
	src := manyStreams(t, 60)
	opt := SlimOptions{PartSize: 20000, Bundle: 1500, Workers: 2}

	whole := t.TempDir()
	if _, err := Slim(src, whole, opt, nil); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for round := 0; ; round++ {
		stop := make(chan struct{})
		var once sync.Once
		var firstSeen int64 = -1
		o := opt
		o.Stop = stop
		o.Progress = func(done, total int64) {
			if firstSeen < 0 {
				firstSeen = done
			}
			// Stopped at a third, then at two thirds, then to the end.
			if round < 2 && done > total*int64(round+1)/3 {
				once.Do(func() { close(stop) })
			}
		}
		if round > 0 && firstSeen == 0 {
			t.Fatal("started again from the beginning")
		}
		_, err := Slim(src, dir, o, nil)
		if round > 0 && firstSeen <= 0 {
			t.Fatalf("round %d did not carry on (first progress %d)", round, firstSeen)
		}
		if err == nil {
			break
		}
		if !errors.Is(err, ErrSlimStopped) || round > 2 {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	ents, _ := os.ReadDir(whole)
	for _, e := range ents {
		a, _ := os.ReadFile(filepath.Join(whole, e.Name()))
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s differs after carrying on (%v)", e.Name(), err)
		}
	}
	left, _ := os.ReadDir(dir)
	if len(left) != len(ents) {
		t.Fatalf("%d files, want %d (temporary files left?)", len(left), len(ents))
	}
}

// manyStreams makes a dump of n streams of 20 pages each, one in five of
// them a page NABU leaves out.
func manyStreams(t *testing.T, n int) *Files {
	t.Helper()
	dir := t.TempDir()
	var data, index bytes.Buffer
	data.Write(bzip2w.Compress([]byte(`<mediawiki xml:lang="en"><siteinfo><sitename>Wikipedia</sitename><dbname>testwiki</dbname>` +
		`<base>https://en.wikipedia.org/wiki/Main_Page</base><case>first-letter</case><namespaces>` +
		`<namespace key="0" case="first-letter" /><namespace key="4" case="first-letter">Wikipedia</namespace></namespaces></siteinfo>` + "\n")))
	id := 0
	for s := 0; s < n; s++ {
		off := data.Len()
		var xml strings.Builder
		for p := 0; p < 20; p++ {
			id++
			ns, title := 0, fmt.Sprintf("Article %d", id)
			if id%5 == 0 {
				ns, title = 4, fmt.Sprintf("Wikipedia:Talk %d", id)
			}
			fmt.Fprintf(&xml, "  <page>\n    <title>%s</title>\n    <ns>%d</ns>\n    <id>%d</id>\n    <revision><text>%s</text></revision>\n  </page>\n",
				title, ns, id, strings.Repeat(fmt.Sprintf("Text of page %d. ", id), 30))
			fmt.Fprintf(&index, "%d:%d:%s\n", off, id, title)
		}
		data.Write(bzip2w.Compress([]byte(xml.String())))
	}
	data.Write(bzip2w.Compress([]byte("</mediawiki>\n")))
	name := filepath.Join(dir, "testwiki-20261101-pages-articles-multistream")
	os.WriteFile(name+".xml.bz2", data.Bytes(), 0o644)
	os.WriteFile(name+"-index.txt.bz2", bzip2w.Compress(index.Bytes()), 0o644)
	return scanOne(t, dir, "testwiki")
}
