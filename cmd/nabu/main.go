// NABU: plug-and-play offline Wikipedia, read straight from Wikimedia's
// compressed dumps.
//
// Usage:
//
//	nabu [flags]              start the library and open the browser
//	nabu catalog              newest dump of each offered Wikipedia
//	nabu download <wiki>      download a dump, e.g. "nabu download enwiki"
//	nabu prepare              build search indexes now (e.g. on a fast PC)
//	nabu info                 list the dumps in the library
//	nabu version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/server"
	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/update"
)

var version = "dev"

func main() {
	server.Version = version
	update.UserAgent = "NABU/" + version + " (offline Wikipedia reader; https://github.com/vajra-wielder/nabu)"

	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, rest := args[0], args[1:]
		var err error
		switch cmd {
		case "catalog", "catalogue":
			err = cmdCatalog(rest)
		case "download":
			err = cmdDownload(rest)
		case "prepare", "index":
			err = cmdPrepare(rest)
		case "info":
			err = cmdInfo(rest)
		case "export":
			err = cmdExport(rest)
		case "pictures":
			err = cmdPictures(rest)
		case "copy":
			err = cmdCopy(rest)
		case "version":
			fmt.Printf("NABU %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		case "about-folders": // used by scripts/build.sh
			var o *opts
			if o, _, err = parse("about-folders", rest); err == nil {
				drive.Find(o.drive, o.library).WriteNotes(true)
			}
		case "serve":
			err = cmdServe(rest)
		case "help":
			usage()
		default:
			err = fmt.Errorf("unknown command %q (try: nabu help)", cmd)
		}
		exit(err)
		return
	}
	exit(cmdServe(args))
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "NABU:", err)
		pauseIfDoubleClicked()
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`NABU: plug-and-play offline Wikipedia

  nabu [flags]             start NABU and open it in the browser
  nabu catalog             newest dump of each offered Wikipedia
  nabu download <wiki>     download a dump (enwiki, frwiki, ... or en, fr, ...)
  nabu prepare [--full-text]  build the title index of every dump now
                           (and search inside articles with --full-text)
  nabu export <wiki>       write every article's structured data as JSON
                           lines (links, categories, templates, places,
                           infoboxes, tables, media, glossary)
  nabu pictures <wiki> [--all]  keep a Wikipedia's pictures on the drive for
                           reading offline (main picture of each article,
                           or every picture with --all)
  nabu copy --to <folder>  copy NABU for someone else (see --wikis,
                           --systems, --without, --with); your saved
                           articles and history stay behind
  nabu info                list the dumps in the library
  nabu version             print the version

Flags:
`)
	fs := flag.NewFlagSet("nabu", flag.ContinueOnError)
	bindFlags(fs, &opts{})
	fs.PrintDefaults()
}

type opts struct {
	rebuild   bool
	window    bool
	fullText  bool
	out       string
	withText  bool
	all       bool
	to        string
	wikis     string
	systems   string
	without   string
	with      string
	library   string
	drive     string
	port      int
	lan       bool
	noBrowser bool
	offline   bool
	mirror    string
}

func bindFlags(fs *flag.FlagSet, o *opts) {
	fs.StringVar(&o.drive, "drive", "", "the NABU folder (default: found from where the program is)")
	fs.StringVar(&o.library, "library", "", "a folder of dumps to read instead of the NABU folder's wikipedia folder")
	fs.IntVar(&o.port, "port", 8080, "port to listen on; the next free one is used if taken")
	fs.BoolVar(&o.lan, "lan", false, "share the library with other devices on the local network")
	fs.BoolVar(&o.noBrowser, "no-browser", false, "do not open the web browser")
	fs.BoolVar(&o.offline, "offline", false, "never connect to the internet (no update checks, no pictures)")
	fs.StringVar(&o.mirror, "mirror", update.DefaultMirror, "Wikimedia dump server or mirror")
	fs.BoolVar(&o.rebuild, "rebuild", false, "delete and rebuild the indexes (after a damaged or changed dump)")
	fs.BoolVar(&o.window, "window", false, "open NABU in its own app window instead of a browser tab")
	fs.BoolVar(&o.fullText, "full-text", false, "prepare: also build search inside articles")
	fs.StringVar(&o.out, "out", "", "export: output file (default: <wiki>-<date>-pages.jsonl in the NABU folder)")
	fs.BoolVar(&o.withText, "text", false, "export: include each article's plain text")
	fs.BoolVar(&o.all, "all", false, "pictures: every picture, not only the main one of each article")
	fs.StringVar(&o.to, "to", "", "copy: the folder to copy NABU into")
	fs.StringVar(&o.wikis, "wikis", "", "copy: Wikipedias to include, e.g. en,fr (default: all)")
	fs.StringVar(&o.systems, "systems", "windows,mac,linux", "copy: computers the copy starts on")
	fs.StringVar(&o.without, "without", "", "copy: parts to leave out: indexes,search,pictures,collections")
	fs.StringVar(&o.with, "with", "", "copy: extra parts: personal,source")
}

func parse(name string, args []string) (*opts, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	o := &opts{}
	bindFlags(fs, o)
	fs.Usage = usage
	// Accept flags before or after other arguments
	// ("nabu download en --library x" as well as "nabu download --library x en").
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return o, fs, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	err := fs.Parse(append([]string{"--"}, pos...))
	return o, fs, err
}

// findDrive works out where NABU's folders are (the launchers pass
// --drive) and moves a drive set up by an earlier version to the current
// layout.
func findDrive(o *opts) *drive.Layout {
	d := drive.Find(o.drive, o.library)
	for _, n := range d.Migrate() {
		fmt.Println("  Updated the drive's layout:", n)
	}
	d.WriteNotes(false)
	return d
}

func httpClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 60 * time.Second
	return &http.Client{Transport: tr}
}

func cmdServe(args []string) error {
	o, _, err := parse("nabu", args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	d := findDrive(o)
	dir := d.Wikipedia

	// Started twice (a double double-click)? Just show the running one.
	if url, ok := alreadyRunning(o.port, dir); ok {
		fmt.Println("NABU is already running at", url)
		if !o.noBrowser {
			sysutil.OpenBrowser(url)
		}
		return nil
	}

	var rebuildFT []string
	if o.rebuild {
		rebuildFT = removeIndexes(d)
	}
	client := httpClient()
	pics := pictures.NewService(d.Pictures, pictures.NewFetcher(client, update.UserAgent))
	online := func() bool { return !o.offline && update.LoadSettings(d.Personal).Pictures }
	lib, err := library.OpenDrive(d, library.Config{Pictures: pics, Online: online})
	if err != nil {
		return fmt.Errorf("cannot open the library folder %s: %w", dir, err)
	}
	defer lib.Close()
	if len(rebuildFT) > 0 {
		go rebuildFullText(lib, rebuildFT)
	}

	checker := &update.Checker{Dir: d.Work, Mirror: o.mirror, Client: client}
	checker.Load()
	dl := update.NewManager(d, client)
	dl.OnFinish = func(*update.Release) { lib.Rescan() }

	srv, err := server.New(lib, checker, dl)
	if err != nil {
		return err
	}
	srv.Offline, srv.LAN = o.offline, o.lan

	host := "127.0.0.1"
	if o.lan {
		host = ""
	}
	ln, port, err := listen(host, o.port)
	if err != nil {
		return err
	}
	url := "http://localhost:" + strconv.Itoa(port) + "/"
	if o.lan {
		for _, ip := range sysutil.LANAddresses() {
			srv.Addrs = append(srv.Addrs, "http://"+ip+":"+strconv.Itoa(port)+"/")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.Shutdown = stop

	banner(url, lib, d.Root, srv.Addrs)
	go func() {
		time.Sleep(200 * time.Millisecond)
		if !o.noBrowser {
			open := sysutil.OpenBrowser
			if o.window {
				open = sysutil.OpenAppWindow
			}
			if err := open(url); err != nil {
				fmt.Println("  Open this address in your web browser:", url)
			}
		}
	}()
	if !o.offline {
		go startupCheck(ctx, lib, checker, dl, d)
	}
	err = srv.Run(ctx, ln)
	dl.Cancel()
	fmt.Println("\nNABU has stopped. It is safe to remove the drive.")
	return err
}

func listen(host string, port int) (net.Listener, int, error) {
	for p := port; p < port+20; p++ {
		if ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p))); err == nil {
			return ln, p, nil
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, 0, fmt.Errorf("cannot open a network port: %w", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

func alreadyRunning(port int, dir string) (string, bool) {
	c := &http.Client{Timeout: 400 * time.Millisecond}
	for p := port; p < port+5; p++ {
		resp, err := c.Get("http://127.0.0.1:" + strconv.Itoa(p) + "/nabu/api/ping")
		if err != nil {
			continue
		}
		var info struct{ App, Library string }
		json.NewDecoder(resp.Body).Decode(&info)
		resp.Body.Close()
		if info.App == "nabu" && info.Library == dir {
			return "http://localhost:" + strconv.Itoa(p) + "/", true
		}
	}
	return "", false
}

func banner(url string, lib *library.Library, dir string, addrs []string) {
	fmt.Println()
	fmt.Println("  ===============================================")
	fmt.Println("     NABU  -  Wikipedia, anywhere, offline")
	fmt.Println("  ===============================================")
	fmt.Println()
	fmt.Println("  Open in your browser:  ", url)
	for _, a := range addrs {
		fmt.Println("  On your network:       ", a)
	}
	fmt.Println("  NABU folder:           ", dir)
	books := lib.Books()
	if len(books) == 0 {
		fmt.Println("  Wikipedias:             none yet; the browser page will help you get one")
	}
	for _, b := range books {
		state := ""
		if b.Reader() == nil {
			state = "  (preparing for first use)"
		}
		fmt.Printf("  Wikipedia:              %s, dump of %s, %s%s\n", b.Files.DB, dump.HumanDate(b.Files.Date), update.HumanBytes(b.Files.Size), state)
	}
	for _, n := range lib.Notes {
		fmt.Println("  Note:                  ", n)
	}
	fmt.Println()
	fmt.Println("  Keep this window open while you read.")
	fmt.Println("  To stop NABU, close this window or press Ctrl+C.")
	fmt.Println()
}

// startupCheck asks Wikimedia whether newer dumps of the Wikipedias on
// the drive exist (the "ping on activation").
func startupCheck(ctx context.Context, lib *library.Library, c *update.Checker, dl *update.Manager, d *drive.Layout) {
	settings := update.LoadSettings(d.Personal)
	dir := d.Wikipedia
	if !settings.CheckOnStart {
		return
	}
	newest := map[string]string{}
	for _, b := range lib.Books() {
		if b.Files.Date > newest[b.Files.DB] {
			newest[b.Files.DB] = b.Files.Date
		}
	}
	if len(newest) == 0 {
		// Nothing installed yet: fetch the list so the page is ready.
		for _, l := range update.Languages {
			go c.Check(ctx, l.Wiki)
		}
		return
	}
	for db, have := range newest {
		res := c.Check(ctx, db)
		if res.Release == nil {
			if res.Err != "" {
				fmt.Println("  (Could not reach Wikimedia; reading works fine offline.)")
			}
			continue
		}
		if have == "latest" || res.Release.Date <= have {
			fmt.Printf("  %s is up to date (newest dump: %s).\n", db, dump.HumanDate(res.Release.Date))
			continue
		}
		fmt.Printf("  Newer dump of %s available: %s (%s). See \"Get & update\".\n", db,
			dump.HumanDate(res.Release.Date), update.HumanBytes(res.Release.Size()))
		if settings.AutoDownload && !dl.Busy() {
			if free, err := sysutil.FreeSpace(dir); err == nil && free < res.Release.Size()*102/100 {
				fmt.Println("  Not downloading automatically: not enough free space.")
				continue
			}
			if err := dl.Start(res.Release); err == nil {
				fmt.Println("  Downloading it now (automatic updates are on).")
			}
		}
	}
}

func wikiArg(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.HasSuffix(s, "wiki") && !strings.Contains(s, "wik") {
		s = strings.ReplaceAll(s, "-", "_") + "wiki"
	}
	return s
}

func cmdCatalog(args []string) error {
	o, fs, err := parse("catalog", args)
	if err != nil {
		return err
	}
	wikis := fs.Args()
	if len(wikis) == 0 {
		for _, l := range update.Languages {
			wikis = append(wikis, l.Wiki)
		}
	}
	client := httpClient()
	for _, w := range wikis {
		w = wikiArg(w)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		rel, err := update.Latest(ctx, client, o.mirror, w)
		cancel()
		if err != nil {
			fmt.Printf("%-14s  %v\n", w, err)
			continue
		}
		fmt.Printf("%-14s  %s  %9s\n", w, dump.HumanDate(rel.Date), update.HumanBytes(rel.Size()))
	}
	return nil
}

func cmdDownload(args []string) error {
	o, fs, err := parse("download", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: nabu download <wiki>, e.g. nabu download enwiki")
	}
	d := findDrive(o)
	dir := d.Wikipedia
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	client := httpClient()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	rel, err := update.Latest(cctx, client, o.mirror, wikiArg(fs.Arg(0)))
	cancel()
	if err != nil {
		return err
	}
	fmt.Printf("Downloading %s (dump of %s, %s) into %s\n", rel.Wiki, dump.HumanDate(rel.Date), update.HumanBytes(rel.Size()), dir)
	m := update.NewManager(d, client)
	if err := m.Start(rel); err != nil {
		return err
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			m.Cancel()
			fmt.Println("\nPaused. Run the same command again to resume.")
			for m.Busy() {
				time.Sleep(100 * time.Millisecond)
			}
			return nil
		case <-tick.C:
		}
		st := m.Status()
		pct := 0.0
		if st.Total > 0 {
			pct = 100 * float64(st.Done) / float64(st.Total)
		}
		fmt.Printf("\r  %-11s %5.1f%%  %s / %s  %s/s        ", st.State, pct,
			update.HumanBytes(st.Done), update.HumanBytes(st.Total), update.HumanBytes(int64(st.Speed)))
		switch st.State {
		case update.StateDone:
			fmt.Println("\nDone and verified. Run \"nabu prepare\" now, or NABU prepares it on next start.")
			return nil
		case update.StateFailed:
			fmt.Println()
			return errors.New(st.Error)
		}
	}
}

func cmdPrepare(args []string) error {
	o, _, err := parse("prepare", args)
	if err != nil {
		return err
	}
	d := findDrive(o)
	dir := d.Wikipedia
	eds, notes := dump.Scan(dir)
	for _, n := range notes {
		fmt.Println("Note:", n)
	}
	if len(eds) == 0 {
		return fmt.Errorf("no dumps found in %s", dir)
	}
	for _, f := range eds {
		out := d.TitleIndex(f.Key())
		if ix, err := dump.OpenIndex(out, f); err == nil {
			fmt.Printf("%s: ready (%d pages)\n", f.Key(), ix.Meta.Records)
			ix.Close()
			continue
		}
		os.MkdirAll(filepath.Dir(out), 0o755)
		start := time.Now()
		tmp := d.Temp(f.Key())
		err := dump.Build(f, out, tmp, library.BuildBudget(), func(done, total int64) {
			fmt.Printf("\r%s: preparing %5.1f%%", f.Key(), 100*float64(done)/float64(total))
		})
		os.RemoveAll(tmp)
		if err != nil {
			fmt.Println()
			return err
		}
		fmt.Printf("\r%s: prepared in %v                \n", f.Key(), time.Since(start).Round(time.Second))
	}
	if o.fullText {
		for _, f := range eds {
			if err := prepareFullText(d, f); err != nil {
				return err
			}
		}
	}
	return nil
}

func cmdInfo(args []string) error {
	o, _, err := parse("info", args)
	if err != nil {
		return err
	}
	d := findDrive(o)
	eds, notes := dump.Scan(d.Wikipedia)
	fmt.Println("NABU folder:", d.Root)
	for _, f := range eds {
		state := "not prepared yet"
		out := d.TitleIndex(f.Key())
		if ix, err := dump.OpenIndex(out, f); err == nil {
			si := ix.Site()
			state = fmt.Sprintf("%s, %s, %d pages (%d in main namespace), main page %q", si.Sitename, si.Lang, ix.Meta.Records, ix.Meta.Articles, si.MainPage())
			ix.Close()
		}
		fmt.Printf("  %s  %s  %d file(s), %s: %s\n", f.DB, dump.HumanDate(f.Date), len(f.Pairs), update.HumanBytes(f.Size), state)
	}
	for _, n := range notes {
		fmt.Println("  Note:", n)
	}
	return nil
}

// pauseIfDoubleClicked keeps an error visible on Windows, where a window
// opened by double-clicking closes the moment the program exits.
func pauseIfDoubleClicked() {
	if runtime.GOOS == "windows" && os.Getenv("PROMPT") == "" {
		fmt.Fprintln(os.Stderr, "\nPress Enter to close this window.")
		var b [1]byte
		os.Stdin.Read(b[:])
	}
}
