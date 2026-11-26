package fulltext

import (
	"encoding/binary"
	"math/bits"
)

// The article list of a word (format 3).
//
// Short lists (under shortList articles: most words are in a handful)
// are written as in format 2: per article one uvarint, the doc-number
// delta shifted left 2 with the weight in the low 2 bits.
//
// Longer lists start with a byte saying how the articles are written:
//
//	0       uvarints: the first doc number, then each gap less one
//	0x80|l  Elias-Fano with l low bits: the low bits of every doc number
//	        packed, then the high parts in unary (a 1 for each article,
//	        after as many 0s as the high part grew), each to a whole byte
//
// whichever is smaller, then the weights in a prefix code, least
// significant bit first: 0 once, 10 twice, 110 a few times, 111 often.
// Three articles in four have a word once, so that takes about 1.4 bits
// an article where format 2 took 2, with the same four weights.
const shortList = 16

// appendList writes ps (doc numbers ascending) in format 3.
func appendList(out []byte, ps []posting) []byte {
	var tmp [binary.MaxVarintLen64]byte
	if len(ps) < shortList {
		last := uint32(0)
		for _, p := range ps {
			out = append(out, tmp[:binary.PutUvarint(tmp[:], uint64(p.doc-last)<<2|uint64(tfBucket(p.tf)))]...)
			last = p.doc
		}
		return out
	}
	n := uint64(len(ps))
	u := uint64(ps[len(ps)-1].doc) + 1
	l := 0
	if u > n {
		l = bits.Len64(u/n) - 1
	}
	efBytes := (n*uint64(l)+7)/8 + (n+u>>uint(l)+8)/8
	var vBytes uint64
	last := uint32(0)
	for i, p := range ps {
		g := p.doc
		if i > 0 {
			g = p.doc - last - 1
		}
		vBytes += uint64(uvlen(uint64(g)))
		last = p.doc
	}
	if vBytes <= efBytes {
		out = append(out, 0)
		last = 0
		for i, p := range ps {
			g := p.doc
			if i > 0 {
				g = p.doc - last - 1
			}
			out = append(out, tmp[:binary.PutUvarint(tmp[:], uint64(g))]...)
			last = p.doc
		}
	} else {
		out = append(out, 0x80|byte(l))
		var w bitWriter
		for _, p := range ps {
			w.put(uint64(p.doc)&(1<<uint(l)-1), l)
		}
		out = w.flush(out)
		high := uint64(0)
		for _, p := range ps {
			h := uint64(p.doc) >> uint(l)
			w.put(0, int(h-high))
			w.put(1, 1)
			high = h
		}
		out = w.flush(out)
	}
	var w bitWriter
	for _, p := range ps {
		switch tfBucket(p.tf) {
		case 0:
			w.put(0, 1)
		case 1:
			w.put(1, 2)
		case 2:
			w.put(3, 3)
		default:
			w.put(7, 3)
		}
	}
	return w.flush(out)
}

// readList decodes a list of df articles written by appendList.
func readList(buf []byte, df uint32, out []posting) ([]posting, bool) {
	if df < shortList {
		var doc uint32
		for i := 0; i < len(buf); {
			v, n := binary.Uvarint(buf[i:])
			if n <= 0 {
				return nil, false
			}
			i += n
			doc += uint32(v >> 2)
			out = append(out, posting{doc, bucketTF[v&3]})
		}
		return out, uint32(len(out)) == df
	}
	if len(buf) == 0 {
		return nil, false
	}
	start := len(out)
	kind, i := buf[0], 1
	if kind == 0 {
		var doc uint32
		for k := uint32(0); k < df; k++ {
			v, n := binary.Uvarint(buf[i:])
			if n <= 0 {
				return nil, false
			}
			i += n
			if k == 0 {
				doc = uint32(v)
			} else {
				doc += uint32(v) + 1
			}
			out = append(out, posting{doc: doc})
		}
	} else {
		l := int(kind & 0x7f)
		lowBytes := (int(df)*l + 7) / 8
		if l > 32 || i+lowBytes > len(buf) {
			return nil, false
		}
		r := bitReader{b: buf[i : i+lowBytes]}
		for k := uint32(0); k < df; k++ {
			out = append(out, posting{doc: uint32(r.get(l))})
		}
		i += lowBytes
		high, k, pos := uint64(0), start, 0
		for ; i < len(buf) && k < start+int(df); i++ {
			b := buf[i]
			for b != 0 && k < start+int(df) {
				z := bits.TrailingZeros8(b)
				p := pos + z
				high = uint64(p - (k - start))
				out[k].doc |= uint32(high << uint(l))
				k++
				b &= b - 1
			}
			pos += 8
		}
		if k != start+int(df) {
			return nil, false
		}
	}
	r := bitReader{b: buf[i:]}
	for k := start; k < len(out); k++ {
		t := 0
		for t < 3 && r.get(1) == 1 {
			t++
		}
		out[k].tf = bucketTF[t]
	}
	return out, !r.over
}

type bitWriter struct {
	acc  uint64
	n    int
	done []byte
}

func (w *bitWriter) put(v uint64, n int) {
	for n > 0 {
		k := n
		if k > 32 {
			k = 32
		}
		w.acc |= (v & (1<<uint(k) - 1)) << uint(w.n)
		w.n += k
		v >>= uint(k)
		n -= k
		for w.n >= 8 {
			w.done = append(w.done, byte(w.acc))
			w.acc >>= 8
			w.n -= 8
		}
	}
}

// flush appends what was written, to a whole byte, and starts afresh.
func (w *bitWriter) flush(out []byte) []byte {
	if w.n > 0 {
		w.done = append(w.done, byte(w.acc))
	}
	out = append(out, w.done...)
	*w = bitWriter{done: w.done[:0]}
	return out
}

type bitReader struct {
	b    []byte
	pos  int // in bits
	over bool
}

func (r *bitReader) get(n int) uint64 {
	var v uint64
	for k := 0; k < n; {
		byteAt := r.pos >> 3
		if byteAt >= len(r.b) {
			r.over = true
			return v
		}
		off := r.pos & 7
		take := 8 - off
		if take > n-k {
			take = n - k
		}
		v |= uint64(r.b[byteAt]>>uint(off)&(1<<uint(take)-1)) << uint(k)
		k += take
		r.pos += take
	}
	return v
}

func uvlen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}
