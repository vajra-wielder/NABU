// NABU: plug-and-play offline Wikipedia.
//
// Usage:
//
//	nabu [flags]                 start the library and open the browser
//	nabu catalog [lang]          list Wikipedia editions available online
//	nabu download <file|series>  download an edition into the library
//	nabu info <file.zim>         show what is inside a ZIM file
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

	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/server"
	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/update"
	"github.com/vajra-wielder/nabu/internal/zim"
)

var version = "dev"

func main() {
	server.Version = version
	update.UserAgent = "NABU/" + version + " (offline Wikipedia reader; +https://github.com/vajra-wielder/nabu)"

	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, rest := args[0], args[1:]
		var err error
		switch cmd {
		case "catalog", "catalogue":
			err = cmdCatalog(rest)
		case "download":
			err = cmdDownload(rest)
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

  nabu [flags]                  start NABU and open it in the browser
  nabu catalog [language]       list Wikipedia editions available online
  nabu download <file|series>   download an edition, e.g.
                                  nabu download wikipedia_en_all_maxi
  nabu info <file.zim>          describe a ZIM file
  nabu version                  print the version

Flags for starting NABU:
`)
	serveFlags(flag.NewFlagSet("nabu", flag.ContinueOnError)).PrintDefaults()
}

type serveOpts struct {
	library   string
	port      int
	lan       bool
	noBrowser bool
	offline   bool
	catalog   string
}

func serveFlags(fs *flag.FlagSet) *flag.FlagSet {
	o := &serveOpts{}
	bindServeFlags(fs, o)
	return fs
}

func bindServeFlags(fs *flag.FlagSet, o *serveOpts) {
	fs.StringVar(&o.library, "library", "", "folder holding .zim files (default: the drive's library folder)")
	fs.IntVar(&o.port, "port", 8080, "port to listen on; the next free one is used if taken")
	fs.BoolVar(&o.lan, "lan", false, "share the library with other devices on the local network")
	fs.BoolVar(&o.noBrowser, "no-browser", false, "do not open the web browser")
	fs.BoolVar(&o.offline, "offline", false, "never connect to the internet (no update checks)")
	fs.StringVar(&o.catalog, "catalog", update.DefaultCatalogURL, "where to look for Wikipedia editions")
}

// findLibrary locates the library folder on the drive. The launchers
// pass --library explicitly; this is for running the binary by hand.
func findLibrary(flagValue string) string {
	if flagValue != "" {
		abs, err := filepath.Abs(flagValue)
		if err == nil {
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
	fs := flag.NewFlagSet("nabu", flag.ContinueOnError)
	o := &serveOpts{}
	bindServeFlags(fs, o)
	fs.Usage = usage
	if err := fs.Parse(args); err != nil {
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
	checker := &update.Checker{Dir: dir, CatalogURL: o.catalog, Client: client}
	checker.LoadCache()
	dl := update.NewManager(dir, client)
	dl.OnFinish = func(string) { lib.Rescan() }

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
		// Give the server a moment so the first page loads instantly.
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
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p)))
		if err == nil {
			return ln, p, nil
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, 0, fmt.Errorf("cannot open a network port: %w", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

// alreadyRunning asks the usual ports whether NABU is serving the same
// library already.
func alreadyRunning(port int, dir string) (string, bool) {
	c := &http.Client{Timeout: 400 * time.Millisecond}
	for p := port; p < port+5; p++ {
		url := "http://localhost:" + strconv.Itoa(p) + "/"
		resp, err := c.Get("http://127.0.0.1:" + strconv.Itoa(p) + "/nabu/api/ping")
		if err != nil {
			continue
		}
		var info struct{ App, Library string }
		json.NewDecoder(resp.Body).Decode(&info)
		resp.Body.Close()
		if info.App == "nabu" && info.Library == dir {
			return url, true
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
		fmt.Println("  Books:                  none yet; the browser page will help you get one")
	}
	for _, b := range books {
		fmt.Printf("  Book:                   %s (%s, %s)\n", b.Title, b.Edition(), update.HumanBytes(b.Size))
	}
	for _, e := range lib.Errs {
		fmt.Println("  Could not open:        ", e)
	}
	fmt.Println()
	fmt.Println("  Keep this window open while you read.")
	fmt.Println("  To stop NABU, close this window or press Ctrl+C.")
	fmt.Println()
}

// startupCheck is the "ping the catalogue when activated" step.
func startupCheck(ctx context.Context, lib *library.Library, c *update.Checker, dl *update.Manager, dir string) {
	settings := update.LoadSettings(dir)
	if !settings.CheckOnStart {
		return
	}
	if err := c.Check(ctx); err != nil {
		fmt.Println("  (No internet, or the catalogue is unreachable. Reading works fine offline.)")
		return
	}
	latest, _, _, _ := c.Snapshot()
	var inst []update.Installed
	for _, b := range lib.Books() {
		inst = append(inst, update.Installed{ID: b.ID, Series: b.Series(), Edition: b.Edition()})
	}
	ups := update.UpdatesFor(inst, latest)
	if len(ups) == 0 {
		if len(inst) > 0 {
			fmt.Println("  Your Wikipedia is the newest edition available.")
		}
		return
	}
	for _, u := range ups {
		fmt.Printf("  Newer edition available: %s (%s, %s). See the \"Get & update\" page.\n",
			u.Newer.File, u.Newer.Date, update.HumanBytes(u.Newer.Size))
	}
	if settings.AutoDownload {
		u := ups[0]
		if free, err := sysutil.FreeSpace(dir); err == nil && free < u.Newer.Size {
			fmt.Println("  Not downloading automatically: not enough free space on the drive.")
			return
		}
		if err := dl.Start(u.Newer); err == nil {
			fmt.Println("  Downloading it now (automatic updates are on).")
		}
	}
}

func cmdCatalog(args []string) error {
	fs := flag.NewFlagSet("catalog", flag.ContinueOnError)
	cat := fs.String("catalog", update.DefaultCatalogURL, "catalogue URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lang := fs.Arg(0)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	eds, err := update.FetchCatalog(ctx, httpClient(), *cat)
	if err != nil {
		return err
	}
	for _, e := range update.Latest(eds) {
		if lang != "" && e.Lang != lang {
			continue
		}
		fmt.Printf("%-48s %8s  %s\n", strings.TrimSuffix(e.File, ".zim"), update.HumanBytes(e.Size), e.Describe())
	}
	return nil
}

func cmdDownload(args []string) error {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	o := &serveOpts{}
	bindServeFlags(fs, o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: nabu download <file or series>, e.g. nabu download wikipedia_en_all_maxi")
	}
	want := strings.TrimSuffix(fs.Arg(0), ".zim")
	dir := findLibrary(o.library)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	client := httpClient()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	eds, err := update.FetchCatalog(cctx, client, o.catalog)
	cancel()
	if err != nil {
		return err
	}
	var pick *update.Edition
	for _, e := range update.Latest(eds) {
		e := e
		if e.Series() == want {
			pick = &e
		}
	}
	for _, e := range eds {
		e := e
		if strings.TrimSuffix(e.File, ".zim") == want {
			pick = &e
		}
	}
	if pick == nil {
		return fmt.Errorf("%s is not in the catalogue (see: nabu catalog)", want)
	}
	fmt.Printf("Downloading %s (%s) into %s\n", pick.File, update.HumanBytes(pick.Size), dir)
	m := update.NewManager(dir, client)
	if err := m.Start(*pick); err != nil {
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
			fmt.Println("\nDone. Verified:", st.Verified)
			return nil
		case update.StateFailed:
			fmt.Println()
			return errors.New(st.Error)
		}
	}
}

func cmdInfo(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: nabu info <file.zim>")
	}
	a, err := zim.Open(args[0])
	if err != nil {
		return err
	}
	defer a.Close()
	h := a.Hdr
	fmt.Printf("File:        %s (%s)\n", args[0], update.HumanBytes(a.Size()))
	fmt.Printf("Format:      ZIM %d.%d, %d entries, %d clusters\n", h.Major, h.Minor, h.EntryCount, h.ClusterCount)
	fmt.Printf("UUID:        %s\n", a.UUIDString())
	for _, k := range []string{"Title", "Description", "Language", "Name", "Flavour", "Date", "Creator", "Publisher"} {
		if v := a.Metadata(k); v != "" {
			fmt.Printf("%-12s %s\n", k+":", v)
		}
	}
	fmt.Printf("Articles:    %d\n", a.ArticleCount())
	if m, err := a.MainEntry(); err == nil {
		fmt.Printf("Main page:   %s\n", m.Path())
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
