package dump

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Pair is one compressed XML file with its index. Small wikis ship a
// single pair; English Wikipedia also ships ~70 numbered part pairs,
// which NABU reads just as well.
type Pair struct {
	Data  []string // the .xml.bz2, or its pieces .xml.bz2aa, .xml.bz2ab, ...
	Index string   // the -index.txt.bz2
	Size  int64    // total bytes of Data
	part  int      // 0 for the combined file, N for multistreamN
}

// Files describes one edition of one wiki found on disk.
type Files struct {
	DB    string // e.g. "enwiki"
	Date  string // e.g. "20261101"
	Pairs []Pair
	Size  int64 // data + index bytes
}

// Key identifies the edition, e.g. "enwiki-20261101".
func (f *Files) Key() string { return f.DB + "-" + f.Date }

var (
	dataRe  = regexp.MustCompile(`^([a-z0-9_]+)-(\d{8}|latest)-pages-articles-multistream(\d*)\.xml(-p\d+p\d+)?\.bz2([a-z]{2})?$`)
	indexRe = regexp.MustCompile(`^([a-z0-9_]+)-(\d{8}|latest)-pages-articles-multistream-index(\d*)\.txt(-p\d+p\d+)?\.bz2$`)
)

// IsDumpFile reports whether name is part of a multistream dump.
func IsDumpFile(name string) bool { return dataRe.MatchString(name) || indexRe.MatchString(name) }

// Scan finds complete dumps in dir. Problems (a data file without its
// index, say) are returned as notes for the user.
func Scan(dir string) ([]*Files, []string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, []string{err.Error()}
	}
	type pk struct{ db, date, n, rng string }
	data := map[pk][]string{}
	index := map[pk]string{}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if m := dataRe.FindStringSubmatch(name); m != nil {
			k := pk{m[1], m[2], m[3], m[4]}
			data[k] = append(data[k], filepath.Join(dir, name))
		} else if m := indexRe.FindStringSubmatch(name); m != nil {
			index[pk{m[1], m[2], m[3], m[4]}] = filepath.Join(dir, name)
		}
	}
	editions := map[string]*Files{}
	var notes []string
	for k, files := range data {
		idx, ok := index[k]
		if !ok {
			notes = append(notes, fmt.Sprintf("%s has no index file (%s-%s-pages-articles-multistream-index%s.txt%s.bz2); download it too.",
				filepath.Base(files[0]), k.db, k.date, k.n, k.rng))
			continue
		}
		sort.Strings(files)
		p := Pair{Data: files, Index: idx, part: atoi(k.n)}
		for _, f := range files {
			if st, err := os.Stat(f); err == nil {
				p.Size += st.Size()
			}
		}
		ek := k.db + "-" + k.date
		ed := editions[ek]
		if ed == nil {
			ed = &Files{DB: k.db, Date: k.date}
			editions[ek] = ed
		}
		ed.Pairs = append(ed.Pairs, p)
	}
	var out []*Files
	for _, ed := range editions {
		// Prefer the single combined file when both layouts are present.
		var combined []Pair
		for _, p := range ed.Pairs {
			if p.part == 0 {
				combined = append(combined, p)
			}
		}
		if len(combined) > 0 {
			ed.Pairs = combined
		}
		sort.Slice(ed.Pairs, func(i, j int) bool { return ed.Pairs[i].part < ed.Pairs[j].part })
		for _, p := range ed.Pairs {
			ed.Size += p.Size
			if st, err := os.Stat(p.Index); err == nil {
				ed.Size += st.Size()
			}
		}
		out = append(out, ed)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DB != out[j].DB {
			return out[i].DB < out[j].DB
		}
		return out[i].Date > out[j].Date
	})
	sort.Strings(notes)
	return out, notes
}

// OpenData opens a data file that may be split into pieces.
func OpenData(paths []string) (ReaderAtCloser, int64, error) {
	m := &multiFile{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			m.Close()
			return nil, 0, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			m.Close()
			return nil, 0, err
		}
		m.files = append(m.files, f)
		m.starts = append(m.starts, m.size)
		m.size += st.Size()
	}
	if len(m.files) == 1 {
		return m.files[0], m.size, nil
	}
	return m, m.size, nil
}

// ReaderAtCloser is a random-access file.
type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}

// multiFile joins the pieces of a split file (name.xml.bz2aa, ...ab) so
// a dump bigger than 4 GB can live on a FAT32 drive.
type multiFile struct {
	files  []*os.File
	starts []int64
	size   int64
}

func (m *multiFile) ReadAt(p []byte, off int64) (int, error) {
	total := 0
	for len(p) > 0 {
		if off >= m.size {
			return total, io.EOF
		}
		i := sort.Search(len(m.starts), func(i int) bool { return m.starts[i] > off }) - 1
		end := m.size
		if i+1 < len(m.starts) {
			end = m.starts[i+1]
		}
		chunk := p
		if room := end - off; int64(len(chunk)) > room {
			chunk = chunk[:room]
		}
		n, err := m.files[i].ReadAt(chunk, off-m.starts[i])
		total += n
		off += int64(n)
		p = p[n:]
		if err != nil && !(err == io.EOF && n == len(chunk)) {
			return total, err
		}
	}
	return total, nil
}

func (m *multiFile) Close() error {
	for _, f := range m.files {
		f.Close()
	}
	return nil
}

// HumanDate turns "20261101" into "2026-11-01".
func HumanDate(d string) string {
	if len(d) == 8 && !strings.ContainsAny(d, "abcdefghijklmnopqrstuvwxyz") {
		return d[:4] + "-" + d[4:6] + "-" + d[6:]
	}
	return d
}
