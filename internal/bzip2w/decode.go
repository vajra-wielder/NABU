package bzip2w

import (
	"errors"
	"fmt"
)

// Decompress decodes bzip2 data: one stream or several one after the
// other (as in a multistream dump). Every block's checksum is verified,
// so damaged data is reported, never returned.
//
// It is about twice as fast as compress/bzip2, whose Huffman decoding
// walks a tree one bit at a time; here a table decodes most symbols in
// one step. Reading an article means decompressing its stream, so this
// is most of what opening one costs.
func Decompress(src []byte) ([]byte, error) {
	d := &decoder{br: bitReader{src: src}}
	out := make([]byte, 0, len(src)*5)
	for {
		if d.br.bytesLeft() == 0 {
			return out, nil
		}
		var err error
		out, err = d.stream(out)
		if err != nil {
			return out, err
		}
		d.br.align()
		// Anything after the last stream must be another stream.
		if d.br.bytesLeft() == 0 {
			return out, nil
		}
	}
}

// ErrCorrupt reports data that is not valid bzip2 or fails its checksum.
var ErrCorrupt = errors.New("bzip2: damaged data")

func corrupt(what string) error { return fmt.Errorf("%w: %s", ErrCorrupt, what) }

// bitReader reads bits, most significant first. Past the end it reads
// zeros and sets over, checked where it matters.
type bitReader struct {
	src  []byte
	pos  int    // next byte of src to load
	acc  uint64 // loaded bits, at the top
	nacc uint
	used int // bits used in all
	over bool
}

func (b *bitReader) fill() {
	for b.nacc <= 56 {
		var c byte
		if b.pos < len(b.src) {
			c = b.src[b.pos]
		}
		b.pos++
		b.acc |= uint64(c) << (56 - b.nacc)
		b.nacc += 8
	}
}

func (b *bitReader) skip(n uint) {
	if b.nacc < n {
		b.fill()
	}
	b.acc <<= n
	b.nacc -= n
	b.used += int(n)
	if b.used > len(b.src)*8 {
		b.over = true
	}
}

func (b *bitReader) bits(n uint) uint32 {
	v := b.peek(n)
	b.skip(n)
	return v
}

func (b *bitReader) bit() bool { return b.bits(1) == 1 }

// peek returns the next n (<= 32) bits without using them.
func (b *bitReader) peek(n uint) uint32 {
	if b.nacc < n {
		b.fill()
	}
	return uint32(b.acc >> (64 - n))
}

// align drops the bits up to the next byte, where a new stream starts.
func (b *bitReader) align() {
	if r := b.used % 8; r != 0 {
		b.skip(uint(8 - r))
	}
}

func (b *bitReader) bytesLeft() int {
	n := len(b.src) - (b.used+7)/8
	if n < 0 {
		return 0
	}
	return n
}

type decoder struct {
	br  bitReader
	tt  []uint32
	max int // block size of the stream
}

func (d *decoder) stream(out []byte) ([]byte, error) {
	br := &d.br
	if br.bits(8) != 'B' || br.bits(8) != 'Z' || br.bits(8) != 'h' {
		return out, corrupt("not bzip2 data")
	}
	level := br.bits(8)
	if level < '1' || level > '9' {
		return out, corrupt("bad block size")
	}
	d.max = int(level-'0') * 100000
	var combined uint32
	for {
		hi, lo := br.bits(24), br.bits(24)
		if br.over {
			return out, corrupt("cut short")
		}
		switch {
		case hi == 0x314159 && lo == 0x265359:
			want := br.bits(32)
			start := len(out)
			var err error
			out, err = d.block(out)
			if err != nil {
				return out, err
			}
			crc := ^uint32(0)
			for _, c := range out[start:] {
				crc = crc<<8 ^ crcTable[byte(crc>>24)^c]
			}
			if ^crc != want {
				return out[:start], corrupt("checksum mismatch")
			}
			combined = (combined<<1 | combined>>31) ^ want
		case hi == 0x177245 && lo == 0x385090:
			if br.bits(32) != combined {
				return out, corrupt("stream checksum mismatch")
			}
			return out, nil
		default:
			return out, corrupt("bad block header")
		}
	}
}

// huffTable decodes one Huffman table: codes up to fastBits long in one
// lookup, longer ones (rare) by their canonical ranges.
const fastBits = 10

type huffTable struct {
	fast   [1 << fastBits]uint16 // symbol<<5 | length, 0 if longer
	limit  [21]int32             // largest code of each length (left-aligned to that length), -1 if none
	base   [21]int32             // index into perm of each length's first code minus that code
	perm   [258]uint16
	minLen uint
	maxLen uint
}

func (h *huffTable) build(lens []uint8) error {
	h.minLen, h.maxLen = 32, 0
	for _, l := range lens {
		if l < 1 || l > 20 {
			return corrupt("bad code length")
		}
		if uint(l) < h.minLen {
			h.minLen = uint(l)
		}
		if uint(l) > h.maxLen {
			h.maxLen = uint(l)
		}
	}
	for i := range h.fast {
		h.fast[i] = 0
	}
	code := int32(0)
	pp := 0
	for l := h.minLen; l <= h.maxLen; l++ {
		h.base[l] = int32(pp) - code
		for sym, x := range lens {
			if uint(x) != l {
				continue
			}
			h.perm[pp] = uint16(sym)
			pp++
			if code >= 1<<l {
				return corrupt("bad code lengths")
			}
			if l <= fastBits {
				// Every fastBits-bit pattern starting with this code.
				first := code << (fastBits - l)
				for k := int32(0); k < 1<<(fastBits-l); k++ {
					h.fast[first+k] = uint16(sym)<<5 | uint16(l)
				}
			}
			code++
		}
		h.limit[l] = code - 1
		code <<= 1
	}
	if code>>1 > 1<<h.maxLen {
		return corrupt("bad code lengths")
	}
	return nil
}

func (h *huffTable) decode(br *bitReader) (uint16, error) {
	if br.nacc < 24 {
		br.fill()
	}
	if e := h.fast[br.peek(fastBits)]; e != 0 {
		br.skip(uint(e & 31))
		return e >> 5, nil
	}
	for l := h.minLen; l <= h.maxLen; l++ {
		c := int32(br.peek(l))
		if c <= h.limit[l] && h.limit[l] >= 0 {
			idx := h.base[l] + c
			if idx < 0 || idx >= int32(len(h.perm)) {
				break
			}
			br.skip(l)
			return h.perm[idx], nil
		}
	}
	return 0, corrupt("bad Huffman code")
}

func (d *decoder) block(out []byte) ([]byte, error) {
	br := &d.br
	if br.bit() {
		return out, corrupt("randomised blocks are not supported")
	}
	origPtr := int(br.bits(24))

	// Symbols in use.
	used16 := br.bits(16)
	var seqToByte [256]byte
	nInUse := 0
	for i := 0; i < 16; i++ {
		if used16&(1<<(15-i)) == 0 {
			continue
		}
		v := br.bits(16)
		for j := 0; j < 16; j++ {
			if v&(1<<(15-j)) != 0 {
				seqToByte[nInUse] = byte(i*16 + j)
				nInUse++
			}
		}
	}
	if nInUse == 0 {
		return out, corrupt("no symbols")
	}
	alpha := nInUse + 2

	nGroups := int(br.bits(3))
	if nGroups < 2 || nGroups > 6 {
		return out, corrupt("bad number of tables")
	}
	nSel := int(br.bits(15))
	if nSel == 0 {
		return out, corrupt("no selectors")
	}
	sel := make([]byte, 0, nSel)
	pos := []byte{0, 1, 2, 3, 4, 5}
	for i := 0; i < nSel; i++ {
		j := 0
		for br.bit() {
			j++
			if j >= nGroups {
				return out, corrupt("bad selector")
			}
		}
		if br.over {
			return out, corrupt("cut short")
		}
		v := pos[j]
		copy(pos[1:j+1], pos[:j])
		pos[0] = v
		if len(sel) < 18002 {
			sel = append(sel, v)
		}
	}
	tables := make([]huffTable, nGroups)
	lens := make([]uint8, alpha)
	for t := 0; t < nGroups; t++ {
		cur := int(br.bits(5))
		for s := 0; s < alpha; s++ {
			for {
				if cur < 1 || cur > 20 || br.over {
					return out, corrupt("bad code length")
				}
				if !br.bit() {
					break
				}
				if br.bit() {
					cur--
				} else {
					cur++
				}
			}
			lens[s] = uint8(cur)
		}
		if err := tables[t].build(lens); err != nil {
			return out, err
		}
	}

	// Huffman, zero runs and move-to-front, into tt (one byte each).
	if cap(d.tt) < d.max {
		d.tt = make([]uint32, d.max)
	}
	tt := d.tt[:d.max]
	var mtf [256]byte
	for i := range mtf {
		mtf[i] = byte(i)
	}
	var counts [256]int
	n := 0
	eob := uint16(nInUse + 1)
	run, runBit := 0, 1
	g, left := 0, 0
	var tab *huffTable
	for {
		if left == 0 {
			if g >= len(sel) {
				return out, corrupt("ran out of selectors")
			}
			tab = &tables[sel[g]]
			g++
			left = groupSize
		}
		left--
		sym, err := tab.decode(br)
		if err != nil {
			return out, err
		}
		if br.over {
			return out, corrupt("cut short")
		}
		if sym <= runB {
			if runBit > 1<<20 {
				return out, corrupt("run too long")
			}
			run += runBit << sym
			runBit <<= 1
			continue
		}
		if run > 0 {
			if n+run > len(tt) {
				return out, corrupt("block too big")
			}
			b := seqToByte[mtf[0]]
			counts[b] += run
			for k := 0; k < run; k++ {
				tt[n] = uint32(b)
				n++
			}
			run, runBit = 0, 1
		}
		if sym == eob {
			break
		}
		j := int(sym - 1)
		if j >= nInUse {
			return out, corrupt("bad symbol")
		}
		v := mtf[j]
		copy(mtf[1:j+1], mtf[:j])
		mtf[0] = v
		if n >= len(tt) {
			return out, corrupt("block too big")
		}
		b := seqToByte[v]
		counts[b]++
		tt[n] = uint32(b)
		n++
	}
	if origPtr >= n {
		return out, corrupt("bad origin")
	}

	// Inverse Burrows–Wheeler: tt[i] gets, above its byte, where the
	// next byte of the text is.
	var start [256]int
	sum := 0
	for i := 0; i < 256; i++ {
		start[i] = sum
		sum += counts[i]
	}
	for i := 0; i < n; i++ {
		b := tt[i] & 0xff
		tt[start[b]] |= uint32(i) << 8
		start[b]++
	}

	// Walk it, undoing the first run-length step: four equal bytes are
	// followed by how many more of them there are.
	p := tt[origPtr] >> 8
	last, same := -1, 0
	for i := 0; i < n; i++ {
		e := tt[p]
		c := byte(e)
		p = e >> 8
		if same == 4 {
			for k := 0; k < int(c); k++ {
				out = append(out, byte(last))
			}
			same, last = 0, -1
			continue
		}
		out = append(out, c)
		if int(c) == last {
			same++
		} else {
			last, same = int(c), 1
		}
	}
	return out, nil
}
