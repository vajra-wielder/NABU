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

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
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
		case "version":
			fmt.Printf("NABU %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
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
  nabu prepare             build the search index of every dump now
  nabu info                list the dumps in the library
  nabu version             print the version

Flags:
`)
	fs := flag.NewFlagSet("nabu", flag.ContinueOnError)
	bindFlags(fs, &opts{})
	fs.PrintDefaults()
}

type opts struct {
	library   string
	port      int
	lan       bool
	noBrowser bool
	offline   bool
	mirror    string
}

func bindFlags(fs *flag.FlagSet, o *opts) {
	fs.StringVar(&o.library, "library", "", "folder holding the dumps (default: the drive's library folder)")
	fs.IntVar(&o.port, "port", 8080, "port to listen on; the next free one is used if taken")
	fs.BoolVar(&o.lan, "lan", false, "share the library with other devices on the local network")
	fs.BoolVar(&o.noBrowser, "no-browser", false, "do not open the web browser")
	fs.BoolVar(&o.offline, "offline", false, "never connect to the internet (no update checks, no pictures)")
	fs.StringVar(&o.mirror, "mirror", update.DefaultMirror, "Wikimedia dump server or mirror")
}

func parse(name string, args []string) (*opts, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	o := &opts{}
	bindFlags(fs, o)
	fs.Usage = usage
	err := fs.Parse(args)
	return o, fs, err
}

// findLibrary locates the library folder. The launchers pass --library;
// this is for running the program by hand.
func findLibrary(flagValue string) string {
	if flagValue != "" {
		if abs, err := filepath.Abs(flagValue); err == nil {
			return abs
		}
		return flagValue
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "library"), filepath.Join(dir, "..", "library"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "library"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	if len(candidates) > 0 {
		abs, _ := filepath.Abs(candidates[len(candidates)-1])
		return abs
	}
	return "library"
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
	dir := findLibrary(o.library)

	// Started twice (a double double-click)? Just show the running one.
	if url, ok := alreadyRunning(o.port, dir); ok {
		fmt.Println("NABU is already running at", url)
		if !o.noBrowser {
			sysutil.OpenBrowser(url)
		}
		return nil
	}

	lib, err := library.Open(dir)
	if err != nil {
		return fmt.Errorf("cannot open the library folder %s: %w", dir, err)
	}
	defer lib.Close()

	client := httpClient()
	checker := &update.Checker{Dir: dir, Mirror: o.mirror, Client: client}
	checker.Load()
	dl := update.NewManager(dir, client)
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

	banner(url, lib, dir, srv.Addrs)
	go func() {
		time.Sleep(200 * time.Millisecond)
		if !o.noBrowser {
			if err := sysutil.OpenBrowser(url); err != nil {
				fmt.Println("  Open this address in your web browser:", url)
			}
		}
	}()
	if !o.offline {
		go startupCheck(ctx, lib, checker, dl, dir)
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
	fmt.Println("  Library folder:        ", dir)
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
func startupCheck(ctx context.Context, lib *library.Library, c *update.Checker, dl *update.Manager, dir string) {
	settings := update.LoadSettings(dir)
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
	dir := findLibrary(o.library)
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
	m := update.NewManager(dir, client)
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
	dir := findLibrary(o.library)
	eds, notes := dump.Scan(dir)
	for _, n := range notes {
		fmt.Println("Note:", n)
	}
	if len(eds) == 0 {
		return fmt.Errorf("no dumps found in %s", dir)
	}
	for _, f := range eds {
		out := filepath.Join(dir, ".nabu", "index", f.Key()+".nidx")
		if ix, err := dump.OpenIndex(out, f); err == nil {
			fmt.Printf("%s: ready (%d pages)\n", f.Key(), ix.Meta.Records)
			ix.Close()
			continue
		}
		os.MkdirAll(filepath.Dir(out), 0o755)
		start := time.Now()
		tmp := filepath.Join(dir, ".nabu", "tmp", f.Key())
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
	return nil
}

func cmdInfo(args []string) error {
	o, _, err := parse("info", args)
	if err != nil {
		return err
	}
	dir := findLibrary(o.library)
	eds, notes := dump.Scan(dir)
	fmt.Println("Library:", dir)
	for _, f := range eds {
		state := "not prepared yet"
		out := filepath.Join(dir, ".nabu", "index", f.Key()+".nidx")
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
