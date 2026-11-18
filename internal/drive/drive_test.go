package drive

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateOldDrive(t *testing.T) {
	root := t.TempDir()
	dump := "xxwiki-20261101-pages-articles-multistream.xml.bz2"
	write(t, filepath.Join(root, "library", dump), "d")
	write(t, filepath.Join(root, "library", ".nabu", "index", "xxwiki-20261101.nidx"), "i")
	write(t, filepath.Join(root, "library", ".nabu", "index", "xxwiki-20261101.nfts"), "f")
	write(t, filepath.Join(root, "library", ".nabu", "reader.json"), "r")
	write(t, filepath.Join(root, "library", ".nabu", "settings.json"), "s")
	write(t, filepath.Join(root, "library", ".nabu", "fulltext.json"), "w")
	write(t, filepath.Join(root, "library", ".nabu", "incoming", "xxwiki-20261201", "release.json"), "{}")
	write(t, filepath.Join(root, "library", ".nabu", "tmp", "junk"), "j")
	write(t, filepath.Join(root, "library", "collections", "Gods", "collection.json"), "{}")
	write(t, filepath.Join(root, "nabu", "nabu-linux-amd64"), "old program")
	write(t, filepath.Join(root, "programs", "linux", "nabu-linux-amd64"), "new program")

	d := At(root)
	if done := d.Migrate(); len(done) < 3 {
		t.Fatalf("migration did too little: %q", done)
	}
	for _, p := range []string{
		filepath.Join(d.Wikipedia, dump),
		d.TitleIndex("xxwiki-20261101"),
		d.FullText("xxwiki-20261101"),
		filepath.Join(d.Search, "wanted.json"),
		d.PersonalFile("reader.json"),
		d.PersonalFile("settings.json"),
		filepath.Join(d.Incoming(), "xxwiki-20261201", "release.json"),
		filepath.Join(d.Collections, "Gods", "collection.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not moved: %s", p)
		}
	}
	for _, p := range []string{filepath.Join(root, "library"), filepath.Join(d.Wikipedia, ".nabu"), filepath.Join(root, "nabu")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("left behind: %s", p)
		}
	}
	if done := d.Migrate(); len(done) != 0 {
		t.Errorf("second migration did %q", done)
	}
	d.WriteNotes(true)
	if _, err := os.Stat(filepath.Join(d.Pictures, AboutFile)); err != nil {
		t.Error("no note in pictures/")
	}
	if _, err := os.Stat(filepath.Join(root, Source)); err == nil {
		t.Error("source/ created")
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "wikipedia"), 0o755)
	if d := Find(root, ""); d.Wikipedia != filepath.Join(root, "wikipedia") || d.Personal != filepath.Join(root, "personal") {
		t.Fatalf("--drive: %+v", d)
	}
	if d := Find("", filepath.Join(root, "library")); d.Root != root || d.Wikipedia != filepath.Join(root, "library") {
		t.Fatalf("--library .../library: %+v", d)
	}
	other := filepath.Join(root, "dumps")
	if d := Find("", other); d.Root != other || d.Wikipedia != other || d.Indexes != filepath.Join(other, "indexes") {
		t.Fatalf("--library elsewhere: %+v", d)
	}
}
