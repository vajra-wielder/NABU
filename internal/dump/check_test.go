package dump

import (
	"os"
	"path/filepath"
	"testing"
)

func copyDump(t *testing.T, f *Files) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range f.Pairs {
		for _, x := range append(append([]string{}, p.Data...), p.Index) {
			b, err := os.ReadFile(x)
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, filepath.Base(x)), b, 0o644)
		}
	}
	return dir
}

func TestCheckAndRepair(t *testing.T) {
	// In one file, and in FAT32-style pieces (a damaged bundle may lie
	// across two of them).
	for _, part := range []int64{0, 3000} {
		good := scanOne(t, "../../testdata", "testwiki")
		if part > 0 {
			dir := t.TempDir()
			if _, err := Slim(good, dir, SlimOptions{PartSize: part, Bundle: 2000}, nil); err != nil {
				t.Fatal(err)
			}
			good = scanOne(t, dir, "testwiki")
		}
		f := scanOne(t, copyDump(t, good), "testwiki")
		r := openBig(t, f)
		if res, err := r.Check(CheckOptions{}); err != nil || len(res.Damaged) != 0 || res.Index != "" || res.Streams < 2 {
			t.Fatalf("clean dump: %+v %v", res, err)
		}
		// Damage one byte in the middle of the second bundle.
		_, start, end, _ := r.Index.streamRange(1)
		if err := writeData(f.Pairs[0].Data, (start+end)/2, []byte{0x5a ^ 0xff}); err != nil {
			t.Fatal(err)
		}
		r.streams = newStreamCache(1 << 20)
		res, err := r.Check(CheckOptions{Workers: 2})
		if err != nil || len(res.Damaged) != 1 || len(res.Damaged[0].Titles) == 0 || res.Damaged[0].Start != start {
			t.Fatalf("damaged dump: %+v %v", res, err)
		}
		title := res.Damaged[0].Titles[0]
		if _, _, err := r.Lookup(title); err == nil {
			t.Fatalf("%s reads although damaged", title)
		}
		// Repaired from the other copy, it reads again.
		n, err := Repair(f, res.Damaged, good)
		if err != nil || n != 1 {
			t.Fatalf("repair: %d %v", n, err)
		}
		r.streams = newStreamCache(1 << 20)
		if res, _ := r.Check(CheckOptions{}); len(res.Damaged) != 0 {
			t.Fatalf("still damaged: %+v", res)
		}
		if p, _, err := r.Lookup(title); err != nil || p.Title != title {
			t.Fatalf("%s after repair: %v", title, err)
		}
	}
	// Another dump is not a source of repairs.
	a := scanOne(t, "../../testdata", "testwiki")
	b := scanOne(t, "../../testdata", "fawiki")
	if SameDump(a, b) {
		t.Fatal("different dumps taken for the same")
	}
	if _, err := Repair(a, []Damage{{Start: 0, End: 10}}, b); err == nil {
		t.Fatal("repaired from another dump")
	}
}
