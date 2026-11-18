//go:build windows

package sysutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// modernBrowserIfDefaultIsIE returns the path of an installed modern
// browser when Internet Explorer is still the default (common on old
// Windows 7 and 8.1 machines), or "" to use the default browser.
func modernBrowserIfDefaultIsIE() string {
	if !defaultIsIE() {
		return ""
	}
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "LOCALAPPDATA"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v)
		}
	}
	for _, rel := range []string{
		`Google\Chrome\Application\chrome.exe`,
		`Mozilla Firefox\firefox.exe`,
		`Microsoft\Edge\Application\msedge.exe`,
		`Yandex\YandexBrowser\Application\browser.exe`,
		`BraveSoftware\Brave-Browser\Application\brave.exe`,
		`Programs\Opera\launcher.exe`,
		`Opera\launcher.exe`,
		`Chromium\Application\chrome.exe`,
	} {
		for _, root := range roots {
			p := filepath.Join(root, rel)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func defaultIsIE() bool {
	progID, ok := regString(syscall.HKEY_CURRENT_USER,
		`Software\Microsoft\Windows\Shell\Associations\UrlAssociations\http\UserChoice`, "ProgId")
	if ok && progID != "" {
		return strings.HasPrefix(strings.ToUpper(progID), "IE.")
	}
	cmd, ok := regString(syscall.HKEY_CLASSES_ROOT, `http\shell\open\command`, "")
	return ok && strings.Contains(strings.ToLower(cmd), "iexplore")
}

func regString(root syscall.Handle, path, name string) (string, bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	var key syscall.Handle
	if syscall.RegOpenKeyEx(root, p, 0, syscall.KEY_READ, &key) != nil {
		return "", false
	}
	defer syscall.RegCloseKey(key)
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	var typ uint32
	buf := make([]uint16, 512)
	size := uint32(len(buf) * 2)
	if syscall.RegQueryValueEx(key, n, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size) != nil {
		return "", false
	}
	if typ != syscall.REG_SZ && typ != syscall.REG_EXPAND_SZ {
		return "", false
	}
	return syscall.UTF16ToString(buf[:size/2]), true
}
