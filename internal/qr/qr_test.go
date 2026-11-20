package qr

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var samples = []string{
	"http://192.168.1.23:8080/",
	"http://10.0.0.5:8081/",
	"http://172.16.254.199:8099/simplewiki/wiki/First_aid",
	"A",
	strings.Repeat("x", 106),
}

func TestEncode(t *testing.T) {
	for _, s := range samples {
		c, err := Encode(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if c.Size < 21 || c.Size > 41 || (c.Size-17)%4 != 0 {
			t.Fatalf("%q: size %d", s, c.Size)
		}
		// The three finder patterns: dark corners, light separators.
		for _, p := range [][2]int{{0, 0}, {c.Size - 1, 0}, {0, c.Size - 1}} {
			if !c.Modules[p[1]][p[0]] {
				t.Fatalf("%q: no finder at %v", s, p)
			}
		}
		if !strings.HasPrefix(c.SVG(200), "<svg") {
			t.Fatal("svg")
		}
	}
	if _, err := Encode(strings.Repeat("x", 107)); err != ErrTooLong {
		t.Fatal("too long accepted")
	}
	// Reed–Solomon: the example of the QR standard's annex (version 1-M,
	// "01234567"): data codewords and their 10 error correction ones.
	data := []byte{0x10, 0x20, 0x0C, 0x56, 0x61, 0x80, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11}
	want := []byte{0xA5, 0x24, 0xD4, 0xC1, 0xED, 0x36, 0xC7, 0x87, 0x2C, 0x55}
	if got := reedSolomon(data, 10); string(got) != string(want) {
		t.Fatalf("Reed-Solomon % X", got)
	}
}

// TestWriteImages writes the samples as pictures for an independent
// reader to decode (QR_IMAGES: a folder).
func TestWriteImages(t *testing.T) {
	dir := os.Getenv("QR_IMAGES")
	if dir == "" {
		t.Skip("set QR_IMAGES")
	}
	for i, s := range samples {
		c, _ := Encode(s)
		const scale, quiet = 8, 4
		n := (c.Size + 2*quiet) * scale
		img := image.NewGray(image.Rect(0, 0, n, n))
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				mx, my := x/scale-quiet, y/scale-quiet
				v := uint8(255)
				if mx >= 0 && my >= 0 && mx < c.Size && my < c.Size && c.Modules[my][mx] {
					v = 0
				}
				img.SetGray(x, y, color.Gray{v})
			}
		}
		f, _ := os.Create(filepath.Join(dir, strconv.Itoa(i)+".png"))
		png.Encode(f, img)
		f.Close()
		os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".txt"), []byte(s), 0o644)
	}
}
