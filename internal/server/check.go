package server

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
)

// driveCheck reads every bundle of every Wikipedia on the drive, to find
// what a rough unplugging or a loose connection damaged; damaged bundles
// can be copied back from another NABU drive with the same dump.
type driveCheck struct {
	mu       sync.Mutex
	running  bool
	stop     chan struct{}
	book     string // being checked
	done     int64
	total    int64
	started  time.Time
	finished time.Time
	results  []*bookCheck
	err      string
}

type bookCheck struct {
	ID, Name string
	Streams  int
	Damaged  []dump.Damage
	Index    string
	Copies   []string // folders of other drives with the same dump
	Fixed    int
	Note     string
	Articles []string // the first articles in the damaged bundles
	More     int      // how many more
}

func (bc *bookCheck) list() {
	bc.Articles, bc.More = nil, 0
	for _, d := range bc.Damaged {
		for _, t := range d.Titles {
			if len(bc.Articles) < 30 {
				bc.Articles = append(bc.Articles, t)
			} else {
				bc.More++
			}
		}
	}
}

type checkView struct {
	Running        bool
	Book           string
	Done, Total    int64
	Started        time.Time
	Finished       time.Time
	Results        []*bookCheck
	Err            string
	DamagedReaders int
}

func (c *driveCheck) view() checkView {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := checkView{Running: c.running, Book: c.book, Done: c.done, Total: c.total, Started: c.started,
		Finished: c.finished, Results: c.results, Err: c.err}
	for _, r := range c.results {
		v.DamagedReaders += len(r.Damaged)
	}
	return v
}

// startCheck checks the books one after the other in the background.
func (s *Server) startCheck() error {
	c := &s.check
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}
	c.running, c.stop, c.results, c.err = true, make(chan struct{}), nil, ""
	c.started, c.finished = time.Now(), time.Time{}
	stop := c.stop
	go func() {
		var results []*bookCheck
		var err error
		for _, b := range s.Lib.Books() {
			rd := b.Reader()
			if rd == nil {
				continue
			}
			c.mu.Lock()
			c.book, c.done, c.total = wikiName(b.Files.DB), 0, 0
			c.mu.Unlock()
			res, e := rd.Check(dump.CheckOptions{Stop: stop, Progress: func(d, t int64) {
				c.mu.Lock()
				c.done, c.total = d, t
				c.mu.Unlock()
			}})
			if e != nil {
				err = e
				break
			}
			bc := &bookCheck{ID: b.ID, Name: wikiName(b.Files.DB), Streams: res.Streams, Damaged: res.Damaged, Index: res.Index}
			if len(res.Damaged) > 0 {
				for _, o := range drive.OtherCopies(b.Files, s.Lib.Dir) {
					bc.Copies = append(bc.Copies, filepath.Dir(filepath.Dir(o.Pairs[0].Index)))
				}
			}
			bc.list()
			results = append(results, bc)
			c.mu.Lock()
			c.results = append([]*bookCheck(nil), results...)
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.running, c.book, c.finished = false, "", time.Now()
		if err != nil && !errors.Is(err, dump.ErrCheckStopped) {
			c.err = err.Error()
		}
		c.mu.Unlock()
	}()
	return nil
}

func (s *Server) stopCheck() {
	c := &s.check
	c.mu.Lock()
	if c.running && c.stop != nil {
		close(c.stop)
		c.stop = nil
	}
	c.mu.Unlock()
}

// repairBook copies a book's damaged bundles from another NABU drive.
func (s *Server) repairBook(id string) error {
	c := &s.check
	c.mu.Lock()
	var bc *bookCheck
	for _, r := range c.results {
		if r.ID == id {
			bc = r
		}
	}
	c.mu.Unlock()
	b := s.Lib.Get(id)
	if bc == nil || b == nil {
		return errors.New("check the drive first")
	}
	copies := drive.OtherCopies(b.Files, s.Lib.Dir)
	if len(copies) == 0 {
		return errors.New("plug in another NABU drive with the same dump of " + bc.Name + " first")
	}
	n, err := dump.Repair(b.Files, bc.Damaged, copies[0])
	if err != nil {
		return err
	}
	c.mu.Lock()
	bc.Fixed = n
	if n == len(bc.Damaged) {
		bc.Note = "All repaired."
		bc.Damaged = nil
	} else {
		bc.Note = "Some bundles are damaged on the other drive too; check again to see which."
	}
	bc.list()
	c.mu.Unlock()
	if rd := b.Reader(); rd != nil {
		rd.ForgetCache()
	}
	return nil
}

// serveCheck shows the "Check the drive" page; while a check runs it
// reloads itself.
func (s *Server) serveCheck(w http.ResponseWriter, r *http.Request) {
	v := s.check.view()
	if v.Running {
		w.Header().Set("Refresh", "2")
	}
	var books []*library.Book
	for _, b := range s.Lib.Books() {
		if b.Reader() != nil {
			books = append(books, b)
		}
	}
	s.render(w, r, "check.html", map[string]interface{}{"PageTitle": "Check the drive", "Check": v, "Ready": books})
}
