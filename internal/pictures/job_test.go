package pictures

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/vajra-wielder/nabu/internal/dump"
)

func testReader(t *testing.T) *dump.Reader {
	t.Helper()
	eds, _ := dump.Scan("../../testdata")
	for _, f := range eds {
		if f.DB != "testwiki" {
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
	t.Fatal("no testwiki")
	return nil
}

// A job stops at the space limit, saving its place, before asking
// Wikimedia for anything.
func TestRunStopsAtTheLimit(t *testing.T) {
	rd := testReader(t)
	dir := t.TempDir()
	w, _ := Open(filepath.Join(dir, "testwiki"))
	c, _ := Open(filepath.Join(dir, Commons))
	defer w.Close()
	defer c.Close()
	j := &Job{Mode: All, Dump: rd.Files.Key()}
	err := Run(context.Background(), j, rd, Stores{Wiki: w, Shared: c}, nil, "test.wikipedia.org", nil,
		func() bool { return true }, func(Progress) {})
	if !errors.Is(err, ErrFull) || j.Done {
		t.Fatalf("got %v, done %v", err, j.Done)
	}
	saved, err := LoadJob(w.Dir)
	if err != nil || saved.Done || saved.Pass >= len(Tiers) {
		t.Fatalf("saved job %+v %v", saved, err)
	}
}
