package drive

import (
	"bufio"
	"github.com/vajra-wielder/nabu/internal/testfolder"
	"os"
	"path/filepath"
	"strings"
)

// NABU's own source code is never a drive. Run from a checkout (go run,
// or a program built into it) or pointed at one, NABU keeps everything in
// a test folder inside it instead, "Nabu Test system" by default (any
// folder named "Nabu Test…" will do; Git ignores them), so nothing is
// written among the source files and pulls and pushes stay clean.
//
// The test folder can be anywhere else too: set NABU_TEST to it (a folder
// on another disk with room for a full dump, say). Everything NABU keeps
// (dumps, indexes, pictures, settings) then goes there whenever it runs
// from the source code. A NABU drive or a built package never looks at
// NABU_TEST, so the real drive is not affected by it.

// TestFolder is the test folder made in a checkout when it has none.
const TestFolder = testfolder.Name

// TestEnv names the variable holding a test folder outside the checkout.
const TestEnv = "NABU_TEST"

// TestFolderSet returns the folder NABU_TEST names, if it is set.
func TestFolderSet() string { return testfolder.FromEnv() }

// IsTestFolder reports whether a folder name is that of a test folder.
func IsTestFolder(name string) bool { return testfolder.IsName(name) }

// IsCheckout reports whether dir is (the top of) NABU's source code.
func IsCheckout(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")) == "github.com/vajra-wielder/nabu"
		}
	}
	return false
}

// checkoutOf returns the checkout dir is in (or is), if any.
func checkoutOf(dir string) string {
	for d := abs(dir); ; d = filepath.Dir(d) {
		if IsCheckout(d) {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// testFolderIn is the checkout's test folder: the one there is (the
// first by name if several), or TestFolder.
func testFolderIn(checkout string) string { return testfolder.For(checkout) }

// awayFromSource keeps a NABU folder out of the source code: a root that
// is in a checkout but not in one of its test folders becomes the
// checkout's test folder. Moved reports that it did.
func awayFromSource(root string) (string, bool) {
	// A test folder (or a folder in one) is used as it is, even with a
	// copy of the source code in it.
	for d := abs(root); ; d = filepath.Dir(d) {
		if testfolder.IsName(filepath.Base(d)) {
			return root, false
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if t := testfolder.FromEnv(); t != "" {
		if rel, err := filepath.Rel(t, abs(root)); err == nil && !strings.HasPrefix(rel, "..") {
			return root, false // in the test folder NABU_TEST names
		}
	}
	if c := checkoutOf(root); c != "" {
		if t := testfolder.Beside(c); t != "" {
			return t, true
		}
	}
	if t := TestFolderSet(); t != "" {
		if rel, err := filepath.Rel(t, abs(root)); err == nil && !strings.HasPrefix(rel, "..") {
			return root, false // in the test folder NABU_TEST names
		}
	}
	c := checkoutOf(root)
	if c == "" {
		return root, false
	}
	rel, err := filepath.Rel(c, abs(root))
	if err == nil && rel != "." {
		first := strings.Split(rel, string(filepath.Separator))[0]
		if IsTestFolder(first) {
			return root, false // already in a test folder
		}
		if first == "dist" {
			return root, false // a built package (dist/NABU), ignored by Git
		}
	}
	return testFolderIn(c), true
}
