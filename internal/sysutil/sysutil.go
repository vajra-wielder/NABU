// Package sysutil holds the few operating-system specific helpers NABU
// needs: opening the browser, measuring free space and finding the
// machine's network address.
package sysutil

import (
	"net"
	"os/exec"
	"runtime"
	"sort"
)

// OpenBrowser opens url in the default web browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		// xdg-open covers every mainstream Linux desktop; the rest are
		// fallbacks for minimal systems.
		for _, c := range []string{"xdg-open", "sensible-browser", "x-www-browser", "gio", "firefox", "chromium", "google-chrome"} {
			if p, err := exec.LookPath(c); err == nil {
				if c == "gio" {
					cmd = exec.Command(p, "open", url)
				} else {
					cmd = exec.Command(p, url)
				}
				break
			}
		}
		if cmd == nil {
			return exec.ErrNotFound
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// LANAddresses lists this machine's private IPv4 addresses, so a reader
// can share NABU with other devices on the same Wi-Fi.
func LANAddresses() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil && ip4.IsPrivate() {
					out = append(out, ip4.String())
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
