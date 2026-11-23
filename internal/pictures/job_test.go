package pictures

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vajra-wielder/nabu/internal/bzip2w"
	"github.com/vajra-wielder/nabu/internal/dump"
)

func testReader(t *testing.T) *dump.Reader {
	t.Helper()
	eds, _ := dump.Scan("../../testdata")
	for _, f := range eds {
		if f.DB != "testwiki" {
			continue
		}
		out := filepath.Join(t.TempDir(), "t.nidx")
		if err := dump.Build(f, out, t.TempDir(), 0, nil); err != nil {
			t.Fatal(err)
		}
		ix, err := dump.OpenIndex(out, f)
		if err != nil {
			t.Fatal(err)
		}
		rd, err := dump.NewReader(f, ix)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rd.Close() })
		return rd
	}
	t.Fatal("no testwiki")
	return nil
}

// spreadReader is a dump of 40 streams with one picture each, as in a
// pass over long articles where most streams hold one or two.
func spreadReader(t *testing.T) *dump.Reader {
	t.Helper()
	dir := t.TempDir()
	var data, index bytes.Buffer
	data.Write(bzip2w.Compress([]byte(`<mediawiki xml:lang="en"><siteinfo><sitename>Wikipedia</sitename><dbname>testwiki</dbname>` +
		`<base>https://en.wikipedia.org/wiki/Main_Page</base><case>first-letter</case><namespaces>` +
		`<namespace key="0" case="first-letter" /><namespace key="6" case="first-letter">File</namespace></namespaces></siteinfo>` + "\n")))
	for s := 0; s < 40; s++ {
		off := data.Len()
		var xml strings.Builder
		for p := 0; p < 3; p++ {
			id := s*3 + p + 1
			text := "Some article text."
			if p == 0 {
				text = fmt.Sprintf("[[File:Picture %d.jpg|thumb|A picture]] Text.", s)
			}
			fmt.Fprintf(&xml, "<page><title>Article %d</title><ns>0</ns><id>%d</id><revision><text>%s</text></revision></page>\n", id, id, text)
			fmt.Fprintf(&index, "%d:%d:Article %d\n", off, id, id)
		}
		data.Write(bzip2w.Compress([]byte(xml.String())))
	}
	data.Write(bzip2w.Compress([]byte("</mediawiki>\n")))
	name := filepath.Join(dir, "testwiki-20261101-pages-articles-multistream")
	os.WriteFile(name+".xml.bz2", data.Bytes(), 0o644)
	os.WriteFile(name+"-index.txt.bz2", bzip2w.Compress(index.Bytes()), 0o644)
	eds, problems := dump.Scan(dir)
	if len(eds) != 1 {
		t.Fatalf("scan: %v %d", problems, len(eds))
	}
	out := filepath.Join(t.TempDir(), "t.nidx")
	if err := dump.Build(eds[0], out, t.TempDir(), 0, nil); err != nil {
		t.Fatal(err)
	}
	ix, err := dump.OpenIndex(out, eds[0])
	if err != nil {
		t.Fatal(err)
	}
	rd, err := dump.NewReader(eds[0], ix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rd.Close() })
	return rd
}

// A job stops at the space limit, saving its place, before asking
// Wikimedia for anything.
func TestRunStopsAtTheLimit(t *testing.T) {
	rd := testReader(t)
	dir := t.TempDir()
	w, _ := Open(filepath.Join(dir, "testwiki"))
	c, _ := Open(filepath.Join(dir, Commons))
	defer w.Close()
	defer c.Close()
	j := &Job{Mode: All, Dump: rd.Files.Key()}
	err := Run(context.Background(), j, rd, Stores{Wiki: w, Shared: c}, nil, "test.wikipedia.org", nil,
		func() bool { return true }, func(Progress) {})
	if !errors.Is(err, ErrFull) || j.Done {
		t.Fatalf("got %v, done %v", err, j.Done)
	}
	saved, err := LoadJob(w.Dir)
	if err != nil || saved.Done || saved.Pass >= len(Tiers) {
		t.Fatalf("saved job %+v %v", saved, err)
	}
}

// Names from many streams share API requests: every request but the last
// of a pass asks about a full batch. (One request per stream made
// English Wikipedia's pictures take weeks.)
func TestRunBatchesAcrossStreams(t *testing.T) {
	rd := spreadReader(t)
	var mu sync.Mutex
	var sizes []int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/w/api.php" {
			t.Errorf("downloaded %s", r.URL.Path)
			return
		}
		mu.Lock()
		sizes = append(sizes, len(strings.Split(r.URL.Query().Get("titles"), "|")))
		mu.Unlock()
		w.Write([]byte(`{"query":{"pages":[]}}`)) // every picture missing
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), "NABU/test")
	dir := t.TempDir()
	w, _ := Open(filepath.Join(dir, "testwiki"))
	c, _ := Open(filepath.Join(dir, Commons))
	defer w.Close()
	defer c.Close()
	j := &Job{Mode: All, Dump: rd.Files.Key()}
	host := strings.TrimPrefix(srv.URL, "https://")
	if err := Run(context.Background(), j, rd, Stores{Wiki: w, Shared: c}, f, host, nil, nil, func(Progress) {}); err != nil {
		t.Fatal(err)
	}
	if !j.Done || j.Missing != 40 {
		t.Fatalf("job %+v", j)
	}
	small := 0
	for _, n := range sizes {
		if n < apiBatch {
			small++
		}
	}
	if small > len(Tiers) {
		t.Fatalf("%d requests for %d pictures, %d of them not full: %v", len(sizes), j.Missing, small, sizes)
	}
	t.Logf("%d pictures, %d streams, %d requests %v", j.Missing, j.Streams, len(sizes), sizes)
}
