package server

import (
	"strings"

	"container/list"
	"github.com/vajra-wielder/nabu/internal/essentials"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
)

// itemView is a saved, recent or suggested article for the templates.
type itemView struct {
	Book, Title, URL, Wiki, Extract string
	At                              time.Time
}

func (s *Server) itemViews(items []readItem, max int) []itemView {
	var out []itemView
	for _, it := range items {
		if max > 0 && len(out) >= max {
			break
		}
		v := itemView{Book: it.Book, Title: it.Title, Extract: it.Extract, At: it.At, Wiki: it.Book}
		if b := s.Lib.Get(it.Book); b != nil {
			v.URL = articleURL(b, it.Title)
			v.Wiki = wikiName(b.Files.DB)
		} else {
			v.URL = "/" + it.Book + "/wiki/" + wikiPath(it.Title)
		}
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------------
// Link previews

type preview struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Extract string `json:"extract"`
	Image   string `json:"image,omitempty"`
}

type previewCache struct {
	mu    sync.Mutex
	max   int
	ll    *list.List
	items map[string]*list.Element
}

type previewEntry struct {
	key string
	p   preview
}

func newPreviewCache(max int) *previewCache {
	return &previewCache{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *previewCache) get(key string) (preview, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*previewEntry).p, true
	}
	return preview{}, false
}

func (c *previewCache) put(key string, p preview) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; ok {
		return
	}
	c.items[key] = c.ll.PushFront(&previewEntry{key, p})
	for c.ll.Len() > c.max {
		back := c.ll.Back()
		delete(c.items, back.Value.(*previewEntry).key)
		c.ll.Remove(back)
	}
}

// servePreview answers GET /nabu/api/preview?book=enwiki&title=Babylon
// with the opening of an article, for hover cards.
func (s *Server) servePreview(w http.ResponseWriter, r *http.Request) {
	b := s.Lib.Get(r.URL.Query().Get("book"))
	title := r.URL.Query().Get("title")
	if b == nil || b.Reader() == nil || title == "" {
		http.NotFound(w, r)
		return
	}
	key := b.ID + "|" + title
	if p, ok := s.previews.get(key); ok {
		w.Header().Set("Cache-Control", "max-age=3600")
		writeJSON(w, p)
		return
	}
	page, err := s.loadFollow(b, title)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out := s.renderPage(b, page)
	p := preview{Title: page.Title, URL: articleURL(b, page.Title), Extract: extract(out.HTML, 520), Image: firstImage(out.HTML)}
	s.previews.put(key, p)
	w.Header().Set("Cache-Control", "max-age=3600")
	writeJSON(w, p)
}

// ---------------------------------------------------------------------
// Suggested reading on the home page

type discoverSet struct {
	at    time.Time
	items []itemView
}

// discoverFor picks a few substantial random articles with their opening
// lines. The picks change every ten minutes.
func (s *Server) discoverFor(b *library.Book) []itemView {
	if b == nil || b.Reader() == nil {
		return nil
	}
	s.discoverMu.Lock()
	if d, ok := s.discover[b.ID]; ok && time.Since(d.at) < 10*time.Minute {
		s.discoverMu.Unlock()
		return d.items
	}
	s.discoverMu.Unlock()

	rd := b.Reader()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var items []itemView
	seen := map[string]bool{}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for tries := 0; tries < 60 && len(items) < 3 && time.Now().Before(deadline); tries++ {
		e, err := rd.Index.Random(rng, dump.NSMain)
		if err != nil {
			break
		}
		p, err := rd.Load(e)
		if err != nil || p.Redirect != "" || len(p.Text) < 2500 || seen[p.Title] {
			continue
		}
		seen[p.Title] = true
		ex := extract(s.renderPage(b, p).HTML, 180)
		if ex == "" {
			continue
		}
		items = append(items, itemView{Book: b.ID, Title: p.Title, URL: articleURL(b, p.Title), Wiki: wikiName(b.Files.DB), Extract: ex})
	}
	s.discoverMu.Lock()
	s.discover[b.ID] = discoverSet{at: time.Now(), items: items}
	s.discoverMu.Unlock()
	return items
}

// ---------------------------------------------------------------------
// Saved articles and history

func (s *Server) serveSaved(w http.ResponseWriter, r *http.Request) {
	saved, history := s.reads.lists()
	ev := s.essentialsView()
	if ev.Running {
		w.Header().Set("Refresh", "3")
	}
	hasEssentials := false
	for _, c := range s.collections.List() {
		if strings.HasPrefix(c.Name, essentials.Prefix) {
			hasEssentials = true
		}
	}
	s.render(w, r, "saved.html", map[string]interface{}{
		"Essentials":    ev,
		"HasEssentials": hasEssentials,
		"EnglishBook":   s.englishBook() != nil,
		"PageTitle":     "Saved & recent",
		"Nav":           "saved",
		"Saved":         s.itemViews(saved, 0),
		"History":       s.itemViews(history, 0),
		"Settings":      s.settings(),
		"Colls":         s.collections.List(),
	})
}
