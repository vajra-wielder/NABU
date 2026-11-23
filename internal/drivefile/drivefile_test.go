package drivefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClosesWhenIdleAndReopens(t *testing.T) {
	old := Idle
	Idle = 50 * time.Millisecond
	defer func() { Idle = old }()
	dir := t.TempDir()
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte("hello world"), 0o644)
	base := Open()
	f, err := OpenRead(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := f.ReadAt(buf, 6); err != nil || string(buf) != "world" {
		t.Fatalf("%q %v", buf, err)
	}
	if Open() != base+1 {
		t.Fatalf("%d open", Open()-base)
	}
	time.Sleep(200 * time.Millisecond)
	if Open() != base {
		t.Fatal("still open after being idle")
	}
	// The drive pulled out and put back: the file is there again.
	away := dir + "-away"
	os.Rename(dir, away)
	if _, err := f.ReadAt(buf, 0); err == nil {
		t.Fatal("read with the drive out")
	}
	os.Rename(away, dir)
	if _, err := f.ReadAt(buf, 0); err != nil || string(buf) != "hello" {
		t.Fatalf("after the drive came back: %q %v", buf, err)
	}
	f.Close()
	if Open() != base {
		t.Fatal("open after Close")
	}
}

func TestWriteAtAndWriteFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "w")
	f, err := OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("abc"), 0)
	f.WriteAt([]byte("def"), 3)
	f.Sync()
	f.Close()
	// Reopening must not truncate what was written.
	if _, err := f.WriteAt([]byte("X"), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if b, _ := os.ReadFile(p); string(b) != "Xbcdef" {
		t.Fatalf("%q", b)
	}
	q := filepath.Join(dir, "settings.json")
	if err := WriteFile(q, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(q); string(b) != "{}" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(q + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left")
	}
}

// Replacing a file someone is reading at that moment works (on Windows
// the rename is refused while the old file is open, and NABU's picture
// download stopped on it).
func TestWriteFileWhileRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "job.json")
	if err := WriteFile(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	r, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		r.Close()
	}()
	if err := WriteFile(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("got %q", b)
	}
}

// Two parts of NABU saving the same file at once both succeed, and the
// file is one of the two whole versions.
func TestWriteFileConcurrent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "job.json")
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) { errs <- WriteFile(p, []byte(strings.Repeat(string(rune('a'+i)), 5000))) }(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(p)
	if len(b) != 5000 || strings.Trim(string(b), string(b[:1])) != "" {
		t.Fatalf("mixed or short file: %d bytes", len(b))
	}
	if left, _ := filepath.Glob(p + "*.tmp"); len(left) > 0 {
		t.Fatalf("left behind: %v", left)
	}
}
