// Package server is NABU's local web server: it serves articles straight
// out of ZIM files, adds a small search bar to every page, and hosts the
// library, search and update pages.
package server

import (
	"context"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/library"
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
	Offline   bool     // never touch the network
	LAN       bool     // listening on all interfaces
	Addrs     []string // URLs where NABU can be reached, for the UI
	Shutdown  func()   // stops the program

	settingsMu sync.Mutex
	tmpl       *template.Template
	static     http.Handler
	started    time.Time
}

// New builds a Server.
func New(lib *library.Library, checker *update.Checker, dl *update.Manager) (*Server, error) {
	s := &Server{Lib: lib, Checker: checker, Downloads: dl, started: time.Now()}
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

// ServeHTTP dispatches requests. A hand-written router is used instead of
// http.ServeMux because the mux "cleans" paths, and real article names
// contain things like "//" and "/./" that must reach the archive intact.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r.Host) {
		// Blocks DNS rebinding: a web page on some evil.example domain
		// resolving to 127.0.0.1 cannot talk to NABU.
		http.Error(w, "NABU only answers to localhost and IP addresses", http.StatusMisdirectedRequest)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/content/"):
		s.serveContent(w, r)
	case p == "/":
		s.serveHome(w, r)
	case p == "/search":
		s.serveSearch(w, r)
	case p == "/suggest":
		s.serveSuggest(w, r)
	case p == "/random":
		s.serveRandom(w, r)
	case p == "/nabu/updates":
		s.serveUpdates(w, r)
	case p == "/nabu/about":
		s.serveAbout(w, r)
	case strings.HasPrefix(p, "/nabu/icon/"):
		s.serveIcon(w, r)
	case strings.HasPrefix(p, "/nabu/static/"):
		w.Header().Set("Cache-Control", "public, max-age=86400")
		s.static.ServeHTTP(w, r)
	case p == "/favicon.ico":
		http.Redirect(w, r, "/nabu/static/nabu.svg", http.StatusMovedPermanently)
	case strings.HasPrefix(p, "/nabu/api/"):
		s.serveAPI(w, r)
	default:
		s.notFound(w, r, "", "")
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
	// Bare machine names (no dots) for LAN sharing: http://my-laptop:8080
	return s.LAN && h != "" && !strings.Contains(h, ".")
}

// isLocal reports whether the request comes from this computer. Actions
// that change the drive (downloads, deletions, shutdown) are only
// accepted from here, even when sharing on the network.
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
	err := srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
