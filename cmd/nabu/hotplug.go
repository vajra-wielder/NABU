package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/update"
)

const fromCopyEnv = "NABU_FROM_COPY"

// runFromCopy starts NABU again from a copy of itself on this computer
// when it was started from the drive (root), and waits for it. A program
// runs from its file: pulled out with the program on it, the drive would
// take NABU down; a copy on the computer keeps running and simply asks
// for the drive back. It reports done=false (run from here after all)
// when there is no need, or no way, to use a copy.
func runFromCopy(root string) (done bool, err error) {
	if os.Getenv(fromCopyEnv) != "" || root == "" {
		return false, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, nil
	}
	if e, err := filepath.EvalSymlinks(exe); err == nil {
		exe = e
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if rel, err := filepath.Rel(root, exe); err != nil || strings.HasPrefix(rel, "..") {
		return false, nil // not on the drive
	}
	cp, err := programCopy(exe)
	if err != nil {
		fmt.Println("  (Could not copy NABU to this computer; running it from the drive:", err, ")")
		return false, nil
	}
	cmd := exec.Command(cp, os.Args[1:]...)
	cmd.Env = append(os.Environ(), fromCopyEnv+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	detached := sysutil.Detach(cmd)
	if detached {
		// Without a window of its own, its messages come through this one
		// while it lasts.
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, writerOnly{os.Stdout}, writerOnly{os.Stderr}
	}
	if err := cmd.Start(); err != nil {
		return false, nil
	}
	if detached {
		// Ctrl+C or closing the window stops the copy too.
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sig
			cmd.Process.Kill() // what it writes is always complete (drivefile)
		}()
	} else {
		// Ctrl+C reaches the copy too; this one only waits for it. If the
		// drive is pulled out and this one goes with it, the copy runs on.
		signal.Ignore(os.Interrupt, syscall.SIGTERM)
		ignoreHangup()
	}
	err = cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	return true, err
}

// programCopy copies the program into this computer's cache folder,
// once per version of it.
func programCopy(exe string) (string, error) {
	dir := update.ComputerIncoming()
	if dir == "" {
		return "", errors.New("no cache folder")
	}
	dir = filepath.Join(filepath.Dir(dir), "program")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer in.Close()
	h := sha1.New()
	if _, err := io.Copy(h, in); err != nil {
		return "", err
	}
	name := "nabu-" + hex.EncodeToString(h.Sum(nil))[:12] + filepath.Ext(exe)
	dst := filepath.Join(dir, name)
	if st, err := os.Stat(dst); err == nil && st.Mode().IsRegular() {
		return dst, nil
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	// Older copies are no longer needed.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != name && strings.HasPrefix(e.Name(), "nabu-") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return dst, nil
}

// ignoreHangup keeps NABU running when the terminal window that started
// it goes (with the launcher on the drive, say). It is stopped with
// Ctrl+C, or "Turn off NABU" on any page.
func ignoreHangup() { signal.Ignore(syscall.SIGHUP) }

// sharedListener keeps one network address across restarts of the
// server, so the reader's browser tab keeps working.
type sharedListener struct {
	conns chan net.Conn
	errc  chan error
	addr  net.Addr
}

func newSharedListener(ln net.Listener) *sharedListener {
	s := &sharedListener{conns: make(chan net.Conn), errc: make(chan error, 1), addr: ln.Addr()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				s.errc <- err
				return
			}
			s.conns <- c
		}
	}()
	return s
}

// feed adds the connections of another listener (one on a network
// address, for sharing on Wi-Fi) until it is closed.
func (s *sharedListener) feed(ln net.Listener) {
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.conns <- c
		}
	}()
}

// session is a listener for one run of the server; closing it leaves the
// address open for the next.
func (s *sharedListener) session() net.Listener {
	return &sessionListener{s: s, done: make(chan struct{})}
}

type sessionListener struct {
	s    *sharedListener
	done chan struct{}
	once sync.Once
}

func (l *sessionListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.s.conns:
		return c, nil
	case err := <-l.s.errc:
		l.s.errc <- err
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sessionListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *sessionListener) Addr() net.Addr { return l.s.addr }

// writerOnly hides that a writer is a file, so a child process gets a
// pipe to it rather than the file itself.
type writerOnly struct{ io.Writer }
