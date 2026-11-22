package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/pictures"
)

// Picture download states, per wiki.
const (
	PicNone    = ""
	PicQueued  = "queued"
	PicRunning = "running"
	PicPaused  = "paused"
	PicDone    = "done"
	PicFailed  = "failed"
	PicFull    = "full" // stopped at the space limit for pictures
)

type picJobs struct {
	mu      sync.Mutex
	state   map[string]*picState
	queue   chan string
	running string
	cancel  context.CancelFunc
	closed  bool // the library is closing: start nothing more
}

type picState struct {
	state   string
	prog    pictures.Progress
	err     string
	updated time.Time
}

// PicStatus is a wiki's offline pictures, for display.
type PicStatus struct {
	State    string
	Mode     string
	Waiting  string
	Err      string
	Done     int // streams gone through
	Total    int
	Pictures int // this wiki's own pictures
	Bytes    int64
	Shared   int // pictures from Commons, shared by every language
	SBytes   int64
	ETA      time.Duration
	PerHour  int
	English  bool  // shows English's pictures and keeps none of its own
	Used     int64 // room all pictures take
	Limit    int64 // room they may take (0: no limit)
}

// Pictures is the pictures service (nil if not connected).
func (l *Library) Pictures() *pictures.Service { return l.pics }

func (l *Library) picDir(db string) string { return filepath.Join(l.pics.Dir, db) }

// PictureStatus reports a wiki's offline pictures.
func (l *Library) PictureStatus(db string) PicStatus {
	var s PicStatus
	if l.pics == nil {
		return s
	}
	if st, _ := l.pics.Store(db, false); st != nil {
		stats := st.Stats()
		s.Pictures, s.Bytes = stats.Pictures, stats.Bytes
	}
	if st, _ := l.pics.Store(pictures.Commons, false); st != nil {
		stats := st.Stats()
		s.Shared, s.SBytes = stats.Pictures, stats.Bytes
	}
	s.Used, s.Limit = l.pics.Used(), l.pics.Limit()
	s.English = l.UsesEnglishPictures(db)
	j, _ := pictures.LoadJob(l.picDir(db))
	l.pj.mu.Lock()
	ps := l.pj.state[db]
	if ps != nil {
		s.State, s.Err, s.Waiting = ps.state, ps.err, ps.prog.Waiting
		if ps.state == PicRunning {
			jj := ps.prog.Job
			j = &jj
		}
	}
	l.pj.mu.Unlock()
	if j != nil {
		// Every pass goes through the whole dump (pictures.Tiers).
		s.Mode, s.Done, s.Total = j.Mode, j.Pass*j.Streams+j.Stream, len(pictures.Tiers)*j.Streams
		if j.Done {
			s.Done = s.Total
		}
		if s.State == PicNone {
			switch {
			case j.Done:
				s.State = PicDone
			case s.Limit > 0 && s.Used >= s.Limit:
				s.State = PicFull
			default:
				s.State = PicPaused
			}
		}
		if j.Seconds > 60 && j.Fetched > 0 {
			s.PerHour = int(float64(j.Fetched) / j.Seconds * 3600)
		}
		// Time left: the pictures still to come (the estimate for this
		// wiki, up to the room set aside) at the pace so far. None while
		// it waits (offline, say): it would mean nothing.
		if s.PerHour > 0 && s.Waiting == "" && !j.Done {
			if b := l.newest(db); b != nil {
				lead, all := pictures.Estimate(db, b.Status().Articles, s.English)
				want := lead
				if j.Mode == pictures.All {
					want = all
				}
				have := int64(s.Pictures + s.Shared)
				left := want.Pictures - have
				if s.Limit > 0 && have > 0 {
					per := (s.Bytes + s.SBytes) / have
					if per > 0 {
						if room := (s.Limit - s.Used) / per; room < left {
							left = room
						}
					}
				}
				if left > 0 {
					s.ETA = time.Duration(float64(left) / float64(s.PerHour) * float64(time.Hour))
				}
			}
		}
	}
	return s
}

// EnglishDB is English Wikipedia (a variable so tests can stand in for it).
var EnglishDB = "enwiki"

// UsesEnglishPictures reports whether a wiki only shows the pictures
// English keeps: the default for every other language when English is
// on the drive. Most pictures are the same files in every language, so
// it shows most of its pictures for no extra room. It keeps pictures of
// its own only once asked to (StartPictures).
func (l *Library) UsesEnglishPictures(db string) bool {
	if db == EnglishDB || l.newest(EnglishDB) == nil {
		return false
	}
	if l.pics != nil {
		if _, err := pictures.LoadJob(l.picDir(db)); err == nil {
			return false
		}
	}
	return true
}

// newest is the newest ready book of a wiki.
func (l *Library) newest(db string) *Book {
	var best *Book
	for _, b := range l.Books() {
		if b.Files.DB == db && b.Reader() != nil && (best == nil || b.Files.Date > best.Files.Date) {
			best = b
		}
	}
	return best
}

// StartPictures starts (or resumes) downloading a wiki's pictures for
// reading offline: the main picture of each article (lead), or all.
func (l *Library) StartPictures(db, mode string) error {
	if l.pics == nil {
		return errors.New("pictures are not available")
	}
	if mode != pictures.Lead && mode != pictures.All {
		return errors.New("unknown picture mode")
	}
	b := l.newest(db)
	if b == nil {
		return errors.New("this Wikipedia is not ready yet")
	}
	if l.pics.Full() {
		return fmt.Errorf("pictures already take %s, the room set aside for them; raise the limit in Updates & settings", humanGB(l.pics.Limit()))
	}
	dir := l.picDir(db)
	j, _ := pictures.LoadJob(dir)
	switch {
	case j == nil, j.Dump != b.Files.Key(), j.Mode != mode:
		// A new dump or a different choice: go through it all again. What
		// is already on the drive is skipped, so this is quick for it.
		j = &pictures.Job{Mode: mode, Dump: b.Files.Key()}
	case j.Done:
		return nil
	}
	j.Paused = false
	if err := j.Save(dir); err != nil {
		return err
	}
	if _, err := l.pics.Store(db, true); err != nil {
		return err
	}
	l.pj.mu.Lock()
	defer l.pj.mu.Unlock()
	if ps := l.pj.state[db]; ps != nil && (ps.state == PicQueued || ps.state == PicRunning) {
		if l.pj.running == db && l.pj.cancel != nil {
			l.pj.cancel() // restart with the new choice
			l.pj.state[db] = &picState{state: PicQueued}
		}
		if l.pj.running != db || ps.state == PicRunning {
			select {
			case l.pj.queue <- db:
			default:
			}
		}
		return nil
	}
	l.pj.state[db] = &picState{state: PicQueued}
	select {
	case l.pj.queue <- db:
		return nil
	default:
		return errors.New("too many downloads waiting")
	}
}

// PausePictures stops a wiki's picture download; it can be resumed.
func (l *Library) PausePictures(db string) {
	if l.pics == nil {
		return
	}
	dir := l.picDir(db)
	if j, err := pictures.LoadJob(dir); err == nil && !j.Done {
		j.Paused = true
		j.Save(dir)
	}
	l.pj.mu.Lock()
	if l.pj.running == db && l.pj.cancel != nil {
		l.pj.cancel()
	}
	delete(l.pj.state, db)
	l.pj.mu.Unlock()
}

// RemovePictures deletes a wiki's offline pictures.
func (l *Library) RemovePictures(db string) error {
	if l.pics == nil {
		return nil
	}
	l.PausePictures(db)
	for i := 0; i < 50; i++ { // let a running download stop
		l.pj.mu.Lock()
		r := l.pj.running
		l.pj.mu.Unlock()
		if r != db {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return l.pics.Remove(db)
}

func (l *Library) picWorker() {
	for db := range l.pj.queue {
		l.pj.mu.Lock()
		ps := l.pj.state[db]
		if ps == nil || ps.state != PicQueued || l.pj.closed {
			l.pj.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		l.pj.running, l.pj.cancel = db, cancel
		ps.state = PicRunning
		l.pj.mu.Unlock()

		err := l.runPictures(ctx, db)

		cancel()
		l.pj.mu.Lock()
		l.pj.running, l.pj.cancel = "", nil
		if ps := l.pj.state[db]; ps != nil && ps.state == PicRunning {
			switch {
			case err == nil:
				ps.state = PicDone
			case errors.Is(err, context.Canceled):
				delete(l.pj.state, db)
			case errors.Is(err, pictures.ErrFull):
				ps.state = PicFull
			default:
				ps.state, ps.err = PicFailed, err.Error()
			}
		}
		l.pj.mu.Unlock()
	}
}

func (l *Library) runPictures(ctx context.Context, db string) error {
	b := l.newest(db)
	if b == nil {
		return errors.New("this Wikipedia is not ready")
	}
	dir := l.picDir(db)
	j, err := pictures.LoadJob(dir)
	if err != nil {
		return err
	}
	if j.Dump != b.Files.Key() { // a newer dump arrived meanwhile
		j = &pictures.Job{Mode: j.Mode, Dump: b.Files.Key()}
	}
	st, err := l.pics.Stores(db)
	if err != nil {
		return err
	}
	// Wait for permission to use the internet (offline mode, or pictures
	// switched off in the settings).
	for l.online != nil && !l.online() {
		l.picReport(db, pictures.Progress{Job: *j, Waiting: "waiting: NABU is set to stay offline"})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
	rd := b.Reader()
	if rd == nil { // closed meanwhile (NABU stopping, or the dump removed)
		return context.Canceled
	}
	err = pictures.Run(ctx, j, rd, st, l.pics.Fetch, rd.Site().Host(), l.pics.Busy, l.pics.Full, func(p pictures.Progress) {
		l.picReport(db, p)
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, pictures.ErrFull) {
		return fmt.Errorf("pictures: %w", err)
	}
	return err
}

func (l *Library) picReport(db string, p pictures.Progress) {
	l.pj.mu.Lock()
	if ps := l.pj.state[db]; ps != nil {
		ps.prog, ps.updated = p, time.Now()
	}
	l.pj.mu.Unlock()
}

// autoPictures continues a wiki's picture download when NABU starts, and
// starts it again for a newer dump (fetching only the new pictures).
func (l *Library) autoPictures(b *Book) {
	if l.pics == nil || b.Reader() == nil {
		return
	}
	nb := l.newest(b.Files.DB)
	if nb != b {
		return
	}
	j, err := pictures.LoadJob(l.picDir(b.Files.DB))
	if err != nil || j.Paused || (j.Done && j.Dump == b.Files.Key()) {
		return
	}
	if _, err := os.Stat(l.picDir(b.Files.DB)); err != nil || l.pics.Full() {
		return
	}
	l.StartPictures(b.Files.DB, j.Mode)
}

func humanGB(n int64) string { return fmt.Sprintf("%.0f GB", float64(n)/1e9) }
