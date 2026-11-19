// Package bzip2w writes bzip2 data. Go's standard library reads bzip2
// but cannot write it; NABU needs to, to repack a dump without the pages
// it never shows (see dump.Slim), in the same format so that everything
// reading dumps keeps working.
//
// It follows the reference bzip2 1.0 at block size 9: run-length coding
// of long runs, the Burrows–Wheeler transform, move-to-front with zero
// runs, and two to six Huffman tables chosen per group of 50 symbols.
package bzip2w

import (
	"bytes"
	"encoding/binary"
	"index/suffixarray"
)

const (
	maxBlock   = 900000 - 19 // bytes after the first run-length step
	groupSize  = 50
	maxCodeLen = 17
	runA, runB = 0, 1
)

// Compress returns data as one complete bzip2 stream.
func Compress(data []byte) []byte {
	w := &bitWriter{out: make([]byte, 0, len(data)/4+64)}
	w.out = append(w.out, 'B', 'Z', 'h', '9')
	var combined uint32
	block := make([]byte, 0, min(maxBlock, len(data)+8))
	crc := ^uint32(0)
	flush := func() {
		if len(block) == 0 {
			return
		}
		c := ^crc
		combined = (combined<<1 | combined>>31) ^ c
		writeBlock(w, block, c)
		block, crc = block[:0], ^uint32(0)
	}
	for i := 0; i < len(data); {
		b := data[i]
		j := i + 1
		for j < len(data) && data[j] == b && j-i < 255 {
			j++
		}
		n := j - i
		size := n
		if n >= 4 {
			size = 5
		}
		if len(block)+size > maxBlock {
			flush()
		}
		for k := i; k < j; k++ {
			crc = crc<<8 ^ crcTable[byte(crc>>24)^b]
		}
		if n < 4 {
			for k := 0; k < n; k++ {
				block = append(block, b)
			}
		} else {
			block = append(block, b, b, b, b, byte(n-4))
		}
		i = j
	}
	flush()
	w.bits(24, 0x177245)
	w.bits(24, 0x385090)
	w.bits(32, combined)
	w.flush()
	return w.out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var crcTable = func() (t [256]uint32) {
	for i := range t {
		c := uint32(i) << 24
		for k := 0; k < 8; k++ {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return
}()

type bitWriter struct {
	out  []byte
	acc  uint64
	nacc uint
}

func (w *bitWriter) bits(n uint, v uint32) {
	w.acc = w.acc<<n | uint64(v)&(1<<n-1)
	w.nacc += n
	for w.nacc >= 8 {
		w.nacc -= 8
		w.out = append(w.out, byte(w.acc>>w.nacc))
	}
}

func (w *bitWriter) flush() {
	if w.nacc > 0 {
		w.out = append(w.out, byte(w.acc<<(8-w.nacc)))
		w.nacc = 0
	}
}

// bwt sorts the cyclic rotations of t and returns the last column and
// where the original row went.
func bwt(t []byte) ([]byte, int) {
	p := rotations(t)
	n := len(t)
	last := make([]byte, n)
	orig := 0
	for i, j := range p {
		if j == 0 {
			orig = i
			last[i] = t[n-1]
		} else {
			last[i] = t[j-1]
		}
	}
	return last, orig
}

// rotations sorts the rotations of t. The rotations of t are in the order
// of the suffixes of t+t that start in its first half, and the standard
// library sorts suffixes in linear time (SA-IS); its result is read back
// from the documented serialization of suffixarray.Index and checked.
// Should that ever fail, a slower sort of our own is used.
func rotations(t []byte) []int32 {
	n := len(t)
	if n > 1 {
		tt := make([]byte, 2*n)
		copy(tt, t)
		copy(tt[n:], t)
		var buf bytes.Buffer
		buf.Grow(10 + 2*n + 4*2*n)
		if suffixarray.New(tt).Write(&buf) == nil {
			if p := readSuffixes(buf.Bytes(), n); p != nil {
				return p
			}
		}
	}
	return doubling(t)
}

// readSuffixes reads a serialized suffixarray.Index of t+t (length 2n):
// the length as a 10-byte varint, the text, then chunks of uvarints each
// preceded by its byte size as a 10-byte varint. It keeps positions
// below n, and returns nil unless they are exactly 0..n-1 once each.
func readSuffixes(b []byte, n int) []int32 {
	const hdr = binary.MaxVarintLen64
	if len(b) < hdr {
		return nil
	}
	if l, _ := binary.Varint(b[:hdr]); l != int64(2*n) || len(b) < hdr+2*n {
		return nil
	}
	b = b[hdr+2*n:]
	p := make([]int32, 0, n)
	seen := make([]bool, n)
	count := 0
	for len(b) > 0 {
		if len(b) < hdr {
			return nil
		}
		size, _ := binary.Varint(b[:hdr])
		if size < hdr || size > int64(len(b)) {
			return nil
		}
		chunk := b[hdr:size]
		for len(chunk) > 0 {
			v, k := binary.Uvarint(chunk)
			if k <= 0 || v >= uint64(2*n) {
				return nil
			}
			chunk = chunk[k:]
			count++
			if int(v) < n {
				if seen[v] {
					return nil
				}
				seen[v] = true
				p = append(p, int32(v))
			}
		}
		b = b[size:]
	}
	if count != 2*n || len(p) != n {
		return nil
	}
	return p
}

// doubling sorts the rotations of t by prefix doubling with counting
// sorts.
func doubling(t []byte) []int32 {
	n := len(t)
	p := make([]int32, n)
	c := make([]int32, n)
	pn := make([]int32, n)
	cn := make([]int32, n)
	cnt := make([]int32, n+256)
	for _, b := range t {
		cnt[b]++
	}
	for i := 1; i < 256; i++ {
		cnt[i] += cnt[i-1]
	}
	for i := n - 1; i >= 0; i-- {
		cnt[t[i]]--
		p[cnt[t[i]]] = int32(i)
	}
	classes := int32(1)
	c[p[0]] = 0
	for i := 1; i < n; i++ {
		if t[p[i]] != t[p[i-1]] {
			classes++
		}
		c[p[i]] = classes - 1
	}
	for h := 1; h < n && int(classes) < n; h <<= 1 {
		for i, v := range p {
			v -= int32(h)
			if v < 0 {
				v += int32(n)
			}
			pn[i] = v
		}
		for i := int32(0); i < classes; i++ {
			cnt[i] = 0
		}
		for _, v := range pn {
			cnt[c[v]]++
		}
		for i := int32(1); i < classes; i++ {
			cnt[i] += cnt[i-1]
		}
		for i := n - 1; i >= 0; i-- {
			v := pn[i]
			cnt[c[v]]--
			p[cnt[c[v]]] = v
		}
		cn[p[0]] = 0
		classes = 1
		for i := 1; i < n; i++ {
			a, b := p[i], p[i-1]
			ah, bh := int(a)+h, int(b)+h
			if ah >= n {
				ah -= n
			}
			if bh >= n {
				bh -= n
			}
			if c[a] != c[b] || c[ah] != c[bh] {
				classes++
			}
			cn[a] = classes - 1
		}
		c, cn = cn, c
	}
	return p
}

func writeBlock(w *bitWriter, block []byte, crc uint32) {
	last, orig := bwt(block)

	// Symbols in use, and move-to-front with runs of zeros.
	var inUse [256]bool
	for _, b := range block {
		inUse[b] = true
	}
	var seq [256]byte
	nInUse := 0
	for i := 0; i < 256; i++ {
		if inUse[i] {
			seq[i] = byte(nInUse)
			nInUse++
		}
	}
	alpha := nInUse + 2
	eob := uint16(nInUse + 1)
	freq := make([]int32, alpha)
	mtfv := make([]uint16, 0, len(last)+1)
	var list [256]byte
	for i := range list {
		list[i] = byte(i)
	}
	zPend := 0
	runs := func() {
		z := zPend - 1
		for {
			s := uint16(runA)
			if z&1 == 1 {
				s = runB
			}
			mtfv = append(mtfv, s)
			freq[s]++
			if z < 2 {
				break
			}
			z = (z - 2) / 2
		}
		zPend = 0
	}
	for _, b := range last {
		s := seq[b]
		if list[0] == s {
			zPend++
			continue
		}
		if zPend > 0 {
			runs()
		}
		j := 1
		for list[j] != s {
			j++
		}
		copy(list[1:j+1], list[:j])
		list[0] = s
		mtfv = append(mtfv, uint16(j+1))
		freq[j+1]++
	}
	if zPend > 0 {
		runs()
	}
	mtfv = append(mtfv, eob)
	freq[eob]++

	lens, sel := chooseTables(mtfv, freq, alpha)
	nGroups := len(lens)

	w.bits(24, 0x314159)
	w.bits(24, 0x265359)
	w.bits(32, crc)
	w.bits(1, 0)
	w.bits(24, uint32(orig))

	var used16 uint32
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			if inUse[i*16+j] {
				used16 |= 1 << (15 - i)
				break
			}
		}
	}
	w.bits(16, used16)
	for i := 0; i < 16; i++ {
		if used16&(1<<(15-i)) == 0 {
			continue
		}
		var v uint32
		for j := 0; j < 16; j++ {
			if inUse[i*16+j] {
				v |= 1 << (15 - j)
			}
		}
		w.bits(16, v)
	}

	w.bits(3, uint32(nGroups))
	w.bits(15, uint32(len(sel)))
	pos := []byte{0, 1, 2, 3, 4, 5}
	for _, s := range sel {
		j := 0
		for pos[j] != s {
			j++
		}
		copy(pos[1:j+1], pos[:j])
		pos[0] = s
		for k := 0; k < j; k++ {
			w.bits(1, 1)
		}
		w.bits(1, 0)
	}
	codes := make([][]uint32, nGroups)
	for t, l := range lens {
		cur := l[0]
		w.bits(5, uint32(cur))
		for _, want := range l {
			for cur < want {
				w.bits(2, 2)
				cur++
			}
			for cur > want {
				w.bits(2, 3)
				cur--
			}
			w.bits(1, 0)
		}
		codes[t] = canonical(l)
	}
	for g, s := range sel {
		end := (g + 1) * groupSize
		if end > len(mtfv) {
			end = len(mtfv)
		}
		l, c := lens[s], codes[s]
		for _, v := range mtfv[g*groupSize : end] {
			w.bits(uint(l[v]), c[v])
		}
	}
}

// canonical gives the codes the decoder derives from code lengths.
func canonical(l []uint8) []uint32 {
	codes := make([]uint32, len(l))
	minL, maxL := uint8(32), uint8(0)
	for _, x := range l {
		if x < minL {
			minL = x
		}
		if x > maxL {
			maxL = x
		}
	}
	var v uint32
	for n := minL; n <= maxL; n++ {
		for i, x := range l {
			if x == n {
				codes[i] = v
				v++
			}
		}
		v <<= 1
	}
	return codes
}

// chooseTables picks Huffman tables and which one codes each group of
// 50 symbols, as the reference implementation does: an initial split of
// the alphabet by frequency, then four rounds of assigning each group to
// its cheapest table and rebuilding the tables from what they coded.
func chooseTables(mtfv []uint16, freq []int32, alpha int) ([][]uint8, []byte) {
	nMTF := len(mtfv)
	nGroups := 6
	switch {
	case nMTF < 200:
		nGroups = 2
	case nMTF < 600:
		nGroups = 3
	case nMTF < 1200:
		nGroups = 4
	case nMTF < 2400:
		nGroups = 5
	}
	lens := make([][]uint8, nGroups)
	for t := range lens {
		lens[t] = make([]uint8, alpha)
	}
	nPart, remF, gs := nGroups, int32(nMTF), 0
	for nPart > 0 {
		tFreq := remF / int32(nPart)
		ge, aFreq := gs-1, int32(0)
		for aFreq < tFreq && ge < alpha-1 {
			ge++
			aFreq += freq[ge]
		}
		if ge > gs && nPart != nGroups && nPart != 1 && (nGroups-nPart)%2 == 1 {
			aFreq -= freq[ge]
			ge--
		}
		for v := 0; v < alpha; v++ {
			if v >= gs && v <= ge {
				lens[nPart-1][v] = 0
			} else {
				lens[nPart-1][v] = 15
			}
		}
		nPart--
		gs = ge + 1
		remF -= aFreq
	}
	nSel := (nMTF + groupSize - 1) / groupSize
	sel := make([]byte, nSel)
	rfreq := make([][]int32, nGroups)
	for t := range rfreq {
		rfreq[t] = make([]int32, alpha)
	}
	cost := make([]int, nGroups)
	for iter := 0; iter < 4; iter++ {
		for t := range rfreq {
			for v := range rfreq[t] {
				rfreq[t][v] = 0
			}
		}
		for g := 0; g < nSel; g++ {
			end := (g + 1) * groupSize
			if end > nMTF {
				end = nMTF
			}
			for t := range cost {
				cost[t] = 0
			}
			for _, v := range mtfv[g*groupSize : end] {
				for t := range cost {
					cost[t] += int(lens[t][v])
				}
			}
			bt := 0
			for t := 1; t < nGroups; t++ {
				if cost[t] < cost[bt] {
					bt = t
				}
			}
			sel[g] = byte(bt)
			for _, v := range mtfv[g*groupSize : end] {
				rfreq[bt][v]++
			}
		}
		for t := range lens {
			lens[t] = codeLengths(rfreq[t], maxCodeLen)
		}
	}
	return lens, sel
}

// codeLengths builds Huffman code lengths no longer than maxLen. Every
// symbol gets a code (the format requires it), unused ones the longest.
func codeLengths(freq []int32, maxLen int) []uint8 {
	n := len(freq)
	w := make([]int64, n)
	for i, f := range freq {
		w[i] = int64(f)
		if w[i] == 0 {
			w[i] = 1
		}
	}
	for {
		l := huffman(w)
		ok := true
		for _, x := range l {
			if int(x) > maxLen {
				ok = false
				break
			}
		}
		if ok {
			return l
		}
		for i := range w {
			w[i] = w[i]/2 + 1
		}
	}
}

// huffman returns code lengths for the weights (at least two).
func huffman(w []int64) []uint8 {
	n := len(w)
	parent := make([]int32, 2*n)
	weight := make([]int64, 2*n)
	copy(weight, w)
	h := make([]int32, 0, n) // min-heap of node numbers
	less := func(a, b int32) bool {
		if weight[a] != weight[b] {
			return weight[a] < weight[b]
		}
		return a < b
	}
	push := func(x int32) {
		h = append(h, x)
		for i := len(h) - 1; i > 0; {
			p := (i - 1) / 2
			if !less(h[i], h[p]) {
				break
			}
			h[i], h[p] = h[p], h[i]
			i = p
		}
	}
	pop := func() int32 {
		x := h[0]
		last := len(h) - 1
		h[0] = h[last]
		h = h[:last]
		for i := 0; ; {
			l, r, m := 2*i+1, 2*i+2, i
			if l < len(h) && less(h[l], h[m]) {
				m = l
			}
			if r < len(h) && less(h[r], h[m]) {
				m = r
			}
			if m == i {
				break
			}
			h[i], h[m] = h[m], h[i]
			i = m
		}
		return x
	}
	for i := 0; i < n; i++ {
		push(int32(i))
	}
	next := int32(n)
	for len(h) > 1 {
		a, b := pop(), pop()
		weight[next] = weight[a] + weight[b]
		parent[a], parent[b] = next, next
		push(next)
		next++
	}
	root := next - 1
	depth := make([]uint8, 2*n)
	for i := root - 1; i >= 0; i-- {
		depth[i] = depth[parent[i]] + 1
	}
	return depth[:n]
}
