package bzip2w

import (
	"bytes"
	"compress/bzip2"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	z := Compress(data)
	got, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(z)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("%s: %d bytes in, %d out, not equal", name, len(data), len(got))
	}
	return z
}

func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	random := make([]byte, 300_000)
	r.Read(random)
	letters := make([]byte, 200_000)
	for i := range letters {
		letters[i] = "abcde fghij\n"[r.Intn(12)]
	}
	runs := make([]byte, 0, 2_000_000)
	for len(runs) < 2_000_000 { // runs of every length, across block ends
		runs = append(runs, bytes.Repeat([]byte{byte(r.Intn(3))}, 1+r.Intn(600))...)
	}
	cases := map[string][]byte{
		"empty":     {},
		"one":       {'x'},
		"two":       []byte("ab"),
		"same":      bytes.Repeat([]byte{'a'}, 1000),
		"four":      []byte("aaaa"),
		"periodic":  bytes.Repeat([]byte("ab"), 50_000),
		"periodic3": bytes.Repeat([]byte("abc"), 333_333),
		"every byte": func() []byte {
			b := make([]byte, 256*40)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(),
		"random":         random,
		"letters":        letters,
		"runs":           runs,
		"wiki-like":      []byte(strings.Repeat("<page>\n  <title>Babylon</title>\n  <ns>0</ns>\n  <text>'''Babylon''' was a city.</text>\n</page>\n", 30_000)),
		"several blocks": append(append([]byte{}, letters...), append(random, letters...)...),
	}
	for name, data := range cases {
		roundTrip(t, name, data)
	}
}

// On real dump streams it compresses as well as the reference bzip2
// (NABU_REAL_DUMPS: a folder with a multistream dump).
func TestAsGoodAsReference(t *testing.T) {
	dir := os.Getenv("NABU_REAL_DUMPS")
	if dir == "" {
		t.Skip("set NABU_REAL_DUMPS")
	}
	m, _ := filepathGlob(dir + "/*-multistream.xml.bz2")
	if len(m) == 0 {
		t.Skip("no dump")
	}
	f, _ := os.Open(m[0])
	defer f.Close()
	head := make([]byte, 20<<20)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	// Cut at stream starts after the first (site information) stream.
	magic := []byte("BZh91AY&SY")
	var starts []int
	for i := 1; i < len(head); {
		j := bytes.Index(head[i:], magic)
		if j < 0 {
			break
		}
		starts = append(starts, i+j)
		i += j + 1
	}
	var ref, ours int
	for k := 0; k+1 < len(starts) && k < 40; k++ {
		orig := head[starts[k]:starts[k+1]]
		plain, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(orig)))
		if err != nil {
			continue
		}
		ref += len(orig)
		ours += len(roundTrip(t, "stream", plain))
	}
	t.Logf("40 streams: reference %d bytes, ours %d (%.2f%%)", ref, ours, 100*float64(ours)/float64(ref))
	if float64(ours) > float64(ref)*1.02 {
		t.Errorf("more than 2%% bigger than the reference")
	}
}

func filepathGlob(p string) ([]string, error) { return filepathGlobFn(p) }

var filepathGlobFn = filepath.Glob

// Both ways of sorting rotations agree on what they must agree on: the
// rotations come out in order.
func TestRotationsSorted(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, s := range []string{"banana", "abababab", "a", "zzzzzzzzzz", "mississippi"} {
		for _, p := range [][]int32{rotations([]byte(s)), doubling([]byte(s))} {
			checkSorted(t, []byte(s), p)
		}
	}
	b := make([]byte, 5000)
	for i := range b {
		b[i] = "ab"[r.Intn(2)]
	}
	checkSorted(t, b, rotations(b))
	if readSuffixes([]byte("junk"), 3) != nil {
		t.Fatal("junk accepted")
	}
}

func checkSorted(t *testing.T, s []byte, p []int32) {
	t.Helper()
	rot := func(i int32) string { return string(s[i:]) + string(s[:i]) }
	if len(p) != len(s) {
		t.Fatalf("%q: %d rotations", s, len(p))
	}
	for i := 1; i < len(p); i++ {
		if rot(p[i-1]) > rot(p[i]) {
			t.Fatalf("%q: rotations out of order at %d", s, i)
		}
	}
}
