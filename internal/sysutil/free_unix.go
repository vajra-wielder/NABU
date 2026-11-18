//go:build linux || darwin || freebsd

package sysutil

import "syscall"

// FreeSpace returns the bytes available to unprivileged users at path.
func FreeSpace(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
