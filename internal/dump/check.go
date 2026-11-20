package dump

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/vajra-wielder/nabu/internal/bzip2w"
	"github.com/vajra-wielder/nabu/internal/drivefile"
)

// A drive pulled out while busy, or with a loose connection, can come
// back with damaged files. Every bundle (compressed stream) of a dump
// carries checksums, so Check can tell exactly which are damaged and
// which articles they hold; Repair copies those bundles back from
// another copy of the same dump (another NABU drive).

// Damage is one damaged bundle.
type Damage struct {
	Pair       int
	Start, End int64    // its bytes in the pair's data
	Titles     []string // the pages in it, as far as the title index tells
	Err        string
}

// CheckResult is what Check found.
type CheckResult struct {
	Streams int      // bundles read
	Damaged []Damage // in the order of the dump
	Index   string   // a problem with the title index ("" if none)
}

// CheckOptions tune Check.
type CheckOptions struct {
	Workers  int
	Progress Progress // bundles checked
	Stop     <-chan struct{}
}

// ErrCheckStopped is returned when a check is stopped.
var ErrCheckStopped = errors.New("dump: check stopped")

// Check reads every bundle of the dump, and every block of its title
// index.
func (r *Reader) Check(opt CheckOptions) (CheckResult, error) {
	if opt.Workers <= 0 {
		opt.Workers = runtime.NumCPU() - 1
		if opt.Workers < 1 {
			opt.Workers = 1
		}
	}
	ix := r.Index
	n := ix.StreamCount()
	res := CheckResult{Streams: n}

	// The header of each pair: the bytes before its first bundle.
	firstOf := map[int]int64{}
	for _, s := range ix.streams {
		if _, ok := firstOf[int(s.pair)]; !ok {
			firstOf[int(s.pair)] = s.off
		}
	}
	for pair, first := range firstOf {
		if pair < len(r.data) {
			if _, err := r.stream(pair, 0, first); err != nil {
				res.Damaged = append(res.Damaged, Damage{Pair: pair, Start: 0, End: first,
					Titles: []string{"(the dump's header: its site information)"}, Err: err.Error()})
			}
		}
	}

	var mu sync.Mutex
	bad := map[uint32]*Damage{}
	var next, done int64 = -1, 0
	var wg sync.WaitGroup
	stopped := false
	for w := 0; w < opt.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				s := atomic.AddInt64(&next, 1)
				if s >= int64(n) {
					return
				}
				select {
				case <-opt.Stop:
					mu.Lock()
					stopped = true
					mu.Unlock()
					return
				default:
				}
				pair, start, end, err := ix.streamRange(uint32(s))
				if err == nil {
					if pair >= len(r.data) {
						err = ErrStale
					} else {
						_, err = r.stream(pair, start, end)
					}
				}
				if err != nil {
					mu.Lock()
					bad[uint32(s)] = &Damage{Pair: pair, Start: start, End: end, Err: err.Error()}
					mu.Unlock()
				}
				d := atomic.AddInt64(&done, 1)
				if opt.Progress != nil && (d%64 == 0 || d == int64(n)) {
					opt.Progress(d, int64(n))
				}
			}
		}()
	}
	wg.Wait()
	if stopped {
		return res, ErrCheckStopped
	}

	// Which pages are in the damaged bundles: read the whole title
	// index, which checks it too.
	for b := 0; b < ix.Blocks(); b++ {
		entries, err := ix.block(b)
		if err != nil {
			res.Index = fmt.Sprintf("block %d of %d cannot be read (%v)", b+1, ix.Blocks(), err)
			continue
		}
		for _, e := range entries {
			if d := bad[e.Stream]; d != nil && len(d.Titles) < 200 {
				d.Titles = append(d.Titles, e.Title)
			}
		}
	}
	for _, d := range bad {
		sort.Strings(d.Titles)
		res.Damaged = append(res.Damaged, *d)
	}
	sort.Slice(res.Damaged, func(i, j int) bool {
		a, b := res.Damaged[i], res.Damaged[j]
		if a.Pair != b.Pair {
			return a.Pair < b.Pair
		}
		return a.Start < b.Start
	})
	return res, nil
}

// SameDump reports whether other is a copy of the same dump as f: the
// same files, of the same sizes.
func SameDump(f, other *Files) bool {
	if f.Key() != other.Key() || len(f.Pairs) != len(other.Pairs) {
		return false
	}
	for i := range f.Pairs {
		a, b := f.Pairs[i], other.Pairs[i]
		if a.Size != b.Size || fileSize(a.Index) != fileSize(b.Index) {
			return false
		}
	}
	return true
}

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return -1
}

// Repair copies the damaged bundles from another copy of the same dump,
// each checked before it is written. It returns how many were repaired;
// a bundle damaged in both copies stays as it is.
func Repair(f *Files, damaged []Damage, from *Files) (int, error) {
	if !SameDump(f, from) {
		return 0, errors.New("the other copy is not the same dump")
	}
	fixed := 0
	for _, d := range damaged {
		if d.Pair >= len(from.Pairs) || d.End <= d.Start {
			continue
		}
		src, _, err := OpenData(from.Pairs[d.Pair].Data)
		if err != nil {
			return fixed, err
		}
		raw := make([]byte, d.End-d.Start)
		_, err = src.ReadAt(raw, d.Start)
		src.Close()
		if err != nil {
			continue
		}
		if _, err := bzip2w.Decompress(raw); err != nil {
			continue // damaged there too
		}
		if err := writeData(f.Pairs[d.Pair].Data, d.Start, raw); err != nil {
			return fixed, fmt.Errorf("writing the repair: %w", err)
		}
		fixed++
	}
	return fixed, nil
}

// writeData writes b at off of a data file that may be in pieces, and
// flushes it to the drive.
func writeData(pieces []string, off int64, b []byte) error {
	var start int64
	for _, p := range pieces {
		size := fileSize(p)
		if size < 0 {
			return fmt.Errorf("%s is missing", p)
		}
		if off < start+size && len(b) > 0 {
			f, err := drivefile.OpenFile(p, os.O_RDWR, 0o644)
			if err != nil {
				return err
			}
			n := int64(len(b))
			if room := start + size - off; n > room {
				n = room
			}
			_, err = f.WriteAt(b[:n], off-start)
			if err == nil {
				err = f.Sync()
			}
			f.Close()
			if err != nil {
				return err
			}
			b, off = b[n:], off+n
		}
		start += size
	}
	if len(b) > 0 {
		return errors.New("past the end of the dump")
	}
	return nil
}
