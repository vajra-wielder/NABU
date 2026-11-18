// Package drive describes how NABU's files are laid out on the drive.
//
// Every part lives in its own visible folder, so a smaller copy can be
// made just by leaving folders out:
//
//	NABU/
//	  Start NABU (…)     launchers
//	  programs/          one folder per system: windows, mac, linux
//	  wikipedia/         the dumps, two files per language   (needed)
//	  indexes/           title indexes, rebuilt when missing
//	  search/            search inside articles               (optional)
//	  pictures/          offline pictures, one folder per wiki (optional)
//	  collections/       collections of saved articles         (optional)
//	  personal/          saved articles, history, settings     (leave out to share)
//	  .work/             temporary files, safe to delete
package drive

import (
	"os"
	"path/filepath"
	"strings"
)

// Folder names, in the order they are shown.
const (
	Programs    = "programs"
	Wikipedia   = "wikipedia"
	Indexes     = "indexes"
	Search      = "search"
	Pictures    = "pictures"
	Collections = "collections"
	Personal    = "personal"
	Work        = ".work"
	Source      = "source"
	Licenses    = "licenses"
)

// Layout is where each part is.
type Layout struct {
	Root        string
	Wikipedia   string
	Indexes     string
	Search      string
	Pictures    string
	Collections string
	Personal    string
	Work        string
}

// At is the standard layout of a NABU folder.
func At(root string) *Layout {
	root = abs(root)
	j := func(n string) string { return filepath.Join(root, n) }
	return &Layout{Root: root, Wikipedia: j(Wikipedia), Indexes: j(Indexes), Search: j(Search),
		Pictures: j(Pictures), Collections: j(Collections), Personal: j(Personal), Work: j(Work)}
}

// ForLibrary is the layout around a folder of dumps given with
// --library. A folder named "wikipedia" or "library" is taken to be
// inside a NABU folder; any other folder keeps NABU's files inside it.
func ForLibrary(dir string) *Layout {
	dir = abs(dir)
	switch strings.ToLower(filepath.Base(dir)) {
	case Wikipedia, "library":
		l := At(filepath.Dir(dir))
		l.Wikipedia = dir
		return l
	}
	l := At(dir)
	l.Wikipedia = dir
	return l
}

// TitleIndex is the title index of a dump.
func (l *Layout) TitleIndex(key string) string { return filepath.Join(l.Indexes, key+".nidx") }

// FullText is the search-inside-articles index of a dump.
func (l *Layout) FullText(key string) string { return filepath.Join(l.Search, key+".nfts") }

// Temp is a scratch folder for building name.
func (l *Layout) Temp(name string) string { return filepath.Join(l.Work, "tmp", name) }

// Incoming holds downloads in progress.
func (l *Layout) Incoming() string { return filepath.Join(l.Work, "incoming") }

// PersonalFile is a file of the reader's own (saved, history, settings).
func (l *Layout) PersonalFile(name string) string { return filepath.Join(l.Personal, name) }

// Find works out the layout from the command-line flags, or from where
// the program is: programs/<system>/nabu… inside the NABU folder, the
// older nabu/nabu-… layout, or the current folder.
func Find(driveFlag, libraryFlag string) *Layout {
	switch {
	case driveFlag != "":
		return At(driveFlag)
	case libraryFlag != "":
		return ForLibrary(libraryFlag)
	}
	var roots []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		d := filepath.Dir(exe)
		roots = append(roots, filepath.Dir(filepath.Dir(d)), filepath.Dir(d), d)
	}
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd)
	}
	for _, r := range roots {
		if isDir(filepath.Join(r, Wikipedia)) || isDir(filepath.Join(r, "library")) {
			return At(r)
		}
	}
	for _, r := range roots {
		if isDir(filepath.Join(r, Programs)) {
			return At(r)
		}
	}
	if len(roots) > 0 {
		return At(roots[len(roots)-1])
	}
	return At(".")
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
