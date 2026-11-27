package fulltext

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
	"sort"
	"strings"
)

// The article table (format 4): per block of docBlock articles, each
// article's stream (difference from the one before) and page id
// (difference, signed), then the length of the titles compressed and the
// titles themselves, one per line, compressed with deflate and a
// dictionary of the commonest title words (Meta.TitleDict). Titles took
// nearly all of the table (146 MB of 168 in English) and compress to
// about half. Formats 2 and 3 kept each title as it is, after its
// article's numbers.

const titleDictSize = 32 << 10

// titleWords counts title words, from every few titles, for the dictionary.
type titleWords map[string]int

func (tw titleWords) add(n uint32, title string) {
	if n%8 != 0 || len(tw) > 1<<20 {
		return
	}
	for _, w := range strings.Fields(title) {
		tw[w] += len(w)
	}
}

// dict makes the dictionary: the words that would save the most, the
// most useful last (deflate reaches the end of a dictionary most cheaply).
func (tw titleWords) dict() []byte {
	type wc struct {
		w string
		n int
	}
	ws := make([]wc, 0, len(tw))
	for w, n := range tw {
		if n > 16 {
			ws = append(ws, wc{w, n})
		}
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].n > ws[j].n || ws[i].n == ws[j].n && ws[i].w < ws[j].w })
	var keep []string
	size := 0
	for _, w := range ws {
		if size+len(w.w)+1 > titleDictSize {
			break
		}
		keep = append(keep, w.w)
		size += len(w.w) + 1
	}
	var b bytes.Buffer
	for i := len(keep) - 1; i >= 0; i-- {
		b.WriteString(keep[i])
		b.WriteByte(' ')
	}
	return b.Bytes()
}

// encodeDocBlock writes a block of articles in format 4.
func encodeDocBlock(out []byte, docs []Doc, dict []byte) ([]byte, error) {
	var tmp [binary.MaxVarintLen64]byte
	var stream, id uint64
	titles := make([]string, len(docs))
	for i, d := range docs {
		out = append(out, tmp[:binary.PutUvarint(tmp[:], uint64(d.Stream)-stream)]...)
		out = append(out, tmp[:binary.PutVarint(tmp[:], int64(d.PageID-id))]...)
		stream, id = uint64(d.Stream), d.PageID
		titles[i] = d.Title
	}
	var z bytes.Buffer
	w, err := flate.NewWriterDict(&z, flate.BestCompression, dict)
	if err != nil {
		return nil, err
	}
	io.WriteString(w, strings.Join(titles, "\n"))
	if err := w.Close(); err != nil {
		return nil, err
	}
	out = append(out, tmp[:binary.PutUvarint(tmp[:], uint64(z.Len()))]...)
	return append(out, z.Bytes()...), nil
}

// decodeDocBlock reads a block of n articles (format 4 when compressed).
func decodeDocBlock(buf []byte, n int, compressed bool, dict []byte) ([]Doc, bool) {
	docs := make([]Doc, 0, n)
	var stream, id uint64
	i := 0
	for k := 0; k < n; k++ {
		ds, k1 := binary.Uvarint(buf[i:])
		if k1 <= 0 {
			return nil, false
		}
		did, k2 := binary.Varint(buf[i+k1:])
		if k2 <= 0 {
			return nil, false
		}
		i += k1 + k2
		stream += ds
		id = uint64(int64(id) + did)
		d := Doc{Stream: uint32(stream), PageID: id}
		if !compressed {
			l, k3 := binary.Uvarint(buf[i:])
			if k3 <= 0 || i+k3+int(l) > len(buf) {
				return nil, false
			}
			i += k3
			d.Title = string(buf[i : i+int(l)])
			i += int(l)
		}
		docs = append(docs, d)
	}
	if !compressed {
		return docs, true
	}
	l, k := binary.Uvarint(buf[i:])
	if k <= 0 || i+k+int(l) > len(buf) {
		return nil, false
	}
	r := flate.NewReaderDict(bytes.NewReader(buf[i+k:i+k+int(l)]), dict)
	text, err := io.ReadAll(r)
	if err != nil {
		return nil, false
	}
	titles := strings.Split(string(text), "\n")
	if len(titles) != n {
		return nil, false
	}
	for k := range docs {
		docs[k].Title = titles[k]
	}
	return docs, true
}
