package fulltext

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

// File layout of a full-text index (".nfts"):
//
//	"NABUFTS1"
//	postings    per term, one uvarint per article: the doc-number delta
//	            shifted left 2, with the term's weight in that article
//	            (tfBucket) in the low 2 bits
//	dictionary  terms in byte order, 128 per block; a block starts with
//	            the postings offset of its first term, then each term
//	            front-coded with its document count and postings length
//	            (postings follow one another, so offsets need not be kept)
//	docs        per article: stream and page id (as differences from the
//	            previous article, absolute at the start of each block of
//	            128), title
//	geo         articles with coordinates: doc, latitude, longitude
//	meta        JSON
//	trailer     section offsets, "NABUEND2"
const (
	ftsMagic      = "NABUFTS1"
	ftsEnd        = "NABUEND2"
	dictBlock     = 128
	docBlock      = 128
	ftsTrailer    = 7*8 + 8
	FormatVersion = 2
)

// Meta describes a full-text index.
type Meta struct {
	Version   int           `json:"version"`
	Docs      int           `json:"docs"`
	Terms     int           `json:"terms"`
	Geo       int           `json:"geo"`
	Lang      string        `json:"lang"`
	Stopwords []string      `json:"stopwords"` // the language's little words, and any word in over 40% of articles: not indexed
	Capital   []string      `json:"capital"`   // little words still indexed when capitalised ("May")
	Common    []string      `json:"common"`    // the words dropped for being in over 40% of articles
	Postings  int64         `json:"postings"`
	Sources   []dump.Source `json:"sources"`
	Built     time.Time     `json:"built"`
	Seconds   float64       `json:"seconds"`
}

// Progress reports streams processed out of the total.
type Progress func(done, total int64)

// Options tune a build.
type Options struct {
	Workers   int // parallel decompression; default: all CPUs but one
	MemBudget int // bytes for postings before spilling to disk
	Progress  Progress
	Stop      <-chan struct{} // closed to abandon the build
}

type docOut struct {
	stream uint32
	pageID uint64
	title  string
	terms  []termTF
	coord  *wikitext.Coord
}

type termTF struct {
	term string
	tf   uint8
}

type posting struct {
	doc uint32
	tf  uint8
}

// ErrStopped is returned when a build is cancelled.
var ErrStopped = errors.New("fulltext: build stopped")

// Build scans every article of rd's dump and writes the index to out.
func Build(rd *dump.Reader, out, tmpDir string, opt Options) error {
	start := time.Now()
	if opt.Workers <= 0 {
		opt.Workers = runtime.NumCPU() - 1
		if opt.Workers < 1 {
			opt.Workers = 1
		}
	}
	if opt.MemBudget <= 0 {
		opt.MemBudget = 128 << 20
	}
	prev := debug.SetMemoryLimit(int64(opt.MemBudget)*2 + 96<<20)
	defer debug.SetMemoryLimit(prev)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	b := &ftsBuilder{tmp: tmpDir, budget: opt.MemBudget, batch: map[string][]posting{}}
	defer b.cleanup()
	docsTmp, err := os.Create(filepath.Join(tmpDir, "docs"))
	if err != nil {
		return err
	}
	defer docsTmp.Close()
	b.docs = bufio.NewWriterSize(docsTmp, 1<<20)

	site := rd.Site()
	lang := Lang(rd.Files.DB, site)
	rules := NewRules(Stopwords(lang), CapitalNames(lang))
	total := rd.Index.StreamCount()
	jobs := make(chan uint32, opt.Workers*2)
	results := make(chan streamResult, opt.Workers*2)
	var wg sync.WaitGroup
	for w := 0; w < opt.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				results <- scanStream(rd, site, s, rules)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for s := 0; s < total; s++ {
			select {
			case jobs <- uint32(s):
			case <-opt.Stop:
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	// Streams finish out of order; take them in order so article numbers
	// follow the dump.
	pending := map[uint32]streamResult{}
	next := uint32(0)
	var firstErr error
	for res := range results {
		pending[res.stream] = res
		for {
			r, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			next++
			if r.err != nil && firstErr == nil {
				firstErr = fmt.Errorf("stream %d: %w", r.stream, r.err)
			}
			if firstErr == nil {
				if err := b.addStream(r.docs); err != nil {
					firstErr = err
				}
			}
			if opt.Progress != nil {
				opt.Progress(int64(next), int64(total))
			}
		}
	}
	select {
	case <-opt.Stop:
		return ErrStopped
	default:
	}
	if firstErr != nil {
		return firstErr
	}
	if int(next) != total {
		return errors.New("fulltext: not every stream was read")
	}
	if err := b.docs.Flush(); err != nil {
		return err
	}
	meta := &Meta{Version: FormatVersion, Lang: lang, Stopwords: Stopwords(lang), Capital: CapitalNames(lang), Docs: int(b.ndocs), Geo: len(b.geo), Sources: dump.SourcesOf(rd.Files), Built: time.Now().UTC()}
	tmpOut := out + ".tmp"
	if err := b.write(tmpOut, docsTmp, meta, start); err != nil {
		os.Remove(tmpOut)
		return err
	}
	return os.Rename(tmpOut, out)
}

type streamResult struct {
	stream uint32
	docs   []docOut
	err    error
}

// Lang is the language of a wiki, for its list of little words.
func Lang(db string, site *dump.Siteinfo) string {
	if site != nil && site.Lang != "" && !strings.HasPrefix(db, "simple") {
		return site.Lang
	}
	return strings.TrimSuffix(db, "wiki")
}

func scanStream(rd *dump.Reader, site *dump.Siteinfo, s uint32, rules Rules) streamResult {
	pages, err := rd.StreamPages(s)
	if err != nil {
		return streamResult{stream: s, err: err}
	}
	res := streamResult{stream: s}
	for _, p := range pages {
		if p.NS != dump.NSMain || p.Redirect != "" {
			continue
		}
		counts := map[string]int{}
		add := func(w int) func(string, Case) {
			return func(t string, c Case) {
				if rules.Keep(t, c) && !junk(t) {
					counts[t] += w
				}
			}
		}
		TokenizeCase(p.Title, add(3)) // title words weigh more
		TokenizeCase(wikitext.PlainText(p.Text, site), add(1))
		d := docOut{stream: s, pageID: p.ID, title: p.Title, terms: make([]termTF, 0, len(counts))}
		for t, c := range counts {
			if c > 255 {
				c = 255
			}
			d.terms = append(d.terms, termTF{t, uint8(c)})
		}
		if c, ok := wikitext.PrimaryCoord(p.Text); ok {
			d.coord = &c
		}
		res.docs = append(res.docs, d)
	}
	return res
}

type geoRec struct {
	doc      uint32
	lat, lon float32
}

type ftsBuilder struct {
	tmp     string
	budget  int
	batch   map[string][]posting
	mem     int
	runs    []string
	docs    *bufio.Writer
	ndocs   uint32
	docOffs []uint64 // offset in docs temp file of every docBlock-th doc
	docPos  uint64
	geo     []geoRec

	lastStream uint32 // previous article in the block
	lastID     uint64
}

func (b *ftsBuilder) addStream(docs []docOut) error {
	var tmp [binary.MaxVarintLen64]byte
	for _, d := range docs {
		doc := b.ndocs
		b.ndocs++
		if doc%docBlock == 0 {
			b.docOffs = append(b.docOffs, b.docPos)
			b.lastStream, b.lastID = 0, 0
		}
		n := binary.PutUvarint(tmp[:], uint64(d.stream-b.lastStream))
		b.docs.Write(tmp[:n])
		b.docPos += uint64(n)
		n = binary.PutVarint(tmp[:], int64(d.pageID-b.lastID))
		b.lastStream, b.lastID = d.stream, d.pageID
		b.docs.Write(tmp[:n])
		b.docPos += uint64(n)
		n = binary.PutUvarint(tmp[:], uint64(len(d.title)))
		b.docs.Write(tmp[:n])
		b.docs.WriteString(d.title)
		b.docPos += uint64(n + len(d.title))
		if d.coord != nil {
			b.geo = append(b.geo, geoRec{doc, float32(d.coord.Lat), float32(d.coord.Lon)})
		}
		for _, t := range d.terms {
			list, ok := b.batch[t.term]
			if !ok {
				b.mem += len(t.term) + 64
			}
			b.batch[t.term] = append(list, posting{doc, t.tf})
			b.mem += 8
		}
		if b.mem >= b.budget {
			if err := b.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

// flush writes the batch, sorted by term, as a run file.
func (b *ftsBuilder) flush() error {
	if len(b.batch) == 0 {
		return nil
	}
	terms := make([]string, 0, len(b.batch))
	for t := range b.batch {
		terms = append(terms, t)
	}
	sort.Strings(terms)
	name := filepath.Join(b.tmp, fmt.Sprintf("run-%04d", len(b.runs)))
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	var tmp [binary.MaxVarintLen64]byte
	for _, t := range terms {
		list := b.batch[t]
		w.Write(tmp[:binary.PutUvarint(tmp[:], uint64(len(t)))])
		w.WriteString(t)
		w.Write(tmp[:binary.PutUvarint(tmp[:], uint64(len(list)))])
		for _, p := range list {
			w.Write(tmp[:binary.PutUvarint(tmp[:], uint64(p.doc))])
			w.WriteByte(p.tf)
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return friendly(err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	b.runs = append(b.runs, name)
	b.batch = map[string][]posting{}
	b.mem = 0
	return nil
}

func (b *ftsBuilder) cleanup() {
	for _, r := range b.runs {
		os.Remove(r)
	}
	os.Remove(filepath.Join(b.tmp, "docs"))
}

type runCursor struct {
	f    *os.File
	r    *bufio.Reader
	term string
	n    uint64
	ok   bool
}

func (c *runCursor) advance() error {
	l, err := binary.ReadUvarint(c.r)
	if err == io.EOF {
		c.ok = false
		return nil
	}
	if err != nil {
		return err
	}
	buf := make([]byte, l)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return err
	}
	c.term = string(buf)
	c.n, err = binary.ReadUvarint(c.r)
	c.ok = err == nil
	return err
}

type cursorHeap []*runCursor

func (h cursorHeap) Len() int { return len(h) }
func (h cursorHeap) Less(i, j int) bool {
	if h[i].term != h[j].term {
		return h[i].term < h[j].term
	}
	return h[i].f.Name() < h[j].f.Name() // earlier runs hold earlier docs
}
func (h cursorHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *cursorHeap) Push(x interface{}) { *h = append(*h, x.(*runCursor)) }
func (h *cursorHeap) Pop() interface{} {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func (b *ftsBuilder) write(path string, docsTmp *os.File, meta *Meta, start time.Time) error {
	if err := b.flush(); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	var pos int64
	write := func(p []byte) { w.Write(p); pos += int64(len(p)) }
	var tmp [binary.MaxVarintLen64]byte
	uv := func(v uint64) { write(tmp[:binary.PutUvarint(tmp[:], v)]) }
	write([]byte(ftsMagic))

	// Merge the runs term by term, writing postings as we go.
	var h cursorHeap
	for _, name := range b.runs {
		rf, err := os.Open(name)
		if err != nil {
			return err
		}
		defer rf.Close()
		c := &runCursor{f: rf, r: bufio.NewReaderSize(rf, 256<<10)}
		if err := c.advance(); err != nil {
			return err
		}
		if c.ok {
			h = append(h, c)
		}
	}
	heap.Init(&h)
	type dictEntry struct {
		term      string
		df        uint32
		off, size uint64
	}
	var dict []dictEntry
	common := uint32(meta.Docs * 2 / 5)
	var docs []posting
	for h.Len() > 0 {
		term := h[0].term
		docs = docs[:0]
		for h.Len() > 0 && h[0].term == term {
			c := h[0]
			for i := uint64(0); i < c.n; i++ {
				d, err := binary.ReadUvarint(c.r)
				if err != nil {
					return err
				}
				tf, err := c.r.ReadByte()
				if err != nil {
					return err
				}
				docs = append(docs, posting{uint32(d), tf})
			}
			if err := c.advance(); err != nil {
				return err
			}
			if c.ok {
				heap.Fix(&h, 0)
			} else {
				heap.Pop(&h)
			}
		}
		if meta.Docs >= 1000 && uint32(len(docs)) > common {
			meta.Common = append(meta.Common, term)
			continue
		}
		off := pos
		last := uint32(0)
		for _, p := range docs {
			uv(uint64(p.doc-last)<<2 | uint64(tfBucket(p.tf)))
			last = p.doc
		}
		meta.Postings += int64(len(docs))
		dict = append(dict, dictEntry{term, uint32(len(docs)), uint64(off), uint64(pos - off)})
	}
	meta.Terms = len(dict)

	// Dictionary blocks and their directory.
	dictOff := pos
	var blockOffs []int64
	var blockFirst []string
	prev := ""
	for i, e := range dict {
		if i%dictBlock == 0 {
			blockOffs = append(blockOffs, pos)
			blockFirst = append(blockFirst, e.term)
			prev = ""
			uv(e.off)
		}
		shared := commonPrefix(prev, e.term)
		uv(uint64(shared))
		uv(uint64(len(e.term) - shared))
		write([]byte(e.term[shared:]))
		uv(uint64(e.df))
		uv(e.size)
		prev = e.term
	}
	dirOff := pos
	uv(uint64(len(blockOffs)))
	for i, o := range blockOffs {
		uv(uint64(o))
		uv(uint64(len(blockFirst[i])))
		write([]byte(blockFirst[i]))
	}

	// Documents: copy the temporary table and its block offsets.
	docsOff := pos
	if _, err := docsTmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return friendly(err)
	}
	n, err := io.Copy(f, docsTmp)
	if err != nil {
		return friendly(err)
	}
	pos += n
	docDirOff := pos
	uv(uint64(len(b.docOffs)))
	for _, o := range b.docOffs {
		uv(o)
	}

	// Places.
	geoOff := pos
	uv(uint64(len(b.geo)))
	var g [12]byte
	for _, r := range b.geo {
		binary.LittleEndian.PutUint32(g[0:], r.doc)
		binary.LittleEndian.PutUint32(g[4:], math.Float32bits(r.lat))
		binary.LittleEndian.PutUint32(g[8:], math.Float32bits(r.lon))
		write(g[:])
	}

	metaOff := pos
	meta.Seconds = time.Since(start).Seconds()
	mj, _ := json.Marshal(meta)
	write(mj)
	var tr [ftsTrailer]byte
	for i, v := range []int64{dictOff, dirOff, docsOff, docDirOff, geoOff, metaOff, FormatVersion} {
		binary.LittleEndian.PutUint64(tr[i*8:], uint64(v))
	}
	copy(tr[56:], ftsEnd)
	write(tr[:])
	if err := w.Flush(); err != nil {
		return friendly(err)
	}
	return f.Sync()
}

// A term's count in an article is kept as one of four weights: once,
// twice, a few times (or in the title), often. Ranking hardly changes,
// and each article in a term's list takes one byte instead of two.
func tfBucket(tf uint8) uint8 {
	switch {
	case tf <= 1:
		return 0
	case tf == 2:
		return 1
	case tf <= 5:
		return 2
	default:
		return 3
	}
}

var bucketTF = [4]uint8{1, 2, 4, 8}

func commonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n > 255 {
		n = 255
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func friendly(err error) error {
	if err != nil && (errors.Is(err, os.ErrClosed) || containsNoSpace(err.Error())) {
		return errors.New("the drive is full")
	}
	return err
}

func containsNoSpace(s string) bool {
	for i := 0; i+8 <= len(s); i++ {
		if s[i:i+8] == "no space" {
			return true
		}
	}
	return false
}
