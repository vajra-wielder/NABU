package library

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const fixture = "../../testdata/wikipedia_en_test_maxi_2026-10.zim"

func TestOpenAndSplit(t *testing.T) {
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// One whole copy, and one split into uneven parts like a FAT32 drive.
	os.WriteFile(filepath.Join(dir, "whole.zim"), data, 0o644)
	cuts := []int{0, 1000, 70_000, 70_001, 200_000, len(data)}
	for i := 0; i+1 < len(cuts); i++ {
		name := filepath.Join(dir, "split.zim"+string(rune('a'))+string(rune('a'+i)))
		os.WriteFile(name, data[cuts[i]:cuts[i+1]], 0o644)
	}
	os.WriteFile(filepath.Join(dir, "broken.zim"), []byte("not a zim"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)

	lib, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if len(lib.Books()) != 2 || len(lib.Errs) != 1 {
		t.Fatalf("books %d errs %v", len(lib.Books()), lib.Errs)
	}
	whole, split := lib.Get("whole"), lib.Get("split")
	if whole == nil || split == nil {
		t.Fatal("missing book")
	}
	if split.Size != int64(len(data)) || len(split.Files) != 5 {
		t.Fatalf("split size %d files %d", split.Size, len(split.Files))
	}
	if whole.Series() != "wikipedia_en_test_maxi" || whole.Edition() != "2026-10" {
		t.Fatalf("series %q edition %q", whole.Series(), whole.Edition())
	}
	// Every article must read identically from both.
	lo, hi, _ := whole.Archive.NamespaceRange('C')
	for i := lo; i < hi; i++ {
		a, _ := whole.Archive.EntryAt(i)
		b, _ := split.Archive.EntryAt(i)
		if a.IsRedirect() {
			continue
		}
		x, err1 := whole.Archive.ReadAll(a)
		y, err2 := split.Archive.ReadAll(b)
		if err1 != nil || err2 != nil || !bytes.Equal(x, y) {
			t.Fatalf("entry %d differs: %v %v", i, err1, err2)
		}
	}
	if runtime.GOOS == "windows" {
		return // Windows will not delete a file that is open.
	}
	// Rescan after deleting a file drops it.
	os.Remove(filepath.Join(dir, "whole.zim"))
	if err := lib.Rescan(); err != nil {
		t.Fatal(err)
	}
	if lib.Get("whole") != nil || lib.Get("split") != split {
		t.Fatal("rescan did not keep/drop the right books")
	}
}
