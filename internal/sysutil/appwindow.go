package sysutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenAppWindow opens url in a window of its own, without tabs or
// address bar (the Chromium family's --app mode), so NABU feels like a
// desktop program. It falls back to the normal browser.
func OpenAppWindow(url string) error {
	for _, b := range chromiumBrowsers() {
		cmd := exec.Command(b, "--app="+url, "--new-window")
		if cmd.Start() == nil {
			go cmd.Wait()
			return nil
		}
	}
	return OpenBrowser(url)
}

func chromiumBrowsers() []string {
	var found []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			root := os.Getenv(env)
			if root == "" {
				continue
			}
			for _, rel := range []string{`Microsoft\Edge\Application\msedge.exe`, `Google\Chrome\Application\chrome.exe`,
				`BraveSoftware\Brave-Browser\Application\brave.exe`, `Chromium\Application\chrome.exe`} {
				if p := filepath.Join(root, rel); fileExists(p) {
					found = append(found, p)
				}
			}
		}
	case "darwin":
		for _, p := range []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Chromium.app/Contents/MacOS/Chromium"} {
			if fileExists(p) {
				found = append(found, p)
			}
		}
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(n); err == nil {
				found = append(found, p)
			}
		}
	}
	return found
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
