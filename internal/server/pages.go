package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/update"
)

var funcs = template.FuncMap{
	"bytes": update.HumanBytes,
	"hours": func(h float64) string { return humanDur(time.Duration(h * float64(time.Hour))) },
	"count": humanCount,
	"date":  dump.HumanDate,
	"since": humanSince,
	"pct": func(done, total int64) string {
		if total <= 0 {
			return "0"
		}
		p := 100 * float64(done) / float64(total)
		if p > 100 {
			p = 100
		}
		return strconv.FormatFloat(p, 'f', 1, 64)
	},
	"wikiname": wikiName,
	"dict": func(kv ...interface{}) map[string]interface{} {
		m := map[string]interface{}{}
		for i := 0; i+1 < len(kv); i += 2 {
			if k, ok := kv[i].(string); ok {
				m[k] = kv[i+1]
			}
		}
		return m
	},
	"seconds": func(d time.Duration) int64 { return int64(d / time.Second) },
	"int64":   func(n int) int64 { return int64(n) },
	"dur": func(d time.Duration) string {
		switch {
		case d < 2*time.Minute:
			return itoa(int64(d/time.Second)) + " s"
		case d < 2*time.Hour:
			return itoa(int64(d/time.Minute)) + " min"
		}
		return strconv.FormatFloat(d.Hours(), 'f', 1, 64) + " h"
	},
}

// wikiName names a wiki for people: "Русский · Russian Wikipedia".
func wikiName(db string) string {
	if l, ok := update.LanguageByWiki(db); ok {
		if l.Name == l.En {
			return l.Name + " Wikipedia"
		}
		return l.Name + " · " + l.En + " Wikipedia"
	}
	if db == "simplewiki" {
		return "Simple English Wikipedia"
	}
	if strings.HasSuffix(db, "wiki") {
		code := strings.ReplaceAll(strings.TrimSuffix(db, "wiki"), "_", "-")
		if code == "be-x-old" {
			code = "be-tarask"
		}
		if n, ok := langNames[code]; ok {
			native, english := n, n
			if i := strings.Index(n, " ("); i > 0 && strings.HasSuffix(n, ")") {
				native, english = n[:i], n[i+2:len(n)-1]
			}
			if native == english {
				return native + " Wikipedia"
			}
			return native + " · " + english + " Wikipedia"
		}
	}
	return db
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = map[string]interface{}{}
	}
	data["Version"] = Version
	data["Local"] = isLocal(r)
	data["Books"] = s.Lib.Books()
	if _, ok := data["Default"]; !ok {
		if b, ok := data["Book"].(*library.Book); ok && b.Reader() != nil {
			data["Default"] = b
		} else {
			data["Default"] = s.Lib.Default()
		}
	}
	if _, ok := data["Nav"]; !ok {
		data["Nav"] = map[string]string{"home.html": "home", "updates.html": "updates", "about.html": "about"}[name]
	}
	if _, ok := data["PageTitle"]; !ok {
		data["PageTitle"] = map[string]string{"updates.html": "Get & update", "about.html": "About"}[name]
	}
	if _, ok := data["BarBook"]; !ok {
		if b, ok := data["Book"].(*library.Book); ok && b.Reader() != nil {
			data["BarBook"] = b
		} else if d, ok := data["Default"].(*library.Book); ok && d != nil {
			data["BarBook"] = d
		} else {
			data["BarBook"] = (*library.Book)(nil)
		}
	}
	saved, _ := s.reads.lists()
	data["SavedCount"] = len(saved)
	ups := 0
	for _, v := range s.bookViews() {
		if v.Newer != nil {
			ups++
		}
	}
	data["UpdateCount"] = ups
	if _, ok := data["Lang"]; !ok {
		data["Lang"] = "en"
		data["Dir"] = "ltr"
	}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Write(buf.Bytes())
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, code int, heading, msg string) {
	w.WriteHeader(code)
	s.render(w, r, "error.html", map[string]interface{}{"Heading": heading, "Message": msg, "PageTitle": heading})
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

type bookView struct {
	*library.Book
	Status  library.Status
	Name    string
	Site    *dump.Siteinfo
	Newer   *update.Release
	Older   bool // a newer edition of the same wiki is on the drive
	MainURL string
	Pics    library.PicStatus
	PicLine string
	Lead    pictures.Size // estimates for keeping pictures
	All     pictures.Size
}

func (s *Server) bookViews() []bookView {
	newest := map[string]string{}
	for _, b := range s.Lib.Books() {
		if b.Files.Date > newest[b.Files.DB] {
			newest[b.Files.DB] = b.Files.Date
		}
	}
	var out []bookView
	for _, b := range s.Lib.Books() {
		v := bookView{Book: b, Status: b.Status(), Name: wikiName(b.Files.DB), Site: b.Site(),
			Older: b.Files.Date < newest[b.Files.DB]}
		if v.Site != nil {
			v.MainURL = articleURL(b, v.Site.MainPage())
			if v.Name == b.Files.DB && v.Site.Sitename != "" {
				v.Name = v.Site.Sitename + " (" + b.Files.DB + ")"
			}
		}
		if !v.Older && s.Lib.Pictures() != nil {
			v.Pics = s.Lib.PictureStatus(b.Files.DB)
			v.PicLine = picLine(v.Pics)
			v.Lead, v.All = pictures.Estimate(b.Files.DB, v.Status.Articles)
		}
		if c := s.Checker.Get(b.Files.DB); c != nil && c.Release != nil && !v.Older &&
			c.Release.Date > b.Files.Date && b.Files.Date != "latest" {
			v.Newer = c.Release
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) serveHome(w http.ResponseWriter, r *http.Request) {
	saved, history := s.reads.lists()
	s.render(w, r, "home.html", map[string]interface{}{
		"Recent":   s.itemViews(history, 6),
		"SavedTop": s.itemViews(saved, 6),
		"Discover": s.discoverFor(s.Lib.Default()),
		"Cards":    s.bookViews(),
		"Download": s.Downloads.Status(),
		"Notes":    s.Lib.Notes,
		"Addrs":    s.Addrs,
		"LAN":      s.LAN,
		"Dir":      s.Lib.Dir,
		"Root":     s.Lib.Drive.Root,
	})
}

type langView struct {
	update.Language
	Checked   *update.Checked
	Checking  bool
	Installed string // newest date on the drive
	Newer     bool
	TooBig    bool
}

func (s *Server) serveUpdates(w http.ResponseWriter, r *http.Request) {
	installed := map[string]string{}
	for _, b := range s.Lib.Books() {
		if b.Files.Date > installed[b.Files.DB] {
			installed[b.Files.DB] = b.Files.Date
		}
	}
	free, ferr := sysutil.FreeSpace(s.Lib.Dir)
	if ferr != nil {
		free = -1
	}
	var langs []langView
	for _, l := range update.Languages {
		v := langView{Language: l, Checked: s.Checker.Get(l.Wiki), Checking: s.Checker.Checking(l.Wiki), Installed: installed[l.Wiki]}
		if v.Checked != nil && v.Checked.Release != nil {
			v.Newer = v.Installed != "" && v.Checked.Release.Date > v.Installed
			v.TooBig = free >= 0 && v.Checked.Release.Size() > free
		}
		langs = append(langs, v)
	}
	// Installed wikis outside the list still get update checks.
	var others []langView
	for db, date := range installed {
		if _, ok := update.LanguageByWiki(db); ok {
			continue
		}
		v := langView{Language: update.Language{Wiki: db, Name: db, En: db}, Checked: s.Checker.Get(db), Installed: date}
		if v.Checked != nil && v.Checked.Release != nil {
			v.Newer = v.Checked.Release.Date > date
		}
		others = append(others, v)
	}
	s.render(w, r, "updates.html", map[string]interface{}{
		"Langs":    langs,
		"Others":   others,
		"Offline":  s.Offline,
		"Download": s.Downloads.Status(),
		"Paused":   s.Downloads.Paused(),
		"Cards":    s.bookViews(),
		"Free":     free,
		"Settings": s.settings(),
		"Mirror":   s.Checker.Mirror,
	})
}

func (s *Server) serveAbout(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "about.html", map[string]interface{}{
		"Dir":    s.Lib.Dir,
		"Root":   s.Lib.Drive.Root,
		"Uptime": time.Since(s.started).Round(time.Second).String(),
		"Addrs":  s.Addrs,
		"LAN":    s.LAN,
		"Mirror": s.Checker.Mirror,
		"Langs":  update.Languages,
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + " million"
	case n >= 10_000:
		return itoa(n/1000) + ",000"
	}
	return itoa(n)
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
