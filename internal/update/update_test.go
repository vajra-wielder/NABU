package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A trimmed copy of the real download.kiwix.org listing format.
const listing = `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 3.2 Final//EN">
<html><head><title>Index of /zim/wikipedia</title></head><body>
<h1>Index of /zim/wikipedia</h1>
<table>
<tr><th valign="top"><img src="/icons/blank.gif" alt="[ICO]"></th><th><a href="?C=N;O=D">Name</a></th><th><a href="?C=M;O=A">Last modified</a></th><th><a href="?C=S;O=A">Size</a></th></tr>
<tr><td valign="top"><img src="/icons/unknown.gif" alt="[   ]"></td><td><a href="wikipedia_ab_all_maxi_2026-09.zim">wikipedia_ab_all_maxi_2026-09.zim</a></td><td align="right">2026-09-28 11:02  </td><td align="right"> 22M</td><td>&nbsp;</td></tr>
<tr><td valign="top"><img src="/icons/unknown.gif" alt="[   ]"></td><td><a href="wikipedia_en_all_maxi_2026-08.zim">wikipedia_en_all_maxi_2026-08.zim</a></td><td align="right">2026-08-20 03:14  </td><td align="right">109G</td><td>&nbsp;</td></tr>
<tr><td valign="top"><img src="/icons/unknown.gif" alt="[   ]"></td><td><a href="wikipedia_en_all_maxi_2026-10.zim">wikipedia_en_all_maxi_2026-10.zim</a></td><td align="right">2026-10-21 09:40  </td><td align="right">111G</td><td>&nbsp;</td></tr>
<tr><td valign="top"><img src="/icons/unknown.gif" alt="[   ]"></td><td><a href="wikipedia_en_all_nopic_2026-10.zim">wikipedia_en_all_nopic_2026-10.zim</a></td><td align="right">2026-10-22 01:00  </td><td align="right"> 55G</td><td>&nbsp;</td></tr>
<tr><td valign="top"><img src="/icons/unknown.gif" alt="[   ]"></td><td><a href="wikipedia_en_100_mini_2026-10.zim">wikipedia_en_100_mini_2026-10.zim</a></td><td align="right">2026-10-02 07:12  </td><td align="right">1.2M</td><td>&nbsp;</td></tr>
<tr><td valign="top"><img src="/icons/unknown.gif" alt="[   ]"></td><td><a href="wikipedia_zh-classical_all_maxi_2026-07.zim">wikipedia_zh-classical_all_maxi_2026-07.zim</a></td><td align="right">2026-07-02 07:12  </td><td align="right">412M</td><td>&nbsp;</td></tr>
<tr><td valign="top"><img src="/icons/unknown.gif" alt="[   ]"></td><td><a href="wikipedia_en_simple_all_nopic_2026-10.zim">wikipedia_en_simple_all_nopic_2026-10.zim</a></td><td align="right">2026-10-02 07:12  </td><td align="right">1.1G</td><td>&nbsp;</td></tr>
</table></body></html>`

// The plain-text (<pre>) variant some mirrors use.
const preListing = `<html><body><pre><a href="../">../</a>
<a href="wikipedia_fr_all_maxi_2026-10.zim">wikipedia_fr_all_maxi_2026-10.zim</a>                 21-Oct-2026 09:40     38G
<a href="wikipedia_fr_all_mini_2026-10.zim">wikipedia_fr_all_mini_2026-10.zim</a>                 21-Oct-2026 09:40   5.5G
</pre></body></html>`

func TestParseListing(t *testing.T) {
	eds := ParseListing(listing, "https://download.kiwix.org/zim/wikipedia/")
	if len(eds) != 7 {
		t.Fatalf("got %d editions", len(eds))
	}
	byFile := map[string]Edition{}
	for _, e := range eds {
		byFile[e.File] = e
	}
	en := byFile["wikipedia_en_all_maxi_2026-10.zim"]
	if en.Lang != "en" || en.Selection != "all" || en.Flavour != "maxi" || en.Date != "2026-10" {
		t.Fatalf("bad parse: %+v", en)
	}
	if en.Size < 110<<30 || en.Size > 112<<30 {
		t.Fatalf("size %d", en.Size)
	}
	if en.URL != "https://download.kiwix.org/zim/wikipedia/wikipedia_en_all_maxi_2026-10.zim" {
		t.Fatalf("url %s", en.URL)
	}
	zh := byFile["wikipedia_zh-classical_all_maxi_2026-07.zim"]
	if zh.Lang != "zh-classical" || zh.Series() != "wikipedia_zh-classical_all_maxi" {
		t.Fatalf("zh: %+v", zh)
	}
	simple := byFile["wikipedia_en_simple_all_nopic_2026-10.zim"]
	if simple.Selection != "simple_all" || simple.Flavour != "nopic" {
		t.Fatalf("simple: %+v", simple)
	}
	if byFile["wikipedia_en_100_mini_2026-10.zim"].Size != parseSize("1.2", "M") {
		t.Fatalf("mini size")
	}
	latest := Latest(eds)
	for _, e := range latest {
		if e.File == "wikipedia_en_all_maxi_2026-08.zim" {
			t.Fatalf("old edition kept")
		}
	}
	pre := ParseListing(preListing, "https://mirror.example/kiwix/zim/wikipedia/")
	if len(pre) != 2 || pre[0].Size != 38<<30 || pre[1].Flavour != "mini" {
		t.Fatalf("pre listing: %+v", pre)
	}
}

func TestUpdatesFor(t *testing.T) {
	latest := Latest(ParseListing(listing, "https://x/"))
	ups := UpdatesFor([]Installed{
		{ID: "a", Series: "wikipedia_en_all_maxi", Edition: "2026-08"},
		{ID: "b", Series: "wikipedia_ab_all_maxi", Edition: "2026-09"},
		{ID: "c", Series: "wikipedia_en_all_nopic", Edition: "2026-04"},
		{ID: "d", Series: "wikipedia_en_all_nopic", Edition: "2026-10"},
	}, latest)
	if len(ups) != 1 || ups[0].BookID != "a" || ups[0].Newer.Date != "2026-10" {
		t.Fatalf("updates: %+v", ups)
	}
}

// flakyServer serves data with Range support but drops the connection
// after every `chunk` bytes, like a bad rural link.
func flakyServer(t *testing.T, name string, data []byte, chunk int, sum string) (*httptest.Server, *int32) {
	var requests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/zim/wikipedia/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/zim/wikipedia/") {
		case "":
			fmt.Fprintf(w, `<a href="%s">%s</a> 2026-10-21 09:40 %dK`, name, name, len(data)/1024)
		case name + ".sha256":
			fmt.Fprintf(w, "%s  %s\n", sum, name)
		case name:
			atomic.AddInt32(&requests, 1)
			start := int64(0)
			if rg := r.Header.Get("Range"); rg != "" {
				fmt.Sscanf(rg, "bytes=%d-", &start)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
				w.Header().Set("Content-Length", fmt.Sprint(int64(len(data))-start))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			}
			end := start + int64(chunk)
			if end > int64(len(data)) {
				end = int64(len(data))
			}
			w.Write(data[start:end])
			if end < int64(len(data)) {
				// Simulate a dropped connection.
				hj, ok := w.(http.Hijacker)
				if ok {
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					c, _, _ := hj.Hijack()
					c.Close()
				}
			}
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewServer(mux), &requests
}

func TestDownloadResumeSplitVerify(t *testing.T) {
	old := PartSize
	PartSize = 100_000
	defer func() { PartSize = old }()

	data := make([]byte, 350_000)
	rand.New(rand.NewSource(1)).Read(data)
	h := sha256.Sum256(data)
	name := "wikipedia_xx_all_maxi_2026-10.zim"
	srv, reqs := flakyServer(t, name, data, 64_000, hex.EncodeToString(h[:]))
	defer srv.Close()

	dir := t.TempDir()
	c := &Checker{Dir: dir, CatalogURL: srv.URL + "/zim/wikipedia/", Client: srv.Client()}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	ed, ok := c.Find(name)
	if !ok {
		t.Fatal("edition not in catalogue")
	}
	m := NewManager(dir, srv.Client())
	m.RetryDelay = 20 * time.Millisecond
	finished := make(chan string, 1)
	m.OnFinish = func(f string) { finished <- f }
	if err := m.Start(ed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout; status %+v", m.Status())
	}
	for m.Busy() {
		time.Sleep(10 * time.Millisecond)
	}
	st := m.Status()
	if st.State != StateDone || !st.Verified {
		t.Fatalf("status %+v", st)
	}
	if atomic.LoadInt32(reqs) < 5 {
		t.Fatalf("expected several resumed requests, got %d", *reqs)
	}
	var got []byte
	for _, suffix := range []string{"aa", "ab", "ac", "ad"} {
		b, err := os.ReadFile(filepath.Join(dir, "wikipedia_xx_all_maxi_2026-10.zim"+suffix))
		if err != nil {
			t.Fatalf("part %s: %v", suffix, err)
		}
		got = append(got, b...)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("reassembled parts differ from source")
	}
	if _, err := os.Stat(filepath.Join(dir, ".nabu", "incoming", "wikipedia_xx_all_maxi_2026-10")); !os.IsNotExist(err) {
		t.Fatal("incoming dir not cleaned up")
	}
}

func TestDownloadRejectsCorruption(t *testing.T) {
	data := bytes.Repeat([]byte("nabu"), 10_000)
	name := "wikipedia_xx_all_mini_2026-10.zim"
	srv, _ := flakyServer(t, name, data, len(data), strings.Repeat("0", 64))
	defer srv.Close()
	dir := t.TempDir()
	m := NewManager(dir, srv.Client())
	ed, _ := ParseName(name)
	ed.URL = srv.URL + "/zim/wikipedia/" + name
	if err := m.Start(ed); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for m.Busy() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st := m.Status()
	if st.State != StateFailed || !strings.Contains(st.Error, "checksum") {
		t.Fatalf("status %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatal("corrupt file was installed")
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{999: "999 B", 1536: "1.5 KB", 111 << 30: "111 GB", 4_000_000_000: "3.7 GB", 55 << 30: "55.0 GB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("%d: got %s want %s", n, got, want)
		}
	}
}
