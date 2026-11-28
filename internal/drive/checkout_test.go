package drive

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeCheckout(t *testing.T) string {
	t.Helper()
	c := t.TempDir()
	os.WriteFile(filepath.Join(c, "go.mod"), []byte("module github.com/vajra-wielder/nabu\n\ngo 1.20\n"), 0o644)
	for _, d := range []string{"cmd/nabu", "internal/dump", "testdata", "dist/NABU/wikipedia"} {
		os.MkdirAll(filepath.Join(c, d), 0o755)
	}
	return c
}

// NABU's source code is never used as a drive: its test folder is.
func TestNeverTheSourceCode(t *testing.T) {
	t.Setenv(TestEnv, "") // whatever the computer running the tests has set
	c := fakeCheckout(t)
	for _, asked := range []string{c, filepath.Join(c, "internal"), filepath.Join(c, "testdata")} {
		l := Find(asked, "")
		if l.Root != filepath.Join(c, TestFolder) || !l.Moved {
			t.Fatalf("--drive %s: %s", asked, l.Root)
		}
	}
	if !isDir(filepath.Join(c, TestFolder)) {
		t.Fatal("test folder not made")
	}
	// Nothing written among the source files.
	for _, n := range []string{Wikipedia, Work, Personal, Indexes, "library"} {
		if isDir(filepath.Join(c, n)) {
			t.Fatalf("%s made in the source code", n)
		}
	}
	// A test folder of another name is used as it is, and inside it all
	// is as asked.
	os.RemoveAll(filepath.Join(c, TestFolder))
	mine := filepath.Join(c, "NABU-test drive")
	os.MkdirAll(mine, 0o755)
	if l := Find(c, ""); l.Root != mine {
		t.Fatalf("own test folder: %s", l.Root)
	}
	if l := Find(mine, ""); l.Root != mine || l.Moved {
		t.Fatalf("inside the test folder: %s", l.Root)
	}
	// A built package and anything outside a checkout are left alone.
	pkg := filepath.Join(c, "dist", "NABU")
	if l := Find(pkg, ""); l.Root != pkg || l.Moved {
		t.Fatalf("package: %s", l.Root)
	}
	outside := t.TempDir()
	if l := Find(outside, ""); l.Root != outside || l.Moved {
		t.Fatalf("outside: %s", l.Root)
	}
	// --library inside the source code: the dumps are read where they
	// are, everything NABU writes goes to the test folder.
	if l := Find("", filepath.Join(c, "testdata")); l.Root != mine || l.Wikipedia != filepath.Join(c, "testdata") || l.Indexes != filepath.Join(mine, Indexes) {
		t.Fatalf("--library: %+v", l)
	}
}

func TestTestFolderNames(t *testing.T) {
	for name, want := range map[string]bool{"Nabu Test system": true, "NABU-test": true, "nabu_testing": true, "NabuTest": true, "testdata": false, "nabu": false, "internal": false} {
		if IsTestFolder(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}

// NABU_TEST puts the test folder anywhere: whatever points into the
// source code lands there, a drive elsewhere is left alone.
func TestTestFolderFromEnv(t *testing.T) {
	c := fakeCheckout(t)
	ext := filepath.Join(t.TempDir(), "NABU-test")
	t.Setenv(TestEnv, ext)
	for _, asked := range []string{c, filepath.Join(c, "internal")} {
		if l := Find(asked, ""); l.Root != ext || !l.Moved || l.Wikipedia != filepath.Join(ext, Wikipedia) {
			t.Fatalf("--drive %s: %+v", asked, l)
		}
	}
	if !isDir(ext) || isDir(filepath.Join(c, TestFolder)) {
		t.Fatal("test folder made in the wrong place")
	}
	// Pointed at it directly, or a copy of the source code in it: as is.
	os.WriteFile(filepath.Join(ext, "go.mod"), []byte("module github.com/vajra-wielder/nabu\n"), 0o644)
	if l := Find(ext, ""); l.Root != ext || l.Moved {
		t.Fatalf("test folder itself: %s", l.Root)
	}
	// A real drive or a package is never redirected.
	other := t.TempDir()
	if l := Find(other, ""); l.Root != other || l.Moved {
		t.Fatalf("real drive: %s", l.Root)
	}
	pkg := filepath.Join(c, "dist", "NABU")
	if l := Find(pkg, ""); l.Root != pkg || l.Moved {
		t.Fatalf("package: %s", l.Root)
	}
	// --library in the source code: read there, written to NABU_TEST.
	if l := Find("", filepath.Join(c, "testdata")); l.Root != ext || l.Wikipedia != filepath.Join(c, "testdata") {
		t.Fatalf("--library: %+v", l)
	}
}

// A test folder beside the source code is found without NABU_TEST, and
// used as it is even with a copy of the source code in it.
func TestTestFolderBeside(t *testing.T) {
	t.Setenv(TestEnv, "")
	parent := t.TempDir()
	c := filepath.Join(parent, "NABU")
	os.MkdirAll(c, 0o755)
	os.WriteFile(filepath.Join(c, "go.mod"), []byte("module github.com/vajra-wielder/nabu\n"), 0o644)
	beside := filepath.Join(parent, "NABU-test")
	os.MkdirAll(filepath.Join(beside, Wikipedia), 0o755)
	os.WriteFile(filepath.Join(beside, "go.mod"), []byte("module github.com/vajra-wielder/nabu\n"), 0o644)
	if l := Find(c, ""); l.Root != beside || !l.Moved {
		t.Fatalf("from the source code: %s", l.Root)
	}
	if l := Find(beside, ""); l.Root != beside || l.Moved {
		t.Fatalf("the test folder itself: %s", l.Root)
	}
}
