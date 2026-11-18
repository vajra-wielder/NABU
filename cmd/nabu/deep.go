package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/fulltext"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/share"
	"github.com/vajra-wielder/nabu/internal/update"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

// removeIndexes deletes every index NABU built (like v0.1's
// "run.bat --rebuild" deleting its database) and returns the editions
// that had search inside articles, so it can be rebuilt too.
func removeIndexes(d *drive.Layout) (hadFullText []string) {
	for _, dir := range []string{d.Indexes, d.Search} {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			name := e.Name()
			switch {
			case strings.HasSuffix(name, ".nfts"):
				hadFullText = append(hadFullText, strings.TrimSuffix(name, ".nfts"))
				os.Remove(filepath.Join(dir, name))
			case strings.HasSuffix(name, ".nidx"), strings.HasSuffix(name, ".tmp"):
				os.Remove(filepath.Join(dir, name))
			}
		}
	}
	os.RemoveAll(filepath.Join(d.Work, "tmp"))
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

func openForScan(d *drive.Layout, f *dump.Files) (*dump.Reader, error) {
	out := d.TitleIndex(f.Key())
	ix, err := dump.OpenIndex(out, f)
	if err != nil {
		os.MkdirAll(filepath.Dir(out), 0o755)
		tmp := d.Temp(f.Key())
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

func prepareFullText(d *drive.Layout, f *dump.Files) error {
	out := d.FullText(f.Key())
	os.MkdirAll(d.Search, 0o755)
	if ix, err := fulltext.Open(out, f); err == nil {
		fmt.Printf("%s: search inside articles ready (%d articles)\n", f.Key(), ix.Meta.Docs)
		ix.Close()
		return nil
	}
	rd, err := openForScan(d, f)
	if err != nil {
		return err
	}
	defer rd.Close()
	start := time.Now()
	tmp := d.Temp(f.Key() + "-fts")
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
	d := findDrive(o)
	dir := d.Wikipedia
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
	rd, err := openForScan(d, f)
	if err != nil {
		return err
	}
	defer rd.Close()
	outPath := o.out
	if outPath == "" {
		outPath = filepath.Join(d.Root, f.Key()+"-pages.jsonl")
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

// cmdPictures keeps a wiki's pictures on the drive for reading offline.
func cmdPictures(args []string) error {
	o, fs, err := parse("pictures", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: nabu pictures <wiki> [--all], e.g. nabu pictures en")
	}
	db := wikiArg(fs.Arg(0))
	mode := pictures.Lead
	if o.all {
		mode = pictures.All
	}
	d := findDrive(o)
	pics := pictures.NewService(d.Pictures, pictures.NewFetcher(httpClient(), update.UserAgent))
	lib, err := library.OpenDrive(d, library.Config{Pictures: pics, Online: func() bool { return true }})
	if err != nil {
		return err
	}
	defer lib.Close()
	for i := 0; ; i++ {
		var found, ready bool
		for _, b := range lib.Books() {
			if b.Files.DB == db {
				found = true
				ready = ready || b.Reader() != nil
			}
		}
		if !found {
			return fmt.Errorf("no %s dump in %s", db, d.Wikipedia)
		}
		if ready {
			break
		}
		if i == 0 {
			fmt.Printf("%s: preparing the title index first\n", db)
		}
		time.Sleep(time.Second)
	}
	if err := lib.StartPictures(db, mode); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("Keeping the pictures of %s on the drive (%s). Ctrl+C pauses; run again to go on.\n", db, map[string]string{
		pictures.Lead: "the main picture of each article", pictures.All: "every picture"}[mode])
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nPaused; NABU goes on from here next time.")
			return nil
		case <-tick.C:
		}
		st := lib.PictureStatus(db)
		pct := 0.0
		if st.Total > 0 {
			pct = 100 * float64(st.Done) / float64(st.Total)
		}
		line := fmt.Sprintf("\r  %5.1f%%  %d pictures, %s", pct, st.Pictures, update.HumanBytes(st.Bytes))
		if st.ETA > 0 {
			line += fmt.Sprintf(", about %v left", st.ETA.Round(time.Minute))
		}
		if st.Waiting != "" {
			line += " (" + st.Waiting + ")"
		}
		fmt.Printf("%-100s", line)
		switch st.State {
		case library.PicDone:
			fmt.Println("\nDone.")
			return nil
		case library.PicFailed:
			fmt.Println()
			return errors.New(st.Err)
		}
	}
}

// cmdCopy copies NABU (the parts asked for) into another folder.
func cmdCopy(args []string) error {
	o, _, err := parse("copy", args)
	if err != nil {
		return err
	}
	if o.to == "" {
		return errors.New("usage: nabu copy --to <folder> [--wikis en,fr] [--systems windows,mac,linux] [--without pictures,search] [--with personal]")
	}
	d := findDrive(o)
	list := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, x := range strings.Split(s, ",") {
			if x = strings.TrimSpace(strings.ToLower(x)); x != "" {
				m[x] = true
			}
		}
		return m
	}
	without, with := list(o.without), list(o.with)
	opt := share.Options{Indexes: !without["indexes"], Search: !without["search"], Pictures: !without["pictures"],
		Collections: !without["collections"], Personal: with["personal"], Source: with["source"]}
	for s := range list(o.systems) {
		opt.Systems = append(opt.Systems, s)
	}
	parts, files := share.Inventory(d)
	want := list(o.wikis)
	for _, p := range parts {
		if db := strings.TrimPrefix(p.Key, "wiki:"); db != p.Key && (len(want) == 0 || want[db] || want[strings.TrimSuffix(db, "wiki")]) {
			opt.Wikis = append(opt.Wikis, db)
		}
	}
	if len(opt.Wikis) == 0 {
		return errors.New("no such Wikipedia on this drive")
	}
	sel := share.Select(files, opt)
	fmt.Printf("Copying %s (%s) into %s\n", strings.Join(opt.Wikis, ", "), update.HumanBytes(share.Total(sel)), o.to)
	var c share.Copier
	if err := c.Start(d, o.to, sel); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			c.Cancel()
		case <-time.After(time.Second):
		}
		st := c.Status()
		fmt.Printf("\r  %5.1f%%  %s of %s   ", 100*float64(st.Done)/float64(max64(st.Total, 1)), update.HumanBytes(st.Done), update.HumanBytes(st.Total))
		switch st.State {
		case "done":
			fmt.Println("\nDone. The copy starts like this one.")
			return nil
		case "failed":
			fmt.Println()
			return errors.New(st.Err)
		case "cancelled":
			fmt.Println("\nStopped. Run the same command again to go on.")
			return nil
		}
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
