package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/update"
)

const book = "wikipedia_en_test_maxi_2026-10"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + book + ".zim")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, book+".zim"), data, 0o644)
	lib, err := library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lib.Close)
	c := &update.Checker{Dir: dir, CatalogURL: "http://127.0.0.1:1/", Client: http.DefaultClient}
	s, err := New(lib, c, update.NewManager(dir, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, s *Server, path string, hdr ...string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "localhost:8080"
	req.RemoteAddr = "127.0.0.1:5000"
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w.Result()
}

func body(r *http.Response) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func TestPages(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		path, want string
		code       int
	}{
		{"/", "Your library", 200},
		{"/nabu/updates", "Catalogue", 200},
		{"/nabu/about", "god of scribes", 200},
		{"/content/" + book + "/Nabu", `id="nabu-bar"`, 200},
		{"/content/" + book + "/_assets_/style.css", "Linux Libertine", 200},
		{"/content/" + book + "/Nineveh_Palace", "is not in this edition", 404},
		{"/search?list=1&q=bab", "Babylon", 200},
		{"/search?list=1&q=qqqq", "No article title", 200},
		{"/suggest?q=cune", `"title":"Cuneiform"`, 200},
		{"/content/nope/Nabu", "not on this drive", 404},
		{"/nabu/static/fonts.css", "Libertinus", 200},
		{"/nabu/api/ping", `"app":"nabu"`, 200},
	}
	for _, c := range cases {
		r := get(t, s, c.path)
		b := body(r)
		if r.StatusCode != c.code || !strings.Contains(b, c.want) {
			t.Errorf("%s: %d, want %d containing %q\n%.300s", c.path, r.StatusCode, c.code, c.want, b)
		}
	}
}

func TestRedirects(t *testing.T) {
	s := newTestServer(t)
	for path, want := range map[string]string{
		"/content/" + book + "/":          "/content/" + book + "/Main_Page",
		"/content/" + book + "/Babylonia": "/content/" + book + "/Babylon",
		"/search?q=god+of+scribes":        "/content/" + book + "/Nabu",
		"/search?q=%C3%A9criture":         "/content/" + book + "/%C3%89criture",
		"/content/" + book + "/A/Nabu":    "",
	} {
		r := get(t, s, path)
		if want == "" {
			if r.StatusCode != 200 {
				t.Errorf("%s: %d", path, r.StatusCode)
			}
			continue
		}
		if r.StatusCode != http.StatusFound || r.Header.Get("Location") != want {
			t.Errorf("%s: %d -> %q, want %q", path, r.StatusCode, r.Header.Get("Location"), want)
		}
	}
	r := get(t, s, "/random")
	if r.StatusCode != http.StatusFound || !strings.HasPrefix(r.Header.Get("Location"), "/content/"+book+"/") {
		t.Errorf("random: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
}

func TestCachingAndRanges(t *testing.T) {
	s := newTestServer(t)
	r := get(t, s, "/content/"+book+"/_assets_/tablet.png")
	etag := r.Header.Get("ETag")
	if r.StatusCode != 200 || etag == "" || r.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("png: %d %q %q", r.StatusCode, etag, r.Header.Get("Content-Type"))
	}
	if r := get(t, s, "/content/"+book+"/_assets_/tablet.png", "If-None-Match", etag); r.StatusCode != 304 {
		t.Fatalf("conditional: %d", r.StatusCode)
	}
	r = get(t, s, "/content/"+book+"/_assets_/tablet.png", "Range", "bytes=0-7")
	if b := body(r); r.StatusCode != 206 || b != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("range: %d %q", r.StatusCode, b)
	}
}

func TestSecurity(t *testing.T) {
	s := newTestServer(t)
	// DNS rebinding: a foreign host name must be refused.
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "evil.example:8080"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("rebinding: %d", w.Code)
	}
	post := func(path, remote, origin string, nabuHeader bool) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.Host = "localhost:8080"
		req.RemoteAddr = remote
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if nabuHeader {
			req.Header.Set("X-Nabu", "1")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		return w.Code
	}
	if c := post("/nabu/api/rescan", "127.0.0.1:1", "", false); c != 403 {
		t.Errorf("missing header accepted: %d", c)
	}
	if c := post("/nabu/api/rescan", "127.0.0.1:1", "http://evil.example", true); c != 403 {
		t.Errorf("cross origin accepted: %d", c)
	}
	if c := post("/nabu/api/rescan", "192.168.1.20:1", "", true); c != 403 {
		t.Errorf("remote machine accepted: %d", c)
	}
	if c := post("/nabu/api/rescan", "127.0.0.1:1", "http://localhost:8080", true); c != 200 {
		t.Errorf("legit request refused: %d", c)
	}
	if c := post("/nabu/api/remove", "127.0.0.1:1", "", true); c != http.StatusConflict {
		t.Errorf("removing the only edition must be refused: %d", c)
	}
}
