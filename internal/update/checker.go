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

// Settings are the user's choices, stored on the drive.
type Settings struct {
	CheckOnStart bool `json:"check_on_start"` // ask Wikimedia when NABU starts
	AutoDownload bool `json:"auto_download"`  // fetch newer dumps unasked
	Pictures     bool `json:"pictures"`       // load pictures from Wikimedia when online
}

// DefaultSettings: look for news and show pictures when online, but never
// start a big download unasked.
var DefaultSettings = Settings{CheckOnStart: true, Pictures: true}

// Checker remembers, per wiki, the newest release seen and when.
type Checker struct {
	Dir    string
	Mirror string
	Client *http.Client

	mu      sync.Mutex
	known   map[string]*Checked
	running map[string]bool
}

// Checked is the result of asking about one wiki.
type Checked struct {
	Release *Release  `json:"release,omitempty"`
	At      time.Time `json:"at"`
	Err     string    `json:"error,omitempty"`
}

func (c *Checker) cachePath() string { return filepath.Join(c.Dir, ".nabu", "releases.json") }

// Load restores what was learned in earlier sessions.
func (c *Checker) Load() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.known = map[string]*Checked{}
	c.running = map[string]bool{}
	if b, err := os.ReadFile(c.cachePath()); err == nil {
		json.Unmarshal(b, &c.known)
	}
	for k, v := range c.known {
		if v == nil {
			delete(c.known, k)
		}
	}
}

// Check asks the server about wiki now.
func (c *Checker) Check(ctx context.Context, wiki string) *Checked {
	c.mu.Lock()
	if c.known == nil {
		c.known, c.running = map[string]*Checked{}, map[string]bool{}
	}
	if c.running[wiki] {
		c.mu.Unlock()
		return c.Get(wiki)
	}
	c.running[wiki] = true
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rel, err := Latest(ctx, c.Client, c.Mirror, wiki)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.running, wiki)
	res := &Checked{At: time.Now()}
	if err != nil {
		res.Err = err.Error()
		if prev := c.known[wiki]; prev != nil && prev.Release != nil {
			res.Release = prev.Release // keep the last good answer
		}
	} else {
		res.Release = rel
	}
	c.known[wiki] = res
	if err := os.MkdirAll(filepath.Dir(c.cachePath()), 0o755); err == nil {
		if b, err := json.Marshal(c.known); err == nil {
			os.WriteFile(c.cachePath(), b, 0o644)
		}
	}
	return res
}

// Get returns the last known answer for wiki, or nil.
func (c *Checker) Get(wiki string) *Checked {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.known[wiki]
}

// Checking reports whether a check of wiki is in flight.
func (c *Checker) Checking(wiki string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running[wiki]
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
