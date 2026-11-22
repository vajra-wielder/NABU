package server

import (
	"context"
	"crypto/tls"
	"github.com/vajra-wielder/nabu/internal/essentials"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
)

// fakeWikimedia answers every wiki's API and the picture servers. Every
// picture exists except "Nowhere.jpg".
func fakeWikimedia(t *testing.T) (*http.Client, *int32) {
	var downloads int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/w/api.php":
			var pages []string
			for _, title := range strings.Split(r.URL.Query().Get("titles"), "|") {
				name := strings.TrimPrefix(title, "File:")
				if name == "Nowhere.jpg" {
					pages = append(pages, `{"title":"File:Nowhere.jpg","missing":true}`)
					continue
				}
				repo := "shared" // on Commons, except the wiki's own covers
				if strings.HasPrefix(name, "Local") {
					repo = "local"
				}
				pages = append(pages, `{"title":"File:`+strings.ReplaceAll(name, "_", " ")+`","imagerepository":"`+repo+`","imageinfo":[{"mime":"image/png","thumburl":"https://upload.wikimedia.org/thumb/`+name+`/250px-`+name+`?utm=1"}]}`)
			}
			w.Write([]byte(`{"query":{"pages":[` + strings.Join(pages, ",") + `]}}`))
		case strings.HasPrefix(r.URL.Path, "/thumb/"):
			atomic.AddInt32(&downloads, 1)
			// A tiny real PNG, so it is recognised as a picture.
			w.Write([]byte("\x89PNG\r\n\x1a\n" + r.URL.Path))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &http.Client{Transport: tr}, &downloads
}

func TestPicturesOnlineOfflineAndKept(t *testing.T) {
	client, downloads := fakeWikimedia(t)
	var online atomic.Bool
	online.Store(true)
	svc := pictures.NewService("", pictures.NewFetcher(client, "NABU/test (https://example.org)"))
	s := newTestServerWith(t, library.Config{Pictures: svc, Online: online.Load})
	s.Offline = false

	_, page := get(s, "/testwiki/wiki/Babylon")
	src := `src="/nabu/img/testwiki/Babylon_map.png?w=250"`
	if !strings.Contains(page, src) {
		t.Fatalf("article does not ask NABU for its pictures")
	}
	r, body := get(s, "/nabu/img/testwiki/Babylon_map.png?w=250")
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" || !strings.Contains(body, "250px-Babylon_map.png") {
		t.Fatalf("online picture: %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r, _ := get(s, "/nabu/img/testwiki/Nowhere.jpg"); r.StatusCode != 404 {
		t.Fatalf("missing picture: %d", r.StatusCode)
	}

	// Offline now: the kept picture still shows, from the drive.
	s.Offline = true
	online.Store(false)
	n := atomic.LoadInt32(downloads)
	if r, _ := get(s, "/nabu/img/testwiki/Babylon_map.png?w=250"); r.StatusCode != 200 {
		t.Fatalf("kept picture offline: %d", r.StatusCode)
	}
	if r, _ := get(s, "/nabu/img/testwiki/Ishtar_Gate.jpg?w=250"); r.StatusCode != 404 {
		t.Fatalf("picture never kept, offline: %d", r.StatusCode)
	}
	// Kept once, for every language: another Wikipedia shows it offline too.
	if r, _ := get(s, "/nabu/img/fawiki/Babylon_map.png?w=250"); r.StatusCode != 200 {
		t.Fatalf("shared picture for another language: %d", r.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(svc.Dir, pictures.Commons, "recent.bin")); err != nil {
		t.Fatal("Commons picture not in the shared store")
	}
	if atomic.LoadInt32(downloads) != n {
		t.Fatal("went online while offline")
	}
	// Offline, articles still ask for pictures, since the drive has some.
	if _, page := get(s, "/testwiki/wiki/Babylon"); !strings.Contains(page, src) {
		t.Fatal("offline article lost its kept pictures")
	}

	// A collection copy carries its pictures with it.
	s.Offline = false
	online.Store(true)
	call(t, s, "collection-create", `{"name":"Babylonia","book":"testwiki","title":"Babylon"}`)
	c := s.collections.List()[0]
	html, err := os.ReadFile(filepath.Join(s.collections.Dir, c.Slug, c.Items[0].File))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `src="Babylon_files/`) {
		t.Fatalf("saved copy does not use its own pictures")
	}
	pics, _ := os.ReadDir(filepath.Join(s.collections.Dir, c.Slug, "Babylon_files"))
	if len(pics) < 2 {
		t.Fatalf("%d pictures saved with the copy", len(pics))
	}
	if r, _ := get(s, "/nabu/collections/"+c.Slug+"/Babylon_files/"+pics[0].Name()); r.StatusCode != 200 {
		t.Fatalf("collection picture: %d", r.StatusCode)
	}
}

func TestWholeWikiPictures(t *testing.T) {
	client, downloads := fakeWikimedia(t)
	svc := pictures.NewService("", pictures.NewFetcher(client, "NABU/test (https://example.org)"))
	s := newTestServerWith(t, library.Config{Pictures: svc, Online: func() bool { return true }})
	if err := s.Lib.StartPictures("testwiki", pictures.All); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var st library.PicStatus
	for time.Now().Before(deadline) {
		if st = s.Lib.PictureStatus("testwiki"); st.State == library.PicDone {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.State != library.PicDone || st.Pictures+st.Shared < 2 {
		t.Fatalf("whole-wiki download: %+v", st)
	}
	if !strings.Contains(picLine(st), "✓") {
		t.Errorf("line: %q", picLine(st))
	}
	_, home := get(s, "/")
	if !strings.Contains(home, "Pictures for reading offline ✓") {
		t.Error("home page does not show the pictures")
	}
	// A second run over the same dump downloads nothing again.
	n := atomic.LoadInt32(downloads)
	j, _ := pictures.LoadJob(filepath.Join(svc.Dir, "testwiki"))
	j.Mode, j.Done = pictures.Lead, false
	j.Save(filepath.Join(svc.Dir, "testwiki"))
	s.Lib.StartPictures("testwiki", pictures.Lead)
	for time.Now().Before(deadline) && s.Lib.PictureStatus("testwiki").State != library.PicDone {
		time.Sleep(50 * time.Millisecond)
	}
	if atomic.LoadInt32(downloads) != n {
		t.Error("pictures downloaded twice")
	}
	// Another language's download finds the shared pictures already there.
	n = atomic.LoadInt32(downloads)
	if err := s.Lib.StartPictures("fawiki", pictures.All); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) && s.Lib.PictureStatus("fawiki").State != library.PicDone {
		time.Sleep(50 * time.Millisecond)
	}
	if st := s.Lib.PictureStatus("fawiki"); st.State != library.PicDone || st.Shared < 2 {
		t.Fatalf("fawiki: %+v", st)
	}
	if err := s.Lib.RemovePictures("testwiki"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(svc.Dir, "testwiki")); !os.IsNotExist(err) {
		t.Fatal("pictures not removed")
	}
	if _, err := os.Stat(filepath.Join(svc.Dir, pictures.Commons)); err != nil {
		t.Fatal("shared pictures removed while fawiki still uses them")
	}
	s.Lib.RemovePictures("fawiki")
	if _, err := os.Stat(filepath.Join(svc.Dir, pictures.Commons)); !os.IsNotExist(err) {
		t.Fatal("shared pictures left after the last language's were removed")
	}
}

// call POSTs an action as the browser on the computer running NABU does.
func call(t *testing.T, s *Server, action, body string) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/nabu/api/"+action, strings.NewReader(body))
	req.Host, req.RemoteAddr = "localhost:8080", "127.0.0.1:1"
	req.Header.Set("X-Nabu", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// Another language shows English's pictures and keeps none of its own;
// keeping pictures stops at the room set aside for them.
func TestEnglishPicturesAndLimit(t *testing.T) {
	old := library.EnglishDB
	library.EnglishDB = "testwiki" // stands in for English
	defer func() { library.EnglishDB = old }()
	client, _ := fakeWikimedia(t)
	var online atomic.Bool
	online.Store(true)
	svc := pictures.NewService("", pictures.NewFetcher(client, "NABU/test (https://example.org)"))
	s := newTestServerWith(t, library.Config{Pictures: svc, Online: online.Load})
	s.Offline = false

	st := s.Lib.PictureStatus("fawiki")
	if !st.English || !strings.Contains(picLine(st), "pictures kept for English") {
		t.Fatalf("fawiki should show English's pictures: %+v %q", st, picLine(st))
	}
	if s.Lib.PictureStatus("testwiki").English {
		t.Fatal("English itself marked as using English's pictures")
	}
	// Its own picture, seen online, is shown but not kept.
	if r, _ := get(s, "/nabu/img/fawiki/Local_cover.png?w=250"); r.StatusCode != 200 {
		t.Fatalf("online: %d", r.StatusCode)
	}
	s.Offline = true
	online.Store(false)
	if r, _ := get(s, "/nabu/img/fawiki/Local_cover.png?w=250"); r.StatusCode != 404 {
		t.Fatalf("a non-English language kept its own picture: %d", r.StatusCode)
	}
	s.Offline = false
	online.Store(true)

	// The limit: a picture kept while reading fills a one-byte limit, and
	// nothing more is kept or started.
	svc.SetLimit(1)
	if r, _ := get(s, "/nabu/img/testwiki/Babylon_map.png?w=250"); r.StatusCode != 200 {
		t.Fatalf("online: %d", r.StatusCode)
	}
	if !svc.Full() {
		t.Fatalf("used %d of 1", svc.Used())
	}
	used := svc.Used()
	get(s, "/nabu/img/testwiki/Ishtar_Gate.jpg?w=250")
	if svc.Used() != used {
		t.Fatal("kept a picture past the limit")
	}
	if err := s.Lib.StartPictures("testwiki", pictures.All); err == nil || !strings.Contains(err.Error(), "raise the limit") {
		t.Fatalf("started past the limit: %v", err)
	}
	// Raised in the settings, the download runs to the end.
	call(t, s, "settings", `{"picture_limit_gb":0}`)
	if svc.Limit() != 0 {
		t.Fatal("setting not applied")
	}
	if err := s.Lib.StartPictures("testwiki", pictures.All); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && s.Lib.PictureStatus("testwiki").State != library.PicDone {
		time.Sleep(50 * time.Millisecond)
	}
	if st := s.Lib.PictureStatus("testwiki"); st.State != library.PicDone || st.Done != st.Total {
		t.Fatalf("after raising the limit: %+v", st)
	}
	// A job that stopped at the limit says so.
	svc.SetLimit(1)
	j, _ := pictures.LoadJob(filepath.Join(svc.Dir, "testwiki"))
	j.Done = false
	j.Save(filepath.Join(svc.Dir, "testwiki"))
	// (as NABU finds it when it next starts)
	again, err := library.OpenDrive(s.Lib.Drive, library.Config{Pictures: pictures.NewService(svc.Dir, svc.Fetch)})
	if err != nil {
		t.Fatal(err)
	}
	again.Pictures().SetLimit(1)
	if st := again.PictureStatus("testwiki"); st.State != library.PicFull || !strings.Contains(picLine(st), "filled the room") {
		t.Fatalf("full: %+v %q", st, picLine(st))
	}
	again.Close()
	svc.SetLimit(0)
	// Asked to, the other language keeps its own pictures too.
	if err := s.Lib.StartPictures("fawiki", pictures.Lead); err != nil {
		t.Fatal(err)
	}
	if s.Lib.PictureStatus("fawiki").English {
		t.Fatal("still English-only after choosing its own pictures")
	}
}

// Made online, the Essentials fetch all their pictures first, in
// batches, and the saved copies carry them.
func TestEssentialsWithPictures(t *testing.T) {
	old := essentialGroups
	essentialGroups = func() []essentials.Group {
		return []essentials.Group{{Name: "Cities", Titles: []string{"Babylon"}}}
	}
	defer func() { essentialGroups = old }()
	client, downloads := fakeWikimedia(t)
	svc := pictures.NewService("", pictures.NewFetcher(client, "NABU/test (https://example.org)"))
	s := newTestServerWith(t, library.Config{Pictures: svc, Online: func() bool { return true }})
	s.Offline = false
	var phases []string
	missing, err := s.MakeEssentials(context.Background(), func(what string, d, t int) {
		if len(phases) == 0 || phases[len(phases)-1] != what {
			phases = append(phases, what)
		}
	})
	if err != nil || len(missing) != 0 {
		t.Fatalf("%v %v", missing, err)
	}
	if strings.Join(phases, ",") != "finding their pictures,fetching their pictures,saving the articles" {
		t.Fatalf("phases %v", phases)
	}
	if atomic.LoadInt32(downloads) < 2 {
		t.Fatalf("%d pictures fetched", atomic.LoadInt32(downloads))
	}
	c := s.collections.List()[0]
	files, _ := os.ReadDir(filepath.Join(s.collections.Dir, c.Slug, "Babylon_files"))
	if len(files) < 2 {
		t.Fatalf("%d pictures in the saved copy", len(files))
	}
}

// Closing right after starting the pictures (as setup does) must not
// let the download run on with the dump closed.
func TestCloseRightAfterStartingPictures(t *testing.T) {
	client, _ := fakeWikimedia(t)
	for i := 0; i < 20; i++ {
		svc := pictures.NewService("", pictures.NewFetcher(client, "NABU/test (https://example.org)"))
		s := newTestServerWith(t, library.Config{Pictures: svc, Online: func() bool { return true }})
		if err := s.Lib.StartPictures("testwiki", pictures.Lead); err != nil {
			t.Fatal(err)
		}
		s.Lib.Close()
		time.Sleep(time.Duration(i%3) * time.Millisecond)
	}
}
