// Package share makes a copy of NABU for someone else: the parts chosen
// (languages, systems, search, pictures, collections), never the
// reader's personal folder unless asked, onto another drive or folder.
// Copying again onto the same place only copies what changed.
package share

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/update"
)

// Options says what goes into the copy.
type Options struct {
	Wikis       []string `json:"wikis"`    // dbnames, e.g. "enwiki"; their newest dump
	Systems     []string `json:"systems"`  // "windows", "mac", "linux"
	Indexes     bool     `json:"indexes"`  // title indexes (saves the first-start wait)
	Search      bool     `json:"search"`   // search inside articles
	Pictures    bool     `json:"pictures"` // offline pictures
	Collections bool     `json:"collections"`
	Personal    bool     `json:"personal"` // saved articles, history, settings
	Source      bool     `json:"source"`
}

// File is one file to copy: from Src, to Rel in the copy (a standard
// NABU folder, whatever the layout of the original).
type File struct {
	Src  string
	Rel  string
	Size int64
	Part string // what it belongs to
}

// Part sizes for display.
type Part struct {
	Key   string // "wiki:enwiki", "system:windows", "search:enwiki", ...
	Name  string
	Bytes int64
}

// Inventory lists everything on the drive that can go into a copy.
func Inventory(d *drive.Layout) (parts []Part, files map[string][]File) {
	files = map[string][]File{}
	add := func(key, src, rel string, size int64) {
		files[key] = append(files[key], File{Src: src, Rel: filepath.ToSlash(rel), Size: size, Part: key})
	}
	// walk adds the files under dir, to go under folder in the copy.
	walk := func(key, dir, folder string) {
		filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || strings.HasSuffix(p, ".tmp") || strings.HasSuffix(p, ".part") {
				return nil
			}
			if rel, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(rel, "..") {
				add(key, p, filepath.Join(folder, rel), info.Size())
			}
			return nil
		})
	}
	one := func(key, src, rel string) {
		if info, err := os.Stat(src); err == nil && !info.IsDir() {
			add(key, src, rel, info.Size())
		}
	}
	// Always: launchers, the read-me, licences.
	ents, _ := os.ReadDir(d.Root)
	for _, e := range ents {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && (strings.HasPrefix(e.Name(), "Start NABU") || strings.HasPrefix(e.Name(), "start-nabu") || strings.HasPrefix(e.Name(), "Set up NABU") || strings.HasPrefix(e.Name(), "set-up-nabu") || strings.HasPrefix(e.Name(), "READ ME")) {
			one("base", filepath.Join(d.Root, e.Name()), e.Name())
		}
	}
	walk("base", filepath.Join(d.Root, drive.Licenses), drive.Licenses)
	// Folder notes go along with their folder.
	for _, p := range drive.Parts {
		if p.Folder != drive.Work {
			one("about", filepath.Join(d.Root, filepath.FromSlash(p.Folder), drive.AboutFile), filepath.Join(p.Folder, drive.AboutFile))
		}
	}
	one("about", filepath.Join(d.Root, drive.Programs, drive.AboutFile), filepath.Join(drive.Programs, drive.AboutFile))
	for _, sys := range []string{"windows", "mac", "linux"} {
		folder := filepath.Join(drive.Programs, sys)
		walk("system:"+sys, filepath.Join(d.Root, folder), folder)
	}
	for _, f := range files["about"] { // notes are counted with "about"
		for _, sys := range []string{"windows", "mac", "linux"} {
			key := "system:" + sys
			kept := files[key][:0]
			for _, x := range files[key] {
				if x.Rel != f.Rel {
					kept = append(kept, x)
				}
			}
			files[key] = kept
		}
	}
	// The newest dump of each wiki, with what was built for it.
	eds, _ := dump.Scan(d.Wikipedia)
	newest := map[string]*dump.Files{}
	for _, f := range eds {
		if o := newest[f.DB]; o == nil || f.Date > o.Date {
			newest[f.DB] = f
		}
	}
	for db, f := range newest {
		for _, pair := range f.Pairs {
			for _, p := range append(append([]string{}, pair.Data...), pair.Index) {
				one("wiki:"+db, p, filepath.Join(drive.Wikipedia, filepath.Base(p)))
			}
		}
		one("index:"+db, d.TitleIndex(f.Key()), filepath.Join(drive.Indexes, f.Key()+".nidx"))
		one("search:"+db, d.FullText(f.Key()), filepath.Join(drive.Search, f.Key()+".nfts"))
		walk("pictures:"+db, filepath.Join(d.Pictures, db), filepath.Join(drive.Pictures, db))
	}
	walk("pictures:commons", filepath.Join(d.Pictures, "commons"), filepath.Join(drive.Pictures, "commons"))
	one("search", filepath.Join(d.Search, "wanted.json"), filepath.Join(drive.Search, "wanted.json"))
	walk("collections", d.Collections, drive.Collections)
	walk("personal", d.Personal, drive.Personal)
	walk("source", filepath.Join(d.Root, drive.Source), drive.Source)
	for k, fs := range files {
		if k == "base" || k == "about" {
			continue
		}
		var n int64
		for _, f := range fs {
			n += f.Size
		}
		parts = append(parts, Part{Key: k, Bytes: n})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Key < parts[j].Key })
	return parts, files
}

// Select is the files the options ask for.
func Select(files map[string][]File, o Options) []File {
	out := append([]File{}, files["base"]...)
	for _, s := range o.Systems {
		out = append(out, files["system:"+s]...)
	}
	for _, w := range o.Wikis {
		out = append(out, files["wiki:"+w]...)
		if o.Indexes {
			out = append(out, files["index:"+w]...)
		}
		if o.Search {
			out = append(out, files["search:"+w]...)
		}
		if o.Pictures {
			out = append(out, files["pictures:"+w]...)
		}
	}
	if o.Search {
		out = append(out, files["search"]...)
	}
	if o.Pictures { // the pictures every language shares
		out = append(out, files["pictures:commons"]...)
	}
	if o.Collections {
		out = append(out, files["collections"]...)
	}
	if o.Personal {
		out = append(out, files["personal"]...)
	}
	if o.Source {
		out = append(out, files["source"]...)
	}
	for _, a := range files["about"] {
		dir := strings.TrimSuffix(a.Rel, "/"+drive.AboutFile) + "/"
		for _, f := range out {
			if strings.HasPrefix(f.Rel, dir) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// Total is the size of a list of files.
func Total(fs []File) int64 {
	var n int64
	for _, f := range fs {
		n += f.Size
	}
	return n
}

// Progress of a copy.
type Progress struct {
	State   string        `json:"state"` // "", "copying", "done", "failed", "cancelled"
	Dest    string        `json:"dest"`
	File    string        `json:"file"`
	Done    int64         `json:"done"`
	Total   int64         `json:"total"`
	Skipped int64         `json:"skipped"` // already there, unchanged
	Err     string        `json:"error,omitempty"`
	Started time.Time     `json:"started"`
	ETA     time.Duration `json:"eta"`
}

// Copier runs one copy at a time in the background.
type Copier struct {
	mu     sync.Mutex
	p      Progress
	cancel context.CancelFunc
}

// Status reports the copy's progress.
func (c *Copier) Status() Progress {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.p
	if p.State == "copying" && p.Done > p.Skipped {
		el := time.Since(p.Started)
		p.ETA = time.Duration(float64(el) * float64(p.Total-p.Done) / float64(p.Done-p.Skipped))
	}
	return p
}

// Cancel stops the copy; what was copied stays and is not copied again.
func (c *Copier) Cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
}

// Start copies files from the NABU folder src into dest.
func (c *Copier) Start(src *drive.Layout, dest string, files []File) error {
	dest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if err := checkDest(src.Root, dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("cannot make %s: %w", dest, err)
	}
	need := Total(files)
	for _, f := range files { // already there: not needed again
		if st, err := os.Stat(filepath.Join(dest, filepath.FromSlash(f.Rel))); err == nil && st.Size() == f.Size {
			need -= f.Size
		}
	}
	if free, err := sysutil.FreeSpace(dest); err == nil && free < need+16<<20 {
		return fmt.Errorf("not enough room there: %s needed, %s free", update.HumanBytes(need), update.HumanBytes(free))
	}
	c.mu.Lock()
	if c.p.State == "copying" {
		c.mu.Unlock()
		return errors.New("a copy is already being made")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.p = Progress{State: "copying", Dest: dest, Total: Total(files), Started: time.Now()}
	c.mu.Unlock()
	go c.run(ctx, src.Root, dest, files)
	return nil
}

func checkDest(root, dest string) error {
	r, d := filepath.Clean(root), filepath.Clean(dest)
	if strings.EqualFold(r, d) || strings.HasPrefix(strings.ToLower(d), strings.ToLower(r)+string(filepath.Separator)) {
		return errors.New("choose a place outside this NABU folder")
	}
	return nil
}

func (c *Copier) run(ctx context.Context, root, dest string, files []File) {
	defer sysutil.KeepAwake()()
	err := func() error {
		for _, f := range files {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.mu.Lock()
			c.p.File = f.Rel
			c.mu.Unlock()
			if err := c.copyFile(ctx, f.Src, filepath.Join(dest, filepath.FromSlash(f.Rel)), f.Size); err != nil {
				return fmt.Errorf("%s: %w", f.Rel, err)
			}
		}
		// The launchers must stay runnable on Mac and Linux.
		for _, n := range []string{"Start NABU (Mac).command", "start-nabu-linux.sh", "Set up NABU (Mac).command", "set-up-nabu-linux.sh"} {
			os.Chmod(filepath.Join(dest, n), 0o755)
		}
		filepath.Walk(filepath.Join(dest, drive.Programs), func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && !strings.HasSuffix(p, ".txt") {
				os.Chmod(p, 0o755)
			}
			return nil
		})
		return nil
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = nil
	switch {
	case err == nil:
		c.p.State, c.p.File = "done", ""
	case errors.Is(err, context.Canceled):
		c.p.State = "cancelled"
	default:
		c.p.State, c.p.Err = "failed", explain(err)
	}
}

func (c *Copier) copyFile(ctx context.Context, from, to string, size int64) error {
	if st, err := os.Stat(to); err == nil && st.Size() == size {
		if sst, err := os.Stat(from); err == nil && !sst.ModTime().After(st.ModTime()) {
			c.mu.Lock()
			c.p.Done += size
			c.p.Skipped += size
			c.mu.Unlock()
			return nil
		}
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	tmp := to + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for {
		if ctx.Err() != nil {
			out.Close()
			os.Remove(tmp)
			return ctx.Err()
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				os.Remove(tmp)
				return err
			}
			c.mu.Lock()
			c.p.Done += int64(n)
			c.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Remove(to)
	return os.Rename(tmp, to)
}

func explain(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "file too large"):
		return s + " — the drive is probably formatted FAT32, which cannot hold files over 4 GB. Format it as exFAT and copy again."
	case strings.Contains(s, "no space"):
		return s + " — the drive is full."
	}
	return s
}
