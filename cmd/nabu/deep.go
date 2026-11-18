package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/fulltext"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

// removeIndexes deletes every index NABU built (like v0.1's
// "run.bat --rebuild" deleting its database) and returns the editions
// that had search inside articles, so it can be rebuilt too.
func removeIndexes(dir string) (hadFullText []string) {
	idx := filepath.Join(dir, ".nabu", "index")
	ents, _ := os.ReadDir(idx)
	for _, e := range ents {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".nfts"):
			hadFullText = append(hadFullText, strings.TrimSuffix(name, ".nfts"))
			os.Remove(filepath.Join(idx, name))
		case strings.HasSuffix(name, ".nidx"), strings.HasSuffix(name, ".tmp"):
			os.Remove(filepath.Join(idx, name))
		}
	}
	os.RemoveAll(filepath.Join(dir, ".nabu", "tmp"))
	fmt.Println("  Rebuilding the indexes of every Wikipedia on the drive.")
	return hadFullText
}

// rebuildFullText waits for the rebuilt title indexes, then queues the
// full-text indexes that existed before.
func rebuildFullText(lib *library.Library, keys []string) {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	for len(want) > 0 {
		for _, b := range lib.Books() {
			if want[b.Files.Key()] && b.Reader() != nil {
				lib.BuildFullText(b.ID)
				delete(want, b.Files.Key())
			}
		}
		time.Sleep(time.Second)
	}
}

func openForScan(dir string, f *dump.Files) (*dump.Reader, error) {
	out := filepath.Join(dir, ".nabu", "index", f.Key()+".nidx")
	ix, err := dump.OpenIndex(out, f)
	if err != nil {
		os.MkdirAll(filepath.Dir(out), 0o755)
		tmp := filepath.Join(dir, ".nabu", "tmp", f.Key())
		fmt.Printf("%s: preparing the title index first\n", f.Key())
		err = dump.Build(f, out, tmp, library.BuildBudget(), nil)
		os.RemoveAll(tmp)
		if err != nil {
			return nil, err
		}
		if ix, err = dump.OpenIndex(out, f); err != nil {
			return nil, err
		}
	}
	return dump.NewReader(f, ix)
}

func prepareFullText(dir string, f *dump.Files) error {
	out := filepath.Join(dir, ".nabu", "index", f.Key()+".nfts")
	if ix, err := fulltext.Open(out, f); err == nil {
		fmt.Printf("%s: search inside articles ready (%d articles)\n", f.Key(), ix.Meta.Docs)
		ix.Close()
		return nil
	}
	rd, err := openForScan(dir, f)
	if err != nil {
		return err
	}
	defer rd.Close()
	start := time.Now()
	tmp := filepath.Join(dir, ".nabu", "tmp", f.Key()+"-fts")
	err = fulltext.Build(rd, out, tmp, fulltext.Options{
		MemBudget: library.BuildBudget(),
		Progress: func(done, total int64) {
			if done%50 == 0 || done == total {
				el := time.Since(start)
				eta := time.Duration(float64(el) * float64(total-done) / float64(done))
				fmt.Printf("\r%s: search inside articles %5.1f%%  (about %v left)   ", f.Key(), 100*float64(done)/float64(total), eta.Round(time.Second))
			}
		},
	})
	os.RemoveAll(tmp)
	if err != nil {
		fmt.Println()
		return err
	}
	fmt.Printf("\r%s: search inside articles built in %v                    \n", f.Key(), time.Since(start).Round(time.Second))
	return nil
}

// exportRecord is one article in `nabu export`: the fields of NABU
// v0.1's eight database tables, one JSON object per line.
type exportRecord struct {
	ID     uint64 `json:"id"`
	Edited string `json:"edited"`
	*wikitext.PageData
	Text string `json:"text,omitempty"`
}

// cmdExport writes the structured data of every article of a wiki.
func cmdExport(args []string) error {
	o, fs, err := parse("export", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: nabu export <wiki> [--out FILE] [--text], e.g. nabu export enwiki")
	}
	want := wikiArg(fs.Arg(0))
	dir := findLibrary(o.library)
	eds, _ := dump.Scan(dir)
	var f *dump.Files
	for _, e := range eds { // newest first
		if e.DB == want {
			f = e
			break
		}
	}
	if f == nil {
		return fmt.Errorf("no %s dump in %s", want, dir)
	}
	rd, err := openForScan(dir, f)
	if err != nil {
		return err
	}
	defer rd.Close()
	outPath := o.out
	if outPath == "" {
		outPath = filepath.Join(dir, f.Key()+"-pages.jsonl")
	}
	file, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer file.Close()
	w := bufio.NewWriterSize(file, 1<<20)
	site := rd.Site()
	total := rd.Index.StreamCount()
	workers := runtime.NumCPU()

	type result struct {
		s     int
		lines [][]byte
		err   error
	}
	jobs := make(chan int, workers*2)
	results := make(chan result, workers*2)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				pages, err := rd.StreamPages(uint32(s))
				r := result{s: s, err: err}
				for _, p := range pages {
					if p.NS != dump.NSMain || p.Redirect != "" {
						continue
					}
					rec := exportRecord{ID: p.ID, Edited: p.Timestamp, PageData: wikitext.Extract(p.Title, p.Text, site)}
					if o.withText {
						rec.Text = strings.Join(strings.Fields(wikitext.PlainText(p.Text, site)), " ")
					}
					b, _ := json.Marshal(rec)
					r.lines = append(r.lines, b)
				}
				results <- r
			}
		}()
	}
	go func() {
		for s := 0; s < total; s++ {
			jobs <- s
		}
		close(jobs)
	}()
	go func() { wg.Wait(); close(results) }()
	pending := map[int]result{}
	next, articles := 0, 0
	start := time.Now()
	for r := range results {
		pending[r.s] = r
		for {
			r, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			next++
			if r.err != nil {
				return fmt.Errorf("stream %d: %w", r.s, r.err)
			}
			for _, l := range r.lines {
				w.Write(l)
				w.WriteByte('\n')
				articles++
			}
			if next%200 == 0 || next == total {
				fmt.Printf("\rExporting %s: %5.1f%%, %d articles", f.Key(), 100*float64(next)/float64(total), articles)
			}
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nWrote %d articles to %s in %v\n", articles, outPath, time.Since(start).Round(time.Second))
	return nil
}
