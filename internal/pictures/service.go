package pictures

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Service is NABU's pictures: the stores of the drive's wikis, and the
// one Fetcher that talks to Wikimedia.
type Service struct {
	Dir   string // the drive's pictures folder
	Fetch *Fetcher

	mu       sync.Mutex
	stores   map[string]*Store
	failed   map[string]time.Time // picture → do not try again before
	lastView time.Time
}

// NewService returns the pictures of the folder dir.
func NewService(dir string, f *Fetcher) *Service {
	return &Service{Dir: dir, Fetch: f, stores: map[string]*Store{}, failed: map[string]time.Time{}}
}

// Store opens a wiki's store. Unless create is set, a wiki without a
// pictures folder has none (nil, nil): a folder left out of a copy of
// the drive is not made again just by reading.
func (s *Service) Store(db string, create bool) (*Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[db]; ok {
		return st, nil
	}
	dir := filepath.Join(s.Dir, db)
	if _, err := os.Stat(filepath.Join(dir, "index.bin")); err != nil && !create {
		if _, err := os.Stat(filepath.Join(dir, "recent.bin")); err != nil {
			return nil, nil
		}
	}
	st, err := Open(dir)
	if err != nil {
		return nil, err
	}
	s.stores[db] = st
	return st, nil
}

// Viewing notes that someone is reading, so whole-wiki downloads make
// room for the pictures of the page they are on.
func (s *Service) Viewing() {
	s.mu.Lock()
	s.lastView = time.Now()
	s.mu.Unlock()
}

// Busy reports whether someone read a page in the last 10 seconds.
func (s *Service) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastView) < 10*time.Second
}

// Get returns a picture for a reader: from the drive if it is there,
// else (online) from Wikimedia, keeping it on the drive if keep is set.
func (s *Service) Get(ctx context.Context, db, host, name string, online, keep bool) ([]byte, error) {
	name = Normalize(name)
	st, err := s.Store(db, false)
	if err != nil {
		return nil, err
	}
	if st != nil {
		data, known, err := st.Get(name)
		if err == nil && known {
			if data == nil {
				return nil, ErrNotFound
			}
			return data, nil
		}
	}
	if !online {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	if t, ok := s.failed[name]; ok && time.Now().Before(t) {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	s.mu.Unlock()
	fail := func(err error) ([]byte, error) {
		s.mu.Lock()
		if len(s.failed) > 10000 {
			s.failed = map[string]time.Time{}
		}
		s.failed[name] = time.Now().Add(5 * time.Minute)
		s.mu.Unlock()
		return nil, err
	}
	info, err := s.Fetch.ResolveOne(ctx, host, name)
	if err != nil {
		return fail(err)
	}
	if info.Missing || info.Skip {
		if keep {
			if st, _ := s.Store(db, true); st != nil {
				st.Put(name, nil)
			}
		}
		return nil, ErrNotFound
	}
	data, err := s.Fetch.Download(ctx, info.URL)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return fail(err)
	}
	if keep {
		if st, _ := s.Store(db, true); st != nil {
			st.Put(name, data)
		}
	}
	return data, nil
}

// Remove deletes a wiki's pictures from the drive.
func (s *Service) Remove(db string) error {
	s.mu.Lock()
	if st, ok := s.stores[db]; ok {
		st.Close()
		delete(s.stores, db)
	}
	s.mu.Unlock()
	return os.RemoveAll(filepath.Join(s.Dir, db))
}

// Sync flushes every open store.
func (s *Service) Sync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.stores {
		st.Sync()
	}
}

// Close closes every store.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for db, st := range s.stores {
		st.Close()
		delete(s.stores, db)
	}
}
