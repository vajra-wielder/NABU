package fulltext

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"html"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Index is an opened full-text index.
type Index struct {
	f        *os.File
	Meta     Meta
	stop     map[string]bool
	dictOff  []int64 // block offsets (+ end)
	dictTerm []string
	docsOff  int64
	docOffs  []uint64
	docsEnd  int64
	geo      []geoRec

	mu    sync.Mutex
	cache map[int][]dictRow
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
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	ix := &Index{f: file, cache: map[int][]dictRow{}, stop: map[string]bool{}}
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
	if o[6] != FormatVersion || !(dictOff <= dirOff && dirOff <= docsOff && docsOff <= docDirOff && docDirOff <= geoOff && geoOff <= metaOff && metaOff <= size-ftsTrailer) {
		return ErrStale
	}
	mb := make([]byte, size-ftsTrailer-metaOff)
	if _, err := ix.f.ReadAt(mb, metaOff); err != nil {
		return err
	}
	if err := json.Unmarshal(mb, &ix.Meta); err != nil || ix.Meta.Version != FormatVersion {
		return ErrStale
	}
	for _, s := range ix.Meta.Stopwords {
		ix.stop[s] = true
	}
	r := bufio.NewReader(io.NewSectionReader(ix.f, dirOff, docsOff-dirOff))
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return ErrStale
	}
	for i := uint64(0); i < n; i++ {
		off, e1 := binary.ReadUvarint(r)
		l, e2 := binary.ReadUvarint(r)
		if e1 != nil || e2 != nil || l > 1024 {
			return ErrStale
		}
		t := make([]byte, l)
		if _, err := io.ReadFull(r, t); err != nil {
			return ErrStale
		}
		ix.dictOff = append(ix.dictOff, int64(off))
		ix.dictTerm = append(ix.dictTerm, string(t))
	}
	ix.dictOff = append(ix.dictOff, dirOff)
	ix.docsOff, ix.docsEnd = docsOff, docDirOff
	r = bufio.NewReader(io.NewSectionReader(ix.f, docDirOff, geoOff-docDirOff))
	n, err = binary.ReadUvarint(r)
	if err != nil {
		return ErrStale
	}
	ix.docOffs = make([]uint64, 0, n)
	for i := uint64(0); i < n; i++ {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return ErrStale
		}
		ix.docOffs = append(ix.docOffs, v)
	}
	r = bufio.NewReader(io.NewSectionReader(ix.f, geoOff, metaOff-geoOff))
	n, err = binary.ReadUvarint(r)
	if err != nil {
		return ErrStale
	}
	ix.geo = make([]geoRec, 0, n)
	var g [12]byte
	for i := uint64(0); i < n; i++ {
		if _, err := io.ReadFull(r, g[:]); err != nil {
			return ErrStale
		}
		ix.geo = append(ix.geo, geoRec{binary.LittleEndian.Uint32(g[0:]),
			math.Float32frombits(binary.LittleEndian.Uint32(g[4:])), math.Float32frombits(binary.LittleEndian.Uint32(g[8:]))})
	}
	return nil
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
	blk := int(n / docBlock)
	if blk >= len(ix.docOffs) {
		return Doc{}, errors.New("fulltext: no such document")
	}
	end := ix.docsEnd
	if blk+1 < len(ix.docOffs) {
		end = ix.docsOff + int64(ix.docOffs[blk+1])
	}
	start := ix.docsOff + int64(ix.docOffs[blk])
	buf := make([]byte, end-start)
	if _, err := ix.f.ReadAt(buf, start); err != nil {
		return Doc{}, err
	}
	r := bytes.NewReader(buf)
	var stream, id uint64
	for i := uint32(0); ; i++ {
		ds, e1 := binary.ReadUvarint(r)
		did, e2 := binary.ReadVarint(r)
		l, e3 := binary.ReadUvarint(r)
		if e1 != nil || e2 != nil || e3 != nil || int(l) > r.Len() {
			return Doc{}, ErrStale
		}
		stream += ds
		id = uint64(int64(id) + did)
		if uint32(blk)*docBlock+i == n {
			t := make([]byte, l)
			r.Read(t)
			return Doc{Stream: uint32(stream), PageID: id, Title: string(t)}, nil
		}
		r.Seek(int64(l), io.SeekCurrent)
	}
}

// Hit is a search result.
type Hit struct {
	Doc
	N     uint32
	Score float64
}

// Result is the outcome of a search.
type Result struct {
	Hits    []Hit
	Total   int      // articles containing every searched term
	Terms   []string // terms searched
	Ignored []string // too common to search
}

// Search finds articles containing every term of q, best first.
func (ix *Index) Search(q string, offset, limit int) (*Result, error) {
	res := &Result{}
	type termList struct {
		term string
		df   uint32
		row  dictRow
	}
	var lists []termList
	for _, t := range Terms(q) {
		if ix.stop[t] {
			res.Ignored = append(res.Ignored, t)
			continue
		}
		row, ok, err := ix.lookup(t)
		if err != nil {
			return nil, err
		}
		res.Terms = append(res.Terms, t)
		if !ok {
			return res, nil // a term in no article: nothing matches
		}
		lists = append(lists, termList{t, row.df, row})
	}
	if len(lists) == 0 {
		return res, nil
	}
	sort.Slice(lists, func(i, j int) bool { return lists[i].df < lists[j].df })
	N := float64(ix.Meta.Docs)
	idf := func(df uint32) float64 { return math.Log(1 + (N-float64(df)+0.5)/(float64(df)+0.5)) }
	type cand struct {
		doc   uint32
		score float64
	}
	first, err := ix.postings(lists[0].row)
	if err != nil {
		return nil, err
	}
	cands := make([]cand, len(first))
	w := idf(lists[0].df)
	for i, p := range first {
		cands[i] = cand{p.doc, w * float64(p.tf) / (float64(p.tf) + 1.2)}
	}
	for _, l := range lists[1:] {
		ps, err := ix.postings(l.row)
		if err != nil {
			return nil, err
		}
		w := idf(l.df)
		out := cands[:0]
		j := 0
		for _, c := range cands {
			for j < len(ps) && ps[j].doc < c.doc {
				j++
			}
			if j < len(ps) && ps[j].doc == c.doc {
				c.score += w * float64(ps[j].tf) / (float64(ps[j].tf) + 1.2)
				out = append(out, c)
			}
		}
		cands = out
		if len(cands) == 0 {
			break
		}
	}
	res.Total = len(cands)
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	// Re-rank the best few with their titles: a title containing the
	// words, or equal to the query, comes first.
	top := offset + limit + 50
	if top > len(cands) {
		top = len(cands)
	}
	if top > 400 {
		top = 400
	}
	fq := dump.Fold(q)
	hits := make([]Hit, 0, top)
	for _, c := range cands[:top] {
		d, err := ix.Doc(c.doc)
		if err != nil {
			return nil, err
		}
		h := Hit{Doc: d, N: c.doc, Score: c.score}
		ft := dump.Fold(d.Title)
		if ft == fq {
			h.Score += 100
		} else {
			inTitle := 0
			Tokenize(d.Title, func(t string) {
				for _, q := range res.Terms {
					if t == q {
						inTitle++
					}
				}
			})
			h.Score += 2 * float64(inTitle)
		}
		hits = append(hits, h)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if offset > len(hits) {
		offset = len(hits)
	}
	end := offset + limit
	if end > len(hits) {
		end = len(hits)
	}
	res.Hits = hits[offset:end]
	return res, nil
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
	for i, g := range ix.geo {
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
		g := ix.geo[c.i]
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
	return b.String()
}
