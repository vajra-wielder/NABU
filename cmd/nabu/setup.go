package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/plan"
	"github.com/vajra-wielder/nabu/internal/server"
	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/timer"
	"github.com/vajra-wielder/nabu/internal/update"
)

// cmdSetup gets a NABU drive ready in one go, once the computer is
// online: the newest English Wikipedia (or those given with --wikis)
// downloaded and written to the drive without the pages NABU never
// shows, its title index and search inside articles, the Essentials
// with their pictures, and the main pictures started. Every step is
// skipped when already done, so running it again (after an interruption,
// or next month) carries on or updates. At the end NABU starts, so the
// pictures keep coming in while it runs.
func cmdSetup(args []string) error {
	o, _, err := parse("setup", args)
	if err != nil {
		return err
	}
	d := findDrive(o)
	if done, err := runFromCopy(d.Root); done {
		return err
	}
	var wikis []string
	for _, w := range strings.Split(o.wikis, ",") {
		if w = strings.TrimSpace(w); w != "" {
			wikis = append(wikis, wikiArg(w))
		}
	}
	if len(wikis) == 0 {
		wikis = []string{"enwiki"}
	}
	os.MkdirAll(d.Work, 0o755)
	logf, _ := os.OpenFile(filepath.Join(d.Work, "setup-log.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if logf != nil {
		defer logf.Close()
	}
	say := func(format string, a ...interface{}) {
		line := fmt.Sprintf(format, a...)
		fmt.Println(line)
		if logf != nil {
			fmt.Fprintf(logf, "%s  %s\n", time.Now().Format("2006-01-02 15:04"), line)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	release := sysutil.KeepAwake() // until NABU starts at the end
	defer release()
	start := time.Now()
	var took []string
	step := func(name string, f func() error) error {
		say("")
		say("== %s", name)
		t0 := time.Now()
		err := f()
		if ctx.Err() != nil {
			return errStopped
		}
		if err != nil {
			say("   Stopped: %v", err)
			return err
		}
		took = append(took, fmt.Sprintf("  %-46s %s", name, timer.Elapsed(time.Since(t0))))
		return nil
	}

	say("NABU setup on %s, for %s.", d.Root, strings.Join(wikis, ", "))
	say("Leave this window open. You can stop it (Ctrl+C or close the window) and run it again later: it carries on.")
	say("Roughly, for English on a 4-core laptop: downloading 25 GB (about 1 hour at 50 Mbit/s,")
	say("6 hours at 10 Mbit/s), writing it to the drive about 2 hours, title index about 15 minutes,")
	say("search inside articles about 2.5 hours, Essentials a few minutes. The pictures then")
	say("take about a week of NABU running online, and continue whenever it runs.")

	client := httpClient()
	releases := map[string]*update.Release{}
	err = step("1. Checking the connection to Wikimedia", func() error {
		for _, w := range wikis {
			cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			rel, err := update.Latest(cctx, client, o.mirror, w)
			cancel()
			if err != nil {
				return fmt.Errorf("cannot reach Wikimedia (%v). Connect to the internet and run this again", err)
			}
			releases[w] = rel
			say("   %s: newest dump of %s, %s to download", w, dump.HumanDate(rel.Date), update.HumanBytes(rel.Size()))
		}
		return nil
	})
	if err != nil {
		return finish(say, err, took, start)
	}

	for i, w := range wikis {
		rel := releases[w]
		err = step(fmt.Sprintf("2.%d %s: the newest dump on the drive", i+1, w), func() error {
			return setupDump(ctx, d, client, rel, say)
		})
		if err != nil {
			return finish(say, err, took, start)
		}
	}

	eds, _ := dump.Scan(d.Wikipedia)
	newest := map[string]*dump.Files{}
	for _, f := range eds {
		if n := newest[f.DB]; n == nil || f.Date > n.Date {
			newest[f.DB] = f
		}
	}
	for i, w := range wikis {
		f := newest[w]
		if f == nil {
			continue
		}
		err = step(fmt.Sprintf("3.%d %s: title index", i+1, w), func() error {
			rd, err := openForScan(d, f)
			if err == nil {
				say("   ready: %s pages", humanInt(int64(rd.Index.Meta.Records)))
				rd.Close()
			}
			return err
		})
		if err != nil {
			return finish(say, err, took, start)
		}
	}
	// Search inside articles for the first language (the one read most).
	if f := newest[wikis[0]]; f != nil {
		err = step("4. "+wikis[0]+": search inside articles", func() error {
			return prepareFullText(d, f)
		})
		if err != nil {
			return finish(say, err, took, start)
		}
	}

	// The Essentials and the pictures, through NABU itself.
	pics := pictures.NewService(d.Pictures, pictures.NewFetcher(client, update.UserAgent))
	pics.SetLimit(update.LoadSettings(d.Personal).PictureLimit())
	lib, err := library.OpenDrive(d, library.Config{Pictures: pics, Online: func() bool { return true }})
	if err != nil {
		return finish(say, err, took, start)
	}
	srv, err := server.New(lib, &update.Checker{Dir: d.Work, Client: client}, update.NewManager(d, client))
	if err != nil {
		lib.Close()
		return finish(say, err, took, start)
	}
	srv.Offline = false
	err = step("5. Essentials: 268 articles with their pictures", func() error {
		var pr *progress
		what := ""
		missing, err := srv.MakeEssentials(ctx, func(w string, done, total int) {
			if w != what {
				if pr != nil {
					t := pr.done()
					say("   %s: %s", what, t)
				}
				what, pr = w, newProgress(w)
			}
			pr.update(int64(done), int64(total))
		})
		if pr != nil {
			t := pr.done()
			say("   %s: %s", what, t)
		}
		if len(missing) > 0 {
			say("   not in this Wikipedia: %s", strings.Join(missing, ", "))
		}
		return err
	})
	if err != nil {
		lib.Close()
		return finish(say, err, took, start)
	}
	err = step("6. Pictures: the main picture of each article", func() error {
		db := wikis[0]
		if db != "enwiki" && lib.Get("enwiki") != nil {
			db = "enwiki"
		}
		if err := lib.StartPictures(db, pictures.Lead); err != nil {
			return err
		}
		if w, ok := plan.Find(db); ok {
			lead, _ := pictures.Estimate(db, w.Articles, false)
			say("   %s pictures, about %s, longest articles first, up to the %d GB set aside (Updates & settings).",
				humanInt(lead.Pictures), update.HumanBytes(lead.Bytes), update.LoadSettings(d.Personal).PictureLimitGB)
			say("   At about %s pictures an hour that is %s of NABU running online.", humanInt(int64(pictures.PerHour)),
				timer.Left(time.Duration(lead.Hours*float64(time.Hour))))
		}
		say("   Started; it continues by itself whenever NABU runs while online.")
		return nil
	})
	lib.Close()
	if err != nil {
		return finish(say, err, took, start)
	}
	finish(say, nil, took, start)
	release() // NABU keeps the computer awake itself while it works

	// NABU itself, so the pictures keep coming in.
	say("")
	say("Starting NABU (close this window or click Turn off NABU to stop it).")
	serve := []string{"--drive", d.Root, "--port", fmt.Sprint(o.port), "--mirror", o.mirror}
	if o.noBrowser {
		serve = append(serve, "--no-browser")
	}
	if o.lan {
		serve = append(serve, "--lan")
	}
	return cmdServe(serve)
}

var errStopped = errors.New("stopped")

func finish(say func(string, ...interface{}), err error, took []string, start time.Time) error {
	say("")
	if len(took) > 0 {
		say("Time taken:")
		for _, t := range took {
			say("%s", t)
		}
		say("  %-46s %s", "In all", timer.Elapsed(time.Since(start)))
	}
	switch {
	case errors.Is(err, errStopped):
		say("Stopped. Run setup again to carry on from here.")
		return nil
	case err != nil:
		say("Setup did not finish: %v", err)
		say("Run it again to carry on; what is done stays done.")
		return err
	}
	say("Setup is done.")
	return nil
}

// setupDump makes sure the newest edition of a wiki is on the drive,
// without the pages NABU never shows: downloaded to this computer and
// written to the drive slimmed (the older edition is removed once the
// new one is there), or, when it is there already, slimmed in place if
// it still needs it.
func setupDump(ctx context.Context, d *drive.Layout, client *http.Client, rel *update.Release, say func(string, ...interface{})) error {
	eds, _ := dump.Scan(d.Wikipedia)
	for _, f := range eds {
		if f.DB == rel.Wiki && f.Date == rel.Date {
			say("   %s is on the drive already.", rel.Key())
			return slimOne(ctx, d, f)
		}
	}
	m := update.NewManager(d, client)
	m.MakeRoom = func(wiki string, need int64) error { return removeOlder(d, wiki, rel.Date, need, say) }
	if m.Computer != "" {
		say("   Downloading to this computer (%s), then writing it to the drive.", m.Computer)
	}
	if err := m.Start(rel); err != nil {
		return err
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	dl, slim := newProgress("downloading"), newProgress("writing to the drive")
	state := ""
	for {
		select {
		case <-ctx.Done():
			m.Cancel()
			for m.Busy() {
				time.Sleep(100 * time.Millisecond)
			}
			return ctx.Err()
		case <-tick.C:
		}
		st := m.Status()
		if st.State != state {
			if state == update.StateDownloading {
				t := dl.done()
				say("   downloaded in %s", t)
			}
			state = st.State
		}
		switch st.State {
		case update.StateDownloading, update.StateChecking, update.StateRetrying:
			dl.label = fmt.Sprintf("downloading %s of %s, %s/s", update.HumanBytes(st.Done), update.HumanBytes(st.Total), update.HumanBytes(int64(st.Speed)))
			if st.State == update.StateRetrying {
				dl.label = "connection lost, trying again"
			}
			dl.update(st.Done, st.Total)
		case update.StateSlimming:
			slim.update(st.Done, st.Total)
		case update.StateDone:
			if slim.width > 0 {
				t := slim.done()
				say("   written to the drive in %s", t)
			}
			// The older editions of this wiki are no longer needed.
			return removeOlder(d, rel.Wiki, rel.Date, -1, say)
		case update.StateFailed:
			dl.done()
			return errors.New(st.Error)
		}
	}
}

// removeOlder removes editions of a wiki older than date: all of them
// (need < 0), or, oldest first, until the drive has need bytes free.
func removeOlder(d *drive.Layout, wiki, date string, need int64, say func(string, ...interface{})) error {
	eds, _ := dump.Scan(d.Wikipedia)
	sort.Slice(eds, func(i, j int) bool { return eds[i].Date < eds[j].Date })
	for _, f := range eds {
		if f.DB != wiki || f.Date >= date {
			continue
		}
		if need >= 0 {
			if free, err := sysutil.FreeSpace(d.Wikipedia); err != nil || free >= need {
				return nil
			}
		}
		for _, p := range f.Pairs {
			for _, x := range append(append([]string{}, p.Data...), p.Index) {
				os.Remove(x)
			}
		}
		os.Remove(d.TitleIndex(f.Key()))
		os.Remove(d.FullText(f.Key()))
		say("   removed the older %s from the drive", f.Key())
	}
	if need >= 0 {
		if free, err := sysutil.FreeSpace(d.Wikipedia); err == nil && free < need {
			return fmt.Errorf("not enough space on the drive: %s needed, %s free", update.HumanBytes(need), update.HumanBytes(free))
		}
	}
	return nil
}

func humanInt(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
