package server

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/update"
)

// imageURL is how articles ask NABU for a picture: NABU answers from the
// drive, or (online) from Wikimedia, so pictures work the same offline,
// for every device reading over the network, and in saved copies.
func imageURL(db, file string) string {
	return "/nabu/img/" + db + "/" + url.PathEscape(pictures.Normalize(file)) + "?w=" + strconv.Itoa(pictures.Width)
}

// imagesFor reports whether articles of b should show pictures at all:
// when NABU may go online for them, or when the drive has some.
func (s *Server) imagesFor(b *library.Book, st update.Settings) bool {
	if st.Pictures && !s.Offline {
		return true
	}
	if ps := s.Lib.Pictures(); ps != nil {
		for _, name := range []string{b.Files.DB, pictures.Commons} {
			if store, _ := ps.Store(name, false); store != nil {
				return true
			}
		}
	}
	return false
}

// servePicture answers GET /nabu/img/<wiki>/<file name>.
func (s *Server) servePicture(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/nabu/img/")
	i := strings.IndexByte(rest, '/')
	ps := s.Lib.Pictures()
	if i <= 0 || ps == nil {
		http.NotFound(w, r)
		return
	}
	db, name := rest[:i], rest[i+1:]
	var host string
	for _, b := range s.Lib.Books() {
		if b.Files.DB == db {
			if site := b.Site(); site != nil {
				host = site.Host()
				break
			}
		}
	}
	if host == "" || name == "" || strings.ContainsAny(name, "/\\") {
		http.NotFound(w, r)
		return
	}
	ps.Viewing()
	st := s.settings()
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	// A language showing English's pictures keeps none of its own unless
	// asked to: seen online, they are shown but not kept.
	keep := st.KeepPictures && !s.Lib.UsesEnglishPictures(db)
	data, err := ps.Get(ctx, db, host, name, st.Pictures && !s.Offline, keep)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// picLine describes a wiki's offline pictures in a sentence.
func picLine(p library.PicStatus) string {
	kept := func() string {
		n, b := p.Pictures+p.Shared, p.Bytes+p.SBytes
		s := count64(int64(n)) + " pictures, " + update.HumanBytes(b)
		if p.Shared > 0 {
			s += " (shared by all languages, plus this one's own)"
		}
		if p.Limit > 0 {
			s += " · all pictures: " + decimalGB(p.Used) + " of " + decimalGB(p.Limit) + " set aside"
		}
		return s
	}
	what := "the main picture of each article"
	if p.Mode == pictures.All {
		what = "every picture"
	}
	pct := ""
	if p.Total > 0 {
		pct = strconv.FormatFloat(100*float64(p.Done)/float64(p.Total), 'f', 1, 64) + "%"
	}
	switch p.State {
	case library.PicQueued:
		return "Waiting to keep pictures for reading offline…"
	case library.PicRunning:
		s := "Keeping " + what + " for reading offline: "
		if pct != "" {
			s += pct + " · "
		}
		s += kept()
		if p.ETA > 0 {
			s += " · about " + humanDur(p.ETA) + " left"
		}
		if p.Waiting != "" {
			s += " · " + p.Waiting
		}
		return s
	case library.PicPaused:
		return "Pictures for reading offline: " + kept() + " · paused at " + pct
	case library.PicDone:
		return "Pictures for reading offline ✓ " + kept() + " (" + what + ")"
	case library.PicFailed:
		return "Keeping pictures stopped: " + p.Err
	case library.PicFull:
		return "Pictures filled the room set aside for them: " + kept() +
			". The longest articles have theirs; raise the limit in Updates & settings to keep more."
	}
	if p.English {
		s := "Shows the pictures kept for English Wikipedia, most of which are the same files in every language"
		if p.Shared > 0 {
			s += " (" + count64(int64(p.Shared)) + " on the drive)"
		}
		return s + ". Pictures only this language has show when the computer is online."
	}
	if p.Pictures+p.Shared > 0 {
		return "Pictures on the drive: " + kept()
	}
	return "Pictures show when the computer is online."
}

// decimalGB writes sizes the way the picture limit is set: in GB of
// 1,000,000,000 bytes, as drives are sold.
func decimalGB(n int64) string {
	if n < 1e9 {
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + " MB"
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + " GB"
}

func count64(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func humanDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "a minute"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes()+0.5)) + " minutes"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours()+0.5)) + " hours"
	}
	return strconv.Itoa(int(d.Hours()/24+0.5)) + " days"
}

// englishPicturesKept reports whether English Wikipedia's pictures (most
// of which other languages use too) are on the drive.
func (s *Server) englishPicturesKept() bool {
	ps := s.Lib.Pictures()
	if ps == nil {
		return false
	}
	// Once English keeps its pictures, the others need only theirs.
	_, err := pictures.LoadJob(filepath.Join(ps.Dir, library.EnglishDB))
	return err == nil
}
