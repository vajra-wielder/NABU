// Package zim reads ZIM archives, the open file format used by Kiwix and
// the openZIM project to ship whole websites (Wikipedia included) as a
// single file: rendered HTML, images, stylesheets and an index.
//
// The reader is written for slow, cheap storage such as a USB pen drive:
// nothing is loaded up front beyond an 80 byte header and the MIME table,
// every lookup is a handful of positioned reads, and decompressed clusters
// are kept in a small memory-bounded cache.
//
// Format reference: https://wiki.openzim.org/wiki/ZIM_file_format
package zim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// Magic is the number every ZIM file starts with ("ZIM\x04" little endian).
const Magic = 72173914

const (
	mimeRedirect   = 0xffff
	mimeLinkTarget = 0xfffe
	mimeDeleted    = 0xfffd
	noMainPage     = 0xffffffff
	headerSize     = 80
)

// Errors returned by lookups.
var (
	ErrNotFound    = errors.New("zim: entry not found")
	ErrNotZIM      = errors.New("zim: not a ZIM file (bad magic number)")
	ErrRedirectCap = errors.New("zim: too many chained redirects")
)

// Header is the fixed 80 byte header at the start of every ZIM file.
type Header struct {
	Magic         uint32
	Major, Minor  uint16
	UUID          [16]byte
	EntryCount    uint32
	ClusterCount  uint32
	URLPtrPos     uint64
	TitlePtrPos   uint64
	ClusterPtrPos uint64
	MimeListPos   uint64
	MainPage      uint32
	LayoutPage    uint32
	ChecksumPos   uint64
}

// Archive is an open ZIM file. It is safe for concurrent use.
type Archive struct {
	r    io.ReaderAt
	c    io.Closer
	size int64
	Hdr  Header

	mimes []string

	// NewNamespaceScheme is true for ZIM files written by libzim 7 or later
	// (minor version >= 1), where all user content lives in namespace 'C'.
	// Older files keep articles in 'A', images in 'I' and assets in '-'.
	NewNamespaceScheme bool

	clusters *clusterCache

	direntMu    sync.Mutex
	direntCache map[uint32]*Entry

	titles     titleIndex
	titlesOnce sync.Once

	nsMu     sync.Mutex
	nsRanges map[byte][2]uint32
}

// Open opens the ZIM file at path.
func Open(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	a, err := New(f, st.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	a.c = f
	return a, nil
}

// New reads a ZIM archive from r, which must be size bytes long.
func New(r io.ReaderAt, size int64) (*Archive, error) {
	a := &Archive{
		r:           r,
		size:        size,
		clusters:    newClusterCache(64 << 20),
		direntCache: make(map[uint32]*Entry),
		nsRanges:    make(map[byte][2]uint32),
	}
	var buf [headerSize]byte
	if _, err := r.ReadAt(buf[:], 0); err != nil {
		return nil, fmt.Errorf("zim: reading header: %w", err)
	}
	le := binary.LittleEndian
	h := &a.Hdr
	h.Magic = le.Uint32(buf[0:])
	if h.Magic != Magic {
		return nil, ErrNotZIM
	}
	h.Major = le.Uint16(buf[4:])
	h.Minor = le.Uint16(buf[6:])
	copy(h.UUID[:], buf[8:24])
	h.EntryCount = le.Uint32(buf[24:])
	h.ClusterCount = le.Uint32(buf[28:])
	h.URLPtrPos = le.Uint64(buf[32:])
	h.TitlePtrPos = le.Uint64(buf[40:])
	h.ClusterPtrPos = le.Uint64(buf[48:])
	h.MimeListPos = le.Uint64(buf[56:])
	h.MainPage = le.Uint32(buf[64:])
	h.LayoutPage = le.Uint32(buf[68:])
	h.ChecksumPos = le.Uint64(buf[72:])

	if h.Major != 5 && h.Major != 6 {
		return nil, fmt.Errorf("zim: unsupported major version %d", h.Major)
	}
	for _, p := range []uint64{h.URLPtrPos, h.ClusterPtrPos, h.MimeListPos} {
		if p >= uint64(size) {
			return nil, errors.New("zim: header points outside the file (truncated download?)")
		}
	}
	if end := h.URLPtrPos + 8*uint64(h.EntryCount); end > uint64(size) {
		return nil, errors.New("zim: URL pointer list runs past end of file (truncated download?)")
	}
	a.NewNamespaceScheme = h.Major == 6 && h.Minor >= 1
	if err := a.readMimeList(); err != nil {
		return nil, err
	}
	return a, nil
}

// Close releases the underlying file.
func (a *Archive) Close() error {
	if a.c != nil {
		return a.c.Close()
	}
	return nil
}

// SetCloser registers what Close should release, for archives created
// with New over a caller-managed reader.
func (a *Archive) SetCloser(c io.Closer) { a.c = c }

// Size is the size of the archive in bytes.
func (a *Archive) Size() int64 { return a.size }

// UUIDString renders the archive UUID as hex.
func (a *Archive) UUIDString() string { return fmt.Sprintf("%x", a.Hdr.UUID[:]) }

func (a *Archive) readMimeList() error {
	// The MIME list is a sequence of NUL terminated strings ended by an
	// empty string. It sits between the header and the first pointer list
	// and is small, so read generously and parse.
	const max = 64 << 10
	n := int64(max)
	if rem := a.size - int64(a.Hdr.MimeListPos); rem < n {
		n = rem
	}
	buf := make([]byte, n)
	m, err := a.r.ReadAt(buf, int64(a.Hdr.MimeListPos))
	if err != nil && !(errors.Is(err, io.EOF) && m > 0) {
		return fmt.Errorf("zim: reading MIME list: %w", err)
	}
	buf = buf[:m]
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, 0)
		if i < 0 {
			return errors.New("zim: MIME list not terminated")
		}
		if i == 0 {
			return nil
		}
		a.mimes = append(a.mimes, string(buf[:i]))
		buf = buf[i+1:]
	}
	return errors.New("zim: MIME list not terminated")
}

// MimeTypes lists the archive's MIME table.
func (a *Archive) MimeTypes() []string { return append([]string(nil), a.mimes...) }

// Entry is a directory entry: one article, image, stylesheet, metadata
// value or redirect.
type Entry struct {
	Index     uint32
	mime      uint16
	Namespace byte
	URL       string
	title     string
	Cluster   uint32
	Blob      uint32
	Target    uint32 // redirect target index
	a         *Archive
}

// IsRedirect reports whether e points at another entry.
func (e *Entry) IsRedirect() bool { return e.mime == mimeRedirect }

// IsSpecial reports whether e is a link target or deleted entry
// (both obsolete and carry no content).
func (e *Entry) IsSpecial() bool { return e.mime == mimeLinkTarget || e.mime == mimeDeleted }

// Title returns the human title, falling back to the URL.
func (e *Entry) Title() string {
	if e.title != "" {
		return e.title
	}
	return e.URL
}

// MimeType returns the content type, or "" for redirects.
func (e *Entry) MimeType() string {
	if int(e.mime) < len(e.a.mimes) {
		return e.a.mimes[e.mime]
	}
	return ""
}

// Path is the entry address as "<namespace>/<url>".
func (e *Entry) Path() string { return string(e.Namespace) + "/" + e.URL }

// EntryAt reads the directory entry with the given URL-order index.
func (a *Archive) EntryAt(idx uint32) (*Entry, error) {
	if idx >= a.Hdr.EntryCount {
		return nil, fmt.Errorf("zim: entry index %d out of range", idx)
	}
	a.direntMu.Lock()
	if e, ok := a.direntCache[idx]; ok {
		a.direntMu.Unlock()
		return e, nil
	}
	a.direntMu.Unlock()

	var pb [8]byte
	if _, err := a.r.ReadAt(pb[:], int64(a.Hdr.URLPtrPos)+8*int64(idx)); err != nil {
		return nil, fmt.Errorf("zim: reading URL pointer %d: %w", idx, err)
	}
	off := int64(binary.LittleEndian.Uint64(pb[:]))
	e, err := a.readDirent(off)
	if err != nil {
		return nil, fmt.Errorf("zim: entry %d: %w", idx, err)
	}
	e.Index = idx

	a.direntMu.Lock()
	if len(a.direntCache) > 16384 {
		// Cheap bound: binary searches revisit the same upper levels of
		// the tree, which repopulate within a few lookups.
		a.direntCache = make(map[uint32]*Entry)
	}
	a.direntCache[idx] = e
	a.direntMu.Unlock()
	return e, nil
}

func (a *Archive) readDirent(off int64) (*Entry, error) {
	if off < 0 || off >= a.size {
		return nil, errors.New("directory entry offset outside file")
	}
	for size := 512; ; size *= 4 {
		if rem := a.size - off; int64(size) > rem {
			size = int(rem)
		}
		buf := make([]byte, size)
		n, err := a.r.ReadAt(buf, off)
		if err != nil && !(errors.Is(err, io.EOF) && n > 0) {
			return nil, err
		}
		buf = buf[:n]
		e, complete, perr := a.parseDirent(buf)
		if perr != nil {
			return nil, perr
		}
		if complete {
			return e, nil
		}
		if int64(n) >= a.size-off || size >= 1<<20 {
			return nil, errors.New("directory entry truncated")
		}
	}
}

func (a *Archive) parseDirent(b []byte) (e *Entry, complete bool, err error) {
	le := binary.LittleEndian
	if len(b) < 8 {
		return nil, false, nil
	}
	e = &Entry{a: a}
	e.mime = le.Uint16(b[0:])
	paramLen := int(b[2])
	e.Namespace = b[3]
	var p int
	switch e.mime {
	case mimeRedirect:
		if len(b) < 12 {
			return nil, false, nil
		}
		e.Target = le.Uint32(b[8:])
		p = 12
	case mimeLinkTarget, mimeDeleted:
		p = 8
	default:
		if len(b) < 16 {
			return nil, false, nil
		}
		e.Cluster = le.Uint32(b[8:])
		e.Blob = le.Uint32(b[12:])
		p = 16
	}
	rest := b[p:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, false, nil
	}
	e.URL = string(rest[:i])
	rest = rest[i+1:]
	j := bytes.IndexByte(rest, 0)
	if j < 0 {
		return nil, false, nil
	}
	e.title = string(rest[:j])
	if len(rest[j+1:]) < paramLen {
		return nil, false, nil
	}
	return e, true, nil
}

// compareKey orders (namespace, key) pairs the way libzim does: by
// namespace byte, then by raw byte comparison of the key.
func compareKey(ns byte, key string, ens byte, ekey string) int {
	if ns != ens {
		if ns < ens {
			return -1
		}
		return 1
	}
	return strings.Compare(key, ekey)
}

// lowerBoundURL returns the first URL index whose (ns,url) >= (ns,url).
func (a *Archive) lowerBoundURL(ns byte, url string) (uint32, error) {
	var ferr error
	i := sort.Search(int(a.Hdr.EntryCount), func(i int) bool {
		if ferr != nil {
			return true
		}
		e, err := a.EntryAt(uint32(i))
		if err != nil {
			ferr = err
			return true
		}
		return compareKey(e.Namespace, e.URL, ns, url) >= 0
	})
	return uint32(i), ferr
}

// Find looks up an entry by namespace and URL without following redirects.
func (a *Archive) Find(ns byte, url string) (*Entry, error) {
	i, err := a.lowerBoundURL(ns, url)
	if err != nil {
		return nil, err
	}
	if i >= a.Hdr.EntryCount {
		return nil, ErrNotFound
	}
	e, err := a.EntryAt(i)
	if err != nil {
		return nil, err
	}
	if e.Namespace != ns || e.URL != url {
		return nil, ErrNotFound
	}
	return e, nil
}

// Resolve follows redirects starting at e and returns the final entry.
func (a *Archive) Resolve(e *Entry) (*Entry, error) {
	for hops := 0; e.IsRedirect(); hops++ {
		if hops > 32 {
			return nil, ErrRedirectCap
		}
		next, err := a.EntryAt(e.Target)
		if err != nil {
			return nil, err
		}
		e = next
	}
	return e, nil
}

// NamespaceRange returns the half open URL index range [lo,hi) of ns.
func (a *Archive) NamespaceRange(ns byte) (lo, hi uint32, err error) {
	a.nsMu.Lock()
	r, ok := a.nsRanges[ns]
	a.nsMu.Unlock()
	if ok {
		return r[0], r[1], nil
	}
	lo, err = a.lowerBoundURL(ns, "")
	if err != nil {
		return 0, 0, err
	}
	if ns == 0xff {
		hi = a.Hdr.EntryCount
	} else {
		hi, err = a.lowerBoundURL(ns+1, "")
		if err != nil {
			return 0, 0, err
		}
	}
	a.nsMu.Lock()
	a.nsRanges[ns] = [2]uint32{lo, hi}
	a.nsMu.Unlock()
	return lo, hi, nil
}

// ContentNamespace is the namespace holding articles: 'C' for modern
// files and 'A' for older ones.
func (a *Archive) ContentNamespace() byte {
	if a.NewNamespaceScheme {
		return 'C'
	}
	return 'A'
}

// MainEntry returns the archive's main (home) page, redirects resolved.
func (a *Archive) MainEntry() (*Entry, error) {
	if a.Hdr.MainPage != noMainPage && a.Hdr.MainPage < a.Hdr.EntryCount {
		e, err := a.EntryAt(a.Hdr.MainPage)
		if err == nil {
			return a.Resolve(e)
		}
	}
	if e, err := a.Find('W', "mainPage"); err == nil {
		return a.Resolve(e)
	}
	return nil, ErrNotFound
}

// Metadata returns the string value of metadata key (e.g. "Title",
// "Language", "Date"), or "" if absent.
func (a *Archive) Metadata(key string) string {
	e, err := a.Find('M', key)
	if err != nil {
		return ""
	}
	b, err := a.ReadAll(e)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// MetadataBytes returns the raw value of a metadata entry such as
// "Illustration_48x48@1" (a PNG).
func (a *Archive) MetadataBytes(key string) ([]byte, error) {
	e, err := a.Find('M', key)
	if err != nil {
		return nil, err
	}
	return a.ReadAll(e)
}
