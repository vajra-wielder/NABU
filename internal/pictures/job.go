package pictures

import (
	"context"
	"encoding/json"
	"errors"
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
	Stream  int       `json:"stream"` // next stream (100 pages each)
	Streams int       `json:"streams"`
	Done    bool      `json:"done"`
	Paused  bool      `json:"paused"`
	Fetched int       `json:"fetched"` // downloaded by this job
	Missing int       `json:"missing"`
	Started time.Time `json:"started"`
	Seconds float64   `json:"seconds"` // time spent running
}

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
	tmp := JobPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, JobPath(dir))
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

// Progress is reported as a job runs.
type Progress struct {
	Job     Job
	Waiting string // why it is waiting, if it is
}

// Busy tells a job to make room for a reader who is looking at pages.
type Busy func() bool

// Run goes through the dump from j.Stream on, keeping every picture the
// store does not have yet. It returns when done, stopped (ctx), or on
// an error that waiting will not cure. Network failures are waited out.
func Run(ctx context.Context, j *Job, rd *dump.Reader, st *Store, f *Fetcher, host string, busy Busy, report func(Progress)) error {
	site := rd.Site()
	j.Streams = rd.Index.StreamCount()
	if j.Started.IsZero() {
		j.Started = time.Now()
	}
	lead := j.Mode == Lead
	runStart, runSecs := time.Now(), j.Seconds
	elapsed := func() float64 { return runSecs + time.Since(runStart).Seconds() }

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
					if p.NS != dump.NSMain || p.Redirect != "" {
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
	pending := map[int]names{}
	lastSave := time.Now()
	save := func() {
		j.Seconds = elapsed()
		j.Save(st.Dir)
		st.Sync()
		lastSave = time.Now()
	}
	for r := range results {
		pending[r.s] = r
		for {
			r, ok := pending[j.Stream]
			if !ok {
				break
			}
			delete(pending, j.Stream)
			if r.err != nil {
				save()
				return r.err
			}
			if err := fetchAll(ctx, r.names, st, f, host, busy, func(w string) {
				report(Progress{Job: *j, Waiting: w})
			}, j); err != nil {
				save()
				return err
			}
			j.Stream++
			j.Seconds = elapsed()
			report(Progress{Job: *j})
			if time.Since(lastSave) > 20*time.Second {
				save()
			}
		}
	}
	if ctx.Err() != nil {
		save()
		return ctx.Err()
	}
	j.Done = true
	save()
	st.Compact()
	return nil
}

// fetchAll keeps the given pictures, waiting out network trouble.
func fetchAll(ctx context.Context, list []string, st *Store, f *Fetcher, host string, busy Busy, waiting func(string), j *Job) error {
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
				st.Put(name, nil)
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

func downloadAll(ctx context.Context, todo []Info, st *Store, f *Fetcher, busy Busy, waiting func(string), j *Job) error {
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
					if !sleep(ctx, time.Second) {
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
						st.Put(info.Name, data)
						fetched++
						mu.Unlock()
					case errors.Is(err, ErrNotFound), errors.Is(err, ErrSkipped):
						mu.Lock()
						st.Put(info.Name, nil)
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
