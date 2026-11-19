//go:build !windows

package sysutil

import "os/exec"

// Detach lets a child process outlive the one that started it. On Unix
// it already does (NABU ignores the terminal hanging up), and it keeps
// sharing the terminal: false.
func Detach(cmd *exec.Cmd) bool { return false }
