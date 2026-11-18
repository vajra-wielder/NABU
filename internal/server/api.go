package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/update"
)

// serveAPI handles /nabu/api/*. Reads are open to everyone who can see
// the library; anything that changes the drive or stops NABU must come
// from this computer, carry NABU's own header (which a foreign web page
// cannot add without a CORS preflight NABU never approves) and, when the
// browser says where it comes from, come from NABU's own pages.
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/nabu/api/")
	if name == "ping" {
		writeJSON(w, map[string]string{"app": "nabu", "version": Version, "library": s.Lib.Dir})
		return
	}
	if name == "status" {
		writeJSON(w, s.status())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	if !isLocal(r) {
		http.Error(w, "only allowed from the computer running NABU", http.StatusForbidden)
		return
	}
	if r.Header.Get("X-Nabu") != "1" || !sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	var body struct {
		File         string `json:"file"`
		Book         string `json:"book"`
		CheckOnStart *bool  `json:"check_on_start"`
		AutoDownload *bool  `json:"auto_download"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	var err error
	switch name {
	case "check":
		if s.Offline {
			err = errors.New("NABU was started in offline mode")
			break
		}
		go s.Checker.Check(context.Background())
		time.Sleep(150 * time.Millisecond) // lets the page show "checking"
	case "download":
		ed, ok := s.Checker.Find(body.File)
		if !ok {
			err = errors.New("that edition is not in the catalogue; check for updates first")
			break
		}
		err = s.Downloads.Start(ed)
	case "resume":
		ed, ok := s.Checker.Find(body.File)
		if !ok {
			// The catalogue moved on (or we are offline): resume from the
			// URL saved when the download began.
			ed, ok = update.ParseName(body.File)
			ed.URL = savedURL(s.Lib.Dir, body.File)
			if !ok || ed.URL == "" {
				err = errors.New("cannot resume: this edition is no longer offered")
				break
			}
		}
		err = s.Downloads.Start(ed)
	case "cancel":
		s.Downloads.Cancel()
	case "discard":
		err = s.Downloads.Discard(body.File)
	case "remove":
		err = s.removeBook(body.Book)
	case "settings":
		s.settingsMu.Lock()
		st := update.LoadSettings(s.Lib.Dir)
		if body.CheckOnStart != nil {
			st.CheckOnStart = *body.CheckOnStart
		}
		if body.AutoDownload != nil {
			st.AutoDownload = *body.AutoDownload
		}
		err = update.SaveSettings(s.Lib.Dir, st)
		s.settingsMu.Unlock()
	case "rescan":
		err = s.Lib.Rescan()
	case "shutdown":
		writeJSON(w, map[string]bool{"ok": true})
		if s.Shutdown != nil {
			go func() {
				time.Sleep(300 * time.Millisecond)
				s.Shutdown()
			}()
		}
		return
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, s.status())
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// removeBook deletes an edition that a newer one of the same series has
// replaced. Only superseded editions can be removed from the interface,
// so a misclick can never leave the drive without its Wikipedia.
func (s *Server) removeBook(id string) error {
	b := s.Lib.Get(id)
	if b == nil {
		return errors.New("no such book")
	}
	newer := false
	for _, o := range s.Lib.Books() {
		if o.ID != b.ID && o.Series() == b.Series() && o.Edition() > b.Edition() {
			newer = true
		}
	}
	if !newer {
		return errors.New("only editions replaced by a newer one can be removed here")
	}
	files := append([]string(nil), b.Files...)
	b.Archive.Close()
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			s.Lib.Rescan()
			return err
		}
	}
	return s.Lib.Rescan()
}

func savedURL(dir, file string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".nabu", "incoming", strings.TrimSuffix(file, ".zim"), "meta.json"))
	if err != nil {
		return ""
	}
	var m struct {
		URL string `json:"url"`
	}
	json.Unmarshal(b, &m)
	return m.URL
}

type apiStatus struct {
	Download  update.Status `json:"download"`
	Updates   int           `json:"updates"`
	Checking  bool          `json:"checking"`
	CheckedAt time.Time     `json:"checked_at"`
	CheckErr  string        `json:"check_error,omitempty"`
	Books     int           `json:"books"`
}

func (s *Server) status() apiStatus {
	_, at, cerr, checking := s.Checker.Snapshot()
	return apiStatus{
		Download:  s.Downloads.Status(),
		Updates:   len(s.availableUpdates()),
		Checking:  checking,
		CheckedAt: at,
		CheckErr:  cerr,
		Books:     len(s.Lib.Books()),
	}
}
