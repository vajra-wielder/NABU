package server

import (
	"encoding/json"
	"github.com/vajra-wielder/nabu/internal/essentials"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/update"
)

func newTestServer(t *testing.T) *Server { return newTestServerWith(t, library.Config{}) }

func newTestServerWith(t *testing.T, cfg library.Config) *Server {
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
	if cfg.Pictures != nil {
		cfg.Pictures.Dir = filepath.Join(dir, "pictures")
	}
	lib, err := library.OpenDrive(drive.ForLibrary(dir), cfg)
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
	c := &update.Checker{Dir: lib.Drive.Work, Mirror: "http://127.0.0.1:1", Client: http.DefaultClient}
	s, err := New(lib, c, update.NewManager(lib.Drive, http.DefaultClient))
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

func TestSearchInsideArticlesAndPlaces(t *testing.T) {
	s := newTestServer(t)
	// Before building: the search page offers to build it.
	if _, b := get(s, "/testwiki/search?list=1&q=scribes"); !strings.Contains(b, "Build search inside articles") {
		t.Fatal("no offer to build full-text search")
	}
	req := httptest.NewRequest("POST", "/nabu/api/fulltext", strings.NewReader(`{"book":"testwiki"}`))
	req.Host, req.RemoteAddr = "localhost:8080", "127.0.0.1:1"
	req.Header.Set("X-Nabu", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("build request: %d %s", w.Code, w.Body.String())
	}
	b := s.Lib.Get("testwiki")
	deadline := time.Now().Add(20 * time.Second)
	for b.FullText() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if b.FullText() == nil {
		t.Fatalf("full-text index never became ready: %+v", b.FTStatus())
	}
	_, body := get(s, "/testwiki/search?list=1&q=god+of+scribes")
	for _, want := range []string{"In article text", "<mark>scribes</mark>", `href="/testwiki/wiki/Nabu"`} {
		if !strings.Contains(body, want) {
			t.Errorf("search page lacks %q:\n%.3000s", want, body)
		}
	}
	// Location panel with the offline map and places nearby.
	_, body = get(s, "/testwiki/wiki/Babylon")
	for _, want := range []string{`id="location"`, "world.svg", "32.536°N", "Places nearby", `href="/testwiki/wiki/Nabu"`, "Templates used", "Infobox settlement"} {
		if !strings.Contains(body, want) {
			t.Errorf("article lacks %q", want)
		}
	}
	// Structured data, as NABU v0.1's database held it.
	r, body := get(s, "/nabu/api/page?book=testwiki&title=Babylonia")
	for _, want := range []string{`"title":"Babylon"`, `"links":[`, `"Mesopotamia"`, `"categories":["Ancient cities"]`, `"media":["Babylon map.png"]`, `"coordinates":[{"lat":32.53`, `"infoboxes":[{"name":"Infobox settlement"`, `"tables":["{|`} {
		if r.StatusCode != 200 || !strings.Contains(body, want) {
			t.Errorf("page data lacks %s:\n%.600s", want, body)
		}
	}
}

func TestCollections(t *testing.T) {
	s := newTestServer(t)
	call := func(action, remote string, body string) (int, string) {
		req := httptest.NewRequest("POST", "/nabu/api/"+action, strings.NewReader(body))
		req.Host, req.RemoteAddr = "localhost:8080", remote
		req.Header.Set("X-Nabu", "1")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	code, body := call("collection-create", "127.0.0.1:1", `{"name":"Ancient Iraq","book":"testwiki","title":"Babylon"}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	if code, _ := call("collection-create", "192.168.1.5:1", `{"name":"From the network"}`); code != 403 {
		t.Fatalf("a device on the network created a collection: %d", code)
	}
	slug := s.collections.List()[0].Slug
	if code, body := call("collection-add", "127.0.0.1:1", `{"slug":"`+slug+`","book":"testwiki","title":"nabu"}`); code != 200 {
		t.Fatalf("add: %d %s", code, body)
	}
	_, list := get(s, "/nabu/api/collections?book=testwiki&title=Babylon")
	if !strings.Contains(list, `"name":"Ancient Iraq","count":2,"has":true`) {
		t.Fatalf("collections API: %s", list)
	}
	c, _ := s.collections.Get(slug)
	babylon := c.Items[0]

	// The saved file is complete on its own: styles inside, links that the
	// collection map can redirect, attribution.
	raw, err := os.ReadFile(filepath.Join(s.Lib.Drive.Collections, slug, babylon.File))
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	for _, want := range []string{"<style>", "--lapis", `class="infobox"`, `href="https://en.wikipedia.org/wiki/Nabu" data-t="testwiki|Nabu"`,
		`<script src="collection.js">`, "CC BY-SA 4.0", "index.html"} {
		if !strings.Contains(page, want) {
			t.Errorf("saved copy lacks %q", want)
		}
	}
	if strings.Contains(page, `src="/nabu/static`) || strings.Contains(page, `href="/nabu/static`) {
		t.Error("saved copy depends on NABU's server")
	}
	// Served through NABU, wiki links point at the local copy.
	r, served := get(s, "/nabu/collections/"+slug+"/"+babylon.File)
	if r.StatusCode != 200 || !strings.Contains(served, `href="/testwiki/wiki/Nabu" data-t=`) {
		t.Errorf("served copy: %d", r.StatusCode)
	}
	for path, want := range map[string]string{
		"/nabu/collections/" + slug + "/":           "Read as one book",
		"/nabu/collections/" + slug + "/index.html": "Ancient Iraq",
		"/nabu/collections/" + slug + "/book":       `href="#ch2"`, // Babylon's link to Nabu becomes a chapter link
		"/nabu/saved":                               "Ancient Iraq",
	} {
		if r, b := get(s, path); r.StatusCode != 200 || !strings.Contains(b, want) {
			t.Errorf("%s: %d lacks %q", path, r.StatusCode, want)
		}
	}
	if r, _ := get(s, "/nabu/collections/"+slug+"/..%2Fcollection.json"); r.StatusCode != 404 {
		t.Errorf("path escape: %d", r.StatusCode)
	}
	if r, _ := get(s, "/testwiki/wiki/Babylon"); r.StatusCode != 200 {
		t.Fatal("article page broke")
	} else if _, b := get(s, "/testwiki/wiki/Babylon"); !strings.Contains(b, "data-collect") {
		t.Error("article has no Collect button")
	}
	if code, body := call("collection-refresh", "127.0.0.1:1", `{"slug":"`+slug+`"}`); code != 200 || !strings.Contains(body, `"refreshed":2`) {
		t.Errorf("refresh: %d %s", code, body)
	}
	call("collection-remove", "127.0.0.1:1", `{"slug":"`+slug+`","book":"testwiki","title":"Nabu"}`)
	if c, _ := s.collections.Get(slug); len(c.Items) != 1 {
		t.Fatalf("remove: %+v", c.Items)
	}
	call("collection-delete", "127.0.0.1:1", `{"slug":"`+slug+`"}`)
	if len(s.collections.List()) != 0 {
		t.Fatal("delete failed")
	}
}

func TestMakeACopy(t *testing.T) {
	s := newTestServer(t)
	r, page := get(s, "/nabu/copy")
	if r.StatusCode != 200 || !strings.Contains(page, `value="testwiki"`) || !strings.Contains(page, "Make the copy") {
		t.Fatalf("copy page: %d", r.StatusCode)
	}
	dest := filepath.Join(t.TempDir(), "NABU")
	call(t, s, "copy-start", `{"to":`+strconvQuote(dest)+`,"copy":{"wikis":["fawiki"],"systems":["linux"],"indexes":true}}`)
	for i := 0; i < 200 && s.copier.Status().State == "copying"; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if st := s.copier.Status(); st.State != "done" {
		t.Fatalf("copy: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dest, "wikipedia", "fawiki-20261101-pages-articles-multistream.xml.bz2")); err != nil {
		t.Fatal("dump not copied")
	}
	if _, err := os.Stat(filepath.Join(dest, "wikipedia", "testwiki-20261101-pages-articles-multistream.xml.bz2")); err == nil {
		t.Fatal("unchosen Wikipedia copied")
	}
	if _, body := get(s, "/nabu/api/status"); !strings.Contains(body, `"copy":{"state":"done"`) {
		t.Errorf("status: %s", body)
	}
	// From another device on the network: refused.
	req := httptest.NewRequest("POST", "/nabu/api/copy-start", strings.NewReader(`{"to":"/tmp/x"}`))
	req.Host, req.RemoteAddr = "localhost:8080", "192.168.1.9:1"
	req.Header.Set("X-Nabu", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("copy from the network: %d", w.Code)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// With the drive pulled out, pages ask for it back and reload themselves.
func TestDriveGonePage(t *testing.T) {
	s := newTestServer(t)
	gone := true
	s.DriveGone = func() bool { return gone }
	r, body := get(s, "/testwiki/wiki/Babylon")
	if r.StatusCode != 503 || r.Header.Get("Refresh") == "" || !strings.Contains(body, "not connected") {
		t.Fatalf("%d %q", r.StatusCode, r.Header.Get("Refresh"))
	}
	if r, _ := get(s, "/nabu/static/nabu.css"); r.StatusCode != 200 {
		t.Fatal("styles are inside the program and still load")
	}
	if r, _ := get(s, "/nabu/img/testwiki/Babylon_map.png"); r.StatusCode != 503 {
		t.Fatalf("picture: %d", r.StatusCode)
	}
	gone = false
	if r, body := get(s, "/testwiki/wiki/Babylon"); r.StatusCode != 200 || !strings.Contains(body, "Safe to pull out the drive") {
		t.Fatalf("back: %d", r.StatusCode)
	}
}

// The drive check reads everything, then names the articles in a damaged
// bundle; a damaged title index can be built again.
func TestCheckTheDrive(t *testing.T) {
	s := newTestServer(t)
	run := func() checkView {
		call(t, s, "check-start", `{}`)
		deadline := time.Now().Add(20 * time.Second)
		for s.check.view().Running && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		return s.check.view()
	}
	v := run()
	if len(v.Results) != 2 || v.DamagedReaders != 0 || v.Err != "" {
		t.Fatalf("clean drive: %+v", v)
	}
	if _, page := get(s, "/nabu/check"); !strings.Contains(page, "bundles are intact") {
		t.Fatal("page does not say the drive is fine")
	}
	// Damage the middle of the testwiki dump.
	b := s.Lib.Get("testwiki")
	p := b.Files.Pairs[0].Data[0]
	data, _ := os.ReadFile(p)
	data[len(data)/2] ^= 0xff
	os.WriteFile(p, data, 0o644)
	b.Reader().ForgetCache()
	v = run()
	if v.DamagedReaders != 1 {
		t.Fatalf("damage not found: %+v", v)
	}
	_, page := get(s, "/nabu/check")
	if strings.Contains(page, "Repair from the other drive") {
		// Another NABU drive with this dump happens to be attached: repair.
		if err := s.repairBook("testwiki"); err != nil {
			t.Fatal(err)
		}
		if v = run(); v.DamagedReaders != 0 {
			t.Fatalf("not repaired: %+v", v)
		}
		os.WriteFile(p, data, 0o644) // the damaged bytes again, for the rest
		b.Reader().ForgetCache()
		run()
		_, page = get(s, "/nabu/check")
	} else if !strings.Contains(page, "plug in another NABU drive") {
		t.Fatalf("page does not offer a way to repair: %s", page)
	}
	if !strings.Contains(page, "bundles are damaged") || !strings.Contains(page, "more.") {
		t.Fatalf("page does not report the damage: %s", page[strings.Index(page, "<h1"):])
	}
	call(t, s, "check-reindex", `{"book":"testwiki"}`)
	deadline := time.Now().Add(10 * time.Second)
	for s.Lib.Get("testwiki").Reader() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Lib.Get("testwiki").Reader() == nil {
		t.Fatal("title index not rebuilt")
	}
}

// The Essentials collections are made in one go from the English
// Wikipedia on the drive; titles it lacks are listed.
func TestEssentials(t *testing.T) {
	old := essentialGroups
	essentialGroups = func() []essentials.Group {
		return []essentials.Group{
			{Name: "First aid", Titles: []string{"Babylon", "No such article anywhere"}},
			{Name: "Tools", Titles: []string{"Nabu"}},
		}
	}
	defer func() { essentialGroups = old }()
	s := newTestServer(t)
	_, page := get(s, "/nabu/saved")
	if !strings.Contains(page, "Make the Essentials collections") {
		t.Fatal("no button")
	}
	for round := 0; round < 2; round++ { // the second time brings them up to date
		call(t, s, "essentials", `{}`)
		deadline := time.Now().Add(20 * time.Second)
		for s.essentialsView().Running && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	v := s.essentialsView()
	if !v.Made || v.Err != "" || len(v.Missing) != 1 || v.Missing[0] != "No such article anywhere" {
		t.Fatalf("%+v", v)
	}
	names := map[string]int{}
	for _, c := range s.collections.List() {
		names[c.Name] = len(c.Items)
	}
	if names["Essentials: First aid"] != 1 || names["Essentials: Tools"] != 1 || len(names) != 2 {
		t.Fatalf("collections %v", names)
	}
	if _, page := get(s, "/nabu/saved"); !strings.Contains(page, "Bring the Essentials up to date") || !strings.Contains(page, "Not in this Wikipedia: No such article anywhere") {
		t.Fatal("page after making them")
	}
}

type fakeWiFi struct{ on bool }

func (f *fakeWiFi) On() bool          { return f.on }
func (f *fakeWiFi) Fixed() bool       { return false }
func (f *fakeWiFi) Set(on bool) error { f.on = on; return nil }
func (f *fakeWiFi) Addrs() []string {
	if f.on {
		return []string{"http://192.168.43.17:8080/"}
	}
	return nil
}

// "Read on phones": a switch, then the address with its QR code.
func TestSharePage(t *testing.T) {
	s := newTestServer(t)
	s.WiFi = &fakeWiFi{}
	if _, page := get(s, "/nabu/share"); !strings.Contains(page, `data-action="share-wifi" data-mode="on"`) {
		t.Fatal("no switch")
	}
	call(t, s, "share-wifi", `{"mode":"on"}`)
	if !update.LoadSettings(s.Lib.Drive.Personal).ShareWifi {
		t.Fatal("not remembered")
	}
	_, page := get(s, "/nabu/share")
	if !strings.Contains(page, "<svg") || !strings.Contains(page, "http://192.168.43.17:8080/") || !strings.Contains(page, "Stop sharing") {
		t.Fatal("no address or code")
	}
	if os.Getenv("SHARE_SHOT") != "" {
		srv := httptest.NewServer(s)
		defer srv.Close()
		os.WriteFile(os.Getenv("SHARE_SHOT"), []byte(srv.URL), 0o644)
		time.Sleep(15 * time.Second)
	}
	call(t, s, "share-wifi", `{"mode":"off"}`)
	if update.LoadSettings(s.Lib.Drive.Personal).ShareWifi {
		t.Fatal("still remembered on")
	}
}
