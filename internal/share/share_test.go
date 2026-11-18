package share

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopy(t *testing.T) {
	root := t.TempDir()
	d := drive.At(root)
	write(t, filepath.Join(root, "Start NABU (Windows).bat"), "bat")
	write(t, filepath.Join(root, "start-nabu-linux.sh"), "sh")
	write(t, filepath.Join(root, "READ ME FIRST.txt"), "readme")
	write(t, filepath.Join(root, "licenses", "NABU-MIT.txt"), "mit")
	write(t, filepath.Join(root, "programs", "windows", "nabu-windows-amd64.exe"), "win")
	write(t, filepath.Join(root, "programs", "linux", "nabu-linux-amd64"), "linux")
	for _, db := range []string{"enwiki", "dawiki"} {
		write(t, filepath.Join(d.Wikipedia, db+"-20261101-pages-articles-multistream.xml.bz2"), db+" data")
		write(t, filepath.Join(d.Wikipedia, db+"-20261101-pages-articles-multistream-index.txt.bz2"), db+" index")
		write(t, d.TitleIndex(db+"-20261101"), "nidx")
		write(t, d.FullText(db+"-20261101"), "nfts")
		write(t, filepath.Join(d.Pictures, db, "pictures-000.pack"), "pack")
	}
	write(t, filepath.Join(d.Wikipedia, "dawiki-20261001-pages-articles-multistream.xml.bz2"), "older")
	write(t, filepath.Join(d.Wikipedia, "dawiki-20261001-pages-articles-multistream-index.txt.bz2"), "older")
	write(t, d.PersonalFile("reader.json"), "my history")
	write(t, filepath.Join(d.Collections, "Gods", "index.html"), "gods")
	d.WriteNotes(false)

	parts, files := Inventory(d)
	if len(parts) == 0 {
		t.Fatal("nothing found")
	}
	sel := Select(files, Options{Wikis: []string{"dawiki"}, Systems: []string{"linux"}, Indexes: true, Pictures: true, Collections: true})
	dest := filepath.Join(t.TempDir(), "NABU")
	var c Copier
	if err := c.Start(d, filepath.Join(root, "wikipedia", "x"), sel); err == nil {
		t.Fatal("copied into itself")
	}
	if err := c.Start(d, dest, sel); err != nil {
		t.Fatal(err)
	}
	wait := func() Progress {
		for i := 0; i < 200; i++ {
			if p := c.Status(); p.State != "copying" {
				return p
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("copy never finished")
		return Progress{}
	}
	if p := wait(); p.State != "done" || p.Done != p.Total {
		t.Fatalf("copy: %+v", p)
	}
	for _, want := range []string{
		"Start NABU (Windows).bat", "start-nabu-linux.sh", "READ ME FIRST.txt", "licenses/NABU-MIT.txt",
		"programs/linux/nabu-linux-amd64",
		"wikipedia/dawiki-20261101-pages-articles-multistream.xml.bz2", "indexes/dawiki-20261101.nidx",
		"pictures/dawiki/pictures-000.pack", "collections/Gods/index.html", "wikipedia/" + drive.AboutFile,
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("missing in the copy: %s", want)
		}
	}
	for _, not := range []string{
		"programs/windows", "personal/reader.json", "search/dawiki-20261101.nfts",
		"wikipedia/enwiki-20261101-pages-articles-multistream.xml.bz2", "wikipedia/dawiki-20261001-pages-articles-multistream.xml.bz2",
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(not))); err == nil {
			t.Errorf("should not be in the copy: %s", not)
		}
	}
	if st, _ := os.Stat(filepath.Join(dest, "start-nabu-linux.sh")); st.Mode()&0o100 == 0 {
		t.Error("launcher not executable")
	}
	// Copying again only copies what changed.
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(d.Collections, "Gods", "index.html"), "gods, updated")
	_, files = Inventory(d)
	sel = Select(files, Options{Wikis: []string{"dawiki"}, Systems: []string{"linux"}, Indexes: true, Pictures: true, Collections: true})
	if err := c.Start(d, dest, sel); err != nil {
		t.Fatal(err)
	}
	p := wait()
	if p.State != "done" || p.Skipped < p.Total-int64(len("gods, updated")) {
		t.Fatalf("second copy: %+v", p)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "collections", "Gods", "index.html")); !strings.Contains(string(b), "updated") {
		t.Error("changed file not copied")
	}
}
