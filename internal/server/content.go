package server

import (
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/zim"
)

// zimPath is the part of the URL after /content/<book>/ for an entry.
// Modern archives keep everything people see in namespace C and links
// inside them are relative to it, so the namespace is left out of the
// URL; older archives have links like "../I/foo.png", so it stays in.
func zimPath(b *library.Book, e *zim.Entry) string {
	if b.Archive.NewNamespaceScheme {
		return e.URL
	}
	return e.Path()
}

// contentURL is the absolute, escaped URL of an entry.
func contentURL(b *library.Book, e *zim.Entry) string {
	return "/content/" + url.PathEscape(b.ID) + "/" + escapePath(zimPath(b, e))
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// splitContentPath turns "/content/<book>/<rest>" into its parts.
func splitContentPath(p string) (book, rest string) {
	p = strings.TrimPrefix(p, "/content/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

func (s *Server) serveContent(w http.ResponseWriter, r *http.Request) {
	bookID, rest := splitContentPath(r.URL.Path)
	b := s.Lib.Get(bookID)
	if b == nil {
		s.notFound(w, r, "", "That book is not on this drive.")
		return
	}
	a := b.Archive
	if rest == "" {
		main, err := a.MainEntry()
		if err != nil {
			if e, err := a.RandomArticle(); err == nil {
				http.Redirect(w, r, contentURL(b, e), http.StatusFound)
				return
			}
			s.notFound(w, r, b.ID, "This book has no main page.")
			return
		}
		http.Redirect(w, r, contentURL(b, main), http.StatusFound)
		return
	}

	e, err := s.lookup(b, rest)
	if err != nil {
		s.missingArticle(w, r, b, rest)
		return
	}
	if e.IsRedirect() {
		target, err := a.Resolve(e)
		if err != nil {
			s.missingArticle(w, r, b, rest)
			return
		}
		http.Redirect(w, r, contentURL(b, target), http.StatusFound)
		return
	}

	mime := e.MimeType()
	etag := `"` + a.UUIDString()[:16] + "-" + itoa(int64(e.Index)) + "-" + Version + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}

	if strings.HasPrefix(mime, "text/html") {
		body, err := a.ReadAll(e)
		if err != nil {
			s.brokenArchive(w, r, b, err)
			return
		}
		body = s.inject(body, b, e)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
		return
	}
	rd, _, err := a.BlobReader(e)
	if err != nil {
		s.brokenArchive(w, r, b, err)
		return
	}
	// ServeContent handles Range requests, so video and audio seek.
	http.ServeContent(w, r, "", time.Time{}, rd)
}

// lookup finds the entry for a content path.
func (s *Server) lookup(b *library.Book, rest string) (*zim.Entry, error) {
	a := b.Archive
	if a.NewNamespaceScheme {
		e, err := a.Find('C', rest)
		if err == nil {
			return e, nil
		}
		// Old-style links ("A/Foo", "I/bar.png") in a new archive.
		if len(rest) > 2 && rest[1] == '/' {
			if e, err2 := a.Find('C', rest[2:]); err2 == nil {
				return e, nil
			}
		}
		return nil, err
	}
	if len(rest) > 2 && rest[1] == '/' {
		e, err := a.Find(rest[0], rest[2:])
		if err == nil {
			return e, nil
		}
	}
	return a.Find('A', rest)
}

// barTemplate is the small toolbar placed at the top of every article.
var barTemplate = template.Must(template.New("bar").Parse(`<div id="nabu-bar" data-book="{{.ID}}" role="navigation" aria-label="NABU">` +
	`<a class="nabu-brand" href="/" title="NABU library"><img src="/nabu/static/nabu.svg" alt="" width="22" height="22"><span>NABU</span></a>` +
	`<a class="nabu-book" href="/content/{{.ID}}/" title="Main page of {{.Title}}">{{.Title}}</a>` +
	`<form class="nabu-search" action="/search" method="get" role="search">` +
	`<input type="hidden" name="book" value="{{.ID}}">` +
	`<input class="nabu-q" type="search" name="q" placeholder="Search {{.Title}}" aria-label="Search" autocomplete="off" spellcheck="false">` +
	`<button type="submit" aria-label="Search"><svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" fill="none" stroke="currentColor" stroke-width="2"/><path d="M15.5 15.5 21 21" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></button>` +
	`</form>` +
	`<a class="nabu-random" href="/random?book={{.ID}}" title="Random article" aria-label="Random article"><svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="4" fill="none" stroke="currentColor" stroke-width="2"/><circle cx="8.5" cy="8.5" r="1.6" fill="currentColor"/><circle cx="15.5" cy="15.5" r="1.6" fill="currentColor"/><circle cx="12" cy="12" r="1.6" fill="currentColor"/></svg></a>` +
	`</div>`))

var headInject = []byte(`<link rel="stylesheet" href="/nabu/static/fonts.css"><link rel="stylesheet" href="/nabu/static/bar.css"><script src="/nabu/static/bar.js" defer></script><link rel="icon" href="/nabu/static/nabu.svg">`)

// inject adds the NABU bar, fonts and script to an article.
func (s *Server) inject(body []byte, b *library.Book, e *zim.Entry) []byte {
	var bar bytes.Buffer
	barTemplate.Execute(&bar, b)
	lower := bytes.ToLower(body[:minInt(len(body), 64<<10)])
	out := make([]byte, 0, len(body)+bar.Len()+len(headInject)+16)

	headEnd := bytes.Index(lower, []byte("</head>"))
	bodyStart := -1
	if i := bytes.Index(lower, []byte("<body")); i >= 0 {
		if j := bytes.IndexByte(lower[i:], '>'); j >= 0 {
			bodyStart = i + j + 1
		}
	}
	switch {
	case headEnd >= 0 && bodyStart > headEnd:
		out = append(out, body[:headEnd]...)
		out = append(out, headInject...)
		out = append(out, body[headEnd:bodyStart]...)
		out = append(out, bar.Bytes()...)
		out = append(out, body[bodyStart:]...)
	case bodyStart >= 0:
		out = append(out, body[:bodyStart]...)
		out = append(out, headInject...)
		out = append(out, bar.Bytes()...)
		out = append(out, body[bodyStart:]...)
	default:
		out = append(out, headInject...)
		out = append(out, bar.Bytes()...)
		out = append(out, body...)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// missingArticle explains a link to a page that is not in this edition
// (common with "mini", "nopic" or topic selections) and offers
// alternatives instead of a bare error.
func (s *Server) missingArticle(w http.ResponseWriter, r *http.Request, b *library.Book, rest string) {
	name := rest
	if i := strings.LastIndexByte(name, '/'); i >= 0 && !b.Archive.NewNamespaceScheme {
		name = name[i+1:]
	}
	title := strings.ReplaceAll(name, "_", " ")
	if looksLikeAsset(rest) {
		http.NotFound(w, r)
		return
	}
	// Nothing starts with the full title? Drop words from the end until
	// something does: "Babylon Clay" still suggests "Babylon".
	var results []zim.Result
	for words := strings.Fields(title); len(words) > 0 && len(results) == 0; words = words[:len(words)-1] {
		results, _ = b.Archive.SearchTitles(strings.Join(words, " "), 8)
	}
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "missing.html", map[string]interface{}{
		"Book":    b,
		"Title":   title,
		"Results": s.resultsView(b, results),
		"Online":  onlineLink(b, title),
	})
}

func looksLikeAsset(p string) bool {
	p = strings.ToLower(p)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".css", ".js", ".woff", ".woff2", ".ttf", ".ico", ".mp3", ".ogg", ".webm", ".mp4", ".json"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// onlineLink returns the live Wikipedia URL for a title, for when the
// reader is connected and wants something this edition lacks.
func onlineLink(b *library.Book, title string) string {
	lang := wikiLang(b)
	if lang == "" || !strings.HasPrefix(b.Name, "wikipedia_") {
		return ""
	}
	return "https://" + lang + ".wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
}

// wikiLang maps a book to its Wikipedia language code, preferring the
// code in its name (wikipedia_en_all) over the ISO 639-3 metadata.
func wikiLang(b *library.Book) string {
	if parts := strings.SplitN(b.Name, "_", 3); len(parts) >= 2 && parts[0] == "wikipedia" {
		return parts[1]
	}
	return ""
}

func (s *Server) brokenArchive(w http.ResponseWriter, r *http.Request, b *library.Book, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	s.render(w, r, "error.html", map[string]interface{}{
		"Heading": "This page could not be read",
		"Message": "The file " + b.ID + " seems damaged or the drive was unplugged. Details: " + err.Error(),
	})
}
