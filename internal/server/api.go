package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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
	switch name {
	case "ping":
		writeJSON(w, map[string]string{"app": "nabu", "version": Version, "library": s.Lib.Dir})
		return
	case "status":
		writeJSON(w, s.status())
		return
	case "preview":
		s.servePreview(w, r)
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
		Wiki         string `json:"wiki"`
		Book         string `json:"book"`
		Key          string `json:"key"`
		Title        string `json:"title"`
		Saved        *bool  `json:"saved"`
		History      *bool  `json:"history"`
		CheckOnStart *bool  `json:"check_on_start"`
		AutoDownload *bool  `json:"auto_download"`
		Pictures     *bool  `json:"pictures"`
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
		wikis := []string{body.Wiki}
		if body.Wiki == "" {
			wikis = s.wikisToCheck()
		}
		for _, wk := range wikis {
			go s.Checker.Check(context.Background(), wk)
		}
		time.Sleep(150 * time.Millisecond)
	case "download":
		c := s.Checker.Get(body.Wiki)
		if c == nil || c.Release == nil {
			err = errors.New("check for the newest dump first")
			break
		}
		err = s.Downloads.Start(c.Release)
	case "resume":
		for _, rel := range s.Downloads.Paused() {
			if rel.Key() == body.Key {
				rel := rel
				err = s.Downloads.Start(&rel)
				break
			}
		}
	case "cancel":
		s.Downloads.Cancel()
	case "discard":
		err = s.Downloads.Discard(body.Key)
	case "remove":
		err = s.removeOld(body.Book)
	case "retry":
		s.Lib.Retry(body.Book)
	case "settings":
		s.settingsMu.Lock()
		st := update.LoadSettings(s.Lib.Dir)
		if body.CheckOnStart != nil {
			st.CheckOnStart = *body.CheckOnStart
		}
		if body.AutoDownload != nil {
			st.AutoDownload = *body.AutoDownload
		}
		if body.Pictures != nil {
			st.Pictures = *body.Pictures
		}
		if body.History != nil {
			st.History = *body.History
		}
		err = update.SaveSettings(s.Lib.Dir, st)
		s.settingsMu.Unlock()
	case "rescan":
		err = s.Lib.Rescan()
	case "save":
		b := s.Lib.Get(body.Book)
		if b == nil || b.Reader() == nil || body.Title == "" {
			err = errors.New("no such article")
			break
		}
		on := body.Saved == nil || *body.Saved
		ex := ""
		if on {
			if page, e := s.loadFollow(b, body.Title); e == nil {
				ex = extract(s.renderPage(b, page).HTML, 220)
			}
		}
		s.reads.setSaved(b.ID, body.Title, ex, on)
	case "unsave":
		s.reads.setSaved(body.Book, body.Title, "", false)
	case "forget":
		s.reads.forget(body.Book, body.Title)
	case "clear-history":
		s.reads.clearHistory()
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

// wikisToCheck: the wikis on the drive plus the offered languages.
func (s *Server) wikisToCheck() []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range s.Lib.Books() {
		if !seen[b.Files.DB] {
			seen[b.Files.DB] = true
			out = append(out, b.Files.DB)
		}
	}
	for _, l := range update.Languages {
		if !seen[l.Wiki] {
			seen[l.Wiki] = true
			out = append(out, l.Wiki)
		}
	}
	return out
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// removeOld deletes an edition only when a newer edition of the same
// wiki is on the drive, so a misclick never leaves the drive empty.
func (s *Server) removeOld(id string) error {
	b := s.Lib.Get(id)
	if b == nil {
		return errors.New("no such book")
	}
	for _, o := range s.Lib.Books() {
		if o != b && o.Files.DB == b.Files.DB && o.Files.Date > b.Files.Date {
			return s.Lib.Remove(id)
		}
	}
	return errors.New("only editions replaced by a newer one can be removed here")
}

type bookStatus struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Err   string `json:"error,omitempty"`
}

type apiStatus struct {
	Download update.Status `json:"download"`
	Books    []bookStatus  `json:"books"`
	Checking bool          `json:"checking"`
}

func (s *Server) status() apiStatus {
	st := apiStatus{Download: s.Downloads.Status()}
	for _, b := range s.Lib.Books() {
		x := b.Status()
		st.Books = append(st.Books, bookStatus{ID: b.ID, State: x.State, Done: x.Done, Total: x.Total, Err: x.Err})
	}
	for _, wk := range s.wikisToCheck() {
		if s.Checker.Checking(wk) {
			st.Checking = true
		}
	}
	return st
}
