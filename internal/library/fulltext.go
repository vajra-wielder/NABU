package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/fulltext"
)

// Full-text index states. Search inside article text is optional: it
// means reading the whole dump once, so the reader chooses when.
const (
	FTNone     = ""
	FTQueued   = "queued"
	FTBuilding = "building"
	FTReady    = "ready"
	FTFailed   = "failed"
)

type ftState struct {
	state   string
	done    int64
	total   int64
	err     string
	started time.Time
	index   *fulltext.Index
	stop    chan struct{}
}

// FTStatus is a snapshot of a book's full-text index.
type FTStatus struct {
	State   string
	Done    int64 // streams read
	Total   int64
	Err     string
	Elapsed time.Duration
	ETA     time.Duration
	Meta    *fulltext.Meta
}

func (l *Library) ftPath(f *dump.Files) string {
	return filepath.Join(l.Dir, ".nabu", "index", f.Key()+".nfts")
}

// openFullText opens an existing full-text index. The caller holds b.mu
// or b is not yet shared.
func (l *Library) openFullText(b *Book) {
	ix, err := fulltext.Open(l.ftPath(b.Files), b.Files)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			os.Remove(l.ftPath(b.Files)) // stale: from an older dump
		}
		return
	}
	b.ft.index, b.ft.state = ix, FTReady
}

// closeFullText releases the index; the caller holds b.mu.
func (b *Book) closeFullText() {
	if b.ft.index != nil {
		b.ft.index.Close()
		b.ft.index = nil
	}
}

// FullText returns the full-text index, or nil.
func (b *Book) FullText() *fulltext.Index {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ft.index
}

// FTStatus reports the state of the book's full-text index.
func (b *Book) FTStatus() FTStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := FTStatus{State: b.ft.state, Done: b.ft.done, Total: b.ft.total, Err: b.ft.err}
	if b.ft.state == FTBuilding && !b.ft.started.IsZero() {
		s.Elapsed = time.Since(b.ft.started)
		if b.ft.done > 0 && b.ft.total > b.ft.done {
			s.ETA = time.Duration(float64(s.Elapsed) * float64(b.ft.total-b.ft.done) / float64(b.ft.done))
		}
	}
	if b.ft.index != nil {
		m := b.ft.index.Meta
		s.Meta = &m
	}
	return s
}

// BuildFullText queues building the full-text index of a ready book.
func (l *Library) BuildFullText(id string) error {
	b := l.Get(id)
	if b == nil {
		return errors.New("no such book")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reader == nil {
		return errors.New("this Wikipedia is still being prepared")
	}
	switch b.ft.state {
	case FTQueued, FTBuilding:
		return nil
	}
	b.ft = ftState{state: FTQueued, index: b.ft.index, stop: make(chan struct{})}
	select {
	case l.ftQueue <- b:
		return nil
	default:
		b.ft.state = FTNone
		return errors.New("too many builds waiting")
	}
}

// CancelFullText stops a queued or running build.
func (l *Library) CancelFullText(id string) {
	if b := l.Get(id); b != nil {
		b.mu.Lock()
		if b.ft.stop != nil && (b.ft.state == FTQueued || b.ft.state == FTBuilding) {
			close(b.ft.stop)
			b.ft.stop = nil
		}
		b.mu.Unlock()
	}
}

// ftWorker builds one full-text index at a time.
func (l *Library) ftWorker() {
	for b := range l.ftQueue {
		l.buildFullText(b)
	}
}

func (l *Library) buildFullText(b *Book) {
	b.mu.Lock()
	rd, stop := b.reader, b.ft.stop
	if b.ft.state != FTQueued || rd == nil || stop == nil {
		if b.ft.state == FTQueued {
			b.ft.state = FTNone
		}
		b.mu.Unlock()
		return
	}
	b.ft.state, b.ft.started = FTBuilding, time.Now()
	// Windows cannot replace a file that is open, so the old index (if
	// any) closes now; search inside articles returns when this finishes.
	b.closeFullText()
	b.mu.Unlock()

	out := l.ftPath(b.Files)
	tmp := filepath.Join(l.Dir, ".nabu", "tmp", b.Files.Key()+"-fts")
	os.MkdirAll(filepath.Dir(out), 0o755)
	err := fulltext.Build(rd, out, tmp, fulltext.Options{
		MemBudget: BuildBudget(),
		Stop:      stop,
		Progress: func(done, total int64) {
			b.mu.Lock()
			b.ft.done, b.ft.total = done, total
			b.mu.Unlock()
		},
	})
	os.RemoveAll(tmp)
	var ix *fulltext.Index
	if err == nil {
		ix, err = fulltext.Open(out, b.Files)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ft.stop = nil
	switch {
	case errors.Is(err, fulltext.ErrStopped):
		b.ft.state = FTNone
		l.openFullText(b) // the previous index, if it is still there
	case err != nil:
		b.ft.state, b.ft.err = FTFailed, fmt.Sprintf("could not build full-text search: %v", err)
	default:
		b.closeFullText()
		b.ft.index, b.ft.state = ix, FTReady
	}
}

// FullTextEstimate guesses build time and size from the dump size,
// measured on Simple English Wikipedia (395 MB: 73 s on 3 cores, 79 MB).
func FullTextEstimate(dumpBytes int64, cores int) (minutes int, bytes int64) {
	if cores < 1 {
		cores = 1
	}
	secs := float64(dumpBytes) / 5.4e6 * 3 / float64(cores) * 1.5 // 1.5: older machines and USB sticks
	return int(secs/60) + 1, dumpBytes / 5
}
