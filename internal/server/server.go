// Package server is NABU's local web server: it renders articles from
// the dumps on the drive and hosts the library, search and update pages.
package server

import (
	"context"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/collect"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/share"
	"github.com/vajra-wielder/nabu/internal/update"
	"github.com/vajra-wielder/nabu/internal/web"
)

// Version is set at build time.
var Version = "dev"

// Server holds everything the handlers need.
type Server struct {
	Lib       *library.Library
	Checker   *update.Checker
	Downloads *update.Manager
	Offline   bool
	LAN       bool
	Addrs     []string
	Shutdown  func()
	// DriveGone reports that the NABU drive was pulled out (or lost its
	// connection). Pages then ask for it back and reload by themselves.
	DriveGone func() bool

	settingsMu  sync.Mutex
	reads       *readerStore
	collections *collect.Store
	copier      share.Copier
	check       driveCheck
	essentials  essentialsJob
	previews    *previewCache
	discoverMu  sync.Mutex
	discover    map[string]discoverSet
	tmpl        *template.Template
	static      http.Handler
	started     time.Time
}

// New builds a Server.
func New(lib *library.Library, checker *update.Checker, dl *update.Manager) (*Server, error) {
	s := &Server{Lib: lib, Checker: checker, Downloads: dl, started: time.Now(),
		reads: openReaderStore(lib.Drive.Personal), previews: newPreviewCache(256), discover: map[string]discoverSet{},
		collections: collect.New(lib.Drive.Collections)}
	t, err := template.New("").Funcs(funcs).ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.tmpl = t
	sub, err := fs.Sub(web.Assets, "assets")
	if err != nil {
		return nil, err
	}
	s.static = http.StripPrefix("/nabu/static/", http.FileServer(http.FS(sub)))
	return s, nil
}

var bookPathRe = regexp.MustCompile(`^/([a-z0-9_]+(?:-\d{8})?)(/.*)?$`)

// ServeHTTP dispatches requests. A hand-written router is used because
// http.ServeMux "cleans" paths, and article titles can contain "//" or
// "/./" that must arrive intact.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r.Host) {
		// Blocks DNS rebinding: a page on some evil.example domain that
		// resolves to 127.0.0.1 cannot talk to NABU.
		http.Error(w, "NABU only answers to localhost and IP addresses", http.StatusMisdirectedRequest)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	p := r.URL.Path
	if s.DriveGone != nil && !strings.HasPrefix(p, "/nabu/static/") && p != "/nabu/api/ping" && s.DriveGone() {
		s.driveGone(w, r)
		return
	}
	switch {
	case p == "/":
		s.serveHome(w, r)
	case p == "/search":
		s.serveSearch(w, r, s.pickBook(r))
	case p == "/suggest":
		s.serveSuggest(w, r)
	case p == "/nabu/updates":
		s.serveUpdates(w, r)
	case p == "/nabu/copy":
		s.serveCopy(w, r)
	case p == "/nabu/check":
		s.serveCheck(w, r)
	case p == "/nabu/about":
		s.serveAbout(w, r)
	case p == "/nabu/saved":
		s.serveSaved(w, r)
	case p == "/nabu/collections" || strings.HasPrefix(p, "/nabu/collections/"):
		s.serveCollections(w, r)
	case strings.HasPrefix(p, "/nabu/img/"):
		s.servePicture(w, r)
	case strings.HasPrefix(p, "/nabu/static/"):
		w.Header().Set("Cache-Control", "public, max-age=86400")
		if s.serveLegacyCSS(w, r) {
			return
		}
		w.Header().Set("Vary", "User-Agent")
		s.static.ServeHTTP(w, r)
	case strings.HasPrefix(p, "/nabu/api/"):
		s.serveAPI(w, r)
	case p == "/favicon.ico":
		http.Redirect(w, r, "/nabu/static/nabu.svg", http.StatusMovedPermanently)
	default:
		if m := bookPathRe.FindStringSubmatch(p); m != nil {
			if b := s.Lib.Get(m[1]); b != nil {
				s.serveBook(w, r, b, m[2])
				return
			}
		}
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is nothing at this address.")
	}
}

func (s *Server) serveBook(w http.ResponseWriter, r *http.Request, b *library.Book, rest string) {
	if b.Reader() == nil {
		s.preparing(w, r, b)
		return
	}
	switch {
	case rest == "" || rest == "/":
		http.Redirect(w, r, articleURL(b, b.Site().MainPage()), http.StatusFound)
	case strings.HasPrefix(rest, "/wiki/"):
		s.serveArticle(w, r, b, rest[len("/wiki/"):], false)
	case strings.HasPrefix(rest, "/source/"):
		s.serveArticle(w, r, b, rest[len("/source/"):], true)
	case rest == "/search":
		s.serveSearch(w, r, b)
	case rest == "/random":
		s.serveRandom(w, r, b)
	default:
		s.errorPage(w, r, http.StatusNotFound, "Not found", "There is nothing at this address.")
	}
}

func (s *Server) hostAllowed(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" || net.ParseIP(h) != nil {
		return true
	}
	// ChromeOS shows its Linux container to the browser under this
	// fixed internal name; it cannot be pointed elsewhere by a website.
	if h == "penguin.linux.test" {
		return true
	}
	// Bare machine names (no dots) for LAN sharing: http://my-laptop:8080
	return s.LAN && h != "" && !strings.Contains(h, ".")
}

// isLocal reports whether the request comes from this computer. Actions
// that change the drive are only accepted from here.
func isLocal(r *http.Request) bool {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Run serves on ln until ctx is cancelled.
func (s *Server) Run(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(discard{}, "", 0),
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// driveGone answers while the drive is out: the page reloads itself
// every few seconds, so it shows what was asked for as soon as the drive
// is back.
func (s *Server) driveGone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "3")
	if strings.HasPrefix(r.URL.Path, "/nabu/api/") || strings.HasPrefix(r.URL.Path, "/nabu/img/") {
		http.Error(w, "the NABU drive is not connected", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Refresh", "3")
	s.errorPage(w, r, http.StatusServiceUnavailable, "The NABU drive is not connected",
		"Plug the drive back in (any USB port will do). This page opens by itself once it is back. Pages already open keep working.")
}
