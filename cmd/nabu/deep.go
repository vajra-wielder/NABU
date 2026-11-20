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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/fulltext"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/plan"
	"github.com/vajra-wielder/nabu/internal/share"
	"github.com/vajra-wielder/nabu/internal/sysutil"
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

func cmdPlan(args []string) error {
	o, fs, err := parse("plan", args)
	if err != nil {
		return err
	}
	second := ""
	if fs.NArg() > 0 {
		w, ok := plan.Find(fs.Arg(0))
		if !ok {
			return fmt.Errorf("unknown Wikipedia %q", fs.Arg(0))
		}
		second = w.DB
	}
	p := plan.Default(second, int64(o.sizeGB)*1e9)
	fmt.Printf("A %d GB drive:\n\n%s\n", o.sizeGB, p)
	fmt.Printf("Room set aside for pictures: %d GB (Updates & settings), longest articles first.\n", p.PictureLimit/1e9)
	fmt.Printf("Free after all that: %.1f GB.\n", float64(int64(o.sizeGB)*1e9-p.Total())/1e9)
	return nil
}

// makeRoom frees room on the drive for a new edition of wiki: when there
// is not enough, older editions of that wiki are removed, oldest first
// (a 125 GB drive keeps no room spare for a second English). The wiki
// cannot be read from then until the new edition is in place.
func makeRoom(lib *library.Library, wiki string, need int64) error {
	free := func() int64 {
		n, err := sysutil.FreeSpace(lib.Dir)
		if err != nil {
			return need // cannot tell: try
		}
		return n
	}
	if free() >= need {
		return nil
	}
	var old []*library.Book
	for _, b := range lib.Books() {
		if b.Files.DB == wiki {
			old = append(old, b)
		}
	}
	sort.Slice(old, func(i, j int) bool { return old[i].Files.Date < old[j].Files.Date })
	for _, b := range old {
		if err := lib.Remove(b.ID); err != nil {
			return fmt.Errorf("making room for the new %s: %w", wiki, err)
		}
		if free() >= need {
			return nil
		}
	}
	return fmt.Errorf("not enough space on the drive: %s needed, %s free", update.HumanBytes(need), update.HumanBytes(free()))
}

// cmdSlim leaves the pages NABU never shows out of the dumps on the drive.
func cmdSlim(args []string) error {
	o, fs, err := parse("slim", args)
	if err != nil {
		return err
	}
	d := findDrive(o)
	want := map[string]bool{}
	for _, a := range fs.Args() {
		want[wikiArg(a)] = true
	}
	eds, _ := dump.Scan(d.Wikipedia)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for _, f := range eds {
		if len(want) > 0 && !want[f.DB] {
			continue
		}
		need, err := dump.NeedsSlim(f)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Key(), err)
		}
		if !need {
			fmt.Printf("%s: already without the pages NABU never shows\n", f.Key())
			continue
		}
		// Written next to the original when the drive has room, else on
		// this computer and then copied over the original.
		room := f.Size*9/10 + 64<<20
		dir := d.Wikipedia
		if free, err := sysutil.FreeSpace(dir); err == nil && free < room {
			dir = filepath.Join(update.ComputerIncoming(), "slim-"+f.Key())
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			if free, err := sysutil.FreeSpace(dir); err == nil && free < room {
				return fmt.Errorf("%s: needs %s free on the drive or on this computer", f.Key(), update.HumanBytes(room))
			}
		}
		fmt.Printf("%s: leaving out the pages NABU never shows (%s)…\n", f.Key(), update.HumanBytes(f.Size))
		remove := func() error {
			for _, p := range f.Pairs {
				for _, x := range append(append([]string{}, p.Data...), p.Index) {
					if err := os.Remove(x); err != nil {
						return err
					}
				}
			}
			return nil
		}
		last := time.Now()
		res, err := dump.Slim(f, dir, dump.SlimOptions{PartSize: update.PartSize, Stop: ctx.Done(),
			Progress: func(done, total int64) {
				if time.Since(last) > 2*time.Second {
					last = time.Now()
					fmt.Printf("\r  %.1f%%", 100*float64(done)/float64(total))
				}
			}}, func() error {
			if dir == d.Wikipedia {
				return remove()
			}
			return nil
		})
		fmt.Print("\r        \r")
		if err != nil {
			return fmt.Errorf("%s: %w (the dump is unchanged)", f.Key(), err)
		}
		if dir != d.Wikipedia {
			if err := remove(); err != nil {
				return err
			}
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				if err := update.CopyFile(filepath.Join(dir, e.Name()), filepath.Join(d.Wikipedia, e.Name())); err != nil {
					return fmt.Errorf("copying %s to the drive: %w", e.Name(), err)
				}
			}
		}
		fmt.Printf("%s: %s to %s; %d of %d pages kept. Its indexes are rebuilt when NABU next starts.\n",
			f.Key(), update.HumanBytes(res.Before), update.HumanBytes(res.After), res.Kept, res.Pages)
	}
	return nil
}

// cmdCheck reads every bundle of the dumps on the drive and repairs the
// damaged ones from another NABU drive attached with the same dump.
func cmdCheck(args []string) error {
	o, fs, err := parse("check", args)
	if err != nil {
		return err
	}
	d := findDrive(o)
	want := map[string]bool{}
	for _, a := range fs.Args() {
		want[wikiArg(a)] = true
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	eds, _ := dump.Scan(d.Wikipedia)
	damagedAll := 0
	for _, f := range eds {
		if len(want) > 0 && !want[f.DB] {
			continue
		}
		rd, err := openForScan(d, f)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Key(), err)
		}
		last, shown := time.Now(), false
		res, err := rd.Check(dump.CheckOptions{Stop: ctx.Done(), Progress: func(done, total int64) {
			if time.Since(last) > 2*time.Second {
				last, shown = time.Now(), true
				fmt.Printf("\r  %s: %.1f%%", f.Key(), 100*float64(done)/float64(total))
			}
		}})
		rd.Close()
		if shown {
			fmt.Print("\r                                \r")
		}
		if err != nil {
			return err
		}
		if res.Index != "" {
			fmt.Printf("%s: the title index is damaged (%s); it is built again when NABU next starts.\n", f.Key(), res.Index)
			os.Remove(d.TitleIndex(f.Key()))
		}
		if len(res.Damaged) == 0 {
			fmt.Printf("%s: all %d bundles are intact.\n", f.Key(), res.Streams)
			continue
		}
		fmt.Printf("%s: %d of %d bundles are damaged. Articles in them:\n", f.Key(), len(res.Damaged), res.Streams)
		for _, dm := range res.Damaged {
			fmt.Printf("  %s\n", strings.Join(dm.Titles, ", "))
		}
		copies := drive.OtherCopies(f, d.Wikipedia)
		if len(copies) == 0 {
			fmt.Println("  To repair them, plug in another NABU drive with the same dump and run this again.")
			damagedAll += len(res.Damaged)
			continue
		}
		n, err := dump.Repair(f, res.Damaged, copies[0])
		if err != nil {
			return err
		}
		fmt.Printf("  Repaired %d of them from %s.\n", n, filepath.Dir(copies[0].Pairs[0].Index))
		damagedAll += len(res.Damaged) - n
	}
	if damagedAll > 0 {
		return fmt.Errorf("%d bundles are still damaged", damagedAll)
	}
	return nil
}
