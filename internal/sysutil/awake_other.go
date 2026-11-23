//go:build !windows

package sysutil

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// inhibitSleep keeps a Mac awake with caffeinate (for as long as NABU
// runs at most), or a Linux computer with systemd-inhibit, where there.
func inhibitSleep() func() {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	case "linux":
		if _, err := exec.LookPath("systemd-inhibit"); err != nil {
			return func() {}
		}
		// (held until NABU lets go, or ends however it ends)
		cmd = exec.Command("systemd-inhibit", "--what=idle:sleep", "--who=NABU",
			"--why=Getting Wikipedia ready", "--mode=block",
			"tail", "--pid="+strconv.Itoa(os.Getpid()), "-f", "/dev/null")
	default:
		return func() {}
	}
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	go cmd.Wait()
	return func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
}
