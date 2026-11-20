package main

import (
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/sysutil"
)

// wifiShare lets phones and other computers on the same Wi-Fi read from
// this NABU, switched on and off while it runs: NABU then also listens
// on this computer's network addresses (every one it has, again as they
// change: joining a phone's hotspot, say). Devices reading this way can
// read and search, never change anything on the drive.
type wifiShare struct {
	mu     sync.Mutex
	shared *sharedListener
	port   int
	lns    map[string]net.Listener // by address
	on     bool
	fixed  bool // started with --lan: always on

	cmu   sync.Mutex
	conns map[net.Conn]bool // open connections from other devices
}

// tracked notes the connections of a listener, so stopping sharing
// closes those of the devices still reading too.
type tracked struct {
	net.Listener
	w *wifiShare
}

func (l tracked) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.w.cmu.Lock()
	l.w.conns[c] = true
	l.w.cmu.Unlock()
	return &trackedConn{Conn: c, w: l.w}, nil
}

type trackedConn struct {
	net.Conn
	w *wifiShare
}

func (c *trackedConn) Close() error {
	c.w.cmu.Lock()
	delete(c.w.conns, c.Conn)
	c.w.cmu.Unlock()
	return c.Conn.Close()
}

func newWifiShare(shared *sharedListener, port int, lan bool) *wifiShare {
	w := &wifiShare{shared: shared, port: port, lns: map[string]net.Listener{}, fixed: lan, on: lan, conns: map[net.Conn]bool{}}
	go func() {
		for range time.Tick(15 * time.Second) {
			w.mu.Lock()
			if w.on && !w.fixed {
				w.refresh()
			}
			w.mu.Unlock()
		}
	}()
	return w
}

// refresh listens on addresses that appeared and stops on those gone;
// w.mu is held.
func (w *wifiShare) refresh() {
	now := map[string]bool{}
	for _, ip := range sysutil.LANAddresses() {
		addr := net.JoinHostPort(ip, strconv.Itoa(w.port))
		now[addr] = true
		if _, ok := w.lns[addr]; ok {
			continue
		}
		if ln, err := net.Listen("tcp", addr); err == nil {
			w.lns[addr] = ln
			w.shared.feed(tracked{ln, w})
		}
	}
	for addr, ln := range w.lns {
		if !now[addr] {
			ln.Close()
			delete(w.lns, addr)
		}
	}
}

func (w *wifiShare) On() bool    { w.mu.Lock(); defer w.mu.Unlock(); return w.on }
func (w *wifiShare) Fixed() bool { return w.fixed }

// Set switches sharing on or off.
func (w *wifiShare) Set(on bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fixed {
		return nil
	}
	w.on = on
	if on {
		w.refresh()
		return nil
	}
	for addr, ln := range w.lns {
		ln.Close()
		delete(w.lns, addr)
	}
	w.cmu.Lock()
	for c := range w.conns {
		c.Close()
	}
	w.conns = map[net.Conn]bool{}
	w.cmu.Unlock()
	return nil
}

// Addrs are the addresses others can open, while sharing.
func (w *wifiShare) Addrs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.on {
		return nil
	}
	if !w.fixed {
		w.refresh()
	}
	var out []string
	for _, ip := range sysutil.LANAddresses() {
		addr := net.JoinHostPort(ip, strconv.Itoa(w.port))
		if _, ok := w.lns[addr]; ok || w.fixed {
			out = append(out, "http://"+addr+"/")
		}
	}
	return out
}
