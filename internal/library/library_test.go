package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func copyDump(t *testing.T, dir, date string) {
	t.Helper()
	for _, suf := range []string{".xml.bz2", "-index.txt.bz2"} {
		b, err := os.ReadFile("../../testdata/testwiki-20261101-pages-articles-multistream" + suf)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "testwiki-"+date+"-pages-articles-multistream"+suf), b, 0o644)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFullTextFollowsUpdates(t *testing.T) {
	dir := t.TempDir()
	copyDump(t, dir, "20261101")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	old := l.Get("testwiki")
	waitFor(t, "title index", func() bool { return old.Reader() != nil })
	if old.FTStatus().State != FTNone {
		t.Fatal("search inside articles must not start unasked")
	}
	if err := l.BuildFullText("testwiki"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first full-text index", func() bool { return old.FullText() != nil })

	// A newer dump arrives (as after a download): it is prepared and gets
	// search inside articles by itself. The old copy is left as it is.
	copyDump(t, dir, "20261201")
	if err := l.Rescan(); err != nil {
		t.Fatal(err)
	}
	newer := l.Get("testwiki")
	if newer == nil || newer.Files.Date != "20261201" {
		t.Fatalf("newest copy should take the plain id: %+v", newer)
	}
	waitFor(t, "automatic full-text index of the new dump", func() bool { return newer.FullText() != nil })
	if l.Get("testwiki-20261101").FullText() == nil {
		t.Fatal("the old copy lost its index")
	}

	// Deleting the old copy removes its files and indexes.
	if err := l.Remove("testwiki-20261101"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nabu", "index", "testwiki-20261101.nfts")); !os.IsNotExist(err) {
		t.Fatal("old full-text index left behind")
	}

	// The wish survives a restart even with no index on the drive...
	os.Remove(filepath.Join(dir, ".nabu", "index", "testwiki-20261201.nfts"))
	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	b2 := l2.Get("testwiki")
	waitFor(t, "rebuild after restart", func() bool { return b2.FullText() != nil })

	// ...and cancelling forgets it.
	l2.CancelFullText("testwiki")
	if l2.WantsFullText("testwiki") {
		t.Fatal("cancel should stop automatic rebuilds")
	}
}
