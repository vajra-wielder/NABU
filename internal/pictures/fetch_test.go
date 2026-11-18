package pictures

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWiki answers like a wiki's API and upload.wikimedia.org.
func fakeWiki(t *testing.T, busyFirst bool) (*httptest.Server, *int32, *int32) {
	var apiCalls, busy int32
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "NABU/") {
			t.Errorf("User-Agent %q", ua)
		}
		if busyFirst && atomic.AddInt32(&busy, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		switch {
		case r.URL.Path == "/w/api.php":
			atomic.AddInt32(&apiCalls, 1)
			titles := strings.Split(r.URL.Query().Get("titles"), "|")
			if len(titles) > 50 {
				t.Errorf("%d titles in one request", len(titles))
			}
			if r.URL.Query().Get("iiurlwidth") != "250" {
				t.Error("not a standard width")
			}
			base := srv.URL + "/thumb/"
			w.Write([]byte(`{"query":{
			 "normalized":[{"from":"File:Ishtar_gate.jpg","to":"Fichier:Ishtar gate.jpg"},{"from":"File:Old_name.jpg","to":"Fichier:Old name.jpg"}],
			 "redirects":[{"from":"Fichier:Old name.jpg","to":"Fichier:New name.jpg"}],
			 "pages":[
			  {"title":"Fichier:Ishtar gate.jpg","imageinfo":[{"mime":"image/jpeg","thumburl":"` + base + `ishtar.jpg?utm_source=x"}]},
			  {"title":"Fichier:New name.jpg","imageinfo":[{"mime":"image/jpeg","thumburl":"` + base + `new.jpg"}]},
			  {"title":"Fichier:Song.ogg","imageinfo":[{"mime":"audio/ogg","thumburl":""}]},
			  {"title":"Fichier:Nothing.jpg","missing":true}]}}`))
		case strings.HasPrefix(r.URL.Path, "/thumb/"):
			if r.URL.RawQuery != "" {
				t.Error("tracking parameters sent")
			}
			if r.URL.Path == "/thumb/gone.jpg" {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte("JPEG:" + r.URL.Path))
		}
	}))
	return srv, &apiCalls, &busy
}

func TestResolveAndDownload(t *testing.T) {
	minBackoff = 10 * time.Millisecond
	srv, apiCalls, _ := fakeWiki(t, true)
	defer srv.Close()
	f := NewFetcher(srv.Client(), "NABU/test (https://example.org)")
	host := strings.TrimPrefix(srv.URL, "https://")
	got, err := f.Resolve(context.Background(), host, []string{"Ishtar gate.jpg", "Old name.jpg", "Song.ogg", "Nothing.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if i := got["Ishtar_gate.jpg"]; i.URL != srv.URL+"/thumb/ishtar.jpg" || i.Missing {
		t.Errorf("Ishtar: %+v", i)
	}
	if i := got["Old_name.jpg"]; i.URL != srv.URL+"/thumb/new.jpg" {
		t.Errorf("redirect not followed: %+v", i)
	}
	if !got["Song.ogg"].Skip || !got["Nothing.jpg"].Missing {
		t.Errorf("song/nothing: %+v %+v", got["Song.ogg"], got["Nothing.jpg"])
	}
	if f.Paused().IsZero() {
		t.Error("429 not noted")
	}
	b, err := f.Download(context.Background(), got["Ishtar_gate.jpg"].URL)
	if err != nil || string(b) != "JPEG:/thumb/ishtar.jpg" {
		t.Fatalf("download: %q %v", b, err)
	}
	if _, err := f.Download(context.Background(), srv.URL+"/thumb/gone.jpg"); err != ErrNotFound {
		t.Fatalf("404: %v", err)
	}

	// Pictures asked for together share one API request.
	n := atomic.LoadInt32(apiCalls)
	done := make(chan Info, 3)
	for _, name := range []string{"Ishtar gate.jpg", "Old name.jpg", "Nothing.jpg"} {
		go func(name string) {
			i, _ := f.ResolveOne(context.Background(), host, name)
			done <- i
		}(name)
	}
	for i := 0; i < 3; i++ {
		<-done
	}
	if c := atomic.LoadInt32(apiCalls) - n; c != 1 {
		t.Fatalf("%d API requests for one page", c)
	}
}

func TestDownloadLimits(t *testing.T) {
	var now, peak int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&now, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&now, -1)
		w.Write(make([]byte, 200_000))
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), "NABU/test")
	start := time.Now()
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() { f.Download(context.Background(), srv.URL); done <- struct{}{} }()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	if peak > maxDownloads {
		t.Errorf("%d downloads at once", peak)
	}
	// 2 MB at 2 MB/s: about a second, never much less.
	if el := time.Since(start); el < 800*time.Millisecond {
		t.Errorf("speed limit not kept: 2 MB in %v", el)
	}
}
