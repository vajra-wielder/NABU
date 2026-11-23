package pictures

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/vajra-wielder/nabu/internal/drivefile"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

// Modes of a whole-wiki download.
const (
	Lead = "lead" // the main picture of each article
	All  = "all"  // every picture the articles show
)

// Job is a whole-wiki picture download, saved in pictures/<wiki>/job.json
// so it resumes where it stopped, even days later on another computer.
type Job struct {
	Mode    string    `json:"mode"`
	Dump    string    `json:"dump"`   // the dump being gone through
	Pass    int       `json:"pass"`   // which length of article (Tiers)
	Stream  int       `json:"stream"` // next stream (100 pages each)
	Streams int       `json:"streams"`
	Done    bool      `json:"done"`
	Paused  bool      `json:"paused"`
	Fetched int       `json:"fetched"` // downloaded by this job
	Missing int       `json:"missing"`
	Started time.Time `json:"started"`
	Seconds float64   `json:"seconds"` // time spent running
}

// Tiers: a job goes through the dump three times, longest articles first
// (by the length of their wikitext), so that when the picture space limit
// is reached the pictures on the drive are those of the fullest, most
// read articles, not of whichever came first in the dump. Each pass
// renders only the articles of its own length, so the passes together
// cost little more than one.
var Tiers = []int{20000, 6000, 0}

// inTier reports whether an article of n bytes belongs to pass p.
func inTier(n, p int) bool {
	if p >= len(Tiers) {
		return false
	}
	return n >= Tiers[p] && (p == 0 || n < Tiers[p-1])
}

// ErrFull means the pictures reached the space limit set for them.
var ErrFull = errors.New("pictures: the space limit for pictures is reached")

// JobPath is where a wiki's job is saved.
func JobPath(dir string) string { return filepath.Join(dir, "job.json") }

// LoadJob reads a wiki's job, if it has one.
func LoadJob(dir string) (*Job, error) {
	b, err := os.ReadFile(JobPath(dir))
	if err != nil {
		return nil, err
	}
	j := &Job{}
	if err := json.Unmarshal(b, j); err != nil {
		return nil, err
	}
	return j, nil
}

// Save writes the job atomically.
func (j *Job) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(j, "", "  ")
	return drivefile.WriteFile(JobPath(dir), b)
}

var srcRe = regexp.MustCompile(`<img src="pic:([^"]+)"`)

// PageNames lists the pictures an article shows, in order, as NABU
// renders it (so infobox images count, and pictures templates drop do
// not). With lead, only the first.
func PageNames(title, text string, site *dump.Siteinfo, lead bool) []string {
	out := wikitext.Render(text, &wikitext.Context{
		Title: title, Site: site,
		PageURL:  func(t string) string { return "#" },
		ImageURL: func(file string, width int) string { return "pic:" + url.PathEscape(Normalize(file)) },
	})
	var names []string
	seen := map[string]bool{}
	for _, m := range srcRe.FindAllStringSubmatch(out.HTML, -1) {
		n, err := url.PathUnescape(html.UnescapeString(m[1]))
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
		if lead {
			break
		}
	}
	return names
}

// Stores are where a job keeps pictures: those on Commons in the shared
// store, the wiki's own files and the missing ones in the wiki's.
type Stores struct{ Wiki, Shared *Store }

// Has reports whether either store knows the picture.
func (s Stores) Has(name string) bool {
	return s.Wiki.Has(name) || (s.Shared != nil && s.Shared.Has(name))
}

func (s Stores) put(info Info, data []byte) {
	if data != nil {
		data = Shrink(data)
	}
	if data != nil && info.Shared && s.Shared != nil {
		s.Shared.Put(info.Name, data)
		return
	}
	s.Wiki.Put(info.Name, data)
}

// Sync flushes both stores.
func (s Stores) Sync() {
	s.Wiki.Sync()
	if s.Shared != nil {
		s.Shared.Sync()
	}
}

// Progress is reported as a job runs.
type Progress struct {
	Job     Job
	Waiting string // why it is waiting, if it is
}

// Busy tells a job to make room for a reader who is looking at pages.
type Busy func() bool

// Run goes through the dump from where j stopped, keeping every picture
// the stores do not have yet. It returns when done, stopped (ctx), when
// full reports the space limit reached (ErrFull), or on an error that
// waiting will not cure. Network failures are waited out.
func Run(ctx context.Context, j *Job, rd *dump.Reader, st Stores, f *Fetcher, host string, busy Busy, full func() bool, report func(Progress)) error {
	j.Streams = rd.Index.StreamCount()
	if j.Started.IsZero() {
		j.Started = time.Now()
	}
	runStart, runSecs := time.Now(), j.Seconds
	elapsed := func() float64 { return runSecs + time.Since(runStart).Seconds() }
	save := func() {
		j.Seconds = elapsed()
		j.Save(st.Wiki.Dir)
		st.Sync()
	}
	for j.Pass < len(Tiers) {
		if err := runPass(ctx, j, rd, st, f, host, busy, full, report, elapsed, save); err != nil {
			return err
		}
		j.Pass++
		j.Stream = 0
		save()
	}
	j.Done = true
	save()
	st.Wiki.Compact()
	if st.Shared != nil {
		st.Shared.Compact()
	}
	return nil
}

func runPass(ctx context.Context, j *Job, rd *dump.Reader, st Stores, f *Fetcher, host string, busy Busy, full func() bool, report func(Progress), elapsed func() float64, save func()) error {
	site := rd.Site()
	lead := j.Mode == Lead
	pass := j.Pass

	// Stage 1, on all but one core: read and render streams ahead.
	type names struct {
		s     int
		names []string
		err   error
	}
	workers := runtime.NumCPU() - 1
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	results := make(chan names, workers*2)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				r := names{s: s}
				pages, err := rd.StreamPages(uint32(s))
				r.err = err
				seen := map[string]bool{}
				for _, p := range pages {
					if p.NS != dump.NSMain || p.Redirect != "" || !inTier(len(p.Text), pass) {
						continue
					}
					for _, n := range PageNames(p.Title, p.Text, site, lead) {
						if !seen[n] && !st.Has(n) {
							seen[n] = true
							r.names = append(r.names, n)
						}
					}
				}
				select {
				case results <- r:
				case <-cctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for s := j.Stream; s < j.Streams; s++ {
			select {
			case jobs <- s:
			case <-cctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	// Stage 2, in stream order: ask where the pictures are, download them.
	// Names are gathered over several streams and asked about apiBatch at
	// a time: in a pass over long articles a stream often has only one or
	// two, and one API request each made English take weeks. A stream
	// counts as done (j.Stream) only once its pictures are kept, so a job
	// stopped midway goes on from the right place.
	pending := map[int]names{}
	lastSave := time.Now()
	next := j.Stream // the next stream to take from the workers
	type queued struct {
		name   string
		stream int
	}
	var buf []queued
	inBuf := map[string]bool{}
	// flush asks about whole batches (all that is left at the end of a
	// pass) and moves j.Stream up to the first stream still waiting.
	flush := func(final bool) error {
		n := len(buf)
		if !final {
			n -= n % apiBatch
		}
		if n > 0 {
			if full != nil && full() {
				return ErrFull
			}
			list := make([]string, n)
			for i, q := range buf[:n] {
				list[i] = q.name
				delete(inBuf, q.name)
			}
			if err := fetchAll(ctx, list, st, f, host, busy, func(w string) {
				report(Progress{Job: *j, Waiting: w})
			}, j); err != nil {
				return err
			}
			buf = append([]queued(nil), buf[n:]...)
		}
		j.Stream = next
		if len(buf) > 0 {
			j.Stream = buf[0].stream
		}
		j.Seconds = elapsed()
		report(Progress{Job: *j})
		if time.Since(lastSave) > 20*time.Second {
			save()
			lastSave = time.Now()
		}
		return nil
	}
	for r := range results {
		pending[r.s] = r
		for {
			r, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			if r.err != nil {
				save()
				return r.err
			}
			for _, n := range r.names {
				// Workers run ahead: a name may have been kept since.
				if !inBuf[n] && !st.Has(n) {
					inBuf[n] = true
					buf = append(buf, queued{n, next})
				}
			}
			next++
			if len(buf) == 0 || len(buf) >= apiBatch {
				if err := flush(false); err != nil {
					save()
					return err
				}
			}
		}
	}
	if ctx.Err() != nil {
		save()
		return ctx.Err()
	}
	if err := flush(true); err != nil {
		save()
		return err
	}
	return nil
}

// fetchAll keeps the given pictures, waiting out network trouble.
func fetchAll(ctx context.Context, list []string, st Stores, f *Fetcher, host string, busy Busy, waiting func(string), j *Job) error {
	for len(list) > 0 {
		n := len(list)
		if n > apiBatch {
			n = apiBatch
		}
		batch := list[:n]
		var infos map[string]Info
		for wait := 30 * time.Second; ; wait = minDur(wait*2, 10*time.Minute) {
			var err error
			infos, err = f.Resolve(ctx, host, batch)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			waiting("no connection to Wikimedia; trying again")
			if !sleep(ctx, wait) {
				return ctx.Err()
			}
		}
		var todo []Info
		for _, name := range batch {
			info, ok := infos[Normalize(name)]
			switch {
			case !ok, info.Missing, info.Skip:
				st.Wiki.Put(name, nil)
				j.Missing++
			default:
				info.Name = name
				todo = append(todo, info)
			}
		}
		if err := downloadAll(ctx, todo, st, f, busy, waiting, j); err != nil {
			return err
		}
		list = list[n:]
	}
	return nil
}

func downloadAll(ctx context.Context, todo []Info, st Stores, f *Fetcher, busy Busy, waiting func(string), j *Job) error {
	var mu sync.Mutex
	next, fetched, missing := 0, 0, 0
	take := func() (Info, bool) {
		mu.Lock()
		defer mu.Unlock()
		if next >= len(todo) {
			return Info{}, false
		}
		next++
		return todo[next-1], true
	}
	var wg sync.WaitGroup
	for w := 0; w < maxDownloads; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				// The second worker steps aside while someone is reading,
				// so their pictures come first.
				for w > 0 && busy != nil && busy() {
					mu.Lock()
					left := next < len(todo)
					mu.Unlock()
					if !left || !sleep(ctx, time.Second) {
						return
					}
				}
				info, ok := take()
				if !ok {
					return
				}
				for wait := 30 * time.Second; ; wait = minDur(wait*2, 10*time.Minute) {
					data, err := f.Download(ctx, info.URL)
					switch {
					case err == nil:
						mu.Lock()
						st.put(info, data)
						fetched++
						mu.Unlock()
					case errors.Is(err, ErrNotFound), errors.Is(err, ErrSkipped):
						mu.Lock()
						st.Wiki.Put(info.Name, nil)
						missing++
						mu.Unlock()
					case ctx.Err() != nil:
						return
					case errors.Is(err, ErrBusy):
						waiting("Wikimedia asked NABU to slow down; waiting")
						continue // the fetcher waits as asked
					default:
						if strings.Contains(err.Error(), "pictures: ") { // other HTTP answers: skip this one
							break
						}
						waiting("no connection to Wikimedia; trying again")
						if !sleep(ctx, wait) {
							return
						}
						continue
					}
					break
				}
			}
		}(w)
	}
	wg.Wait()
	j.Fetched += fetched // only now, so progress reports never race with the workers
	j.Missing += missing
	return ctx.Err()
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Keep fetches the given pictures that the stores do not have yet, the
// way whole-wiki downloads do: names looked up 50 at a time, downloads
// two at a time, network trouble waited out. report is told how many of
// the pictures to fetch are done.
func Keep(ctx context.Context, names []string, st Stores, f *Fetcher, host string, report func(done, total int)) error {
	var todo []string
	seen := map[string]bool{}
	for _, n := range names {
		n = Normalize(n)
		if n != "" && !seen[n] && !st.Has(n) {
			seen[n] = true
			todo = append(todo, n)
		}
	}
	j := &Job{}
	const step = 10 // small batches, so progress shows often
	for i := 0; i < len(todo); i += step {
		end := i + step
		if end > len(todo) {
			end = len(todo)
		}
		if err := fetchAll(ctx, todo[i:end], st, f, host, nil, func(string) {}, j); err != nil {
			st.Sync()
			return err
		}
		if report != nil {
			report(end, len(todo))
		}
	}
	st.Sync()
	return nil
}
