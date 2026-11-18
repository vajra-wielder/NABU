package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/update"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	for _, n := range []string{"testwiki", "fawiki"} {
		for _, suf := range []string{".xml.bz2", "-index.txt.bz2"} {
			name := n + "-20261101-pages-articles-multistream" + suf
			b, err := os.ReadFile(filepath.Join("../../testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, name), b, 0o644)
		}
	}
	lib, err := library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lib.Close)
	deadline := time.Now().Add(10 * time.Second)
	for _, b := range lib.Books() {
		for b.Reader() == nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if b.Reader() == nil {
			t.Fatalf("%s never became ready: %+v", b.ID, b.Status())
		}
	}
	c := &update.Checker{Dir: dir, Mirror: "http://127.0.0.1:1", Client: http.DefaultClient}
	s, err := New(lib, c, update.NewManager(dir, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	s.Offline = true
	return s
}

func do(s *Server, method, path, remote string, hdr map[string]string) *http.Response {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Host = "localhost:8080"
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w.Result()
}

func get(s *Server, path string) (*http.Response, string) {
	r := do(s, "GET", path, "127.0.0.1:5000", nil)
	b, _ := io.ReadAll(r.Body)
	return r, string(b)
}

func TestPages(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		path string
		code int
		want string
	}{
		{"/", 200, "Your library"},
		{"/testwiki/wiki/Babylon", 200, `class="infobox"`},
		{"/testwiki/wiki/Babylon", 200, "Ancient city in Mesopotamia"},
		{"/testwiki/wiki/Babylon", 200, `<h2 id="History">`},
		{"/testwiki/wiki/Nineveh", 404, "is not in this Wikipedia"},
		{"/testwiki/source/Babylon", 200, "{{Infobox settlement"},
		{"/testwiki/search?list=1&q=tab", 200, "Tablet"},
		{"/testwiki/search?list=1&q=qqqq", 200, "No article title"},
		{"/suggest?book=testwiki&q=ecr", 200, `"title":"Écriture"`},
		{"/fawiki/wiki/" + "%D8%A8%D8%A7%D8%A8%D9%84", 200, `dir="rtl"`},
		{"/nabu/updates", 200, "Bahasa Indonesia"},
		{"/nabu/about", 200, "Why these ten languages"},
		{"/nabu/static/wiki.css", 200, "infobox"},
		{"/nowiki/wiki/X", 404, "nothing at this address"},
	}
	for _, c := range cases {
		r, b := get(s, c.path)
		if r.StatusCode != c.code || !strings.Contains(b, c.want) {
			t.Errorf("%s: %d (want %d) containing %q\n%.400s", c.path, r.StatusCode, c.code, c.want, b)
		}
	}
	_, b := get(s, "/testwiki/wiki/Babylon")
	if strings.Contains(b, "<script>alert") || strings.Contains(b, "javascript:x") {
		t.Error("unsafe markup reached the page")
	}
}

func TestRedirects(t *testing.T) {
	s := newTestServer(t)
	for path, want := range map[string]string{
		"/testwiki/":                    "/testwiki/wiki/Main_Page",
		"/testwiki/wiki/babylon":        "/testwiki/wiki/Babylon",
		"/testwiki/wiki/Babylonia":      "/testwiki/wiki/Babylon?from=Babylonia",
		"/testwiki/wiki/God_of_scribes": "/testwiki/wiki/Nabu?from=God+of+scribes#Worship",
		"/testwiki/search?q=ECRITURE":   "/testwiki/wiki/%C3%89criture",
		"/search?book=testwiki&q=nabu":  "/testwiki/wiki/Nabu",
	} {
		r, _ := get(s, path)
		if r.StatusCode != http.StatusFound || r.Header.Get("Location") != want {
			t.Errorf("%s: %d -> %q, want %q", path, r.StatusCode, r.Header.Get("Location"), want)
		}
	}
	r, _ := get(s, "/testwiki/random")
	if r.StatusCode != http.StatusFound || !strings.HasPrefix(r.Header.Get("Location"), "/testwiki/wiki/") {
		t.Errorf("random: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, b := get(s, "/testwiki/wiki/Babylonia?redirect=no"); r.StatusCode != 200 || !strings.Contains(b, "Babylonia") {
		t.Errorf("redirect=no: %d", r.StatusCode)
	}
}

func TestSecurity(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "evil.example:8080"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("DNS rebinding not blocked: %d", w.Code)
	}
	if !s.hostAllowed("penguin.linux.test:8080") || s.hostAllowed("penguin.linux.test.evil.example") {
		t.Error("ChromeOS container host handling is wrong")
	}
	nabu := map[string]string{"X-Nabu": "1"}
	if r := do(s, "POST", "/nabu/api/rescan", "127.0.0.1:1", nil); r.StatusCode != 403 {
		t.Errorf("missing header accepted: %d", r.StatusCode)
	}
	if r := do(s, "POST", "/nabu/api/rescan", "127.0.0.1:1", map[string]string{"X-Nabu": "1", "Origin": "http://evil.example"}); r.StatusCode != 403 {
		t.Errorf("cross-origin accepted: %d", r.StatusCode)
	}
	if r := do(s, "POST", "/nabu/api/rescan", "192.168.1.9:1", nabu); r.StatusCode != 403 {
		t.Errorf("other machine accepted: %d", r.StatusCode)
	}
	if r := do(s, "POST", "/nabu/api/rescan", "127.0.0.1:1", nabu); r.StatusCode != 200 {
		t.Errorf("legit request refused: %d", r.StatusCode)
	}
	req = httptest.NewRequest("POST", "/nabu/api/remove", strings.NewReader(`{"book":"testwiki"}`))
	req.Host, req.RemoteAddr = "localhost:8080", "127.0.0.1:1"
	req.Header.Set("X-Nabu", "1")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("deleting the only copy must be refused: %d", w.Code)
	}
}

func TestInternetExplorerStylesheets(t *testing.T) {
	s := newTestServer(t)
	for _, name := range []string{"theme.css", "nabu.css", "wiki.css"} {
		r := do(s, "GET", "/nabu/static/"+name, "127.0.0.1:1",
			map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 6.1; Trident/7.0; rv:11.0) like Gecko"})
		b, _ := io.ReadAll(r.Body)
		css := string(b)
		if r.StatusCode != 200 || strings.Contains(css, "var(--") || len(css) < 500 {
			t.Errorf("%s for IE11: %d, still has var(): %v", name, r.StatusCode, strings.Contains(css, "var(--"))
		}
		if strings.Contains(css, "inherit") && !strings.Contains(css, "#1d3a8a") && name != "theme.css" {
			t.Errorf("%s for IE11 lost its colours", name)
		}
	}
	_, css := get(s, "/nabu/static/wiki.css")
	if !strings.Contains(css, "var(--") {
		t.Error("modern browsers should get the original stylesheet")
	}
	if !strings.Contains(flattenCSS(":root{--a:#123;--b:var(--a);} x{color:var(--b)}"), "color:#123") {
		t.Error("nested variables not resolved")
	}
}

func TestReadingFeatures(t *testing.T) {
	s := newTestServer(t)
	nabu := map[string]string{"X-Nabu": "1", "Content-Type": "application/json"}
	post := func(path, remote, body string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Host, req.RemoteAddr = "localhost:8080", remote
		for k, v := range nabu {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		return w.Code
	}

	// Reading on this computer is remembered; reading over the network is not.
	get(s, "/testwiki/wiki/Babylon")
	do(s, "GET", "/testwiki/wiki/Nabu", "192.168.1.20:4000", nil)
	saved, history := s.reads.lists()
	if len(history) != 1 || history[0].Title != "Babylon" || !strings.Contains(history[0].Extract, "Babylon was a city") {
		t.Fatalf("history: %+v", history)
	}
	if len(saved) != 0 {
		t.Fatal("nothing should be saved yet")
	}

	// Save, show on the home page and Saved page, unsave.
	if c := post("/nabu/api/save", "127.0.0.1:1", `{"book":"testwiki","title":"Babylon","saved":true}`); c != 200 {
		t.Fatalf("save: %d", c)
	}
	if c := post("/nabu/api/save", "192.168.1.20:1", `{"book":"testwiki","title":"Nabu"}`); c != 403 {
		t.Fatalf("saving from the network must be refused: %d", c)
	}
	if !s.reads.isSaved("testwiki", "Babylon") {
		t.Fatal("not saved")
	}
	for _, path := range []string{"/", "/nabu/saved"} {
		if _, b := get(s, path); !strings.Contains(b, "Saved for later") || !strings.Contains(b, "Babylon was a city") {
			t.Errorf("%s does not list the saved article", path)
		}
	}
	if _, b := get(s, "/testwiki/wiki/Babylon"); !strings.Contains(b, `aria-pressed="true"`) {
		t.Error("article page should show it as saved")
	}
	post("/nabu/api/unsave", "127.0.0.1:1", `{"book":"testwiki","title":"Babylon"}`)
	post("/nabu/api/clear-history", "127.0.0.1:1", `{}`)
	saved, history = s.reads.lists()
	if len(saved)+len(history) != 0 {
		t.Fatalf("unsave/clear failed: %v %v", saved, history)
	}
	// History can be switched off.
	post("/nabu/api/settings", "127.0.0.1:1", `{"history":false}`)
	get(s, "/testwiki/wiki/Nabu")
	if _, history = s.reads.lists(); len(history) != 0 {
		t.Fatal("history recorded while switched off")
	}

	// Link previews follow redirects and return the opening text.
	r, b := get(s, "/nabu/api/preview?book=testwiki&title=Babylonia")
	if r.StatusCode != 200 || !strings.Contains(b, `"title":"Babylon"`) || !strings.Contains(b, "city of") {
		t.Fatalf("preview: %d %s", r.StatusCode, b)
	}
	if r, _ := get(s, "/nabu/api/preview?book=testwiki&title=Nowhere"); r.StatusCode != 404 {
		t.Fatalf("preview of a missing page: %d", r.StatusCode)
	}

	// Pages carry the shared bar, settings panel and contents sidebar.
	_, b = get(s, "/testwiki/wiki/Babylon")
	for _, want := range []string{`id="nabu-bar"`, `id="prefs"`, `class="side-toc"`, `href="#History"`, "reader.js", "manifest.webmanifest"} {
		if !strings.Contains(b, want) {
			t.Errorf("article page lacks %s", want)
		}
	}
	if r, _ := get(s, "/nabu/static/manifest.webmanifest"); r.StatusCode != 200 {
		t.Error("manifest missing")
	}
}
