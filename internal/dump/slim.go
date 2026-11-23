package dump

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vajra-wielder/nabu/internal/drivefile"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/bzip2w"
)

// A dump holds every page of the wiki, but NABU only shows a reader's
// pages: articles (with their redirects), category pages, file pages
// (what a picture is) and portals. The rest is the wiki's own workings:
// discussions about the project (on English Wikipedia 18% of the dump),
// templates and Lua modules (NABU has its own), drafts, user pages,
// help and interface messages. Namespace numbers are the same in every
// language, so this works for any Wikipedia.
var slimKeep = map[int]bool{NSMain: true, NSFile: true, NSCategory: true, 100: true}

// KeptBySlim reports whether pages of a namespace stay in a slim dump.
func KeptBySlim(ns int) bool { return slimKeep[ns] }

// SlimOptions tune Slim.
type SlimOptions struct {
	PartSize int64                    // write data files in pieces of this size (FAT32); 0: one file
	Workers  int                      // default: all CPUs but one
	Progress Progress                 // compressed bytes of the source read
	Stop     <-chan struct{}          // closed to abandon
	Name     func(path string) string // the name a source file gets in dir (default: its own)
	// Bundle is the most text kept in one compressed stream (default
	// BundleSize). An article is read by decompressing its whole stream,
	// so smaller streams open faster.
	Bundle int
}

// BundleSize is how much text a slim dump keeps per compressed stream.
// Wikimedia's streams hold 100 pages, up to megabytes of text; at 128 KB
// an article opens 3.5 times faster on average (measured on Simple
// English: 21 to 6 ms), for 10% more room.
const BundleSize = 128 << 10

// bundledBelow: a stream of several pages bigger than this (compressed)
// is one of Wikimedia's (100 pages: ~100 KB on English Wikipedia);
// bundles of at most 128 KB of text compress to ~30-40 KB.
const bundledBelow = 48 << 10

// SlimResult says what Slim did.
type SlimResult struct {
	Before, After int64 // bytes of data and index files
	Pages, Kept   int
}

// ErrSlimStopped is returned when Slim is stopped.
var ErrSlimStopped = errors.New("dump: slimming stopped")

// FromNamed makes the Files of a dump whose files are not (yet) under
// their own names: names maps each file's real name to the paths holding
// it, in order (a download in pieces, say).
func FromNamed(names map[string][]string) (*Files, error) {
	type key struct{ db, date, n, rng string }
	data := map[key]string{}
	index := map[key]string{}
	for name := range names {
		if m := dataRe.FindStringSubmatch(name); m != nil && m[5] == "" {
			data[key{m[1], m[2], m[3], m[4]}] = name
		} else if m := indexRe.FindStringSubmatch(name); m != nil {
			index[key{m[1], m[2], m[3], m[4]}] = name
		}
	}
	var f *Files
	for k, d := range data {
		i, ok := index[k]
		if !ok || len(names[i]) != 1 {
			return nil, fmt.Errorf("%s has no index", d)
		}
		if f == nil {
			f = &Files{DB: k.db, Date: k.date}
		} else if f.DB != k.db || f.Date != k.date {
			return nil, errors.New("files of different dumps")
		}
		p := Pair{Data: names[d], Index: names[i][0], part: atoi(k.n)}
		for _, x := range p.Data {
			if st, err := os.Stat(x); err == nil {
				p.Size += st.Size()
			}
		}
		f.Pairs = append(f.Pairs, p)
		f.Size += p.Size
	}
	if f == nil {
		return nil, errors.New("no dump among the files")
	}
	sort.Slice(f.Pairs, func(i, j int) bool { return f.Pairs[i].part < f.Pairs[j].part })
	return f, nil
}

// NeedsSlim reports whether a dump still has pages Slim would drop, or
// still has Wikimedia's big streams. It reads only the index (titles
// carry their namespace).
func NeedsSlim(f *Files) (bool, error) {
	for _, p := range f.Pairs {
		data, size, err := OpenData(p.Data)
		if err != nil {
			return false, err
		}
		big := false
		found, err := func() (bool, error) {
			defer data.Close()
			idx, err := os.Open(p.Index)
			if err != nil {
				return false, err
			}
			defer idx.Close()
			sc := bufio.NewScanner(bzip2.NewReader(bufio.NewReaderSize(idx, 256<<10)))
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			var site *Siteinfo
			// Wikimedia's big streams, not yet in small bundles? In a slim
			// dump a bundle of several pages is never big (an article
			// bigger than a bundle has one of its own); in Wikimedia's,
			// most hold 100 pages and far more text.
			multi, bigMulti := 0, 0
			all, other := 0, 0
			last, pages := int64(-1), 0
			closeStream := func(end int64) {
				if pages > 1 {
					multi++
					if end-last > bundledBelow {
						bigMulti++
					}
				}
			}
			defer func() {
				closeStream(size)
				if multi > 0 && bigMulti*2 > multi {
					big = true
				}
			}()
			for sc.Scan() {
				off, _, title, ok := indexLine(sc.Bytes())
				if !ok {
					continue
				}
				if off != last {
					if last >= 0 {
						closeStream(off)
					}
					last, pages = off, 0
				}
				pages++
				if site == nil {
					if site, err = SiteinfoFromDump(data, off); err != nil {
						return false, err
					}
				}
				all++
				if ns, _ := site.SplitTitle(title); !slimKeep[ns] {
					other++
				}
			}
			// A few article titles only look like other pages ("Draft :
			// Shein" is an article); a dump not slimmed has about a fifth.
			// More than 0.2% means not slimmed.
			return other*500 > all, sc.Err()
		}()
		if found || big || err != nil {
			return found || big, err
		}
	}
	return false, nil
}

func indexLine(line []byte) (off int64, id uint64, title string, ok bool) {
	c1 := bytes.IndexByte(line, ':')
	if c1 < 0 {
		return
	}
	c2 := bytes.IndexByte(line[c1+1:], ':')
	if c2 < 0 {
		return
	}
	off, err1 := strconv.ParseInt(string(line[:c1]), 10, 64)
	id, err2 := strconv.ParseUint(string(line[c1+1:c1+1+c2]), 10, 64)
	if err1 != nil || err2 != nil {
		return
	}
	return off, id, string(line[c1+2+c2:]), true
}

// Slim writes f into dir without the pages NABU never shows, as a dump of
// the same names and format: bzip2 multistream data with its index. The
// header and every stream that loses nothing are copied as they are; the
// rest are decompressed, filtered and compressed again. Files are written
// under temporary names; once all are complete, replace is called (to
// delete the original when slimming in place; it may be nil) and the
// temporary files take the real names.
func Slim(f *Files, dir string, opt SlimOptions, replace func() error) (SlimResult, error) {
	var res SlimResult
	if opt.Workers <= 0 {
		opt.Workers = runtime.NumCPU() - 1
		if opt.Workers < 1 {
			opt.Workers = 1
		}
	}
	bundle := opt.Bundle
	if bundle <= 0 {
		bundle = BundleSize
	}
	var total, done int64
	for _, p := range f.Pairs {
		total += p.Size
	}
	names := func(p Pair) (dataName, idxName string) {
		dataName = filepath.Base(p.Data[0])
		if m := dataRe.FindStringSubmatch(dataName); m != nil && m[5] != "" {
			dataName = dataName[:len(dataName)-2] // pieces: name.bz2aa
		}
		idxName = filepath.Base(p.Index)
		if opt.Name != nil {
			dataName, idxName = opt.Name(p.Data[0]), opt.Name(p.Index)
		}
		return
	}

	// Carry on from an interrupted slim of the same dump, if there is one.
	first, _ := names(f.Pairs[0])
	statePath := filepath.Join(dir, first+".slim-tmp-state.json")
	st := loadSlimState(statePath, f, bundle)
	if st == nil {
		st = &slimState{Version: slimStateVersion, Sources: SourcesOf(f), Bundle: bundle}
		for _, p := range f.Pairs { // what an older, different slim left
			d, i := names(p)
			old, _ := filepath.Glob(filepath.Join(dir, d) + ".slim-tmp-[0-9][0-9][0-9]")
			for _, o := range append(old, filepath.Join(dir, i)+".slim-tmp") {
				os.Remove(o)
			}
		}
	}
	save := func() error {
		b, _ := json.Marshal(st)
		return drivefile.WriteFile(statePath, b)
	}

	type rename struct{ from, to string }
	var renames []rename
	var temps []string
	cleanup := func() {
		for _, t := range temps {
			os.Remove(t)
		}
		os.Remove(statePath)
	}
	for pi, p := range f.Pairs {
		if st, err := os.Stat(p.Index); err == nil {
			res.Before += st.Size()
		}
		res.Before += p.Size
		dataName, idxName := names(p)
		dw := &pieceWriter{base: filepath.Join(dir, dataName), part: opt.PartSize}
		idxOut := filepath.Join(dir, idxName) + ".slim-tmp"
		temps = append(temps, idxOut)
		var err error
		if pi < st.Pair { // finished before the interruption
			for i := 0; i < st.Done[pi]; i++ {
				dw.names = append(dw.names, fmt.Sprintf("%s.slim-tmp-%03d", dw.base, i))
			}
		} else {
			rs := slimResume{checkpoint: func(next int, pieces []int64, indexLen int64) error {
				st.Pair, st.Next, st.Pieces, st.Index = pi, next, pieces, indexLen
				st.Pages, st.Kept = res.Pages, res.Kept
				return save()
			}}
			if pi == st.Pair && st.Next > 0 {
				if err = dw.resume(st.Pieces); err == nil {
					rs.next, rs.indexLen = st.Next, st.Index
					res.Pages, res.Kept = st.Pages, st.Kept
				}
			}
			if err == nil {
				err = slimPair(p, dw, idxOut, opt, &res, func(n int64) {
					if opt.Progress != nil {
						opt.Progress(done+n, total)
					}
				}, rs)
			}
			if err == nil {
				err = dw.close()
			} else {
				dw.closeCurrent()
			}
			if err == nil {
				st.Done = append(st.Done[:pi], len(dw.names))
				st.Pair, st.Next, st.Pieces, st.Index = pi+1, 0, nil, 0
				err = save()
			}
		}
		temps = append(temps, dw.names...)
		if err != nil {
			// Stopped: kept to carry on from the last checkpoint. Any
			// other trouble: nothing is left behind.
			if !errors.Is(err, ErrSlimStopped) {
				cleanup()
			}
			return res, err
		}
		done += p.Size
		for i, n := range dw.names {
			to := dw.base
			if len(dw.names) > 1 {
				to += fmt.Sprintf("%c%c", 'a'+byte(i/26), 'a'+byte(i%26))
			}
			renames = append(renames, rename{n, to})
			if st, err := os.Stat(n); err == nil {
				res.After += st.Size()
			}
		}
		renames = append(renames, rename{idxOut, filepath.Join(dir, idxName)})
		if st, err := os.Stat(idxOut); err == nil {
			res.After += st.Size()
		}
	}
	if replace != nil {
		if err := replace(); err != nil {
			cleanup()
			return res, err
		}
	}
	for _, r := range renames {
		if err := os.Rename(r.from, r.to); err != nil {
			return res, fmt.Errorf("putting %s in place: %w", filepath.Base(r.to), err)
		}
	}
	os.Remove(statePath)
	return res, nil
}

// checkpointEvery is how often a slim records its place.
var checkpointEvery = 30 * time.Second

const slimStateVersion = 1

// slimState is where a slim stands, saved beside its temporary files so
// an interrupted one (stopped, or the computer switched off) carries on
// from its last checkpoint instead of starting again: English takes over
// an hour.
type slimState struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"` // the dump being slimmed
	Bundle  int      `json:"bundle"`
	Done    []int    `json:"done"`   // pieces of each finished pair
	Pair    int      `json:"pair"`   // the pair being slimmed
	Next    int      `json:"next"`   // its next source stream
	Pieces  []int64  `json:"pieces"` // sizes of its data pieces so far
	Index   int64    `json:"index"`  // bytes of its index so far
	Pages   int      `json:"pages"`
	Kept    int      `json:"kept"`
}

func loadSlimState(path string, f *Files, bundle int) *slimState {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var st slimState
	if json.Unmarshal(b, &st) != nil || st.Version != slimStateVersion || st.Bundle != bundle ||
		len(st.Done) < st.Pair || st.Pair > len(f.Pairs) {
		return nil
	}
	want := SourcesOf(f)
	if len(want) != len(st.Sources) {
		return nil
	}
	for i := range want {
		if want[i] != st.Sources[i] {
			return nil
		}
	}
	return &st
}

// pieceWriter writes a file in pieces of at most part bytes, under
// temporary names.
type pieceWriter struct {
	base  string
	part  int64
	names []string
	f     *os.File
	w     *bufio.Writer
	n     int64 // in the current piece
	pos   int64 // in the whole file
}

func (p *pieceWriter) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		if p.f == nil || (p.part > 0 && p.n == p.part) {
			if err := p.next(); err != nil {
				return written, err
			}
		}
		chunk := b
		if p.part > 0 && int64(len(chunk)) > p.part-p.n {
			chunk = chunk[:p.part-p.n]
		}
		n, err := p.w.Write(chunk)
		written += n
		p.n += int64(n)
		p.pos += int64(n)
		b = b[n:]
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (p *pieceWriter) next() error {
	if err := p.closeCurrent(); err != nil {
		return err
	}
	name := fmt.Sprintf("%s.slim-tmp-%03d", p.base, len(p.names))
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	p.names = append(p.names, name)
	p.f, p.w, p.n = f, bufio.NewWriterSize(f, 1<<20), 0
	return nil
}

func (p *pieceWriter) closeCurrent() error {
	if p.f == nil {
		return nil
	}
	err := p.w.Flush()
	if err == nil {
		err = p.f.Sync()
	}
	if cerr := p.f.Close(); err == nil {
		err = cerr
	}
	p.f = nil
	return err
}

// sync puts what was written on the drive and returns each piece's size.
func (p *pieceWriter) sync() ([]int64, error) {
	if p.f != nil {
		if err := p.w.Flush(); err != nil {
			return nil, err
		}
		if err := p.f.Sync(); err != nil {
			return nil, err
		}
	}
	sizes := make([]int64, len(p.names))
	for i := range p.names {
		sizes[i] = p.part
		if i == len(p.names)-1 {
			sizes[i] = p.n
		}
	}
	return sizes, nil
}

// resume reopens the pieces written before a checkpoint, cut back to
// their sizes then (anything written after it is dropped).
func (p *pieceWriter) resume(sizes []int64) error {
	p.names, p.pos = nil, 0
	for i, size := range sizes {
		name := fmt.Sprintf("%s.slim-tmp-%03d", p.base, i)
		if err := os.Truncate(name, size); err != nil {
			return err
		}
		p.names = append(p.names, name)
		p.pos += size
	}
	for i := len(sizes); ; i++ { // pieces begun after the checkpoint
		if os.Remove(fmt.Sprintf("%s.slim-tmp-%03d", p.base, i)) != nil {
			break
		}
	}
	if len(sizes) == 0 {
		return nil
	}
	f, err := os.OpenFile(p.names[len(p.names)-1], os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	last := sizes[len(sizes)-1]
	if _, err := f.Seek(last, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	p.f, p.w, p.n = f, bufio.NewWriterSize(f, 1<<20), last
	return nil
}

func (p *pieceWriter) close() error {
	if p.f == nil && len(p.names) == 0 {
		if err := p.next(); err != nil { // an empty file is still a file
			return err
		}
	}
	return p.closeCurrent()
}

type slimJob struct {
	n      int
	start  int64
	raw    []byte
	lines  [][]byte // the index lines of this stream: "id:title"
	last   bool
	bundle int
}

type slimOut struct {
	n       int
	bundles []bundle
	pages   int
	kept    int
	err     error
}

// bundle is one compressed stream of the slim dump with the index lines
// ("id:title") of its pages.
type bundle struct {
	data  []byte
	lines [][]byte
}

// slimResume is where slimPair carries on from, and how it records
// where it is.
type slimResume struct {
	next     int   // first source stream still to do (0: from the start)
	indexLen int64 // bytes of the index written so far
	// checkpoint records the place once everything before it is on the
	// drive: the next stream, the size of each data piece, the index.
	checkpoint func(next int, pieces []int64, indexLen int64) error
}

func slimPair(p Pair, dw *pieceWriter, idxOut string, opt SlimOptions, res *SlimResult, progress func(int64), rs slimResume) error {
	data, size, err := OpenData(p.Data)
	if err != nil {
		return err
	}
	defer data.Close()
	idxFile, err := os.Open(p.Index)
	if err != nil {
		return err
	}
	defer idxFile.Close()

	// The index lists every page with the offset of its stream. Group
	// the lines by stream, in order.
	type stream struct {
		off   int64
		lines [][]byte
	}
	var streams []stream
	sc := bufio.NewScanner(bzip2.NewReader(bufio.NewReaderSize(idxFile, 256<<10)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		c := bytes.IndexByte(line, ':')
		if c < 0 {
			continue
		}
		off, err := strconv.ParseInt(string(line[:c]), 10, 64)
		if err != nil {
			continue
		}
		if len(streams) == 0 || streams[len(streams)-1].off != off {
			if len(streams) > 0 && off < streams[len(streams)-1].off || off >= size {
				return fmt.Errorf("%s: offsets out of order or past the end", filepath.Base(p.Index))
			}
			streams = append(streams, stream{off: off})
		}
		s := &streams[len(streams)-1]
		s.lines = append(s.lines, append([]byte(nil), line[c+1:]...))
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p.Index), err)
	}
	if len(streams) == 0 {
		return errors.New("dump: the index file is empty")
	}

	// The header (site information) is copied as it is.
	if rs.next == 0 {
		head := make([]byte, streams[0].off)
		if _, err := data.ReadAt(head, 0); err != nil {
			return err
		}
		if _, err := dw.Write(head); err != nil {
			return friendly(err)
		}
	}
	if rs.next > len(streams) {
		return errors.New("dump: the place to carry on from is past the end")
	}

	bundleSize := opt.Bundle
	if bundleSize <= 0 {
		bundleSize = BundleSize
	}
	jobs := make(chan slimJob, opt.Workers*2)
	results := make(chan slimOut, opt.Workers*2)
	stop := make(chan struct{})
	var stopOnce sync.Once
	halt := func() { stopOnce.Do(func() { close(stop) }) }
	defer halt()
	var wg sync.WaitGroup
	for w := 0; w < opt.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case results <- slimStream(j):
				case <-stop:
					return
				}
			}
		}()
	}
	var readErr error
	go func() {
		defer close(jobs)
		for i := rs.next; i < len(streams); i++ {
			s := streams[i]
			end := size
			if i+1 < len(streams) {
				end = streams[i+1].off
			}
			raw := make([]byte, end-s.off)
			if _, err := data.ReadAt(raw, s.off); err != nil && !errors.Is(err, io.EOF) {
				readErr = err
				halt()
				return
			}
			select {
			case jobs <- slimJob{n: i, start: s.off, raw: raw, lines: s.lines, last: i == len(streams)-1, bundle: bundleSize}:
			case <-stop:
				return
			case <-opt.Stop:
				halt()
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	// Write in order, with the new index.
	var idxText bytes.Buffer
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if rs.next > 0 {
		flags = os.O_CREATE | os.O_WRONLY
	}
	idxW, err := os.OpenFile(idxOut, flags, 0o644)
	if err != nil {
		return err
	}
	defer idxW.Close()
	if rs.next > 0 { // the index up to the checkpoint, the rest dropped
		if err := idxW.Truncate(rs.indexLen); err != nil {
			return err
		}
		if _, err := idxW.Seek(rs.indexLen, io.SeekStart); err != nil {
			return err
		}
	}
	indexLen := rs.indexLen
	idxBuf := bufio.NewWriterSize(idxW, 1<<20)
	flushIndex := func() error {
		if idxText.Len() == 0 {
			return nil
		}
		z := bzip2w.Compress(idxText.Bytes())
		_, err := idxBuf.Write(z)
		indexLen += int64(len(z))
		idxText.Reset()
		return err
	}
	// Every half minute, everything so far goes to the drive and the place
	// is recorded: stopped or cut off, slimming carries on from there.
	lastCheckpoint := time.Now()
	checkpoint := func(next int) error {
		if rs.checkpoint == nil {
			return nil
		}
		if err := flushIndex(); err != nil {
			return err
		}
		if err := idxBuf.Flush(); err != nil {
			return err
		}
		if err := idxW.Sync(); err != nil {
			return err
		}
		pieces, err := dw.sync()
		if err != nil {
			return err
		}
		return rs.checkpoint(next, pieces, indexLen)
	}
	pending := map[int]slimOut{}
	next := rs.next
	if next > 0 && next < len(streams) {
		progress(streams[next].off)
	}
	var firstErr error
	for r := range results {
		pending[r.n] = r
		for {
			o, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			if firstErr != nil {
				next++
				continue
			}
			if o.err != nil {
				firstErr = fmt.Errorf("stream at %d: %w", streams[next].off, o.err)
				halt()
				next++
				continue
			}
			res.Pages += o.pages
			res.Kept += o.kept
			for _, b := range o.bundles {
				off := dw.pos
				if _, err := dw.Write(b.data); err != nil {
					firstErr = friendly(err)
					halt()
				}
				for _, l := range b.lines {
					idxText.WriteString(strconv.FormatInt(off, 10))
					idxText.WriteByte(':')
					idxText.Write(l)
					idxText.WriteByte('\n')
				}
				if idxText.Len() > 4<<20 { // the index is a series of bzip2 streams
					if err := flushIndex(); err != nil && firstErr == nil {
						firstErr = friendly(err)
						halt()
					}
				}
			}
			next++
			end := size
			if next < len(streams) {
				end = streams[next].off
			}
			progress(end)
			if firstErr == nil && time.Since(lastCheckpoint) > checkpointEvery {
				lastCheckpoint = time.Now()
				if err := checkpoint(next); err != nil {
					firstErr = friendly(err)
					halt()
				}
			}
		}
	}
	if next < len(streams) { // (all done: no point calling it stopped)
		select {
		case <-opt.Stop:
			return ErrSlimStopped
		default:
		}
	}
	if firstErr == nil {
		firstErr = readErr
	}
	if firstErr != nil {
		return firstErr
	}
	if next != len(streams) {
		return errors.New("dump: not every stream was read")
	}
	if err := flushIndex(); err != nil {
		return friendly(err)
	}
	if err := idxBuf.Flush(); err != nil {
		return friendly(err)
	}
	return idxW.Sync()
}

// slimStream filters one compressed stream (the last also holds the
// closing of the XML document).
func slimStream(j slimJob) slimOut {
	out := slimOut{n: j.n}
	plain, err := bzip2w.Decompress(j.raw)
	if err != nil {
		out.err = err
		return out
	}
	lines := map[uint64][]byte{}
	for _, l := range j.lines {
		if c := bytes.IndexByte(l, ':'); c > 0 {
			if id, err := strconv.ParseUint(string(l[:c]), 10, 64); err == nil {
				lines[id] = l
			}
		}
	}
	// Kept pages go into bundles of at most j.bundle bytes of text (a
	// bigger page in one of its own).
	var cur bundle
	var text bytes.Buffer
	dropped := false
	closeBundle := func() {
		if text.Len() > 0 {
			cur.data = bzip2w.Compress(text.Bytes())
			out.bundles = append(out.bundles, cur)
		}
		cur, text = bundle{}, bytes.Buffer{}
	}
	pos := 0
	for {
		i := bytes.Index(plain[pos:], []byte("<page>"))
		if i < 0 {
			break
		}
		start := pos + i
		if nl := bytes.LastIndexByte(plain[pos:start], '\n'); nl >= 0 {
			start = pos + nl + 1 // with the line's indentation
		} else {
			start = pos
		}
		e := bytes.Index(plain[start:], []byte("</page>"))
		if e < 0 {
			out.err = errors.New("a page without its end")
			return out
		}
		end := start + e + len("</page>")
		if nl := bytes.IndexByte(plain[end:], '\n'); nl >= 0 && len(bytes.TrimSpace(plain[end:end+nl])) == 0 {
			end += nl + 1
		}
		page := plain[start:end]
		out.pages++
		ns, id := pageNSAndID(page)
		if slimKeep[ns] {
			if text.Len() > 0 && text.Len()+len(page) > j.bundle {
				closeBundle()
			}
			text.Write(page)
			out.kept++
			if l, ok := lines[id]; ok {
				cur.lines = append(cur.lines, l)
			}
		} else {
			dropped = true
		}
		pos = end
	}
	rest := plain[pos:] // "</mediawiki>" after the last page
	if !dropped && len(plain) <= j.bundle {
		// Nothing to drop and small already: keep it exactly as it was.
		out.bundles = []bundle{{data: j.raw, lines: cur.lines}}
		return out
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		text.Write(rest)
	}
	closeBundle()
	return out
}

func pageNSAndID(page []byte) (int, uint64) {
	ns := -1
	if i := bytes.Index(page, []byte("<ns>")); i >= 0 {
		if e := bytes.Index(page[i:], []byte("</ns>")); e > 0 {
			ns, _ = strconv.Atoi(string(page[i+4 : i+e]))
		}
	}
	var id uint64
	if i := bytes.Index(page, []byte("<id>")); i >= 0 {
		if e := bytes.Index(page[i:], []byte("</id>")); e > 0 {
			id, _ = strconv.ParseUint(string(page[i+4:i+e]), 10, 64)
		}
	}
	return ns, id
}
