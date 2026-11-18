//go:build !linux && !darwin && !freebsd && !windows

package sysutil

import "errors"

// FreeSpace is not implemented on this platform.
func FreeSpace(path string) (int64, error) { return 0, errors.New("free space unknown") }
