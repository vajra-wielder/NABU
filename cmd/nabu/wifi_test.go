package main

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/sysutil"
)

// Sharing on Wi-Fi opens NABU on the computer's network addresses while
// it runs, and closes them again; the address stays the same.
func TestWifiShare(t *testing.T) {
	old := sysutil.LANAddresses
	sysutil.LANAddresses = func() []string { return []string{"127.0.0.2"} } // stands in for 192.168.x.y
	defer func() { sysutil.LANAddresses = old }()

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	port := raw.Addr().(*net.TCPAddr).Port
	shared := newSharedListener(raw)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "nabu") })}
	go srv.Serve(shared.session())
	defer srv.Close()

	get := func(host string) string {
		c := &http.Client{Timeout: 2 * time.Second}
		resp, err := c.Get("http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/")
		if err != nil {
			return "refused"
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	w := newWifiShare(shared, port, false)
	if get("127.0.0.2") != "refused" || w.Addrs() != nil {
		t.Fatal("shared before being switched on")
	}
	w.Set(true)
	if got := get("127.0.0.2"); got != "nabu" {
		t.Fatalf("shared: %q", got)
	}
	if a := w.Addrs(); len(a) != 1 || a[0] != "http://127.0.0.2:"+strconv.Itoa(port)+"/" {
		t.Fatalf("addresses %v", a)
	}
	if get("127.0.0.1") != "nabu" {
		t.Fatal("this computer lost NABU")
	}
	w.Set(false)
	time.Sleep(50 * time.Millisecond)
	if get("127.0.0.2") != "refused" {
		t.Fatal("still shared after switching off")
	}
	if get("127.0.0.1") != "nabu" {
		t.Fatal("this computer lost NABU")
	}
}
