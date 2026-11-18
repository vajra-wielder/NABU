package collect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	s := New(t.TempDir())
	c, err := s.Create("  Mesopotamia:  cities / gods? ")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Mesopotamia: cities / gods?" || strings.ContainsAny(c.Slug, `/\:?*"<>|`) {
		t.Fatalf("name %q slug %q", c.Name, c.Slug)
	}
	c2, _ := s.Create("Mesopotamia: cities / gods?")
	if c2.Slug == c.Slug {
		t.Fatal("two collections share a folder")
	}
	it := Item{Book: "enwiki", Title: "AC/DC: Live?", Dump: "20261101"}
	c, err = s.Add(c.Slug, it, []byte("<html>v1</html>"), []byte("'''AC/DC'''"))
	if err != nil {
		t.Fatal(err)
	}
	f := c.Items[0].File
	if strings.ContainsAny(f, `/\:?*"<>|`) || !strings.HasSuffix(f, ".html") {
		t.Fatalf("unsafe file name %q", f)
	}
	// Same title in different case gets a different file (FAT32, macOS).
	c, _ = s.Add(c.Slug, Item{Book: "enwiki", Title: "Ac/dc: live?"}, []byte("x"), nil)
	if strings.EqualFold(c.Items[0].File, c.Items[1].File) {
		t.Fatal("file names collide when case is ignored")
	}
	// Saving again replaces the copy, keeping its file.
	c, _ = s.Add(c.Slug, it, []byte("<html>v2</html>"), nil)
	if len(c.Items) != 2 || c.Items[0].File != f {
		t.Fatalf("re-save: %+v", c.Items)
	}
	dir := filepath.Join(s.Dir, c.Slug)
	if b, _ := os.ReadFile(filepath.Join(dir, f)); string(b) != "<html>v2</html>" {
		t.Fatalf("copy not replaced: %s", b)
	}
	for _, name := range []string{"collection.json", "collection.js", "index.html", strings.TrimSuffix(f, ".html") + ".wiki"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing", name)
		}
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if !strings.Contains(string(idx), "AC/DC: Live?") || !strings.Contains(string(idx), "2 articles") {
		t.Errorf("index.html: %s", idx)
	}
	js, _ := os.ReadFile(filepath.Join(dir, "collection.js"))
	if !strings.Contains(string(js), `"enwiki|AC/DC: Live?":"`+f+`"`) {
		t.Errorf("collection.js: %s", js)
	}
	// Paths cannot leave the collection.
	for _, bad := range []string{"../x", "..", ".hidden", "a/b"} {
		if _, err := s.File(c.Slug, bad); err == nil {
			t.Errorf("File accepted %q", bad)
		}
	}
	if _, err := s.Get("../" + c.Slug); err == nil {
		t.Error("Get accepted a path")
	}
	if err := s.Remove(c.Slug, "enwiki", "AC/DC: Live?"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
		t.Fatal("removed copy still on disk")
	}
	if err := s.Rename(c.Slug, "Ancient Iraq"); err != nil {
		t.Fatal(err)
	}
	if list := s.List(); len(list) != 2 || list[0].Name != "Ancient Iraq" {
		t.Fatalf("list after rename: %+v", list)
	}
	if err := s.Delete(c.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("deleted collection folder remains")
	}
}
