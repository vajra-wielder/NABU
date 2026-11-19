package update

import (
	"bytes"
	"context"
	"crypto/sha1"
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

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
)

// Trimmed from a real dumps.wikimedia.org/simplewiki/20261101/dumpstatus.json.
const statusJSON = `{"jobs": {
 "articlesmultistreamdump": {"status": "done", "files": {
   "xxwiki-20261101-pages-articles-multistream.xml.bz2": {"size": %d, "url": "/xxwiki/20261101/xxwiki-20261101-pages-articles-multistream.xml.bz2", "sha1": "%s"},
   "xxwiki-20261101-pages-articles-multistream-index.txt.bz2": {"size": %d, "url": "/xxwiki/20261101/xxwiki-20261101-pages-articles-multistream-index.txt.bz2", "sha1": "%s"}}},
 "articlesdump": {"status": "done", "files": {}}},
 "version": "0.8"}`

const rss = `<?xml version="1.0"?><rss version="2.0"><channel><title>x</title>
<link>http://download.wikimedia.org/xxwiki/20261101</link><item><link>http://download.wikimedia.org/xxwiki/20261101</link></item></channel></rss>`

func sum(b []byte) string { h := sha1.Sum(b); return hex.EncodeToString(h[:]) }

// fakeWikimedia serves a dump, dropping the connection every `chunk`
// bytes like a bad rural link, and honouring Range requests.
func fakeWikimedia(t *testing.T, data, index []byte, chunk int, badSum bool) (*httptest.Server, *int32) {
	var reqs int32
	dsum := sum(data)
	if badSum {
		dsum = strings.Repeat("0", 40)
	}
	files := map[string][]byte{
		"/xxwiki/20261101/xxwiki-20261101-pages-articles-multistream.xml.bz2":       data,
		"/xxwiki/20261101/xxwiki-20261101-pages-articles-multistream-index.txt.bz2": index,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "-rss.xml"):
			fmt.Fprint(w, rss)
		case strings.HasSuffix(r.URL.Path, "dumpstatus.json"):
			fmt.Fprintf(w, statusJSON, len(data), dsum, len(index), sum(index))
		default:
			body, ok := files[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			atomic.AddInt32(&reqs, 1)
			var start int64
			if rg := r.Header.Get("Range"); rg != "" {
				fmt.Sscanf(rg, "bytes=%d-", &start)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
				w.Header().Set("Content-Length", fmt.Sprint(int64(len(body))-start))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			}
			end := start + int64(chunk)
			if end > int64(len(body)) {
				end = int64(len(body))
			}
			w.Write(body[start:end])
			if end < int64(len(body)) {
				w.(http.Flusher).Flush()
				c, _, _ := w.(http.Hijacker).Hijack()
				c.Close()
			}
		}
	}))
	return srv, &reqs
}

func TestLatestAndDownload(t *testing.T) {
	old := PartSize
	PartSize = 100_000
	defer func() { PartSize = old }()
	data := make([]byte, 350_000)
	rand.New(rand.NewSource(1)).Read(data)
	index := []byte("index bytes")
	srv, reqs := fakeWikimedia(t, data, index, 64_000, false)
	defer srv.Close()

	rel, err := Latest(context.Background(), srv.Client(), srv.URL, "xxwiki")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Date != "20261101" || len(rel.Files) != 2 || rel.Files[0].Name != "xxwiki-20261101-pages-articles-multistream-index.txt.bz2" ||
		!strings.HasPrefix(rel.Files[1].URL, srv.URL) || rel.Size() != int64(len(data)+len(index)) {
		t.Fatalf("release: %+v", rel)
	}
	dir := t.TempDir()
	m := NewManager(drive.ForLibrary(dir), srv.Client())
	m.Computer = "" // on the drive, as on a computer without room
	m.RetryDelay = 10 * time.Millisecond
	done := make(chan struct{})
	m.OnFinish = func(*Release) { close(done) }
	if err := m.Start(rel); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout: %+v", m.Status())
	}
	for m.Busy() {
		time.Sleep(5 * time.Millisecond)
	}
	if st := m.Status(); st.State != StateDone || !st.Verified {
		t.Fatalf("status %+v", st)
	}
	if atomic.LoadInt32(reqs) < 5 {
		t.Fatalf("expected resumed requests, got %d", *reqs)
	}
	var got []byte
	for _, suf := range []string{"aa", "ab", "ac", "ad"} {
		b, err := os.ReadFile(filepath.Join(dir, "xxwiki-20261101-pages-articles-multistream.xml.bz2"+suf))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, b...)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("pieces do not reassemble the original")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "xxwiki-20261101-pages-articles-multistream-index.txt.bz2")); !bytes.Equal(b, index) {
		t.Fatal("index file wrong")
	}
	if len(m.Paused()) != 0 {
		t.Fatal("incoming folder left behind")
	}
}

func TestCorruptDownloadRejected(t *testing.T) {
	data := bytes.Repeat([]byte("nabu"), 10_000)
	srv, _ := fakeWikimedia(t, data, []byte("i"), len(data), true)
	defer srv.Close()
	rel, err := Latest(context.Background(), srv.Client(), srv.URL, "xxwiki")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m := NewManager(drive.ForLibrary(dir), srv.Client())
	m.Computer = t.TempDir()
	m.Start(rel)
	deadline := time.Now().Add(10 * time.Second)
	for m.Busy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if st := m.Status(); st.State != StateFailed || !strings.Contains(st.Error, "damaged") {
		t.Fatalf("status %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "xxwiki-20261101-pages-articles-multistream.xml.bz2")); !os.IsNotExist(err) {
		t.Fatal("damaged file was installed")
	}
}

func TestUnfinishedDump(t *testing.T) {
	_, err := parseStatus([]byte(`{"jobs":{"articlesmultistreamdump":{"status":"in-progress","files":{}}}}`), "xxwiki", "20261101", "https://x")
	if err == nil || !strings.Contains(err.Error(), "not finished") {
		t.Fatalf("got %v", err)
	}
	if _, err := Latest(context.Background(), http.DefaultClient, "https://x", "../etc"); err == nil {
		t.Fatal("bad wiki name accepted")
	}
}

func TestLanguagesAndBytes(t *testing.T) {
	if len(Languages) != 10 {
		t.Fatalf("%d languages", len(Languages))
	}
	for _, l := range Languages {
		if !wikiRe.MatchString(l.Wiki) || l.Wiki != strings.ReplaceAll(l.Code, "-", "_")+"wiki" {
			t.Errorf("bad wiki for %+v", l)
		}
	}
	for n, want := range map[int64]string{999: "999 B", 1536: "1.5 KB", 25 << 30: "25.0 GB", 4_000_000_000: "3.7 GB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("%d: %s want %s", n, got, want)
		}
	}
}

// A real dump downloads to the computer and arrives on the drive without
// the pages NABU never shows; nothing is left on the computer.
func TestDownloadToComputerAndSlim(t *testing.T) {
	data, _ := os.ReadFile("../../testdata/testwiki-20261101-pages-articles-multistream.xml.bz2")
	index, _ := os.ReadFile("../../testdata/testwiki-20261101-pages-articles-multistream-index.txt.bz2")
	srv, _ := fakeWikimedia(t, data, index, 1<<30, false)
	defer srv.Close()
	rel, err := Latest(context.Background(), srv.Client(), srv.URL, "xxwiki")
	if err != nil {
		t.Fatal(err)
	}
	dir, computer := t.TempDir(), t.TempDir()
	m := NewManager(drive.ForLibrary(dir), srv.Client())
	m.Computer = computer
	var asked int64
	m.MakeRoom = func(wiki string, need int64) error { asked = need; return nil }
	done := make(chan struct{})
	m.OnFinish = func(*Release) { close(done) }
	if err := m.Start(rel); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout: %+v", m.Status())
	}
	for m.Busy() {
		time.Sleep(5 * time.Millisecond)
	}
	if st := m.Status(); st.State != StateDone || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
	if asked == 0 {
		t.Error("room on the drive was not asked for")
	}
	eds, _ := dump.Scan(dir)
	if len(eds) != 1 || eds[0].Size >= int64(len(data)+len(index)) {
		t.Fatalf("on the drive: %+v", eds)
	}
	if need, err := dump.NeedsSlim(eds[0]); err != nil || need {
		t.Fatalf("not slimmed: %v %v", need, err)
	}
	if ents, _ := os.ReadDir(computer); len(ents) != 0 {
		t.Fatalf("left on the computer: %v", ents)
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "..", ".work")); len(ents) > 1 {
		t.Fatalf("left in the drive's work folder: %v", ents)
	}
}
