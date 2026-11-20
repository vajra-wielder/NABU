package server

import (
	"errors"
	"sync"

	"github.com/vajra-wielder/nabu/internal/essentials"
	"github.com/vajra-wielder/nabu/internal/library"
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
}

type essentialsView struct {
	Running     bool
	Done, Total int
	Book        string
	Missing     []string
	Err         string
	Made        bool
	Count       int
}

func (s *Server) essentialsView() essentialsView {
	j := &s.essentials
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, g := range essentialGroups() {
		n += len(g.Titles)
	}
	return essentialsView{Running: j.running, Done: j.done, Total: j.total, Book: j.book,
		Missing: j.missing, Err: j.err, Made: j.made, Count: n}
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
	for _, g := range groups {
		j.total += len(g.Titles)
	}
	j.mu.Unlock()

	go func() {
		var missing []string
		var firstErr string
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
					firstErr = err.Error()
					break
				}
				slug = c.Slug
			}
			for _, title := range g.Titles {
				// Saved again each time: brings copies up to date with the
				// newest dump, and adds the pictures once online.
				if _, err := s.collectInto(slug, b.ID, title); err != nil {
					if err.Error() == "no such article" {
						missing = append(missing, title)
					} else if firstErr == "" {
						firstErr = title + ": " + err.Error()
					}
				}
				j.mu.Lock()
				j.done++
				j.mu.Unlock()
			}
		}
		j.mu.Lock()
		j.running, j.missing, j.err, j.made = false, missing, firstErr, true
		j.mu.Unlock()
	}()
	return nil
}
