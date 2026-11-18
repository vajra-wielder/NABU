package drive

import (
	"os"
	"path/filepath"
	"strings"
)

// Migrate moves a drive set up by an earlier NABU (everything inside
// library/, NABU's own files in library/.nabu/, programs in nabu/) to
// the current layout. Only renames are used, so it is instant and
// nothing is copied. It returns what it did.
func (l *Layout) Migrate() (done []string) {
	note := func(s string) { done = append(done, s) }
	oldLib := filepath.Join(l.Root, "library")
	if l.Wikipedia == filepath.Join(l.Root, Wikipedia) && isDir(oldLib) {
		if !isDir(l.Wikipedia) {
			if os.Rename(oldLib, l.Wikipedia) == nil {
				note("library/ → wikipedia/")
			}
		} else {
			moveAll(oldLib, l.Wikipedia, func(string) bool { return true })
			os.Remove(oldLib)
			note("library/ → wikipedia/")
		}
	}
	old := filepath.Join(l.Wikipedia, ".nabu")
	if isDir(old) {
		moveAll(filepath.Join(old, "index"), l.Indexes, func(n string) bool { return strings.HasSuffix(n, ".nidx") })
		moveAll(filepath.Join(old, "index"), l.Search, func(n string) bool { return strings.HasSuffix(n, ".nfts") })
		move(filepath.Join(old, "fulltext.json"), filepath.Join(l.Search, "wanted.json"))
		move(filepath.Join(old, "reader.json"), l.PersonalFile("reader.json"))
		move(filepath.Join(old, "settings.json"), l.PersonalFile("settings.json"))
		move(filepath.Join(old, "releases.json"), filepath.Join(l.Work, "releases.json"))
		moveAll(filepath.Join(old, "incoming"), l.Incoming(), func(string) bool { return true })
		os.RemoveAll(old) // what is left is temporary
		note("NABU's files sorted into indexes/, search/, personal/")
	}
	if c := filepath.Join(l.Wikipedia, "collections"); isDir(c) && c != l.Collections {
		if !isDir(l.Collections) && os.MkdirAll(filepath.Dir(l.Collections), 0o755) == nil && os.Rename(c, l.Collections) == nil {
			note("collections moved to the top of the drive")
		}
	}
	// Programs of an earlier version, replaced by programs/.
	if oldBin := filepath.Join(l.Root, "nabu"); isDir(oldBin) && isDir(filepath.Join(l.Root, Programs)) {
		ents, _ := os.ReadDir(oldBin)
		ours := true
		for _, e := range ents {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "nabu-") {
				ours = false
			}
		}
		if ours && os.RemoveAll(oldBin) == nil {
			note("old programs folder nabu/ removed (now in programs/)")
		}
	}
	return done
}

func move(from, to string) {
	if _, err := os.Stat(from); err != nil {
		return
	}
	if _, err := os.Stat(to); err == nil {
		return // keep what is there
	}
	os.MkdirAll(filepath.Dir(to), 0o755)
	os.Rename(from, to)
}

func moveAll(fromDir, toDir string, ok func(string) bool) {
	ents, err := os.ReadDir(fromDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if ok(e.Name()) {
			move(filepath.Join(fromDir, e.Name()), filepath.Join(toDir, e.Name()))
		}
	}
	os.Remove(fromDir) // only if empty
}
