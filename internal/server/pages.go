package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/update"
	"github.com/vajra-wielder/nabu/internal/zim"
)

var funcs = template.FuncMap{
	"bytes": update.HumanBytes,
	"count": humanCount,
	"month": humanMonth,
	"since": humanSince,
	"pct": func(done, total int64) string {
		if total <= 0 {
			return "0"
		}
		p := 100 * float64(done) / float64(total)
		if p > 100 {
			p = 100
		}
		return strconvF(p)
	},
	"langname": func(code string) string {
		if n, ok := langNames[code]; ok {
			return n
		}
		return code
	},
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = map[string]interface{}{}
	}
	data["Version"] = Version
	data["Local"] = isLocal(r)
	if _, ok := data["Books"]; !ok {
		data["Books"] = s.Lib.Books()
	}
	if _, ok := data["Default"]; !ok {
		if b, ok := data["Book"].(*library.Book); ok {
			data["Default"] = b
		} else {
			data["Default"] = s.Lib.Default()
		}
	}
	if _, ok := data["Dir"]; !ok {
		data["Dir"] = s.Lib.Dir
	}
	if _, ok := data["Nav"]; !ok {
		data["Nav"] = map[string]string{"home.html": "home", "updates.html": "updates", "about.html": "about"}[name]
	}
	if _, ok := data["PageTitle"]; !ok {
		data["PageTitle"] = ""
		if q, ok := data["Query"].(string); ok {
			data["PageTitle"] = q
		} else if t, ok := data["Heading"].(string); ok {
			data["PageTitle"] = t
		} else if name == "updates.html" {
			data["PageTitle"] = "Get & update"
		} else if name == "about.html" {
			data["PageTitle"] = "About"
		}
	}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, book, msg string) {
	if msg == "" {
		msg = "There is nothing at this address."
	}
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "error.html", map[string]interface{}{"Heading": "Not found", "Message": msg})
}

// pickBook returns the book named in ?book=, or the default one.
func (s *Server) pickBook(r *http.Request) *library.Book {
	if id := r.URL.Query().Get("book"); id != "" {
		if b := s.Lib.Get(id); b != nil {
			return b
		}
	}
	return s.Lib.Default()
}

func (s *Server) serveHome(w http.ResponseWriter, r *http.Request) {
	books := s.Lib.Books()
	updates := s.availableUpdates()
	upByBook := map[string]update.Available{}
	for _, u := range updates {
		upByBook[u.BookID] = u
	}
	type card struct {
		*library.Book
		Update *update.Available
		Lang   string
	}
	var cards []card
	for _, b := range books {
		c := card{Book: b, Lang: wikiLang(b)}
		if u, ok := upByBook[b.ID]; ok {
			c.Update = &u
		}
		cards = append(cards, c)
	}
	s.render(w, r, "home.html", map[string]interface{}{
		"Cards":    cards,
		"Default":  s.Lib.Default(),
		"Updates":  updates,
		"Download": s.Downloads.Status(),
		"Errors":   s.Lib.Errs,
		"Addrs":    s.Addrs,
		"LAN":      s.LAN,
	})
}

type resultView struct {
	Title string
	URL   string
	Via   string
}

func (s *Server) resultsView(b *library.Book, res []zim.Result) []resultView {
	out := make([]resultView, 0, len(res))
	for _, r := range res {
		out = append(out, resultView{Title: r.Title, URL: contentURL(b, r.Entry), Via: r.Via})
	}
	return out
}

func (s *Server) serveSearch(w http.ResponseWriter, r *http.Request) {
	b := s.pickBook(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if b == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if q == "" {
		http.Redirect(w, r, "/content/"+url.PathEscape(b.ID)+"/", http.StatusFound)
		return
	}
	// Like Wikipedia's "Go": an exact title match opens the article.
	if r.URL.Query().Get("list") == "" {
		if e, ok := b.Archive.FindTitle(q); ok {
			http.Redirect(w, r, contentURL(b, e), http.StatusFound)
			return
		}
	}
	res, err := b.Archive.SearchTitles(q, 50)
	data := map[string]interface{}{
		"Book":    b,
		"Query":   q,
		"Results": s.resultsView(b, res),
		"Online":  onlineLink(b, q),
	}
	if err != nil {
		data["Error"] = err.Error()
	}
	// Words in another order or a later word: try each word alone too.
	if len(res) == 0 {
		var more []zim.Result
		seen := map[uint32]bool{}
		for _, word := range strings.Fields(q) {
			if len([]rune(word)) < 3 {
				continue
			}
			rs, _ := b.Archive.SearchTitles(word, 10)
			for _, x := range rs {
				if !seen[x.Entry.Index] {
					seen[x.Entry.Index] = true
					more = append(more, x)
				}
			}
		}
		data["Related"] = s.resultsView(b, more)
	}
	s.render(w, r, "search.html", data)
}

func (s *Server) serveSuggest(w http.ResponseWriter, r *http.Request) {
	b := s.pickBook(r)
	type sugg struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Via   string `json:"via,omitempty"`
	}
	out := []sugg{}
	if b != nil {
		res, _ := b.Archive.SearchTitles(r.URL.Query().Get("q"), 10)
		for _, x := range res {
			out = append(out, sugg{Title: x.Title, URL: contentURL(b, x.Entry), Via: x.Via})
		}
	}
	writeJSON(w, out)
}

func (s *Server) serveRandom(w http.ResponseWriter, r *http.Request) {
	b := s.pickBook(r)
	if b == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	e, err := b.Archive.RandomArticle()
	if err != nil {
		s.brokenArchive(w, r, b, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, contentURL(b, e), http.StatusFound)
}

func (s *Server) serveIcon(w http.ResponseWriter, r *http.Request) {
	b := s.Lib.Get(strings.TrimPrefix(r.URL.Path, "/nabu/icon/"))
	if b == nil {
		http.Redirect(w, r, "/nabu/static/nabu.svg", http.StatusFound)
		return
	}
	data, err := b.Archive.MetadataBytes("Illustration_48x48@1")
	if err != nil {
		if e, err2 := b.Archive.Find('-', "favicon"); err2 == nil {
			data, err = b.Archive.ReadAll(e)
		}
	}
	if err != nil || len(data) == 0 {
		http.Redirect(w, r, "/nabu/static/nabu.svg", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

func (s *Server) availableUpdates() []update.Available {
	latest, _, _, _ := s.Checker.Snapshot()
	var inst []update.Installed
	for _, b := range s.Lib.Books() {
		inst = append(inst, update.Installed{ID: b.ID, Series: b.Series(), Edition: b.Edition()})
	}
	return update.UpdatesFor(inst, latest)
}

func (s *Server) serveUpdates(w http.ResponseWriter, r *http.Request) {
	latest, checkedAt, lastErr, checking := s.Checker.Snapshot()
	installed := map[string]string{} // series -> newest edition on drive
	var superseded []*library.Book
	for _, b := range s.Lib.Books() {
		if b.Edition() > installed[b.Series()] {
			installed[b.Series()] = b.Edition()
		}
	}
	for _, b := range s.Lib.Books() {
		if b.Edition() != "" && b.Edition() < installed[b.Series()] {
			superseded = append(superseded, b)
		}
	}
	langs := map[string]int{}
	for _, e := range latest {
		langs[e.Lang]++
	}
	var langList []string
	for l := range langs {
		langList = append(langList, l)
	}
	sort.Slice(langList, func(i, j int) bool {
		return strings.ToLower(langName(langList[i])) < strings.ToLower(langName(langList[j]))
	})
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = s.guessLang(r)
	}
	type row struct {
		update.Edition
		Desc      string
		Installed string // edition on drive, if any
		Newer     bool
	}
	var rows []row
	for _, e := range latest {
		if lang != "*" && e.Lang != lang {
			continue
		}
		rows = append(rows, row{Edition: e, Desc: e.Describe(), Installed: installed[e.Series()],
			Newer: installed[e.Series()] != "" && e.Date > installed[e.Series()]})
	}
	free, ferr := sysutil.FreeSpace(s.Lib.Dir)
	if ferr != nil {
		free = -1
	}
	s.render(w, r, "updates.html", map[string]interface{}{
		"Rows":       rows,
		"Langs":      langList,
		"Lang":       lang,
		"CheckedAt":  checkedAt,
		"LastErr":    lastErr,
		"Checking":   checking,
		"Offline":    s.Offline,
		"Updates":    s.availableUpdates(),
		"Download":   s.Downloads.Status(),
		"Paused":     s.Downloads.Paused(),
		"Superseded": superseded,
		"Free":       free,
		"Settings":   update.LoadSettings(s.Lib.Dir),
		"Catalog":    s.Checker.CatalogURL,
	})
}

// guessLang picks the catalogue language to show first: the language of
// the books on the drive, else the browser's.
func (s *Server) guessLang(r *http.Request) string {
	if b := s.Lib.Default(); b != nil {
		if l := wikiLang(b); l != "" {
			return l
		}
	}
	al := r.Header.Get("Accept-Language")
	if al != "" {
		first := strings.SplitN(strings.SplitN(al, ",", 2)[0], ";", 2)[0]
		first = strings.ToLower(strings.SplitN(strings.TrimSpace(first), "-", 2)[0])
		if first != "" {
			return first
		}
	}
	return "en"
}

func (s *Server) serveAbout(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "about.html", map[string]interface{}{
		"Dir":     s.Lib.Dir,
		"Uptime":  time.Since(s.started).Round(time.Second).String(),
		"Addrs":   s.Addrs,
		"LAN":     s.LAN,
		"Catalog": s.Checker.CatalogURL,
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return trimZero(float64(n)/1_000_000) + " million"
	case n >= 10_000:
		return itoa(n/1000) + ",000"
	}
	return itoa(n)
}

func trimZero(f float64) string {
	s := strings.TrimRight(strings.TrimRight(strconvF(f), "0"), ".")
	return s
}

func strconvF(f float64) string {
	b, _ := json.Marshal(float64(int64(f*10)) / 10)
	return string(b)
}

func humanMonth(date string) string {
	if len(date) < 7 {
		return date
	}
	t, err := time.Parse("2006-01", date[:7])
	if err != nil {
		return date
	}
	return t.Format("January 2006")
}

func humanSince(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int64(d/time.Minute)) + " min ago"
	case d < 48*time.Hour:
		return itoa(int64(d/time.Hour)) + " h ago"
	}
	return t.Format("2 Jan 2006")
}

func langName(code string) string {
	if n, ok := langNames[code]; ok {
		return n
	}
	return code
}
