// Package drivefile opens files on the NABU drive only while they are
// being used. A reader may pull the drive out at any moment, without
// ejecting it, once a page has loaded: so nothing on it stays open.
// Each File opens on its first read or write and closes itself after a
// moment of not being used (Idle). If the drive was pulled and put back,
// the next read simply opens the file again.
package drivefile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Idle is how long a file stays open after it was last used.
var Idle = 2 * time.Second

var open int64 // files open right now, for tests and the status line

// Open reports how many drive files are open right now.
func Open() int64 { return atomic.LoadInt64(&open) }

// File is a file on the drive opened only while in use.
type File struct {
	path string
	flag int
	perm os.FileMode

	mu    sync.Mutex
	f     *os.File
	gen   int // bumped on every use, so a stale close timer does nothing
	timer *time.Timer
}

// OpenRead returns path for reading. The file must exist.
func OpenRead(path string) (*File, error) { return OpenFile(path, os.O_RDONLY, 0) }

// OpenFile returns path opened with flag (os.O_CREATE and os.O_TRUNC
// apply only to this first opening).
func OpenFile(path string, flag int, perm os.FileMode) (*File, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	atomic.AddInt64(&open, 1)
	d := &File{path: path, flag: flag &^ (os.O_CREATE | os.O_TRUNC | os.O_EXCL | os.O_APPEND), perm: perm, f: f}
	d.mu.Lock()
	d.release()
	d.mu.Unlock()
	return d, nil
}

// Name is the file's path.
func (d *File) Name() string { return d.path }

// get returns the open file, opening it if needed; d.mu is held.
func (d *File) get() (*os.File, error) {
	if d.f == nil {
		f, err := os.OpenFile(d.path, d.flag, d.perm)
		if err != nil {
			return nil, err
		}
		d.f = f
		atomic.AddInt64(&open, 1)
	}
	d.gen++
	return d.f, nil
}

// release arranges for the file to close after Idle; d.mu is held.
func (d *File) release() {
	gen := d.gen
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(Idle, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.gen == gen {
			d.shut()
		}
	})
}

func (d *File) shut() error {
	if d.f == nil {
		return nil
	}
	err := d.f.Close()
	d.f = nil
	atomic.AddInt64(&open, -1)
	return err
}

// do runs op on the open file. An error other than the end of the file
// may mean the handle went bad (the drive was pulled and put back): the
// file is opened afresh and op tried once more.
func (d *File) do(op func(f *os.File) (int, error)) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, err := d.get()
	if err != nil {
		return 0, err
	}
	n, err := op(f)
	if err != nil && !errors.Is(err, io.EOF) {
		d.shut()
		if f, err2 := d.get(); err2 == nil {
			n, err = op(f)
		}
	}
	d.release()
	return n, err
}

// ReadAt reads len(p) bytes at off.
func (d *File) ReadAt(p []byte, off int64) (int, error) {
	return d.do(func(f *os.File) (int, error) { return f.ReadAt(p, off) })
}

// WriteAt writes p at off.
func (d *File) WriteAt(p []byte, off int64) (int, error) {
	return d.do(func(f *os.File) (int, error) { return f.WriteAt(p, off) })
}

// Sync flushes what was written to the drive itself.
func (d *File) Sync() error {
	_, err := d.do(func(f *os.File) (int, error) { return 0, f.Sync() })
	return err
}

// Truncate changes the file's size.
func (d *File) Truncate(size int64) error {
	_, err := d.do(func(f *os.File) (int, error) { return 0, f.Truncate(size) })
	return err
}

// Stat describes the file.
func (d *File) Stat() (os.FileInfo, error) { return os.Stat(d.path) }

// Close closes the file now.
func (d *File) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.gen++
	return d.shut()
}

// WriteFile replaces path with data all at once: written under another
// name, flushed to the drive itself, then renamed. Pulled out at any
// moment, the drive holds the old file or the new, never half of one;
// and once WriteFile returns, the new one is on the drive, not waiting
// in the computer's memory. Each write has a temporary name of its own,
// so two parts of NABU saving the same file at once can't rename away
// each other's half-written copy.
func WriteFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// Rename replaces to with from. Windows refuses while anyone has the old
// file open for a moment: another part of NABU reading it (the picture
// download's progress, a page showing settings), an antivirus scan or
// the search indexer. Those let go quickly, so try again for a while.
func Rename(from, to string) error {
	err := os.Rename(from, to)
	for wait := 10 * time.Millisecond; err != nil && runtime.GOOS == "windows" && wait < 3*time.Second; wait *= 2 {
		time.Sleep(wait)
		err = os.Rename(from, to)
	}
	return err
}

// syncDir flushes a folder's list of files (where the system allows it),
// so a renamed file's new name is on the drive too.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
