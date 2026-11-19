package pictures

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"
)

func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := uint8(r.Intn(40))
			img.Set(x, y, color.RGBA{uint8(x) + n, uint8(y) + n, 128 + n, 255})
		}
	}
	return img
}

func TestShrink(t *testing.T) {
	var j bytes.Buffer
	jpeg.Encode(&j, photo(250, 180), &jpeg.Options{Quality: 95})
	if s := Shrink(j.Bytes()); len(s) >= j.Len()*9/10 || s[0] != 0xFF {
		t.Errorf("photo %d -> %d", j.Len(), len(s))
	}
	// A photo stored as PNG becomes a JPEG.
	var p bytes.Buffer
	png.Encode(&p, photo(250, 180))
	if s := Shrink(p.Bytes()); len(s) > p.Len()/2 || s[0] != 0xFF {
		t.Errorf("PNG photo %d -> %d", p.Len(), len(s))
	}
	// A flat drawing stays PNG; so does anything transparent.
	flat := image.NewRGBA(image.Rect(0, 0, 250, 180))
	for i := range flat.Pix {
		flat.Pix[i] = 255
	}
	p.Reset()
	png.Encode(&p, flat)
	if s := Shrink(p.Bytes()); !bytes.Equal(s, p.Bytes()) {
		t.Error("flat drawing was changed")
	}
	clear := photo(250, 180)
	clear.Pix[3] = 0
	p.Reset()
	png.Encode(&p, clear)
	if s := Shrink(p.Bytes()); !bytes.Equal(s, p.Bytes()) {
		t.Error("transparent picture was changed")
	}
	for _, junk := range [][]byte{nil, []byte("GIF89a..."), {0xFF, 0xD8, 0xFF, 0, 1}} {
		if s := Shrink(junk); !bytes.Equal(s, junk) {
			t.Errorf("%q was changed", junk)
		}
	}
}
