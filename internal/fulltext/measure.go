package fulltext

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// MeasureBuild times the work of building on a sample of rd's bundles,
// spread through the whole dump, on this computer with the workers a
// build uses, for about d: what a build of all of it would take.
//
// Build time is mostly decompressing and turning wikitext into text, and
// how fast that goes depends on the wiki (English articles carry far more
// templates and references per byte than Simple English ones) and on the
// computer (its real cores, its memory, whether it is on battery): no
// rate measured elsewhere predicts it well. A few seconds of the real
// work here does.
func MeasureBuild(rd interface {
	StreamCount() int
	StreamBytes(s uint32) int64
}, scan func(s uint32), workers int, d time.Duration) (total time.Duration, ok bool) {
	n := rd.StreamCount()
	if n == 0 {
		return 0, false
	}
	if workers <= 0 {
		workers = runtime.NumCPU() - 1
		if workers < 1 {
			workers = 1
		}
	}
	// Every k-th bundle from a few starting points, so the sample covers
	// the whole dump (its early and late parts differ).
	order := make(chan uint32, workers)
	stopAt := time.Now().Add(d)
	go func() {
		defer close(order)
		step := n / 97
		if step < 1 {
			step = 1
		}
		for off := 0; off < step; off += 1 + step/7 {
			for s := off; s < n; s += step {
				if time.Now().After(stopAt) {
					return
				}
				order <- uint32(s)
			}
		}
	}()
	var bytes int64
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range order {
				scan(s)
				atomic.AddInt64(&bytes, rd.StreamBytes(s))
			}
		}()
	}
	wg.Wait()
	took := time.Since(start)
	if bytes == 0 || took <= 0 {
		return 0, false
	}
	var all int64
	for s := 0; s < n; s++ {
		all += rd.StreamBytes(uint32(s))
	}
	// Sorting and writing the index after reading: a few percent more.
	return time.Duration(float64(took) * float64(all) / float64(bytes) * 1.05), true
}
