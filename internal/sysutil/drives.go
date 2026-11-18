package sysutil

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Drive is a place NABU could be copied to.
type Drive struct {
	Path string
	Free int64 // -1 if unknown
}

// Drives lists the drives and volumes attached to the computer (USB
// sticks, external disks, other partitions), for choosing where to copy
// NABU.
func Drives() []Drive {
	var paths []string
	switch runtime.GOOS {
	case "windows":
		for c := 'C'; c <= 'Z'; c++ {
			p := string(c) + `:\`
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				paths = append(paths, p)
			}
		}
	case "darwin":
		paths, _ = filepath.Glob("/Volumes/*")
	default:
		for _, pat := range []string{"/media/*/*", "/run/media/*/*", "/media/*", "/mnt/*"} {
			m, _ := filepath.Glob(pat)
			paths = append(paths, m...)
		}
	}
	seen := map[string]bool{}
	var out []Drive
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() || seen[p] {
			continue
		}
		seen[p] = true
		free, err := FreeSpace(p)
		if err != nil {
			free = -1
		}
		out = append(out, Drive{Path: p, Free: free})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
