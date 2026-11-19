package dump

import (
	"os"
	"path/filepath"
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
	// Stopped: nothing changes, nothing is left behind.
	stop := make(chan struct{})
	close(stop)
	g := scanOne(t, "../../testdata", "testwiki")
	out := t.TempDir()
	if _, err := Slim(g, out, SlimOptions{Stop: stop}, nil); err == nil {
		t.Fatal("not stopped")
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Fatalf("left behind after stopping: %d files", len(ents))
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
