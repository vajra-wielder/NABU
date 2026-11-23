package pictures

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"ishtar gate.jpg": "Ishtar_gate.jpg", " Flag of  Iraq.svg ": "Flag_of_Iraq.svg", "élan.png": "Élan.png"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Key("ishtar gate.jpg") != Key("Ishtar_gate.jpg") {
		t.Error("keys differ for the same file")
	}
}

func TestStore(t *testing.T) {
	old := maxPack
	maxPack = 25_000 // several packs
	defer func() { maxPack = old }()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pic := func(i int) []byte { return bytes.Repeat([]byte{byte(i)}, 1000+i) }
	for i := 0; i < 60; i++ {
		if err := s.Put(fmt.Sprintf("Picture %d.jpg", i), pic(i)); err != nil {
			t.Fatal(err)
		}
	}
	s.Put("Gone.jpg", nil)
	gone := true
	check := func(s *Store, upto int) {
		t.Helper()
		for i := 0; i < upto; i++ {
			b, known, err := s.Get(fmt.Sprintf("picture_%d.jpg", i))
			if err != nil || !known || !bytes.Equal(b, pic(i)) {
				t.Fatalf("picture %d: known=%v err=%v len=%d", i, known, err, len(b))
			}
		}
		if b, known, _ := s.Get("Gone.jpg"); gone && (!known || b != nil) {
			t.Fatal("missing picture not remembered")
		}
		if _, known, _ := s.Get("Never asked.jpg"); known {
			t.Fatal("unknown picture known")
		}
	}
	check(s, 60)
	if st := s.Stats(); st.Pictures != 60 {
		t.Fatalf("stats %+v", st)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	check(s, 60)
	for i := 60; i < 80; i++ { // some in the index, some recent
		s.Put(fmt.Sprintf("Picture %d.jpg", i), pic(i))
	}
	s.Put("Gone.jpg", []byte("found after all"))
	s.Close()

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Stats(); st.Pictures != 81 {
		t.Fatalf("after reopen %+v", st)
	}
	gone = false
	if b, _, _ := s.Get("Gone.jpg"); string(b) != "found after all" {
		t.Fatalf("Gone.jpg = %q", b)
	}
	if len(s.packs) < 3 {
		t.Fatalf("only %d packs", len(s.packs))
	}
	// A drive pulled out mid-write: the last entry points past the data.
	s.Put("Half written.jpg", pic(1))
	s.mu.Lock()
	cur := s.packs[s.cur]
	s.mu.Unlock()
	st, _ := cur.Stat()
	cur.Truncate(st.Size() - 10)
	s.log.Close()
	s.log = nil
	for _, f := range s.packs {
		f.Close()
	}
	if s.index != nil { // Windows can't delete the folder while it is open
		s.index.Close()
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, known, _ := s.Get("Half written.jpg"); known {
		t.Fatal("half-written picture accepted")
	}
	check(s, 80)
	s.Close()
	if _, err := os.Stat(s.recentPath()); err != nil {
		t.Fatal(err)
	}
}
