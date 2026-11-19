package drive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/sysutil"
)

// The drive may be pulled out at any moment, or lose its connection for
// a while. NABU notices, asks for it back, and carries on when it is
// back: at the same place, or at another (Windows may give it another
// letter). It is recognised by an identity made once and kept on it.

const idFile = "drive-id"

// ID is the drive's identity ("" if it cannot be read or made, on a
// write-protected drive, say).
func (l *Layout) ID() string {
	p := filepath.Join(l.Work, idFile)
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) == 32 {
		return strings.TrimSpace(string(b))
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(b[:])
	if os.MkdirAll(l.Work, 0o755) != nil || os.WriteFile(p, []byte(id), 0o644) != nil {
		return ""
	}
	return id
}

// here reports whether the drive with identity id is at root.
func here(root, id string) bool {
	if id == "" {
		st, err := os.Stat(filepath.Join(root, Wikipedia))
		return err == nil && st.IsDir()
	}
	b, err := os.ReadFile(filepath.Join(root, Work, idFile))
	return err == nil && strings.TrimSpace(string(b)) == id
}

// Watch looks at the drive every so often. When it is gone, gone(true)
// is called; when it is back, gone(false) and then back with where it is
// now (the same place or another), and Watch returns. It returns at once
// if the NABU folder is not on a drive it can watch (a library folder
// given on the command line).
func (l *Layout) Watch(ctx context.Context, every time.Duration, gone func(bool), back func(*Layout)) {
	if l.Root == "" {
		return
	}
	id := l.ID()
	// Where the NABU folder is within its drive, to look for it on the
	// other drives if it comes back under another name.
	mount, inside := "", ""
	for _, d := range sysutil.Drives() {
		if rel, err := filepath.Rel(d.Path, l.Root); err == nil && !strings.HasPrefix(rel, "..") && len(d.Path) > len(mount) {
			mount, inside = d.Path, rel
		}
	}
	missing := false
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if here(l.Root, id) {
			if missing {
				gone(false)
				back(l)
				return
			}
			continue
		}
		if !missing {
			missing = true
			gone(true)
		}
		if mount == "" || id == "" {
			continue
		}
		for _, d := range sysutil.Drives() {
			root := filepath.Join(d.Path, inside)
			if root != l.Root && here(root, id) {
				gone(false)
				back(At(root))
				return
			}
		}
	}
}
