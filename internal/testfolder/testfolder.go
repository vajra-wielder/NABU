// Package testfolder finds the NABU folder used for testing with real
// data, so nothing needs setting by hand: the folder NABU_TEST names if
// it is set, else a folder named like "NABU-test" beside the source code
// (C:\Projects\NABU and C:\Projects\NABU-test), else one inside it
// ("Nabu Test system").
package testfolder

import (
	"bufio"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Env names the variable that can point elsewhere.
const Env = "NABU_TEST"

// Name is the test folder made inside the source code when there is none.
const Name = "Nabu Test system"

var nameRe = regexp.MustCompile(`(?i)^nabu[ _-]*test`)

// IsName reports whether a folder name is that of a test folder.
func IsName(name string) bool { return nameRe.MatchString(name) }

// FromEnv is the folder NABU_TEST names, or "".
func FromEnv() string {
	if v := strings.TrimSpace(os.Getenv(Env)); v != "" {
		if a, err := filepath.Abs(strings.Trim(v, `"`)); err == nil {
			return a
		}
		return v
	}
	return ""
}

// Beside is a test folder next to the source code at checkout, or "".
func Beside(checkout string) string {
	parent := filepath.Dir(checkout)
	ents, _ := os.ReadDir(parent)
	var found []string
	for _, e := range ents {
		if IsName(e.Name()) && isDir(filepath.Join(parent, e.Name())) && filepath.Join(parent, e.Name()) != checkout {
			found = append(found, e.Name())
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		return filepath.Join(parent, found[0])
	}
	return ""
}

// Inside is the test folder inside the source code (made if needed).
func Inside(checkout string) string {
	ents, _ := os.ReadDir(checkout)
	var found []string
	for _, e := range ents {
		if IsName(e.Name()) && isDir(filepath.Join(checkout, e.Name())) {
			found = append(found, e.Name())
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		return filepath.Join(checkout, found[0])
	}
	return filepath.Join(checkout, Name)
}

// For is the test folder for the source code at checkout.
func For(checkout string) string {
	if t := FromEnv(); t != "" {
		return t
	}
	if t := Beside(checkout); t != "" {
		return t
	}
	return Inside(checkout)
}

// Checkout finds the source code from dir (a test runs in its package's
// folder), or "".
func Checkout(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if isCheckout(d) {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return ""
		}
		d = up
	}
}

func isCheckout(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); strings.HasPrefix(l, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "module ")) == "github.com/vajra-wielder/nabu"
		}
	}
	return false
}

// ForTests is the test folder for a test running in the source code, if
// it has dumps (a "wikipedia" folder), else "".
func ForTests() string {
	c := Checkout(".")
	if c == "" {
		return FromEnv()
	}
	t := For(c)
	if st, err := os.Stat(filepath.Join(t, "wikipedia")); err == nil && st.IsDir() {
		return t
	}
	return ""
}

// Asked reports whether the tests were run with -run naming word: the
// tests on real data run only when asked for by name, so "go test ./..."
// stays quick.
func Asked(word string) bool {
	f := flag.Lookup("test.run")
	return f != nil && strings.Contains(f.Value.String(), word)
}

// isDir follows links: a folder, or a link to one.
func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
