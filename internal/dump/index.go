package dump

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
)

// Entry is one title in the index.
type Entry struct {
	Title  string
	Fold   string
	Stream uint32
	ID     uint64
	NS     int
}

// Index is an opened .nidx file.
type Index struct {
	f        *os.File
	Meta     Meta
	streams  []streamRef
	dirOff   []int64 // block offsets, plus the end of the last block
	dirFold  []string
	dirTitle []string

	cacheMu sync.Mutex
	cache   map[int][]Entry
	order   []int
}

// ErrStale means the index was built from different files, or by an
// older NABU, and must be rebuilt.
var ErrStale = errors.New("dump: index is out of date")

// OpenIndex opens an index and checks it still matches f.
func OpenIndex(path string, f *Files) (*Index, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	ix := &Index{f: file, cache: map[int][]Entry{}}
	if err := ix.load(); err != nil {
		file.Close()
		return nil, err
	}
	if ix.Meta.Version != IndexVersion {
		file.Close()
		return nil, ErrStale
	}
	if f != nil {
		want := SourcesOf(f)
		if len(want) != len(ix.Meta.Sources) {
			file.Close()
			return nil, ErrStale
		}
		for i := range want {
			if want[i] != ix.Meta.Sources[i] {
				file.Close()
				return nil, ErrStale
			}
		}
	}
	return ix, nil
}

func (ix *Index) load() error {
	st, err := ix.f.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	if size < int64(len(idxMagic))+trailerLen {
		return ErrStale
	}
	var tr [trailerLen]byte
	if _, err := ix.f.ReadAt(tr[:], size-trailerLen); err != nil {
		return err
	}
	if string(tr[40:]) != idxEnd {
		return ErrStale // incomplete or foreign file
	}
	le := binary.LittleEndian
	streamsOff := int64(le.Uint64(tr[0:]))
	dirOff := int64(le.Uint64(tr[8:]))
	metaOff := int64(le.Uint64(tr[16:]))
	if !(int64(len(idxMagic)) <= streamsOff && streamsOff <= dirOff && dirOff <= metaOff && metaOff <= size-trailerLen) {
		return ErrStale
	}
	if le.Uint64(tr[32:]) != IndexVersion {
		return ErrStale
	}

	mb := make([]byte, size-trailerLen-metaOff)
	if _, err := ix.f.ReadAt(mb, metaOff); err != nil {
		return err
	}
	if err := json.Unmarshal(mb, &ix.Meta); err != nil {
		return fmt.Errorf("dump: index metadata: %w", err)
	}
	if ix.Meta.Site == nil {
		return ErrStale
	}
	ix.Meta.Site.init()

	r := bufio.NewReaderSize(io.NewSectionReader(ix.f, streamsOff, dirOff-streamsOff), 1<<20)
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	ix.streams = make([]streamRef, 0, n)
	lastPair, lastOff := -1, int64(0)
	for i := uint64(0); i < n; i++ {
		p, err1 := binary.ReadUvarint(r)
		d, err2 := binary.ReadUvarint(r)
		if err1 != nil || err2 != nil {
			return ErrStale
		}
		if int(p) != lastPair {
			lastOff = 0
		}
		lastOff += int64(d)
		lastPair = int(p)
		ix.streams = append(ix.streams, streamRef{pair: uint16(p), off: lastOff})
	}

	r = bufio.NewReaderSize(io.NewSectionReader(ix.f, dirOff, metaOff-dirOff), 1<<20)
	n, err = binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	var off int64
	for i := uint64(0); i < n; i++ {
		d, err1 := binary.ReadUvarint(r)
		l, err2 := binary.ReadUvarint(r)
		if err1 != nil || err2 != nil || l > 1<<16 {
			return ErrStale
		}
		t := make([]byte, l)
		if _, err := io.ReadFull(r, t); err != nil {
			return ErrStale
		}
		off += int64(d)
		ix.dirOff = append(ix.dirOff, off)
		ix.dirTitle = append(ix.dirTitle, string(t))
		ix.dirFold = append(ix.dirFold, Fold(string(t)))
	}
	ix.dirOff = append(ix.dirOff, streamsOff)
	return nil
}

// Close releases the file.
func (ix *Index) Close() error { return ix.f.Close() }

// Site is the wiki's siteinfo.
func (ix *Index) Site() *Siteinfo { return ix.Meta.Site }

// Blocks is the number of blocks.
func (ix *Index) Blocks() int { return len(ix.dirTitle) }

func (ix *Index) block(i int) ([]Entry, error) {
	ix.cacheMu.Lock()
	if b, ok := ix.cache[i]; ok {
		ix.cacheMu.Unlock()
		return b, nil
	}
	ix.cacheMu.Unlock()

	buf := make([]byte, ix.dirOff[i+1]-ix.dirOff[i])
	if _, err := ix.f.ReadAt(buf, ix.dirOff[i]); err != nil {
		return nil, err
	}
	r := bytes.NewReader(buf)
	var out []Entry
	prev := ""
	for r.Len() > 0 {
		shared, err1 := binary.ReadUvarint(r)
		l, err2 := binary.ReadUvarint(r)
		if err1 != nil || err2 != nil || int(shared) > len(prev) || int(l) > r.Len() {
			return nil, errors.New("dump: corrupt index block")
		}
		suf := make([]byte, l)
		r.Read(suf)
		title := prev[:shared] + string(suf)
		s, _ := binary.ReadUvarint(r)
		id, _ := binary.ReadUvarint(r)
		ns, err := binary.ReadVarint(r)
		if err != nil {
			return nil, errors.New("dump: corrupt index block")
		}
		out = append(out, Entry{Title: title, Fold: Fold(title), Stream: uint32(s), ID: id, NS: int(ns)})
		prev = title
	}

	ix.cacheMu.Lock()
	if len(ix.order) >= 512 {
		delete(ix.cache, ix.order[0])
		ix.order = ix.order[1:]
	}
	ix.cache[i] = out
	ix.order = append(ix.order, i)
	ix.cacheMu.Unlock()
	return out, nil
}

// scan calls fn for entries from the first one whose fold is >= key,
// in order, until fn returns false.
func (ix *Index) scan(key string, fn func(Entry) bool) error {
	n := len(ix.dirFold)
	i := sort.Search(n, func(i int) bool { return ix.dirFold[i] >= key })
	if i > 0 {
		i-- // equal keys may begin in the previous block
	}
	for ; i < n; i++ {
		entries, err := ix.block(i)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Fold < key {
				continue
			}
			if !fn(e) {
				return nil
			}
		}
	}
	return nil
}

// Find returns the entry for an exact, normalized title. When no title
// matches exactly, a title equal apart from case and accents is returned
// with exact=false, which is how "go to page" forgives typing.
func (ix *Index) Find(title string) (e Entry, exact bool, err error) {
	key := Fold(title)
	var best *Entry
	err = ix.scan(key, func(c Entry) bool {
		if c.Fold != key {
			return false
		}
		if c.Title == title {
			cc := c
			best = &cc
			exact = true
			return false
		}
		if best == nil {
			cc := c
			best = &cc
		}
		return true
	})
	if err != nil {
		return Entry{}, false, err
	}
	if best == nil {
		return Entry{}, false, ErrNotFound
	}
	return *best, exact, nil
}

// ErrNotFound means no such page.
var ErrNotFound = errors.New("dump: page not found")

// Prefix lists up to limit titles starting with q (case and accents
// ignored) in namespaces accepted by nsOK. Exact matches come first.
func (ix *Index) Prefix(q string, limit int, nsOK func(int) bool) ([]Entry, error) {
	key := Fold(q)
	if key == "" || limit <= 0 {
		return nil, nil
	}
	var exact, rest []Entry
	scanned := 0
	err := ix.scan(key, func(e Entry) bool {
		if !strings.HasPrefix(e.Fold, key) {
			return false
		}
		scanned++
		if nsOK == nil || nsOK(e.NS) {
			if e.Fold == key {
				exact = append(exact, e)
			} else if len(rest) < limit {
				rest = append(rest, e)
			}
		}
		// Sorted by key, so exact matches come first; stop once full.
		return len(rest) < limit && scanned < 50000
	})
	out := append(exact, rest...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, err
}

// Random picks a random entry in namespace ns.
func (ix *Index) Random(rng *rand.Rand, ns int) (Entry, error) {
	if len(ix.dirFold) == 0 {
		return Entry{}, ErrNotFound
	}
	for tries := 0; tries < 64; tries++ {
		b, err := ix.block(rng.Intn(len(ix.dirFold)))
		if err != nil {
			return Entry{}, err
		}
		if len(b) == 0 {
			continue
		}
		e := b[rng.Intn(len(b))]
		if e.NS == ns {
			return e, nil
		}
	}
	return Entry{}, ErrNotFound
}

// streamRange is where stream s lives: data pair and byte range.
func (ix *Index) streamRange(s uint32) (pair int, start, end int64, err error) {
	if int(s) >= len(ix.streams) {
		return 0, 0, 0, errors.New("dump: stream out of range")
	}
	ref := ix.streams[s]
	pair = int(ref.pair)
	if pair >= len(ix.Meta.DataSize) {
		return 0, 0, 0, ErrStale
	}
	end = ix.Meta.DataSize[pair]
	if int(s)+1 < len(ix.streams) && ix.streams[s+1].pair == ref.pair {
		end = ix.streams[s+1].off
	}
	return pair, ref.off, end, nil
}
