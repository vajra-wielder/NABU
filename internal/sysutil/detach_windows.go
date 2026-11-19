//go:build windows

package sysutil

import (
	"os/exec"
	"syscall"
)

// Detach starts a child without the console of the one starting it:
// when the launcher on the drive goes (the drive pulled out), its window
// closes, and would stop everything attached to it. Reports true: the
// child has no console to write to.
func Detach(cmd *exec.Cmd) bool {
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
	return true
}
