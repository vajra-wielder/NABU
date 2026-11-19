package drive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchGoneAndBack(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "NABU")
	os.MkdirAll(filepath.Join(root, Wikipedia), 0o755)
	l := At(root)
	id := l.ID()
	if len(id) != 32 || l.ID() != id {
		t.Fatalf("id %q", id)
	}
	events := make(chan string, 10)
	backAt := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Watch(ctx, 20*time.Millisecond, func(g bool) {
		if g {
			events <- "gone"
		} else {
			events <- "back"
		}
	}, func(n *Layout) { backAt <- n.Root })
	time.Sleep(60 * time.Millisecond)
	os.Rename(root, root+"-out")
	if e := <-events; e != "gone" {
		t.Fatal(e)
	}
	// Another drive appearing at that place is not this one.
	os.MkdirAll(filepath.Join(root, Work), 0o755)
	os.WriteFile(filepath.Join(root, Work, idFile), []byte("00000000000000000000000000000000"), 0o644)
	time.Sleep(80 * time.Millisecond)
	select {
	case e := <-events:
		t.Fatalf("another drive taken for this one: %s", e)
	default:
	}
	os.RemoveAll(root)
	os.Rename(root+"-out", root)
	select {
	case e := <-events:
		if e != "back" || <-backAt != root {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not noticed back")
	}
}
