package dump

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"container/heap"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"time"
)

// The NABU index (".nidx") is Wikimedia's index re-sorted by search key,
// so titles can be found by binary search and searched by prefix.
//
//	"NABUIDX1"
//	blocks      records sorted by (Fold(title), title), 64 per block,
//	            titles front-coded against the previous one in the block
//	streams     every bzip2 stream: data file number + offset
//	directory   offset and first title of each block (kept in memory)
//	meta        JSON: siteinfo, source files, counts
//	trailer     offsets of the sections, record count, "NABUEND1"
//
// For English Wikipedia (~30 million titles) the directory is ~15 MB of
// memory; everything else stays on the drive.
const (
	idxMagic   = "NABUIDX1"
	idxEnd     = "NABUEND1"
	blockSize  = 64
	trailerLen = 5*8 + 8
	// IndexVersion changes whenever the format does, forcing a rebuild.
	IndexVersion = 1
)

type record struct {
	fold   string
	title  string
	stream uint32
	id     uint64
	ns     int32
}

func less(a, b *record) bool {
	if a.fold != b.fold {
		return a.fold < b.fold
	}
	return a.title < b.title
}

// Meta is stored inside the index.
type Meta struct {
	Version  int       `json:"version"`
	Site     *Siteinfo `json:"site"`
	Sources  []Source  `json:"sources"`
	Records  int64     `json:"records"`
	Articles int64     `json:"articles"` // namespace 0 entries, redirects included
	Built    time.Time `json:"built"`
	DataSize []int64   `json:"data_size"` // per pair, for the last stream's end
}

// Source records the files an index was built from, to detect changes.
type Source struct {
	Index     string `json:"index"`
	IndexSize int64  `json:"index_size"`
	DataSize  int64  `json:"data_size"`
}

// SourcesOf describes f for change detection.
func SourcesOf(f *Files) []Source {
	var out []Source
	for _, p := range f.Pairs {
		s := Source{Index: filepath.Base(p.Index), DataSize: p.Size}
		if st, err := os.Stat(p.Index); err == nil {
			s.IndexSize = st.Size()
		}
		out = append(out, s)
	}
	return out
}

// Progress reports index building: bytes of compressed index read.
type Progress func(done, total int64)

// Build creates the NABU index for f at out. tmpDir holds sorted runs
// while building; memBudget bounds the memory used for sorting.
func Build(f *Files, out, tmpDir string, memBudget int, progress Progress) (err error) {
	if len(f.Pairs) == 0 {
		return errors.New("dump: nothing to index")
	}
	var total int64
	for _, p := range f.Pairs {
		if st, err := os.Stat(p.Index); err == nil {
			total += st.Size()
		}
	}
	if memBudget <= 0 {
		memBudget = 96 << 20
	}
	// Keep the garbage collector close to the budget so preparing a big
	// dump never pushes an old laptop into swapping.
	prevLimit := debug.SetMemoryLimit(int64(memBudget)*2 + 32<<20)
	defer debug.SetMemoryLimit(prevLimit)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	b := &builder{tmpDir: tmpDir, budget: memBudget}
	defer b.cleanup()

	var site *Siteinfo
	var dataSizes []int64
	var done int64
	var articles int64
	for pi, p := range f.Pairs {
		data, size, err := OpenData(p.Data)
		if err != nil {
			return err
		}
		dataSizes = append(dataSizes, size)
		idxFile, err := os.Open(p.Index)
		if err != nil {
			data.Close()
			return err
		}
		cr := &countReader{r: idxFile, done: &done, total: total, progress: progress}
		sc := bufio.NewScanner(bzip2.NewReader(bufio.NewReaderSize(cr, 256<<10)))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		lastOff := int64(-1)
		for sc.Scan() {
			line := sc.Bytes()
			c1 := bytes.IndexByte(line, ':')
			if c1 < 0 {
				continue
			}
			c2 := bytes.IndexByte(line[c1+1:], ':')
			if c2 < 0 {
				continue
			}
			off, err1 := strconv.ParseInt(string(line[:c1]), 10, 64)
			id, err2 := strconv.ParseUint(string(line[c1+1:c1+1+c2]), 10, 64)
			if err1 != nil || err2 != nil {
				continue
			}
			title := string(line[c1+2+c2:])
			if site == nil {
				site, err = SiteinfoFromDump(data, off)
				if err != nil {
					idxFile.Close()
					data.Close()
					return fmt.Errorf("reading the dump header: %w", err)
				}
			}
			if off != lastOff {
				if off < lastOff || off >= size {
					idxFile.Close()
					data.Close()
					return fmt.Errorf("%s: offset %d out of order or past the end; is the data file complete?", filepath.Base(p.Index), off)
				}
				b.streams = append(b.streams, streamRef{pair: uint16(pi), off: off})
				lastOff = off
			}
			ns, _ := site.SplitTitle(title)
			if ns == NSMain {
				articles++
			}
			if err := b.add(record{fold: Fold(title), title: title, stream: uint32(len(b.streams) - 1), id: id, ns: int32(ns)}); err != nil {
				idxFile.Close()
				data.Close()
				return err
			}
		}
		idxFile.Close()
		data.Close()
		if err := sc.Err(); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(p.Index), err)
		}
	}
	if site == nil {
		return errors.New("dump: the index file is empty")
	}
	meta := Meta{Version: IndexVersion, Site: site, Sources: SourcesOf(f), Articles: articles,
		Built: time.Now().UTC(), DataSize: dataSizes}
	tmpOut := out + ".tmp"
	if err := b.write(tmpOut, &meta); err != nil {
		os.Remove(tmpOut)
		return err
	}
	return os.Rename(tmpOut, out)
}

type streamRef struct {
	pair uint16
	off  int64
}

type countReader struct {
	r        io.Reader
	done     *int64
	total    int64
	progress Progress
	last     int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.done += int64(n)
	if c.progress != nil && *c.done-c.last > 1<<20 {
		c.last = *c.done
		c.progress(*c.done, c.total)
	}
	return n, err
}

type builder struct {
	tmpDir  string
	budget  int
	buf     []record
	bufMem  int
	runs    []string
	streams []streamRef
	records int64
}

func (b *builder) add(r record) error {
	b.buf = append(b.buf, r)
	// Two strings, the struct, and slack for the slice doubling.
	b.bufMem += len(r.fold) + len(r.title) + 112
	b.records++
	if b.bufMem >= b.budget {
		return b.flush()
	}
	return nil
}

// flush sorts the buffer and writes it out as a run file.
func (b *builder) flush() error {
	if len(b.buf) == 0 {
		return nil
	}
	sort.Slice(b.buf, func(i, j int) bool { return less(&b.buf[i], &b.buf[j]) })
	name := filepath.Join(b.tmpDir, fmt.Sprintf("run-%04d", len(b.runs)))
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for i := range b.buf {
		writeRecord(w, &b.buf[i])
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return friendly(err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	b.runs = append(b.runs, name)
	b.buf = b.buf[:0]
	b.bufMem = 0
	return nil
}

func (b *builder) cleanup() {
	for _, r := range b.runs {
		os.Remove(r)
	}
}

func writeRecord(w *bufio.Writer, r *record) {
	var tmp [binary.MaxVarintLen64]byte
	put := func(v uint64) { w.Write(tmp[:binary.PutUvarint(tmp[:], v)]) }
	put(uint64(len(r.title)))
	w.WriteString(r.title)
	put(uint64(r.stream))
	put(r.id)
	w.Write(tmp[:binary.PutVarint(tmp[:], int64(r.ns))])
}

func readRecord(r *bufio.Reader, rec *record) error {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	s, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	id, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	ns, err := binary.ReadVarint(r)
	if err != nil {
		return err
	}
	rec.title, rec.stream, rec.id, rec.ns = string(buf), uint32(s), id, int32(ns)
	rec.fold = Fold(rec.title)
	return nil
}

// source yields records in sorted order: from memory when everything
// fit, or by merging the run files.
type source interface{ next() (*record, error) }

type memSource struct {
	recs []record
	i    int
}

func (m *memSource) next() (*record, error) {
	if m.i >= len(m.recs) {
		return nil, io.EOF
	}
	m.i++
	return &m.recs[m.i-1], nil
}

type runReader struct {
	r   *bufio.Reader
	f   *os.File
	cur record
}

type mergeHeap []*runReader

func (h mergeHeap) Len() int            { return len(h) }
func (h mergeHeap) Less(i, j int) bool  { return less(&h[i].cur, &h[j].cur) }
func (h mergeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(*runReader)) }
func (h *mergeHeap) Pop() interface{} {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

type mergeSource struct {
	h   mergeHeap
	out record
}

func (m *mergeSource) next() (*record, error) {
	if len(m.h) == 0 {
		return nil, io.EOF
	}
	top := m.h[0]
	m.out = top.cur
	if err := readRecord(top.r, &top.cur); err != nil {
		if err != io.EOF {
			return nil, err
		}
		top.f.Close()
		heap.Pop(&m.h)
	} else {
		heap.Fix(&m.h, 0)
	}
	return &m.out, nil
}

func (b *builder) source() (source, func(), error) {
	if len(b.runs) == 0 {
		sort.Slice(b.buf, func(i, j int) bool { return less(&b.buf[i], &b.buf[j]) })
		return &memSource{recs: b.buf}, func() {}, nil
	}
	if err := b.flush(); err != nil {
		return nil, nil, err
	}
	m := &mergeSource{}
	closeAll := func() {
		for _, rr := range m.h {
			rr.f.Close()
		}
	}
	for _, name := range b.runs {
		f, err := os.Open(name)
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		rr := &runReader{f: f, r: bufio.NewReaderSize(f, 256<<10)}
		if err := readRecord(rr.r, &rr.cur); err != nil {
			f.Close()
			if err == io.EOF {
				continue
			}
			closeAll()
			return nil, nil, err
		}
		m.h = append(m.h, rr)
	}
	heap.Init(&m.h)
	return m, closeAll, nil
}

func (b *builder) write(path string, meta *Meta) error {
	src, closeSrc, err := b.source()
	if err != nil {
		return err
	}
	defer closeSrc()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	var pos int64
	write := func(p []byte) {
		w.Write(p)
		pos += int64(len(p))
	}
	var tmp [binary.MaxVarintLen64]byte
	uv := func(v uint64) { write(tmp[:binary.PutUvarint(tmp[:], v)]) }

	write([]byte(idxMagic))
	var dirOffs []int64
	var dirTitles []string
	var prev string
	inBlock := 0
	for {
		r, err := src.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if inBlock == 0 {
			dirOffs = append(dirOffs, pos)
			dirTitles = append(dirTitles, r.title)
			prev = ""
		}
		shared := commonPrefix(prev, r.title)
		uv(uint64(shared))
		uv(uint64(len(r.title) - shared))
		write([]byte(r.title[shared:]))
		uv(uint64(r.stream))
		uv(r.id)
		write(tmp[:binary.PutVarint(tmp[:], int64(r.ns))])
		prev = r.title
		inBlock++
		meta.Records++
		if inBlock == blockSize {
			inBlock = 0
		}
	}
	streamsOff := pos
	uv(uint64(len(b.streams)))
	lastPair, lastOff := -1, int64(0)
	for _, s := range b.streams {
		uv(uint64(s.pair))
		if int(s.pair) != lastPair {
			lastOff = 0
		}
		uv(uint64(s.off - lastOff))
		lastPair, lastOff = int(s.pair), s.off
	}
	dirOff := pos
	uv(uint64(len(dirOffs)))
	var lastBlock int64
	for i, o := range dirOffs {
		uv(uint64(o - lastBlock))
		lastBlock = o
		uv(uint64(len(dirTitles[i])))
		write([]byte(dirTitles[i]))
	}
	metaOff := pos
	mj, _ := json.Marshal(meta)
	write(mj)
	var tr [trailerLen]byte
	binary.LittleEndian.PutUint64(tr[0:], uint64(streamsOff))
	binary.LittleEndian.PutUint64(tr[8:], uint64(dirOff))
	binary.LittleEndian.PutUint64(tr[16:], uint64(metaOff))
	binary.LittleEndian.PutUint64(tr[24:], uint64(meta.Records))
	binary.LittleEndian.PutUint64(tr[32:], IndexVersion)
	copy(tr[40:], idxEnd)
	write(tr[:])
	if err := w.Flush(); err != nil {
		return friendly(err)
	}
	return f.Sync()
}

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
	if err != nil && bytes.Contains([]byte(err.Error()), []byte("no space")) {
		return errors.New("the drive is full")
	}
	return err
}
