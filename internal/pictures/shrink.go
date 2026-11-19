package pictures

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
)

// Qualities pictures are kept at. Wikimedia's thumbnails are made at a
// high quality; at 250 pixels wide, 60 looks the same on a screen and
// takes about two thirds of the room. Pictures that came as PNG are
// mostly maps and diagrams, whose lines and small text need a little
// more.
const (
	Quality    = 60
	PNGQuality = 70
)

// ShrinkFactor is what Shrink leaves of the pictures' size, measured on
// 155 real pictures (64%) and 23 random English main pictures (67%).
const ShrinkFactor = 0.66

// Shrink makes a picture smaller before it is kept:
//
//   - a JPEG photo is recompressed;
//   - an opaque PNG becomes a JPEG when that halves it or better: that is
//     a photo or a painted map stored as PNG. Drawings with flat colours
//     stay PNG (JPEG would not halve them), and so do pictures with
//     transparent parts and GIFs (which may move).
//
// Anything that does not decode, or would barely shrink, is kept as it
// came.
func Shrink(data []byte) []byte {
	isJPEG := len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF
	isPNG := len(data) > 8 && string(data[1:4]) == "PNG"
	if !isJPEG && !isPNG {
		return data
	}
	var cfg image.Config
	var err error
	if isJPEG {
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	} else {
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	}
	if err != nil || cfg.Width*cfg.Height > 4000*4000 {
		return data
	}
	var img image.Image
	q, limit := Quality, len(data)*9/10 // not worth a generation of loss for less
	if isJPEG {
		img, err = jpeg.Decode(bytes.NewReader(data))
		if _, cmyk := img.(*image.CMYK); cmyk {
			return data // re-encoding would change its colours
		}
	} else {
		img, err = png.Decode(bytes.NewReader(data))
		if o, ok := img.(interface{ Opaque() bool }); err == nil && (!ok || !o.Opaque()) {
			return data
		}
		q, limit = PNGQuality, len(data)/2
	}
	if err != nil {
		return data
	}
	var out bytes.Buffer
	out.Grow(len(data) / 2)
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: q}); err != nil || out.Len() > limit {
		return data
	}
	return out.Bytes()
}
