package server

import (
	"errors"
	"html/template"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/update"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

// articleURL is the address of a page: /enwiki/wiki/Babylon
func articleURL(b *library.Book, title string) string {
	return "/" + b.ID + "/wiki/" + wikiPath(title)
}

// wikiPath escapes a title for a URL path, as Wikipedia does: spaces
// become underscores, slashes and colons stay readable.
func wikiPath(t string) string {
	t = strings.ReplaceAll(t, " ", "_")
	var b strings.Builder
	for _, seg := range strings.Split(t, "/") {
		if b.Len() > 0 || strings.HasPrefix(t, "/") {
			b.WriteByte('/')
		}
		b.WriteString(strings.ReplaceAll(url.PathEscape(seg), "%3A", ":"))
	}
	return b.String()
}

func titleFromPath(p string) string {
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	return strings.ReplaceAll(p, "_", " ")
}

var redirectFragRe = regexp.MustCompile(`\[\[[^\]|#]*#([^\]|]+)`)

func (s *Server) serveArticle(w http.ResponseWriter, r *http.Request, b *library.Book, path string, source bool) {
	rd := b.Reader()
	site := rd.Site()
	asked := titleFromPath(path)
	title := site.Normalize(asked)
	if title == "" {
		http.Redirect(w, r, "/"+b.ID+"/", http.StatusFound)
		return
	}
	e, exact, err := rd.Index.Find(title)
	if errors.Is(err, dump.ErrNotFound) {
		s.missing(w, r, b, title)
		return
	}
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, "This page could not be read", err.Error())
		return
	}
	_ = exact
	if e.Title != asked {
		// Send people to the canonical address (fixes case and spacing).
		base := articleURL(b, e.Title)
		if source {
			base = "/" + b.ID + "/source/" + wikiPath(e.Title)
		}
		http.Redirect(w, r, base+keepQuery(r), http.StatusFound)
		return
	}
	page, err := rd.Load(e)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, "This page could not be read",
			"The dump on the drive seems damaged or was unplugged. Details: "+err.Error())
		return
	}
	if source {
		s.render(w, r, "source.html", map[string]interface{}{
			"Book": b, "Site": site, "Page": page, "PageTitle": page.Title + " (source)",
			"URL": articleURL(b, page.Title),
		})
		return
	}
	if page.Redirect != "" && r.URL.Query().Get("redirect") != "no" {
		frag := ""
		if m := redirectFragRe.FindStringSubmatch(page.Text); m != nil {
			frag = "#" + url.PathEscape(wikitext.Anchor(m[1]))
		}
		q := url.Values{"from": {page.Title}}
		http.Redirect(w, r, articleURL(b, page.Redirect)+"?"+q.Encode()+frag, http.StatusFound)
		return
	}

	ctx := &wikitext.Context{
		Title:   page.Title,
		Site:    site,
		PageURL: func(t string) string { return articleURL(b, t) },
	}
	settings := s.settings()
	if settings.Pictures && !s.Offline {
		host := site.Host()
		ctx.ImageURL = func(file string, width int) string {
			return "https://" + host + "/wiki/Special:FilePath/" + wikiPath(file) + "?width=" + itoa(int64(width))
		}
	}
	start := time.Now()
	out := wikitext.Render(page.Text, ctx)
	display := page.Title
	if out.DisplayTitle != "" && strings.EqualFold(out.DisplayTitle, page.Title) {
		display = out.DisplayTitle
	}
	w.Header().Set("Server-Timing", "render;dur="+itoa(int64(time.Since(start)/time.Millisecond)))
	s.render(w, r, "article.html", map[string]interface{}{
		"Book":       b,
		"Site":       site,
		"PageTitle":  display,
		"Title":      display,
		"Page":       page,
		"Body":       template.HTML(out.HTML),
		"ShortDesc":  out.ShortDesc,
		"Categories": out.Categories,
		"From":       r.URL.Query().Get("from"),
		"Online":     "https://" + site.Host() + "/wiki/" + wikiPath(page.Title),
		"Source":     "/" + b.ID + "/source/" + wikiPath(page.Title),
		"Lang":       site.Lang,
		"Dir":        dirOf(site),
		"Edition":    dump.HumanDate(b.Files.Date),
		"IsMain":     page.Title == site.MainPage(),
	})
}

func keepQuery(r *http.Request) string {
	if r.URL.RawQuery != "" {
		return "?" + r.URL.RawQuery
	}
	return ""
}

func dirOf(site *dump.Siteinfo) string {
	if site.RTL() {
		return "rtl"
	}
	return "ltr"
}

func (s *Server) missing(w http.ResponseWriter, r *http.Request, b *library.Book, title string) {
	rd := b.Reader()
	var results []dump.Entry
	for words := strings.Fields(title); len(words) > 0 && len(results) == 0; words = words[:len(words)-1] {
		results, _ = rd.Index.Prefix(strings.Join(words, " "), 8, articleNS)
	}
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "missing.html", map[string]interface{}{
		"Book": b, "Site": rd.Site(), "Title": title, "PageTitle": title,
		"Results": s.links(b, results),
		"Online":  "https://" + rd.Site().Host() + "/wiki/" + wikiPath(title),
	})
}

func articleNS(ns int) bool { return ns == dump.NSMain }

type linkView struct {
	Title string
	URL   string
}

func (s *Server) links(b *library.Book, es []dump.Entry) []linkView {
	out := make([]linkView, 0, len(es))
	for _, e := range es {
		out = append(out, linkView{Title: e.Title, URL: articleURL(b, e.Title)})
	}
	return out
}

func (s *Server) serveSearch(w http.ResponseWriter, r *http.Request, b *library.Book) {
	if b == nil || b.Reader() == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	rd := b.Reader()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Redirect(w, r, "/"+b.ID+"/", http.StatusFound)
		return
	}
	nsOK := articleNS
	if i := strings.IndexByte(q, ':'); i > 0 {
		if _, ok := rd.Site().NamespaceKey(q[:i]); ok {
			nsOK = nil // searching "Category:..." etc. on purpose
		}
	}
	// Like Wikipedia's search box: an exact title opens the article.
	if r.URL.Query().Get("list") == "" {
		if e, _, err := rd.Index.Find(rd.Site().Normalize(q)); err == nil && (nsOK == nil || nsOK(e.NS)) {
			http.Redirect(w, r, articleURL(b, e.Title), http.StatusFound)
			return
		}
	}
	res, err := rd.Index.Prefix(q, 60, nsOK)
	data := map[string]interface{}{
		"Book": b, "Site": rd.Site(), "Query": q, "PageTitle": q,
		"Results": s.links(b, res),
		"Online":  "https://" + rd.Site().Host() + "/w/index.php?search=" + url.QueryEscape(q),
	}
	if err != nil {
		data["Error"] = err.Error()
	}
	if len(res) == 0 {
		var more []dump.Entry
		seen := map[string]bool{}
		for _, word := range strings.Fields(q) {
			if len([]rune(word)) < 3 {
				continue
			}
			rs, _ := rd.Index.Prefix(word, 8, articleNS)
			for _, x := range rs {
				if !seen[x.Title] {
					seen[x.Title] = true
					more = append(more, x)
				}
			}
		}
		data["Related"] = s.links(b, more)
	}
	s.render(w, r, "search.html", data)
}

func (s *Server) serveSuggest(w http.ResponseWriter, r *http.Request) {
	type sugg struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	out := []sugg{}
	if b := s.pickBook(r); b != nil && b.Reader() != nil {
		res, _ := b.Reader().Index.Prefix(r.URL.Query().Get("q"), 10, articleNS)
		for _, e := range res {
			out = append(out, sugg{Title: e.Title, URL: articleURL(b, e.Title)})
		}
	}
	writeJSON(w, out)
}

var (
	rngMu sync.Mutex
	rng   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func (s *Server) serveRandom(w http.ResponseWriter, r *http.Request, b *library.Book) {
	rd := b.Reader()
	rngMu.Lock()
	defer rngMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	fallback := ""
	for tries := 0; tries < 30; tries++ {
		e, err := rd.Index.Random(rng, dump.NSMain)
		if err != nil {
			break
		}
		p, err := rd.Load(e)
		if err != nil || p.Redirect != "" {
			continue
		}
		if len(p.Text) < 400 { // prefer real articles to stubs
			if fallback == "" {
				fallback = e.Title
			}
			continue
		}
		http.Redirect(w, r, articleURL(b, e.Title), http.StatusFound)
		return
	}
	if fallback != "" {
		http.Redirect(w, r, articleURL(b, fallback), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/"+b.ID+"/", http.StatusFound)
}

func (s *Server) preparing(w http.ResponseWriter, r *http.Request, b *library.Book) {
	st := b.Status()
	w.Header().Set("Refresh", "3")
	w.WriteHeader(http.StatusServiceUnavailable)
	s.render(w, r, "preparing.html", map[string]interface{}{
		"Book": b, "Status": st, "PageTitle": "Preparing " + b.Files.DB,
	})
}

func (s *Server) settings() update.Settings {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return update.LoadSettings(s.Lib.Dir)
}
