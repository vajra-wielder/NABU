// Package library keeps track of the Wikipedia dumps on the NABU drive
// and prepares (indexes) new ones in the background.
package library

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Book states.
const (
	Waiting   = "waiting"   // queued for indexing
	Preparing = "preparing" // building its index (first use only)
	Ready     = "ready"
	Failed    = "failed"
)

// Book is one edition of one wiki.
type Book struct {
	ID    string // URL name: "enwiki", or "enwiki-20261001" for an older copy
	Files *dump.Files

	mu      sync.Mutex
	state   string
	done    int64
	total   int64
	err     string
	started time.Time
	reader  *dump.Reader

	ft ftState
}

// Status is a snapshot for display.
type Status struct {
	State    string
	Done     int64
	Total    int64
	Err      string
	Elapsed  time.Duration
	Articles int64
}

// Status reports the book's state.
func (b *Book) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Status{State: b.state, Done: b.done, Total: b.total, Err: b.err}
	if b.state == Preparing {
		s.Elapsed = time.Since(b.started)
	}
	if b.reader != nil {
		s.Articles = b.reader.Index.Meta.Articles
	}
	return s
}

// Reader returns the page reader, or nil until the book is ready.
func (b *Book) Reader() *dump.Reader {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reader
}

// Site returns the wiki's siteinfo once ready.
func (b *Book) Site() *dump.Siteinfo {
	if r := b.Reader(); r != nil {
		return r.Site()
	}
	return nil
}

// Library is the set of books in a folder.
type Library struct {
	Dir   string
	mu    sync.RWMutex
	books []*Book
	byID  map[string]*Book
	Notes []string

	queue   chan *Book
	ftQueue chan *Book
}

// Open scans dir and starts preparing any new dumps.
func Open(dir string) (*Library, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Library{Dir: dir, byID: map[string]*Book{}, queue: make(chan *Book, 256)}
	l.ftQueue = make(chan *Book, 64)
	go l.worker()
	go l.ftWorker()
	return l, l.Rescan()
}

func (l *Library) indexPath(f *dump.Files) string {
	return filepath.Join(l.Dir, ".nabu", "index", f.Key()+".nidx")
}

// Rescan looks for added or removed dumps.
func (l *Library) Rescan() error {
	found, notes := dump.Scan(l.Dir)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Notes = notes
	old := map[string]*Book{}
	for _, b := range l.books {
		old[b.Files.Key()] = b
	}
	var books []*Book
	seenDB := map[string]bool{}
	for _, f := range found { // newest first within a wiki
		id := f.DB
		if seenDB[f.DB] {
			id = f.Key()
		}
		seenDB[f.DB] = true
		if b, ok := old[f.Key()]; ok && sameSources(b.Files, f) {
			b.ID = id
			books = append(books, b)
			delete(old, f.Key())
			continue
		}
		b := &Book{ID: id, Files: f, state: Waiting}
		if ix, err := dump.OpenIndex(l.indexPath(f), f); err == nil {
			if rd, err := dump.NewReader(f, ix); err == nil {
				b.reader, b.state = rd, Ready
				l.openFullText(b)
			} else {
				ix.Close()
				b.state, b.err = Failed, err.Error()
			}
		} else {
			select {
			case l.queue <- b:
			default:
			}
		}
		books = append(books, b)
	}
	for _, b := range old {
		b.mu.Lock()
		if b.reader != nil {
			b.reader.Close()
			b.reader = nil
		}
		b.closeFullText()
		b.mu.Unlock()
	}
	sort.SliceStable(books, func(i, j int) bool { return books[i].ID < books[j].ID })
	l.books = books
	l.byID = map[string]*Book{}
	for _, b := range books {
		l.byID[b.ID] = b
	}
	return nil
}

func sameSources(a, b *dump.Files) bool {
	x, y := dump.SourcesOf(a), dump.SourcesOf(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// worker prepares books one at a time, so an old laptop is never asked
// to sort two Wikipedias at once.
func (l *Library) worker() {
	for b := range l.queue {
		l.prepare(b)
	}
}

func (l *Library) prepare(b *Book) {
	b.mu.Lock()
	if b.state != Waiting {
		b.mu.Unlock()
		return
	}
	b.state, b.started = Preparing, time.Now()
	b.mu.Unlock()

	out := l.indexPath(b.Files)
	os.MkdirAll(filepath.Dir(out), 0o755)
	tmp := filepath.Join(l.Dir, ".nabu", "tmp", b.Files.Key())
	err := dump.Build(b.Files, out, tmp, BuildBudget(), func(done, total int64) {
		b.mu.Lock()
		b.done, b.total = done, total
		b.mu.Unlock()
	})
	os.RemoveAll(tmp)
	var rd *dump.Reader
	if err == nil {
		var ix *dump.Index
		ix, err = dump.OpenIndex(out, b.Files)
		if err == nil {
			rd, err = dump.NewReader(b.Files, ix)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.state, b.err = Failed, fmt.Sprintf("could not prepare %s: %v", b.Files.Key(), err)
		return
	}
	b.reader, b.state, b.done = rd, Ready, b.total
	l.openFullText(b)
}

// BuildBudget is the memory used for sorting titles while preparing a
// dump: modest, so a 2 GB laptop stays responsive, and smaller on
// 32-bit machines. Bigger runs only mean fewer temporary files.
func BuildBudget() int {
	if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
		return 48 << 20
	}
	return 128 << 20
}

// Retry queues a failed book again.
func (l *Library) Retry(id string) {
	if b := l.Get(id); b != nil {
		b.mu.Lock()
		if b.state == Failed {
			b.state, b.err = Waiting, ""
			b.mu.Unlock()
			l.queue <- b
			return
		}
		b.mu.Unlock()
	}
}

// Books lists the books.
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

// Default is the largest ready book.
func (l *Library) Default() *Book {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var best *Book
	for _, b := range l.books {
		if b.Reader() == nil {
			continue
		}
		if best == nil || b.Files.Size > best.Files.Size {
			best = b
		}
	}
	return best
}

// Remove deletes a book's files and index from the drive.
func (l *Library) Remove(id string) error {
	b := l.Get(id)
	if b == nil {
		return fmt.Errorf("no such book")
	}
	b.mu.Lock()
	if b.state == Preparing {
		b.mu.Unlock()
		return fmt.Errorf("it is being prepared; try again when that finishes")
	}
	if b.ft.state == FTBuilding {
		b.mu.Unlock()
		return fmt.Errorf("its full-text index is being built; cancel that first")
	}
	b.closeFullText()
	if b.reader != nil {
		b.reader.Close()
		b.reader = nil
	}
	b.mu.Unlock()
	os.Remove(l.ftPath(b.Files))
	for _, p := range b.Files.Pairs {
		for _, f := range p.Data {
			os.Remove(f)
		}
		os.Remove(p.Index)
	}
	os.Remove(l.indexPath(b.Files))
	return l.Rescan()
}

// Close releases every file.
func (l *Library) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range l.books {
		b.mu.Lock()
		if b.ft.stop != nil {
			close(b.ft.stop)
			b.ft.stop = nil
		}
		b.closeFullText()
		if b.reader != nil {
			b.reader.Close()
			b.reader = nil
		}
		b.mu.Unlock()
	}
}
