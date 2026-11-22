package server

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/essentials"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/timer"
)

// essentialGroups is the Essentials list (a variable for tests).
var essentialGroups = essentials.Groups

// essentialsJob makes (or brings up to date) the Essentials collections:
// each article saved as its own copy, with its pictures (from the drive,
// or from Wikimedia when online), readable in any browser without NABU.
type essentialsJob struct {
	mu      sync.Mutex
	running bool
	done    int
	total   int
	book    string
	missing []string
	err     string
	made    bool
	started time.Time
	took    time.Duration
	phase   string // what it is doing now
	pdone   int    // ... how far
	ptotal  int
}

type essentialsView struct {
	Running     bool
	Done, Total int
	Book        string
	Missing     []string
	Err         string
	Made        bool
	Count       int
	Timer       string
	Took        string
	Phase       string
}

func (s *Server) essentialsView() essentialsView {
	j := &s.essentials
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, g := range essentialGroups() {
		n += len(g.Titles)
	}
	v := essentialsView{Running: j.running, Done: j.done, Total: j.total, Book: j.book,
		Missing: j.missing, Err: j.err, Made: j.made, Count: n}
	if j.running {
		v.Phase = j.phase
		if j.phase != "saving the articles" && j.ptotal > 0 {
			v.Phase += " (" + strconv.Itoa(j.pdone) + " of " + strconv.Itoa(j.ptotal) + ")"
		}
		v.Timer = timer.Elapsed(time.Since(j.started)) + " so far"
		if j.phase == "saving the articles" {
			v.Timer = timer.Line(time.Since(j.started), int64(j.done), int64(j.total))
		}
	} else if j.made {
		v.Took = timer.Elapsed(j.took)
	}
	return v
}

// englishBook is the English Wikipedia to take the Essentials from:
// English, else Simple English, else any in English.
func (s *Server) englishBook() *library.Book {
	for _, id := range []string{"enwiki", "simplewiki"} {
		if b := s.Lib.Get(id); b != nil && b.Reader() != nil {
			return b
		}
	}
	for _, b := range s.Lib.Books() {
		if site := b.Site(); site != nil && site.Lang == "en" {
			return b
		}
	}
	return nil
}

func (s *Server) startEssentials() error {
	b := s.englishBook()
	if b == nil {
		return errors.New("the Essentials are English Wikipedia articles: put English (or Simple English) Wikipedia on the drive first")
	}
	j := &s.essentials
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return nil
	}
	groups := essentialGroups()
	j.running, j.done, j.total, j.missing, j.err, j.book = true, 0, 0, nil, "", wikiName(b.Files.DB)
	j.started = time.Now()
	for _, g := range groups {
		j.total += len(g.Titles)
	}
	j.mu.Unlock()

	go func() {
		missing, firstErr := s.runEssentials(context.Background(), b, groups, func(what string, d, t int) {
			j.mu.Lock()
			j.phase, j.pdone, j.ptotal = what, d, t
			j.mu.Unlock()
		}, func() {
			j.mu.Lock()
			j.done++
			j.mu.Unlock()
		})
		j.mu.Lock()
		j.running, j.missing, j.err, j.made = false, missing, firstErr, true
		j.took = time.Since(j.started)
		j.mu.Unlock()
	}()
	return nil
}

// runEssentials saves every Essentials article into its collection,
// calling step after each one. Online, their pictures are fetched first,
// all together in batches (much quicker than one article at a time), so
// the saved copies find them on the drive. phase says what is going on.
func (s *Server) runEssentials(ctx context.Context, b *library.Book, groups []essentials.Group, phase func(string, int, int), step func()) (missing []string, firstErr string) {
	if ps := s.Lib.Pictures(); ps != nil && s.settings().Pictures && !s.Offline {
		var names []string
		total, done := 0, 0
		for _, g := range groups {
			total += len(g.Titles)
		}
		for _, g := range groups {
			for _, title := range g.Titles {
				if page, err := s.loadFollow(b, title); err == nil {
					names = append(names, pictures.PageNames(page.Title, page.Text, b.Site(), false)...)
				}
				done++
				phase("finding their pictures", done, total)
			}
		}
		if st, err := ps.Stores(b.Files.DB); err == nil {
			phase("fetching their pictures", 0, len(names))
			err := pictures.Keep(ctx, names, st, ps.Fetch, b.Site().Host(), func(d, t int) { phase("fetching their pictures", d, t) })
			if err != nil && ctx.Err() == nil && firstErr == "" {
				firstErr = "pictures: " + err.Error()
			}
		}
	}
	phase("saving the articles", 0, 0)
	for _, g := range groups {
		name := essentials.Prefix + g.Name
		slug := ""
		for _, c := range s.collections.List() {
			if c.Name == name {
				slug = c.Slug
			}
		}
		if slug == "" {
			c, err := s.collections.Create(name)
			if err != nil {
				return missing, err.Error()
			}
			slug = c.Slug
		}
		for _, title := range g.Titles {
			if ctx.Err() != nil {
				return missing, firstErr
			}
			// Saved again each time: brings copies up to date with the
			// newest dump, and adds the pictures once online.
			if _, err := s.collectInto(slug, b.ID, title); err != nil {
				if err.Error() == "no such article" {
					missing = append(missing, title)
				} else if firstErr == "" {
					firstErr = title + ": " + err.Error()
				}
			}
			step()
		}
	}
	return missing, firstErr
}

// MakeEssentials saves the Essentials collections now (for "nabu
// setup"), reporting each article done.
func (s *Server) MakeEssentials(ctx context.Context, progress func(what string, done, total int)) (missing []string, err error) {
	b := s.englishBook()
	if b == nil {
		return nil, errors.New("no English Wikipedia on the drive")
	}
	groups := essentialGroups()
	total, done := 0, 0
	for _, g := range groups {
		total += len(g.Titles)
	}
	missing, firstErr := s.runEssentials(ctx, b, groups, progress, func() {
		done++
		progress("saving the articles", done, total)
	})
	if firstErr != "" {
		err = errors.New(firstErr)
	}
	return missing, err
}
