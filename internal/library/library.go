// Package library finds and opens the ZIM files on the NABU drive.
package library

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/vajra-wielder/nabu/internal/zim"
)

// Book is one opened archive.
type Book struct {
	ID          string // URL-safe identifier, derived from the file name
	Files       []string
	Size        int64
	Archive     *zim.Archive
	Title       string
	Description string
	Language    string
	Name        string // e.g. "wikipedia_en_all"
	Flavour     string // e.g. "maxi", "nopic", "mini"
	Date        string // e.g. "2026-10-01"
	Articles    int64
	HasIcon     bool
}

// Series identifies a family of editions, e.g. "wikipedia_en_all_maxi".
// A newer dump of the same series replaces an older one.
func (b *Book) Series() string {
	if b.Name != "" {
		if b.Flavour != "" && !strings.HasSuffix(b.Name, "_"+b.Flavour) {
			return b.Name + "_" + b.Flavour
		}
		return b.Name
	}
	if m := fileNameRe.FindStringSubmatch(b.ID); m != nil {
		return m[1]
	}
	return b.ID
}

// Edition is the dump month, "YYYY-MM". The file name is preferred
// because it is what the download catalogue uses; the Date metadata
// (when the file was built) is the fallback for renamed files.
func (b *Book) Edition() string {
	if m := fileNameRe.FindStringSubmatch(b.ID); m != nil {
		return m[2]
	}
	if len(b.Date) >= 7 {
		return b.Date[:7]
	}
	return ""
}

var fileNameRe = regexp.MustCompile(`^(.+)_(\d{4}-\d{2})$`)

// Library is the set of books in a directory.
type Library struct {
	Dir   string
	mu    sync.RWMutex
	books []*Book
	byID  map[string]*Book
	Errs  []string // files that could not be opened, for display
}

// Open scans dir for ZIM files.
func Open(dir string) (*Library, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Library{Dir: dir, byID: map[string]*Book{}}
	return l, l.Rescan()
}

// Rescan re-reads the directory, opening new files and closing removed ones.
func (l *Library) Rescan() error {
	ents, err := os.ReadDir(l.Dir)
	if err != nil {
		return err
	}
	groups := map[string][]string{}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".zim"):
			groups[name[:len(name)-4]] = []string{filepath.Join(l.Dir, name)}
		case len(lower) > 6 && strings.HasSuffix(lower[:len(lower)-2], ".zim") && isAlphaPair(lower[len(lower)-2:]):
			// Split archive: name.zimaa, name.zimab, ...
			base := name[:len(name)-6]
			groups[base] = append(groups[base], filepath.Join(l.Dir, name))
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.byID
	l.byID = map[string]*Book{}
	l.books = nil
	l.Errs = nil
	for base, files := range groups {
		sort.Strings(files)
		id := slug(base)
		if b, ok := old[id]; ok && sameFiles(b.Files, files) {
			l.add(b)
			delete(old, id)
			continue
		}
		b, err := openBook(id, files)
		if err != nil {
			l.Errs = append(l.Errs, fmt.Sprintf("%s: %v", filepath.Base(files[0]), err))
			continue
		}
		l.add(b)
	}
	for _, b := range old {
		b.Archive.Close()
	}
	sort.Slice(l.books, func(i, j int) bool {
		if l.books[i].Title != l.books[j].Title {
			return l.books[i].Title < l.books[j].Title
		}
		return l.books[i].Date > l.books[j].Date
	})
	sort.Strings(l.Errs)
	return nil
}

func (l *Library) add(b *Book) {
	if _, dup := l.byID[b.ID]; dup {
		return
	}
	l.byID[b.ID] = b
	l.books = append(l.books, b)
}

// Books lists all books, sorted by title.
func (l *Library) Books() []*Book {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]*Book(nil), l.books...)
}

// Get finds a book by ID.
func (l *Library) Get(id string) *Book {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.byID[id]
}

// Default is the book to use when none is specified: the largest one,
// which is almost always the main Wikipedia.
func (l *Library) Default() *Book {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var best *Book
	for _, b := range l.books {
		if best == nil || b.Articles > best.Articles {
			best = b
		}
	}
	return best
}

// Close closes all archives.
func (l *Library) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range l.books {
		b.Archive.Close()
	}
	l.books = nil
	l.byID = map[string]*Book{}
}

func openBook(id string, files []string) (*Book, error) {
	r, size, closer, err := openParts(files)
	if err != nil {
		return nil, err
	}
	a, err := zim.New(r, size)
	if err != nil {
		closer.Close()
		return nil, err
	}
	a.SetCloser(closer)
	b := &Book{
		ID:          id,
		Files:       files,
		Size:        size,
		Archive:     a,
		Title:       a.Metadata("Title"),
		Description: a.Metadata("Description"),
		Language:    a.Metadata("Language"),
		Name:        a.Metadata("Name"),
		Flavour:     a.Metadata("Flavour"),
		Date:        a.Metadata("Date"),
		Articles:    a.ArticleCount(),
	}
	if b.Title == "" {
		b.Title = strings.ReplaceAll(id, "_", " ")
	}
	if _, err := a.Find('M', "Illustration_48x48@1"); err == nil {
		b.HasIcon = true
	} else if _, err := a.Find('-', "favicon"); err == nil {
		b.HasIcon = true
	}
	return b, nil
}

func sameFiles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isAlphaPair(s string) bool {
	return len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z'
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// multiFile presents a split archive (.zimaa, .zimab, ...) as one
// contiguous file. Splitting lets a full Wikipedia live on FAT32 drives,
// which cannot hold files of 4 GB or more.
type multiFile struct {
	files  []*os.File
	starts []int64 // starting offset of each part
	size   int64
}

func openParts(paths []string) (io.ReaderAt, int64, io.Closer, error) {
	if len(paths) == 1 {
		f, err := os.Open(paths[0])
		if err != nil {
			return nil, 0, nil, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, 0, nil, err
		}
		return f, st.Size(), f, nil
	}
	m := &multiFile{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			m.Close()
			return nil, 0, nil, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			m.Close()
			return nil, 0, nil, err
		}
		m.files = append(m.files, f)
		m.starts = append(m.starts, m.size)
		m.size += st.Size()
	}
	return m, m.size, m, nil
}

func (m *multiFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= m.size {
		return 0, io.EOF
	}
	total := 0
	for len(p) > 0 {
		if off >= m.size {
			return total, io.EOF
		}
		i := sort.Search(len(m.starts), func(i int) bool { return m.starts[i] > off }) - 1
		local := off - m.starts[i]
		end := m.size
		if i+1 < len(m.starts) {
			end = m.starts[i+1]
		}
		chunk := p
		if max := end - off; int64(len(chunk)) > max {
			chunk = chunk[:max]
		}
		n, err := m.files[i].ReadAt(chunk, local)
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
