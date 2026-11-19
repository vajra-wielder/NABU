package dump

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"

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
}

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

// NeedsSlim reports whether a dump still has pages Slim would drop. It
// reads only the index (titles carry their namespace).
func NeedsSlim(f *Files) (bool, error) {
	for _, p := range f.Pairs {
		data, _, err := OpenData(p.Data)
		if err != nil {
			return false, err
		}
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
			for sc.Scan() {
				off, _, title, ok := indexLine(sc.Bytes())
				if !ok {
					continue
				}
				if site == nil {
					if site, err = SiteinfoFromDump(data, off); err != nil {
						return false, err
					}
				}
				if ns, _ := site.SplitTitle(title); !slimKeep[ns] {
					return true, nil
				}
			}
			return false, sc.Err()
		}()
		if found || err != nil {
			return found, err
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
	var total, done int64
	for _, p := range f.Pairs {
		total += p.Size
	}
	type rename struct{ from, to string }
	var renames []rename
	var temps []string
	cleanup := func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}
	for _, p := range f.Pairs {
		if st, err := os.Stat(p.Index); err == nil {
			res.Before += st.Size()
		}
		res.Before += p.Size
		dataName := filepath.Base(p.Data[0])
		if m := dataRe.FindStringSubmatch(dataName); m != nil && m[5] != "" {
			dataName = dataName[:len(dataName)-2] // pieces: name.bz2aa
		}
		idxName := filepath.Base(p.Index)
		if opt.Name != nil {
			dataName, idxName = opt.Name(p.Data[0]), opt.Name(p.Index)
		}
		dw := &pieceWriter{base: filepath.Join(dir, dataName), part: opt.PartSize}
		idxOut := filepath.Join(dir, idxName) + ".slim-tmp"
		temps = append(temps, idxOut)
		err := slimPair(p, dw, idxOut, opt, &res, func(n int64) {
			if opt.Progress != nil {
				opt.Progress(done+n, total)
			}
		})
		if err == nil {
			err = dw.close()
		} else {
			dw.closeCurrent()
		}
		temps = append(temps, dw.names...)
		if err != nil {
			cleanup()
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
	return res, nil
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

func (p *pieceWriter) close() error {
	if p.f == nil && len(p.names) == 0 {
		if err := p.next(); err != nil { // an empty file is still a file
			return err
		}
	}
	return p.closeCurrent()
}

type slimJob struct {
	n     int
	start int64
	raw   []byte
	lines [][]byte // the index lines of this stream: "id:title"
	last  bool
}

type slimOut struct {
	n     int
	data  []byte
	lines [][]byte // kept index lines, "id:title"
	pages int
	kept  int
	err   error
}

func slimPair(p Pair, dw *pieceWriter, idxOut string, opt SlimOptions, res *SlimResult, progress func(int64)) error {
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
	head := make([]byte, streams[0].off)
	if _, err := data.ReadAt(head, 0); err != nil {
		return err
	}
	if _, err := dw.Write(head); err != nil {
		return friendly(err)
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
		for i, s := range streams {
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
			case jobs <- slimJob{n: i, start: s.off, raw: raw, lines: s.lines, last: i == len(streams)-1}:
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
	idxW, err := os.Create(idxOut)
	if err != nil {
		return err
	}
	defer idxW.Close()
	idxBuf := bufio.NewWriterSize(idxW, 1<<20)
	flushIndex := func() error {
		if idxText.Len() == 0 {
			return nil
		}
		_, err := idxBuf.Write(bzip2w.Compress(idxText.Bytes()))
		idxText.Reset()
		return err
	}
	pending := map[int]slimOut{}
	next := 0
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
			if len(o.lines) > 0 || len(o.data) > 0 {
				off := dw.pos
				if _, err := dw.Write(o.data); err != nil {
					firstErr = friendly(err)
					halt()
				}
				for _, l := range o.lines {
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
		}
	}
	select {
	case <-opt.Stop:
		return ErrSlimStopped
	default:
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
	plain, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(j.raw)))
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
	var kept bytes.Buffer
	dropped := false
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
			kept.Write(page)
			out.kept++
			if l, ok := lines[id]; ok {
				out.lines = append(out.lines, l)
			}
		} else {
			dropped = true
		}
		pos = end
	}
	rest := plain[pos:] // "</mediawiki>" after the last page
	if !dropped {
		out.data = j.raw // nothing to drop: keep it exactly as it was
		return out
	}
	kept.Write(rest)
	if kept.Len() > 0 && (out.kept > 0 || len(bytes.TrimSpace(rest)) > 0) {
		out.data = bzip2w.Compress(kept.Bytes())
	}
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
