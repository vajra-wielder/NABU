// Package pictures keeps Wikipedia's pictures on the drive for reading
// offline, and fetches them from Wikimedia within its rules for bots.
//
// The pictures of one wiki live in pictures/<wiki>/ as a few large pack
// files rather than millions of small ones: FAT32 sticks waste up to
// 32 KB per file and slow down with huge folders. Each pack stays under
// 4 GB, FAT32's limit.
package pictures

import (
	"github.com/vajra-wielder/nabu/internal/drivefile"

	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Width is the width, in pixels, of the pictures kept on the drive:
// Wikipedia's default thumbnail size, one of Wikimedia's standard sizes.
const Width = 250

var maxPack int64 = 3_900_000_000 // bytes per pack file (FAT32: < 4 GiB)

const (
	maxSize   = 1<<24 - 1 // largest picture kept (16 MB)
	entrySize = 16
	dirEvery  = 256 // one in-memory directory key per this many entries
)

// Key identifies a picture by its file name.
func Key(name string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(Normalize(name)))
	k := h.Sum64()
	if k == 0 {
		k = 1
	}
	return k
}

// Normalize is a file name as Wikimedia stores it: underscores for
// spaces, first letter in upper case.
func Normalize(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, " ", "_"))
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	name = strings.Trim(name, "_")
	if r, n := utf8.DecodeRuneInString(name); n > 0 && unicode.IsLower(r) {
		name = string(unicode.ToUpper(r)) + name[n:]
	}
	return name
}

// entry: key, then pack (8 bits) | offset (32 bits) | length (24 bits).
// Length 0 means "Wikimedia has no such picture".
type entry struct{ key, loc uint64 }

func (e entry) pack() int     { return int(e.loc >> 56) }
func (e entry) offset() int64 { return int64(e.loc >> 24 & 0xffffffff) }
func (e entry) length() int   { return int(e.loc & 0xffffff) }

// Store is the pictures of one wiki.
type Store struct {
	Dir string

	mu     sync.Mutex
	packs  []*drivefile.File // opened only while in use: the drive may be pulled out
	cur    int               // pack being appended to
	size   int64             // its size
	index  *drivefile.File
	n      int64    // entries in index.bin
	dir    []uint64 // every dirEvery-th key of index.bin
	recent map[uint64]uint64
	log    *drivefile.File
	logLen int64
	bytes  int64 // total size of the packs
	count  int   // pictures (not counting known-missing)
}

// Open opens (or starts) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{Dir: dir, recent: map[uint64]uint64{}}
	for i := 0; ; i++ {
		f, err := drivefile.OpenFile(s.packPath(i), os.O_RDWR, 0o644)
		if err != nil {
			break
		}
		st, _ := f.Stat()
		s.packs = append(s.packs, f)
		s.cur, s.size = i, st.Size()
		s.bytes += st.Size()
	}
	if err := s.openIndex(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.loadRecent(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) packPath(i int) string {
	return filepath.Join(s.Dir, fmt.Sprintf("pictures-%03d.pack", i))
}
func (s *Store) indexPath() string  { return filepath.Join(s.Dir, "index.bin") }
func (s *Store) recentPath() string { return filepath.Join(s.Dir, "recent.bin") }

func (s *Store) openIndex() error {
	f, err := drivefile.OpenRead(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		s.index, s.n, s.dir = nil, 0, nil
		return nil
	} else if err != nil {
		return err
	}
	st, _ := f.Stat()
	s.index, s.n = f, st.Size()/entrySize
	s.dir = s.dir[:0]
	var b [8]byte
	for i := int64(0); i < s.n; i += dirEvery {
		if _, err := f.ReadAt(b[:], i*entrySize); err != nil {
			return err
		}
		s.dir = append(s.dir, binary.LittleEndian.Uint64(b[:]))
	}
	// Count pictures (entries with data) once: the index is sequential
	// and small (16 bytes a picture).
	s.count = 0
	r := bufio.NewReaderSize(io.NewSectionReader(f, 0, s.n*entrySize), 1<<16)
	var e [entrySize]byte
	for {
		if _, err := io.ReadFull(r, e[:]); err != nil {
			break
		}
		if binary.LittleEndian.Uint64(e[8:])&0xffffff != 0 {
			s.count++
		}
	}
	return nil
}

// loadRecent reads the entries added since the last compaction, keeping
// only those whose picture was fully written (a drive pulled out in the
// middle of a write loses at most that picture).
func (s *Store) loadRecent() error {
	f, err := drivefile.OpenFile(s.recentPath(), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	s.log = f
	st, err := f.Stat()
	if err != nil {
		return err
	}
	b := make([]byte, st.Size())
	if _, err := f.ReadAt(b, 0); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	good := 0
	for i := 0; i+entrySize <= len(b); i += entrySize {
		e := entry{binary.LittleEndian.Uint64(b[i:]), binary.LittleEndian.Uint64(b[i+8:])}
		if e.length() > 0 {
			if e.pack() >= len(s.packs) {
				break
			}
			st, _ := s.packs[e.pack()].Stat()
			if e.offset()+int64(e.length()) > st.Size() {
				break
			}
		}
		if _, dup := s.recent[e.key]; !dup && e.length() > 0 {
			if _, ok := s.lookupIndex(e.key); !ok {
				s.count++
			}
		}
		s.recent[e.key] = e.loc
		good = i + entrySize
	}
	if good != len(b) {
		f.Truncate(int64(good))
	}
	s.logLen = int64(good)
	return nil
}

func (s *Store) lookupIndex(key uint64) (uint64, bool) {
	if s.index == nil || s.n == 0 {
		return 0, false
	}
	// Directory: the block whose first key is the last one <= key.
	blk := sort.Search(len(s.dir), func(i int) bool { return s.dir[i] > key }) - 1
	if blk < 0 {
		return 0, false
	}
	lo := int64(blk) * dirEvery
	hi := lo + dirEvery
	if hi > s.n {
		hi = s.n
	}
	buf := make([]byte, (hi-lo)*entrySize)
	if _, err := s.index.ReadAt(buf, lo*entrySize); err != nil {
		return 0, false
	}
	m := int(hi - lo)
	i := sort.Search(m, func(i int) bool { return binary.LittleEndian.Uint64(buf[i*entrySize:]) >= key })
	if i < m && binary.LittleEndian.Uint64(buf[i*entrySize:]) == key {
		return binary.LittleEndian.Uint64(buf[i*entrySize+8:]), true
	}
	return 0, false
}

func (s *Store) lookup(key uint64) (entry, bool) {
	if loc, ok := s.recent[key]; ok {
		return entry{key, loc}, true
	}
	loc, ok := s.lookupIndex(key)
	return entry{key, loc}, ok
}

// Get returns a picture. known is true when the store has an answer:
// the picture, or the fact that Wikimedia has none by that name.
func (s *Store) Get(name string) (data []byte, known bool, err error) {
	s.mu.Lock()
	e, ok := s.lookup(Key(name))
	var f *drivefile.File
	if ok && e.length() > 0 && e.pack() < len(s.packs) {
		f = s.packs[e.pack()]
	}
	s.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	if e.length() == 0 {
		return nil, true, nil
	}
	if f == nil {
		return nil, false, nil
	}
	data = make([]byte, e.length())
	if _, err := f.ReadAt(data, e.offset()); err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Has reports whether the store knows the picture (kept or missing).
func (s *Store) Has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.lookup(Key(name))
	return ok
}

// Put keeps a picture; nil data records that Wikimedia has none.
func (s *Store) Put(name string, data []byte) error {
	if len(data) > maxSize {
		data = nil
	}
	key := Key(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log == nil {
		return errors.New("pictures: store closed")
	}
	old, had := s.lookup(key)
	if had && (old.length() > 0 || data == nil) {
		return nil
	}
	var loc uint64
	if len(data) > 0 {
		if len(s.packs) == 0 || s.size+int64(len(data)) > maxPack {
			if len(s.packs) >= 255 {
				return errors.New("pictures: store full")
			}
			f, err := drivefile.OpenFile(s.packPath(len(s.packs)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			s.packs = append(s.packs, f)
			s.cur, s.size = len(s.packs)-1, 0
		}
		if _, err := s.packs[s.cur].WriteAt(data, s.size); err != nil {
			return err
		}
		loc = uint64(s.cur)<<56 | uint64(s.size)<<24 | uint64(len(data))
		s.size += int64(len(data))
		s.bytes += int64(len(data))
		s.count++
	}
	var b [entrySize]byte
	binary.LittleEndian.PutUint64(b[:], key)
	binary.LittleEndian.PutUint64(b[8:], loc)
	if _, err := s.log.WriteAt(b[:], s.logLen); err != nil {
		return err
	}
	s.logLen += entrySize
	s.recent[key] = loc
	return nil
}

// Stats is how much the store holds.
type Stats struct {
	Pictures int
	Bytes    int64
}

// Stats reports the store's size.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{Pictures: s.count, Bytes: s.bytes}
}

// Flush makes sure what was kept is on the drive itself, not waiting in
// the computer's memory: done after each picture kept while reading, so
// the drive can be pulled out once the page has loaded.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.packs) > 0 {
		if err := s.packs[s.cur].Sync(); err != nil {
			return err
		}
	}
	if s.log != nil {
		return s.log.Sync()
	}
	return nil
}

// Sync flushes the store to the drive and, when many pictures were added
// since, merges them into the sorted index.
func (s *Store) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.packs {
		f.Sync()
	}
	if s.log != nil {
		s.log.Sync()
	}
	if len(s.recent) >= 50_000 {
		return s.compact()
	}
	return nil
}

// Compact merges recent additions into the sorted index.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compact()
}

func (s *Store) compact() error {
	if len(s.recent) == 0 || s.log == nil {
		return nil
	}
	for _, f := range s.packs {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	keys := make([]uint64, 0, len(s.recent))
	for k := range s.recent {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	tmp := s.indexPath() + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(out, 1<<20)
	put := func(k, loc uint64) {
		var b [entrySize]byte
		binary.LittleEndian.PutUint64(b[:], k)
		binary.LittleEndian.PutUint64(b[8:], loc)
		w.Write(b[:])
	}
	ki := 0
	if s.index != nil {
		r := bufio.NewReaderSize(io.NewSectionReader(s.index, 0, s.n*entrySize), 1<<20)
		var b [entrySize]byte
		for {
			if _, err := io.ReadFull(r, b[:]); err != nil {
				break
			}
			k, loc := binary.LittleEndian.Uint64(b[:]), binary.LittleEndian.Uint64(b[8:])
			for ki < len(keys) && keys[ki] < k {
				put(keys[ki], s.recent[keys[ki]])
				ki++
			}
			if ki < len(keys) && keys[ki] == k {
				if nl := s.recent[k]; nl&0xffffff != 0 || loc&0xffffff == 0 {
					loc = nl // a picture found after all replaces "missing"
				}
				ki++
			}
			put(k, loc)
		}
	}
	for ; ki < len(keys); ki++ {
		put(keys[ki], s.recent[keys[ki]])
	}
	if err := w.Flush(); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	out.Close()
	if s.index != nil {
		s.index.Close()
		s.index = nil
	}
	if err := drivefile.Rename(tmp, s.indexPath()); err != nil { // another NABU may be reading it
		return err
	}
	// Only once the merged index is safely in place is the log emptied.
	if err := s.log.Truncate(0); err != nil {
		return err
	}
	s.logLen = 0
	s.recent = map[uint64]uint64{}
	return s.openIndex()
}

// Close compacts and closes the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.compact()
	for _, f := range s.packs {
		f.Close()
	}
	s.packs = nil
	if s.index != nil {
		s.index.Close()
		s.index = nil
	}
	if s.log != nil {
		s.log.Close()
		s.log = nil
	}
	return err
}
