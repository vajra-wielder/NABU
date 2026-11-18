// Package collect keeps reading collections: named folders on the drive
// in which each saved article is a separate, self-contained copy. A
// collection stays readable without NABU (open its index.html in any
// browser), after the dump it came from is updated or deleted, and can
// be copied to another drive or computer as an ordinary folder.
package collect

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Item is one saved article.
type Item struct {
	Book   string    `json:"book"` // NABU id, e.g. "enwiki"
	Wiki   string    `json:"wiki"` // readable wiki name
	Host   string    `json:"host"` // e.g. "en.wikipedia.org"
	Title  string    `json:"title"`
	File   string    `json:"file"`   // the saved page, e.g. "Babylon.html"
	Dump   string    `json:"dump"`   // dump date the copy was made from
	Edited string    `json:"edited"` // last edit of the article in that dump
	Saved  time.Time `json:"saved"`
}

// Collection is a named set of saved articles.
type Collection struct {
	Slug    string    `json:"-"`
	Name    string    `json:"name"`
	Note    string    `json:"note,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Items   []Item    `json:"items"`
}

// Has reports whether the collection holds an article.
func (c *Collection) Has(book, title string) bool {
	for _, it := range c.Items {
		if it.Book == book && it.Title == title {
			return true
		}
	}
	return false
}

// Store manages the collections folder.
type Store struct {
	Dir string
	mu  sync.Mutex
}

// ErrNotFound means no such collection or item.
var ErrNotFound = errors.New("collect: not found")

// New opens (and creates) the collections folder.
func New(dir string) *Store { return &Store{Dir: dir} }

// Slug makes a file-system-safe name: letters and digits of any script,
// spaces as underscores, nothing that Windows, macOS or FAT32 refuse.
func Slug(s string, max int) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r):
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-' || r == '.' || r == '(' || r == ')' || r == ',' || r == '\'':
			if r == ' ' {
				r = '_'
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= max {
			break
		}
	}
	out := strings.Trim(b.String(), "._ ")
	if out == "" {
		out = "untitled"
	}
	switch strings.ToUpper(out) { // reserved on Windows
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "LPT1", "LPT2", "LPT3":
		out = "_" + out
	}
	return out
}

func (s *Store) path(slug string, parts ...string) (string, error) {
	if slug == "" || slug != filepath.Base(slug) || strings.HasPrefix(slug, ".") {
		return "", ErrNotFound
	}
	p := filepath.Join(append([]string{s.Dir, slug}, parts...)...)
	if !strings.HasPrefix(p, filepath.Join(s.Dir, slug)) {
		return "", ErrNotFound
	}
	return p, nil
}

// File returns the on-disk path of a file inside a collection, or an
// error if the name tries to leave it.
func (s *Store) File(slug, name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", ErrNotFound
	}
	return s.path(slug, name)
}

func (s *Store) load(slug string) (*Collection, error) {
	p, err := s.path(slug, "collection.json")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c := &Collection{Slug: slug}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("collect: %s/collection.json: %w", slug, err)
	}
	return c, nil
}

// save writes collection.json, collection.js and index.html atomically.
func (s *Store) save(c *Collection) error {
	dir, err := s.path(c.Slug)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	c.Updated = time.Now().UTC()
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := writeAtomic(filepath.Join(dir, "collection.json"), b); err != nil {
		return err
	}
	// Saved pages link to each other through this map, so a page saved
	// later is picked up by pages saved earlier, without re-saving them.
	m := map[string]string{}
	for _, it := range c.Items {
		m[it.Book+"|"+it.Title] = it.File
	}
	mj, _ := json.Marshal(m)
	if err := writeAtomic(filepath.Join(dir, "collection.js"), []byte("window.NABU_COLLECTION="+string(mj)+";\n")); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "index.html"), []byte(indexHTML(c)))
}

func writeAtomic(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		if strings.Contains(err.Error(), "no space") {
			return errors.New("the drive is full")
		}
		return err
	}
	return os.Rename(tmp, p)
}

// List returns all collections, most recently changed first.
func (s *Store) List() []*Collection {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, _ := os.ReadDir(s.Dir)
	var out []*Collection
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if c, err := s.load(e.Name()); err == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Get loads one collection.
func (s *Store) Get(slug string) (*Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(slug)
}

// Create makes a new, empty collection.
func (s *Store) Create(name string) (*Collection, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return nil, errors.New("give the collection a name")
	}
	if len([]rune(name)) > 80 {
		return nil, errors.New("that name is too long")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	base := Slug(name, 60)
	slug := base
	for i := 2; ; i++ {
		p, _ := s.path(slug)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			break
		}
		slug = fmt.Sprintf("%s_%d", base, i)
	}
	now := time.Now().UTC()
	c := &Collection{Slug: slug, Name: name, Created: now, Items: []Item{}}
	return c, s.save(c)
}

// Rename changes a collection's display name (its folder keeps its name,
// so copies of it elsewhere stay in step).
func (s *Store) Rename(slug, name string) error {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return errors.New("give the collection a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(slug)
	if err != nil {
		return err
	}
	c.Name = name
	return s.save(c)
}

// Delete removes a collection and every saved page in it.
func (s *Store) Delete(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.load(slug); err != nil {
		return err
	}
	p, _ := s.path(slug)
	return os.RemoveAll(p)
}

// Add saves an article into a collection: page is the self-contained
// HTML, source the wikitext. Saving an article that is already there
// replaces its copy (to refresh it from a newer dump).
func (s *Store) Add(slug string, it Item, page, source []byte) (*Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(slug)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, x := range c.Items {
		if x.Book == it.Book && x.Title == it.Title {
			idx = i
		}
	}
	if idx >= 0 {
		it.File = c.Items[idx].File
	} else {
		it.File = s.freeName(c, Slug(it.Title, 80))
	}
	dir, _ := s.path(slug)
	if err := writeAtomic(filepath.Join(dir, it.File), page); err != nil {
		return nil, err
	}
	if len(source) > 0 {
		if err := writeAtomic(filepath.Join(dir, strings.TrimSuffix(it.File, ".html")+".wiki"), source); err != nil {
			return nil, err
		}
	}
	it.Saved = time.Now().UTC()
	if idx >= 0 {
		c.Items[idx] = it
	} else {
		c.Items = append(c.Items, it)
	}
	return c, s.save(c)
}

func (s *Store) freeName(c *Collection, base string) string {
	used := map[string]bool{"index.html": true}
	for _, it := range c.Items {
		used[strings.ToLower(it.File)] = true // FAT32 and macOS ignore case
	}
	name := base + ".html"
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s_%d.html", base, i)
	}
	return name
}

// Remove deletes one saved article.
func (s *Store) Remove(slug, book, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(slug)
	if err != nil {
		return err
	}
	for i, it := range c.Items {
		if it.Book == book && it.Title == title {
			dir, _ := s.path(slug)
			os.Remove(filepath.Join(dir, it.File))
			os.Remove(filepath.Join(dir, strings.TrimSuffix(it.File, ".html")+".wiki"))
			c.Items = append(c.Items[:i], c.Items[i+1:]...)
			return s.save(c)
		}
	}
	return ErrNotFound
}

// indexHTML is the collection's contents page, readable without NABU.
func indexHTML(c *Collection) string {
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` +
		esc(c.Name) + ` · NABU collection</title><style>` + indexCSS + `</style></head><body><main>`)
	b.WriteString(`<p class="kicker">NABU collection</p><h1>` + esc(c.Name) + `</h1>`)
	if c.Note != "" {
		b.WriteString(`<p>` + esc(c.Note) + `</p>`)
	}
	b.WriteString(fmt.Sprintf(`<p class="meta">%d article%s · saved copies, readable in any browser without NABU</p><ol>`, len(c.Items), plural(len(c.Items))))
	for _, it := range c.Items {
		b.WriteString(`<li><a href="` + esc(urlPath(it.File)) + `" dir="auto">` + esc(it.Title) + `</a> <span>` + esc(it.Wiki) + ` · dump of ` + esc(humanDate(it.Dump)) + `</span></li>`)
	}
	b.WriteString(`</ol><p class="meta">Text from Wikipedia under the CC BY-SA 4.0 licence; each page links to its original.</p></main></body></html>`)
	return b.String()
}

const indexCSS = `body{margin:0;background:#f6f0e4;color:#241f19;font:17px/1.6 system-ui,-apple-system,'Segoe UI',Roboto,Arial,sans-serif}
main{max-width:46rem;margin:0 auto;padding:32px 20px}h1{font-family:Georgia,serif;font-weight:500;border-bottom:2px solid #b5653a;padding-bottom:6px;margin:4px 0 12px}
.kicker{color:#1d3a8a;font-weight:700;letter-spacing:.15em;text-transform:uppercase;font-size:12px;margin:0}.meta{color:#62584a;font-size:14px}
ol{padding-left:1.4em}li{margin:.4em 0}li::marker{color:#b5653a}a{color:#24479f;text-decoration:none;font-family:Georgia,serif;font-size:1.15em}a:hover{text-decoration:underline}
li span{color:#8a7e6d;font-size:13px;margin-left:6px}@media(prefers-color-scheme:dark){body{background:#0e1328;color:#ece6d8}a{color:#9bb3f7}.meta,li span{color:#b3ab97}.kicker{color:#9bb3f7}}`

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func humanDate(d string) string {
	if len(d) == 8 {
		return d[:4] + "-" + d[4:6] + "-" + d[6:]
	}
	return d
}

func urlPath(name string) string {
	r := strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F", " ", "%20")
	return r.Replace(name)
}
