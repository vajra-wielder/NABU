package server

import (
	"encoding/json"
	"github.com/vajra-wielder/nabu/internal/drivefile"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// readItem is one saved or recently read article.
type readItem struct {
	Book    string    `json:"book"`
	Title   string    `json:"title"`
	Extract string    `json:"extract,omitempty"`
	At      time.Time `json:"at"`
}

// readerStore keeps saved articles and reading history in the library
// folder, so they travel with the drive. Only reading done on the
// computer running NABU is recorded, never that of devices on the
// network.
type readerStore struct {
	path    string
	mu      sync.Mutex
	Saved   []readItem `json:"saved"`
	History []readItem `json:"history"`
}

const maxHistory = 300

func openReaderStore(dir string) *readerStore {
	st := &readerStore{path: filepath.Join(dir, "reader.json")}
	if b, err := os.ReadFile(st.path); err == nil {
		json.Unmarshal(b, st)
	}
	return st
}

// save writes atomically: a pulled-out drive never leaves half a file.
func (st *readerStore) save() {
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(st.path), 0o755)
	drivefile.WriteFile(st.path, b)
}

func without(list []readItem, book, title string) []readItem {
	out := list[:0:0]
	for _, it := range list {
		if it.Book != book || it.Title != title {
			out = append(out, it)
		}
	}
	return out
}

func (st *readerStore) isSaved(book, title string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, it := range st.Saved {
		if it.Book == book && it.Title == title {
			return true
		}
	}
	return false
}

func (st *readerStore) setSaved(book, title, extract string, on bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Saved = without(st.Saved, book, title)
	if on {
		st.Saved = append([]readItem{{Book: book, Title: title, Extract: extract, At: time.Now()}}, st.Saved...)
	}
	st.save()
}

func (st *readerStore) visit(book, title, extract string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.History = append([]readItem{{Book: book, Title: title, Extract: extract, At: time.Now()}}, without(st.History, book, title)...)
	if len(st.History) > maxHistory {
		st.History = st.History[:maxHistory]
	}
	st.save()
}

func (st *readerStore) forget(book, title string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.History = without(st.History, book, title)
	st.save()
}

func (st *readerStore) clearHistory() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.History = nil
	st.save()
}

func (st *readerStore) lists() (saved, history []readItem) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]readItem(nil), st.Saved...), append([]readItem(nil), st.History...)
}

var (
	figureRe = regexp.MustCompile(`(?s)<figure.*?</figure>|<span class="pic-ph"[^>]*>[^<]*</span>`)
	paraRe   = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	supRe    = regexp.MustCompile(`(?s)<sup class="reference".*?</sup>`)
	imgRe    = regexp.MustCompile(`<img src="([^"]+)"`)
	spaceRe  = regexp.MustCompile(`\s+`)
)

// extract returns the opening of a rendered article as plain text, cut
// at a word boundary.
func extract(htmlBody string, max int) string {
	htmlBody = figureRe.ReplaceAllString(htmlBody, "")
	for _, m := range paraRe.FindAllStringSubmatch(htmlBody, 12) {
		t := supRe.ReplaceAllString(m[1], "")
		t = html.UnescapeString(tagRe.ReplaceAllString(t, ""))
		t = strings.TrimSpace(spaceRe.ReplaceAllString(t, " "))
		if utf8.RuneCountInString(t) < 40 {
			continue
		}
		if utf8.RuneCountInString(t) > max {
			r := []rune(t)[:max]
			cut := string(r)
			if i := strings.LastIndexByte(cut, ' '); i > max/2 {
				cut = cut[:i]
			}
			t = strings.TrimRight(cut, ",;:") + "…"
		}
		return t
	}
	return ""
}

// firstImage returns the first picture URL of a rendered article.
func firstImage(htmlBody string) string {
	if m := imgRe.FindStringSubmatch(htmlBody); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}
