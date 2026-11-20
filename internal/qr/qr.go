// Package qr makes QR codes, so a phone can open NABU by pointing its
// camera at the screen. It encodes text in byte mode at error correction
// level M (15% of the code may be damaged or dirty), versions 1 to 6:
// up to 106 bytes, plenty for an address like http://192.168.1.23:8080/.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a QR code: Size×Size modules, true for dark.
type Code struct {
	Size    int
	Modules [][]bool
}

type version struct {
	total, ec int // codewords in all, error correction codewords per block
	blocks    int // all of the same size here
	align     int // position of the alignment pattern (0: none)
}

// Level M, versions 1 to 6.
var versions = []version{
	{},
	{26, 10, 1, 0},
	{44, 16, 1, 18},
	{70, 26, 1, 22},
	{100, 18, 2, 26},
	{134, 24, 2, 30},
	{172, 16, 4, 34},
}

func (v version) data() int { return v.total - v.ec*v.blocks }

// ErrTooLong is returned for text that does not fit.
var ErrTooLong = errors.New("qr: text too long")

// Encode makes the smallest QR code holding text.
func Encode(text string) (*Code, error) {
	ver := 0
	for v := 1; v < len(versions); v++ {
		if 4+8+8*len(text) <= versions[v].data()*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, ErrTooLong
	}
	v := versions[ver]

	// Data bits: byte mode, length, the bytes, terminator, padding.
	var bits []bool
	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, val>>i&1 == 1)
		}
	}
	put(0b0100, 4)
	put(len(text), 8)
	for i := 0; i < len(text); i++ {
		put(int(text[i]), 8)
	}
	capacity := v.data() * 8
	for i := 0; i < 4 && len(bits) < capacity; i++ {
		bits = append(bits, false)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	for pad := 0; len(bits) < capacity; pad++ {
		put([]int{0xEC, 0x11}[pad%2], 8)
	}
	data := make([]byte, v.data())
	for i := range data {
		for b := 0; b < 8; b++ {
			if bits[i*8+b] {
				data[i] |= 1 << (7 - b)
			}
		}
	}

	// Blocks with their error correction, interleaved.
	per := v.data() / v.blocks
	var blocks, ecs [][]byte
	for b := 0; b < v.blocks; b++ {
		d := data[b*per : (b+1)*per]
		blocks = append(blocks, d)
		ecs = append(ecs, reedSolomon(d, v.ec))
	}
	var words []byte
	for i := 0; i < per; i++ {
		for _, d := range blocks {
			words = append(words, d[i])
		}
	}
	for i := 0; i < v.ec; i++ {
		for _, e := range ecs {
			words = append(words, e[i])
		}
	}

	m := newMatrix(ver)
	m.place(words)
	best, bestScore := 0, -1
	for mask := 0; mask < 8; mask++ {
		t := m.copy()
		t.mask(mask)
		t.format(mask)
		if s := t.penalty(); bestScore < 0 || s < bestScore {
			best, bestScore = mask, s
		}
	}
	m.mask(best)
	m.format(best)
	return &Code{Size: m.size, Modules: m.dark}, nil
}

// --- Reed–Solomon over GF(256), polynomial 0x11d.

var gfExp, gfLog = func() (exp [512]byte, log [256]byte) {
	x := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(x)
		log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		exp[i] = exp[i-255]
	}
	return
}()

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func reedSolomon(data []byte, n int) []byte {
	// Generator: (x - α^0)(x - α^1)...(x - α^(n-1)), highest power first.
	gen := []byte{1}
	for i := 0; i < n; i++ {
		next := make([]byte, len(gen)+1)
		for j, c := range gen {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		gen = next
	}
	rem := make([]byte, n)
	for _, d := range data {
		f := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for j := 0; j < n; j++ {
			rem[j] ^= gfMul(gen[j+1], f)
		}
	}
	return rem
}

// --- The matrix.

type matrix struct {
	ver      int
	size     int
	dark     [][]bool
	function [][]bool // finder, timing, alignment, format: not data
}

func newMatrix(ver int) *matrix {
	size := 17 + 4*ver
	m := &matrix{ver: ver, size: size}
	m.dark = make([][]bool, size)
	m.function = make([][]bool, size)
	for i := range m.dark {
		m.dark[i] = make([]bool, size)
		m.function[i] = make([]bool, size)
	}
	// Timing patterns.
	for i := 0; i < size; i++ {
		m.set(6, i, i%2 == 0)
		m.set(i, 6, i%2 == 0)
	}
	// Finder patterns with their separators.
	for _, c := range [][2]int{{3, 3}, {size - 4, 3}, {3, size - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := c[0]+dx, c[1]+dy
				if x < 0 || y < 0 || x >= size || y >= size {
					continue
				}
				d := max(abs(dx), abs(dy))
				m.set(x, y, d != 2 && d != 4)
			}
		}
	}
	// The alignment pattern (one, for these versions).
	if a := versions[ver].align; a > 0 {
		for dy := -2; dy <= 2; dy++ {
			for dx := -2; dx <= 2; dx++ {
				m.set(a+dx, a+dy, max(abs(dx), abs(dy)) != 1)
			}
		}
	}
	// Reserve the format areas (written last) and the dark module.
	m.format(0)
	return m
}

func (m *matrix) set(x, y int, dark bool) {
	m.dark[y][x] = dark
	m.function[y][x] = true
}

func (m *matrix) copy() *matrix {
	c := &matrix{ver: m.ver, size: m.size, function: m.function}
	c.dark = make([][]bool, m.size)
	for i := range m.dark {
		c.dark[i] = append([]bool(nil), m.dark[i]...)
	}
	return c
}

// place writes the codewords in the zigzag of two-module columns,
// right to left, skipping the vertical timing pattern.
func (m *matrix) place(words []byte) {
	i, n := 0, len(words)*8
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < m.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = m.size - 1 - vert // going up
				}
				if !m.function[y][x] && i < n {
					m.dark[y][x] = words[i>>3]>>(7-uint(i&7))&1 == 1
					i++
				}
			}
		}
	}
}

func (m *matrix) mask(mask int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if m.function[y][x] {
				continue
			}
			var flip bool
			switch mask {
			case 0:
				flip = (x+y)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (x+y)%3 == 0
			case 4:
				flip = (x/3+y/2)%2 == 0
			case 5:
				flip = x*y%2+x*y%3 == 0
			case 6:
				flip = (x*y%2+x*y%3)%2 == 0
			case 7:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}
			if flip {
				m.dark[y][x] = !m.dark[y][x]
			}
		}
	}
}

// format writes the format information: level M (00) and the mask,
// protected by a BCH code, in its two copies.
func (m *matrix) format(mask int) {
	data := 0b00<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 == 1 }
	for i := 0; i <= 5; i++ {
		m.set(8, i, bit(i))
	}
	m.set(8, 7, bit(6))
	m.set(8, 8, bit(7))
	m.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		m.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		m.set(m.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.set(8, m.size-15+i, bit(i))
	}
	m.set(8, m.size-8, true) // always dark
}

// penalty scores how hard the code may be to read (lower is better).
func (m *matrix) penalty() int {
	s := 0
	n := m.size
	line := func(get func(i int) bool) {
		run := 1
		for i := 1; i <= n; i++ {
			if i < n && get(i) == get(i-1) {
				run++
				continue
			}
			if run >= 5 {
				s += 3 + run - 5
			}
			run = 1
		}
		// Finder-like 1:1:3:1:1 with light on a side.
		for i := 0; i+11 <= n; i++ {
			a := [11]bool{}
			for k := range a {
				a[k] = get(i + k)
			}
			p1 := a == [11]bool{true, false, true, true, true, false, true, false, false, false, false}
			p2 := a == [11]bool{false, false, false, false, true, false, true, true, true, false, true}
			if p1 || p2 {
				s += 40
			}
		}
	}
	dark := 0
	for y := 0; y < n; y++ {
		y := y
		line(func(i int) bool { return m.dark[y][i] })
		line(func(i int) bool { return m.dark[i][y] })
		for x := 0; x < n; x++ {
			if m.dark[y][x] {
				dark++
			}
			if x+1 < n && y+1 < n {
				c := m.dark[y][x]
				if m.dark[y][x+1] == c && m.dark[y+1][x] == c && m.dark[y+1][x+1] == c {
					s += 3
				}
			}
		}
	}
	pct := dark * 100 / (n * n)
	s += abs(pct-50) / 5 * 10
	return s
}

// SVG draws the code with a quiet zone of four modules, scaled to px
// pixels, dark on light whatever the page's colours.
func (c *Code) SVG(px int) string {
	q := 4
	n := c.Size + 2*q
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges" role="img">`, n, n, px, px)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Modules[y][x] {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+q, y+q)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
