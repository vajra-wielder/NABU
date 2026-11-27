package fulltext

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/vajra-wielder/nabu/internal/drivefile"
	"html"
	"io"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Index is an opened full-text index.
type Index struct {
	f        *drivefile.File
	Meta     Meta
	rules    Rules
	common   map[string]bool // in over 40% of articles: not indexed however written
	general  map[string]bool // common words naming nothing: not indexed (vocab.go)
	dictOff  []int64         // block offsets (+ end)
	dictTerm []string
	docsOff  int64
	docOffs  []uint64
	docsEnd  int64
	geo      []geoRec // places (read when first asked for)
	geoOnce  sync.Once
	geoOff   int64
	geoEnd   int64
	lens     []byte // format 3: each article's length (lenByte)

	mu       sync.Mutex
	cache    map[int][]dictRow
	docCache map[int][]Doc
}

type dictRow struct {
	term      string
	df        uint32
	off, size uint64
}

// ErrStale means the index belongs to other files or an older NABU.
var ErrStale = errors.New("fulltext: index is out of date")

// Open opens path and checks it matches the dump files f.
func Open(path string, f *dump.Files) (*Index, error) {
	file, err := drivefile.OpenRead(path)
	if err != nil {
		return nil, err
	}
	ix := &Index{f: file, cache: map[int][]dictRow{}, docCache: map[int][]Doc{}}
	if err := ix.load(); err != nil {
		file.Close()
		return nil, err
	}
	if f != nil {
		want := dump.SourcesOf(f)
		if len(want) != len(ix.Meta.Sources) {
			file.Close()
			return nil, ErrStale
		}
		for i := range want {
			if want[i] != ix.Meta.Sources[i] {
				file.Close()
				return nil, ErrStale
			}
		}
	}
	return ix, nil
}

func (ix *Index) load() error {
	st, err := ix.f.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	if size < int64(len(ftsMagic))+ftsTrailer {
		return ErrStale
	}
	var tr [ftsTrailer]byte
	if _, err := ix.f.ReadAt(tr[:], size-ftsTrailer); err != nil {
		return err
	}
	if string(tr[56:]) != ftsEnd {
		return ErrStale
	}
	var o [7]int64
	for i := range o {
		o[i] = int64(binary.LittleEndian.Uint64(tr[i*8:]))
	}
	dictOff, dirOff, docsOff, docDirOff, geoOff, metaOff := o[0], o[1], o[2], o[3], o[4], o[5]
	if (o[6] > FormatVersion || o[6] < 2) || !(dictOff <= dirOff && dirOff <= docsOff && docsOff <= docDirOff && docDirOff <= geoOff && geoOff <= metaOff && metaOff <= size-ftsTrailer) {
		return ErrStale
	}
	mb := make([]byte, size-ftsTrailer-metaOff)
	if _, err := ix.f.ReadAt(mb, metaOff); err != nil {
		return err
	}
	if err := json.Unmarshal(mb, &ix.Meta); err != nil || int64(ix.Meta.Version) != o[6] {
		return ErrStale
	}
	if ix.Meta.Version >= 3 {
		if ix.Meta.LensOff < geoOff || ix.Meta.LensOff+int64(ix.Meta.Docs) > metaOff {
			return ErrStale
		}
		ix.lens = make([]byte, ix.Meta.Docs)
		if _, err := ix.f.ReadAt(ix.lens, ix.Meta.LensOff); err != nil {
			return err
		}
	}
	ix.rules = NewRules(ix.Meta.Stopwords, ix.Meta.Capital)
	ix.common = map[string]bool{}
	for _, w := range ix.Meta.Common {
		ix.common[w] = true
	}
	ix.general = map[string]bool{}
	for _, w := range ix.Meta.General {
		ix.general[w] = true
	}
	r := bufio.NewReader(io.NewSectionReader(ix.f, dirOff, docsOff-dirOff))
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return stale(err)
	}
	for i := uint64(0); i < n; i++ {
		off, e1 := binary.ReadUvarint(r)
		l, e2 := binary.ReadUvarint(r)
		if e1 != nil || e2 != nil || l > 1024 {
			return stale(e1, e2)
		}
		t := make([]byte, l)
		if _, err := io.ReadFull(r, t); err != nil {
			return stale(err)
		}
		ix.dictOff = append(ix.dictOff, int64(off))
		ix.dictTerm = append(ix.dictTerm, string(t))
	}
	ix.dictOff = append(ix.dictOff, dirOff)
	ix.docsOff, ix.docsEnd = docsOff, docDirOff
	r = bufio.NewReader(io.NewSectionReader(ix.f, docDirOff, geoOff-docDirOff))
	n, err = binary.ReadUvarint(r)
	if err != nil {
		return stale(err)
	}
	ix.docOffs = make([]uint64, 0, n)
	for i := uint64(0); i < n; i++ {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return stale(err)
		}
		ix.docOffs = append(ix.docOffs, v)
	}
	// The places are read when first asked for (English has 1.3 million,
	// 16 MB to read from the drive at every start otherwise).
	ix.geoOff, ix.geoEnd = geoOff, metaOff
	return nil
}

// places reads the articles' locations, once.
func (ix *Index) places() []geoRec {
	ix.geoOnce.Do(func() {
		r := bufio.NewReader(io.NewSectionReader(ix.f, ix.geoOff, ix.geoEnd-ix.geoOff))
		n, err := binary.ReadUvarint(r)
		if err != nil {
			return
		}
		geo := make([]geoRec, 0, n)
		var g [12]byte
		for i := uint64(0); i < n; i++ {
			if _, err := io.ReadFull(r, g[:]); err != nil {
				return
			}
			geo = append(geo, geoRec{binary.LittleEndian.Uint32(g[0:]),
				math.Float32frombits(binary.LittleEndian.Uint32(g[4:])), math.Float32frombits(binary.LittleEndian.Uint32(g[8:]))})
		}
		ix.geo = geo
	})
	return ix.geo
}

// stale tells an index that is cut short or garbled (ErrStale: it goes)
// from one that could not be read just now (kept for next time).
func stale(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
	}
	return ErrStale
}

// Sections reports the bytes of each part of the index file, to see
// where the room goes.
func (ix *Index) Sections() map[string]int64 {
	return map[string]int64{
		"postings":   ix.dictOff[0] - int64(len(ftsMagic)),
		"dictionary": ix.dictOff[len(ix.dictOff)-1] - ix.dictOff[0],
		"docs":       ix.docsEnd - ix.docsOff,
	}
}

// Close releases the file.
func (ix *Index) Close() error { return ix.f.Close() }

func (ix *Index) dictBlockRows(i int) ([]dictRow, error) {
	ix.mu.Lock()
	if rows, ok := ix.cache[i]; ok {
		ix.mu.Unlock()
		return rows, nil
	}
	ix.mu.Unlock()
	buf := make([]byte, ix.dictOff[i+1]-ix.dictOff[i])
	if _, err := ix.f.ReadAt(buf, ix.dictOff[i]); err != nil {
		return nil, err
	}
	r := bytes.NewReader(buf)
	var rows []dictRow
	prev := ""
	off, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, ErrStale
	}
	for r.Len() > 0 {
		shared, e1 := binary.ReadUvarint(r)
		l, e2 := binary.ReadUvarint(r)
		if e1 != nil || e2 != nil || int(shared) > len(prev) || int(l) > r.Len() {
			return nil, ErrStale
		}
		suf := make([]byte, l)
		r.Read(suf)
		df, _ := binary.ReadUvarint(r)
		size, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, ErrStale
		}
		t := prev[:shared] + string(suf)
		rows = append(rows, dictRow{t, uint32(df), off, size})
		prev = t
		off += size
	}
	ix.mu.Lock()
	if len(ix.cache) > 4096 {
		ix.cache = map[int][]dictRow{}
	}
	ix.cache[i] = rows
	ix.mu.Unlock()
	return rows, nil
}

func (ix *Index) lookup(term string) (dictRow, bool, error) {
	i := sort.SearchStrings(ix.dictTerm, term)
	if i == len(ix.dictTerm) || ix.dictTerm[i] != term {
		i--
	}
	if i < 0 {
		return dictRow{}, false, nil
	}
	rows, err := ix.dictBlockRows(i)
	if err != nil {
		return dictRow{}, false, err
	}
	for _, r := range rows {
		if r.term == term {
			return r, true, nil
		}
	}
	return dictRow{}, false, nil
}

func (ix *Index) postings(row dictRow) ([]posting, error) {
	buf := make([]byte, row.size)
	if _, err := ix.f.ReadAt(buf, int64(row.off)); err != nil {
		return nil, err
	}
	out := make([]posting, 0, row.df)
	if ix.Meta.Version >= 3 {
		out, ok := readList(buf, row.df, out)
		if !ok {
			return nil, ErrStale
		}
		return out, nil
	}
	var doc uint32
	for i := 0; i < len(buf); {
		v, n := binary.Uvarint(buf[i:])
		if n <= 0 {
			return nil, ErrStale
		}
		i += n
		doc += uint32(v >> 2)
		out = append(out, posting{doc, bucketTF[v&3]})
	}
	return out, nil
}

// Doc is one indexed article.
type Doc struct {
	Stream uint32
	PageID uint64
	Title  string
}

// Doc reads article n.
func (ix *Index) Doc(n uint32) (Doc, error) {
	docs, err := ix.docsOf(int(n / docBlock))
	if err != nil {
		return Doc{}, err
	}
	if i := int(n % docBlock); i < len(docs) {
		return docs[i], nil
	}
	return Doc{}, errors.New("fulltext: no such document")
}

// docBlock calls fn with each article of block blk of the article table
// (its number, stream, page and title) until fn returns false.
func (ix *Index) docBlock(blk int, fn func(n uint32, stream uint32, id uint64, title []byte) bool) error {
	docs, err := ix.docsOf(blk)
	if err != nil {
		return err
	}
	for i, d := range docs {
		if !fn(uint32(blk*docBlock+i), d.Stream, d.PageID, []byte(d.Title)) {
			return nil
		}
	}
	return nil
}

// docsOf reads block blk of the article table, kept for the next time.
func (ix *Index) docsOf(blk int) ([]Doc, error) {
	if blk < 0 || blk >= len(ix.docOffs) {
		return nil, errors.New("fulltext: no such document")
	}
	ix.mu.Lock()
	if d, ok := ix.docCache[blk]; ok {
		ix.mu.Unlock()
		return d, nil
	}
	ix.mu.Unlock()
	end := ix.docsEnd
	if blk+1 < len(ix.docOffs) {
		end = ix.docsOff + int64(ix.docOffs[blk+1])
	}
	start := ix.docsOff + int64(ix.docOffs[blk])
	buf := make([]byte, end-start)
	if _, err := ix.f.ReadAt(buf, start); err != nil {
		return nil, err
	}
	n := ix.Meta.Docs - blk*docBlock
	if n > docBlock {
		n = docBlock
	}
	docs, ok := decodeDocBlock(buf, n, ix.Meta.Version >= 4, ix.Meta.TitleDict)
	if !ok {
		return nil, ErrStale
	}
	ix.mu.Lock()
	if len(ix.docCache) >= 4096 {
		ix.docCache = map[int][]Doc{}
	}
	ix.docCache[blk] = docs
	ix.mu.Unlock()
	return docs, nil
}

// DocOf finds the number of the article at page id in stream. Articles
// are numbered in the dump's order, so by stream.
func (ix *Index) DocOf(stream uint32, id uint64) (uint32, bool) {
	firstStream := func(blk int) (uint32, bool) {
		var s uint32
		ok := ix.docBlock(blk, func(_ uint32, st uint32, _ uint64, _ []byte) bool { s = st; return false }) == nil
		return s, ok
	}
	// The last block starting at or before the stream; a stream's
	// articles may go on into the next blocks.
	blk := sort.Search(len(ix.docOffs), func(i int) bool {
		s, ok := firstStream(i)
		return !ok || s >= stream
	})
	if blk > 0 {
		blk--
	}
	var found uint32
	ok := false
	for ; blk < len(ix.docOffs) && !ok; blk++ {
		past := false
		ix.docBlock(blk, func(n uint32, st uint32, pid uint64, _ []byte) bool {
			if st == stream && pid == id {
				found, ok = n, true
				return false
			}
			if st > stream {
				past = true
				return false
			}
			return true
		})
		if past {
			break
		}
	}
	return found, ok
}

// Place is an article with a location.
type Place struct {
	Doc
	Lat, Lon float64
	Km       float64
}

// Nearby lists the articles closest to a point, nearest first.
func (ix *Index) Nearby(lat, lon float64, limit int, skipTitle string) ([]Place, error) {
	type cand struct {
		i  int
		km float64
	}
	var best []cand
	geo := ix.places()
	for i, g := range geo {
		km := haversine(lat, lon, float64(g.lat), float64(g.lon))
		if len(best) < limit+1 || km < best[len(best)-1].km {
			best = append(best, cand{i, km})
			sort.Slice(best, func(a, b int) bool { return best[a].km < best[b].km })
			if len(best) > limit+1 {
				best = best[:limit+1]
			}
		}
	}
	var out []Place
	for _, c := range best {
		g := geo[c.i]
		d, err := ix.Doc(g.doc)
		if err != nil {
			return nil, err
		}
		if d.Title == skipTitle {
			continue
		}
		out = append(out, Place{Doc: d, Lat: float64(g.lat), Lon: float64(g.lon), Km: c.km})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Snippet returns HTML: about width characters of text around the first
// place the terms occur, with the terms marked.
func Snippet(text string, terms []string, width int) string {
	s, _ := snippet(text, terms, width)
	return s
}

// snippet is Snippet, reporting whether the terms were found.
func snippet(text string, terms []string, width int) (string, bool) {
	want := map[string]bool{}
	for _, t := range terms {
		want[t] = true
	}
	words := strings.Fields(text)
	at := -1
	for i, w := range words {
		hit := false
		Tokenize(w, func(t string) {
			if want[t] {
				hit = true
			}
		})
		if hit {
			at = i
			break
		}
	}
	found := at >= 0
	if at < 0 {
		at = 0
	}
	start := at - 8
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("… ")
	}
	n := 0
	i := start
	for ; i < len(words) && n < width; i++ {
		w := words[i]
		hit := false
		Tokenize(w, func(t string) {
			if want[t] {
				hit = true
			}
		})
		if i > start {
			b.WriteByte(' ')
		}
		if hit {
			b.WriteString("<mark>" + html.EscapeString(w) + "</mark>")
		} else {
			b.WriteString(html.EscapeString(w))
		}
		n += len([]rune(w)) + 1
	}
	if i < len(words) {
		b.WriteString(" …")
	}
	return b.String(), found
}

// TitleDoc is the article whose title is q (or that q redirects to), for
// Search to put first: nothing when there is none.
func (ix *Index) TitleDoc(rd *dump.Reader, q string) []Doc {
	q = strings.TrimSpace(q)
	if rd == nil || q == "" {
		return nil
	}
	e, _, err := rd.Index.Find(q)
	if err != nil || e.NS != dump.NSMain {
		return nil
	}
	if _, ok := ix.DocOf(e.Stream, e.ID); !ok {
		// not an article of its own: a redirect, read to follow it
		p, err := rd.Load(e)
		if err != nil || p.Redirect == "" {
			return nil
		}
		if e, _, err = rd.Index.Find(p.Redirect); err != nil {
			return nil
		}
	}
	return []Doc{{Stream: e.Stream, PageID: e.ID, Title: e.Title}}
}
