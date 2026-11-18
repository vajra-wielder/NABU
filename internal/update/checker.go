package update

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Settings are the user's choices, stored on the drive next to the books.
type Settings struct {
	CheckOnStart bool `json:"check_on_start"` // ask the catalogue when NABU starts
	AutoDownload bool `json:"auto_download"`  // fetch newer editions without asking
}

// DefaultSettings: look for news, but never start a huge download unasked.
var DefaultSettings = Settings{CheckOnStart: true, AutoDownload: false}

// Checker fetches the catalogue and remembers the last answer on disk,
// so the update page still has something to show when offline.
type Checker struct {
	Dir        string // library directory
	CatalogURL string
	Client     *http.Client

	mu        sync.Mutex
	all       []Edition
	checkedAt time.Time
	lastErr   string
	checking  bool
}

type cacheFile struct {
	CheckedAt time.Time `json:"checked_at"`
	Editions  []Edition `json:"editions"`
}

func (c *Checker) stateDir() string { return filepath.Join(c.Dir, ".nabu") }

// LoadCache restores the last catalogue seen.
func (c *Checker) LoadCache() {
	b, err := os.ReadFile(filepath.Join(c.stateDir(), "catalog.json"))
	if err != nil {
		return
	}
	var cf cacheFile
	if json.Unmarshal(b, &cf) == nil {
		c.mu.Lock()
		c.all, c.checkedAt = cf.Editions, cf.CheckedAt
		c.mu.Unlock()
	}
}

// Check asks the catalogue for the current list of editions.
func (c *Checker) Check(ctx context.Context) error {
	c.mu.Lock()
	if c.checking {
		c.mu.Unlock()
		return nil
	}
	c.checking = true
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	eds, err := FetchCatalog(ctx, c.Client, c.CatalogURL)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.checking = false
	if err != nil {
		c.lastErr = err.Error()
		return err
	}
	c.all, c.checkedAt, c.lastErr = eds, time.Now(), ""
	if err := os.MkdirAll(c.stateDir(), 0o755); err == nil {
		if b, err := json.Marshal(cacheFile{CheckedAt: c.checkedAt, Editions: eds}); err == nil {
			os.WriteFile(filepath.Join(c.stateDir(), "catalog.json"), b, 0o644)
		}
	}
	return nil
}

// Snapshot returns the newest edition of every series and check state.
func (c *Checker) Snapshot() (latest []Edition, checkedAt time.Time, lastErr string, checking bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Latest(c.all), c.checkedAt, c.lastErr, c.checking
}

// Find returns the catalogue entry for a file name.
func (c *Checker) Find(file string) (Edition, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.all {
		if e.File == file {
			return e, true
		}
	}
	return Edition{}, false
}

// Installed describes a book already on the drive.
type Installed struct {
	ID      string
	Series  string
	Edition string // YYYY-MM
}

// Available pairs an installed book with a newer edition of it.
type Available struct {
	BookID  string
	Current string
	Newer   Edition
}

// UpdatesFor lists newer editions for the installed books.
func UpdatesFor(books []Installed, latest []Edition) []Available {
	bySeries := map[string]Edition{}
	for _, e := range latest {
		bySeries[e.Series()] = e
	}
	have := map[string]string{}
	for _, b := range books {
		if b.Edition > have[b.Series] {
			have[b.Series] = b.Edition
		}
	}
	var out []Available
	for _, b := range books {
		e, ok := bySeries[b.Series]
		if ok && b.Edition != "" && e.Date > b.Edition && have[b.Series] == b.Edition {
			out = append(out, Available{BookID: b.ID, Current: b.Edition, Newer: e})
		}
	}
	return out
}

// LoadSettings reads settings from the library, falling back to defaults.
func LoadSettings(dir string) Settings {
	s := DefaultSettings
	if b, err := os.ReadFile(filepath.Join(dir, ".nabu", "settings.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

// SaveSettings writes settings into the library.
func SaveSettings(dir string, s Settings) error {
	if err := os.MkdirAll(filepath.Join(dir, ".nabu"), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, ".nabu", "settings.json"), b, 0o644)
}
