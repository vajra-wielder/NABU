package zim

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/zlib"
	"container/list"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Cluster compression codes (low nibble of the cluster info byte).
const (
	compDefault = 0 // historic alias for "none"
	compNone    = 1
	compZlib    = 2 // deprecated, still readable
	compBzip2   = 3 // deprecated, still readable
	compXZ      = 4 // LZMA2 inside an xz container
	compZstd    = 5
)

// clusterBounds returns the file offset of cluster n and where it ends.
func (a *Archive) clusterBounds(n uint32) (start, end int64, err error) {
	if n >= a.Hdr.ClusterCount {
		return 0, 0, fmt.Errorf("zim: cluster %d out of range", n)
	}
	var b [16]byte
	read := b[:16]
	if n+1 == a.Hdr.ClusterCount {
		read = b[:8]
	}
	if _, err := a.r.ReadAt(read, int64(a.Hdr.ClusterPtrPos)+8*int64(n)); err != nil {
		return 0, 0, fmt.Errorf("zim: reading cluster pointer %d: %w", n, err)
	}
	start = int64(binary.LittleEndian.Uint64(b[0:]))
	if n+1 == a.Hdr.ClusterCount {
		end = int64(a.Hdr.ChecksumPos)
		if end <= start || end > a.size {
			end = a.size
		}
	} else {
		end = int64(binary.LittleEndian.Uint64(b[8:]))
	}
	if start < 0 || start >= end || end > a.size {
		return 0, 0, fmt.Errorf("zim: cluster %d has invalid bounds (truncated file?)", n)
	}
	return start, end, nil
}

// BlobReader returns a seekable reader over the content of e (which must
// not be a redirect) and its size. Uncompressed blobs, typically images,
// are read straight from disk so large files never sit in memory and HTTP
// range requests (video, audio) work.
func (a *Archive) BlobReader(e *Entry) (io.ReadSeeker, int64, error) {
	off, n, compressed, err := a.blobSpan(e)
	if err != nil {
		return nil, 0, err
	}
	if !compressed {
		return io.NewSectionReader(a.r, off, n), n, nil
	}
	start, end, err := a.clusterBounds(e.Cluster)
	if err != nil {
		return nil, 0, err
	}
	var info [1]byte
	if _, err := a.r.ReadAt(info[:], start); err != nil {
		return nil, 0, err
	}
	offSize := int64(4)
	if info[0]&0x10 != 0 {
		offSize = 8
	}
	data, err := a.clusters.get(e.Cluster, func() ([]byte, error) {
		return a.decompressCluster(info[0]&0x0f, start, end)
	})
	if err != nil {
		return nil, 0, err
	}
	blob, err := blobFromData(data, offSize, e.Blob)
	if err != nil {
		return nil, 0, fmt.Errorf("zim: cluster %d: %w", e.Cluster, err)
	}
	return bytes.NewReader(blob), int64(len(blob)), nil
}

// blobSpan locates e's blob. For uncompressed clusters it returns the
// absolute file offset and size; for compressed ones only compressed=true.
func (a *Archive) blobSpan(e *Entry) (off, size int64, compressed bool, err error) {
	if e.IsRedirect() || e.IsSpecial() {
		return 0, 0, false, errors.New("zim: entry has no content")
	}
	start, end, err := a.clusterBounds(e.Cluster)
	if err != nil {
		return 0, 0, false, err
	}
	var info [1]byte
	if _, err := a.r.ReadAt(info[:], start); err != nil {
		return 0, 0, false, err
	}
	comp := info[0] & 0x0f
	if comp != compNone && comp != compDefault {
		return 0, 0, true, nil
	}
	offSize := int64(4)
	if info[0]&0x10 != 0 {
		offSize = 8
	}
	base := start + 1
	readOff := func(i int64) (int64, error) {
		b := make([]byte, offSize)
		if _, err := a.r.ReadAt(b, base+i*offSize); err != nil {
			return 0, err
		}
		if offSize == 8 {
			return int64(binary.LittleEndian.Uint64(b)), nil
		}
		return int64(binary.LittleEndian.Uint32(b)), nil
	}
	first, err := readOff(0)
	if err != nil {
		return 0, 0, false, err
	}
	if int64(e.Blob) >= first/offSize-1 {
		return 0, 0, false, fmt.Errorf("zim: blob %d out of range in cluster %d", e.Blob, e.Cluster)
	}
	bs, err := readOff(int64(e.Blob))
	if err != nil {
		return 0, 0, false, err
	}
	be, err := readOff(int64(e.Blob) + 1)
	if err != nil {
		return 0, 0, false, err
	}
	if be < bs || base+be > end {
		return 0, 0, false, fmt.Errorf("zim: corrupt blob offsets in cluster %d", e.Cluster)
	}
	return base + bs, be - bs, false, nil
}

// ReadAll returns the full content of e. Redirects are followed.
func (a *Archive) ReadAll(e *Entry) ([]byte, error) {
	e, err := a.Resolve(e)
	if err != nil {
		return nil, err
	}
	r, n, err := a.BlobReader(e)
	if err != nil {
		return nil, err
	}
	if br, ok := r.(*bytes.Reader); ok {
		out := make([]byte, n)
		_, err := io.ReadFull(br, out)
		return out, err
	}
	out := make([]byte, n)
	_, err = io.ReadFull(r, out)
	return out, err
}

func blobFromData(data []byte, offSize int64, blob uint32) ([]byte, error) {
	get := func(i int64) (int64, bool) {
		p := i * offSize
		if p+offSize > int64(len(data)) {
			return 0, false
		}
		if offSize == 8 {
			return int64(binary.LittleEndian.Uint64(data[p:])), true
		}
		return int64(binary.LittleEndian.Uint32(data[p:])), true
	}
	first, ok := get(0)
	if !ok {
		return nil, errors.New("cluster too short")
	}
	if int64(blob) >= first/offSize-1 {
		return nil, fmt.Errorf("blob %d out of range", blob)
	}
	s, ok1 := get(int64(blob))
	t, ok2 := get(int64(blob) + 1)
	if !ok1 || !ok2 || s > t || t > int64(len(data)) {
		return nil, errors.New("corrupt blob offsets")
	}
	return data[s:t:t], nil
}

var zstdPool = sync.Pool{New: func() interface{} {
	d, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxWindow(1<<30))
	if err != nil {
		return err
	}
	return d
}}

func (a *Archive) decompressCluster(comp byte, start, end int64) ([]byte, error) {
	src := bufio.NewReaderSize(io.NewSectionReader(a.r, start+1, end-start-1), 64<<10)
	var r io.Reader
	switch comp {
	case compZstd:
		v := zstdPool.Get()
		d, ok := v.(*zstd.Decoder)
		if !ok {
			return nil, fmt.Errorf("zim: zstd: %v", v)
		}
		if err := d.Reset(src); err != nil {
			zstdPool.Put(d)
			return nil, err
		}
		var out bytes.Buffer
		_, err := io.Copy(&out, d)
		d.Reset(nil)
		zstdPool.Put(d)
		if err != nil {
			return nil, fmt.Errorf("zim: zstd: %w", err)
		}
		return out.Bytes(), nil
	case compXZ:
		xr, err := xz.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("zim: xz: %w", err)
		}
		r = xr
	case compZlib:
		zr, err := zlib.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("zim: zlib: %w", err)
		}
		defer zr.Close()
		r = zr
	case compBzip2:
		r = bzip2.NewReader(src)
	default:
		return nil, fmt.Errorf("zim: unknown cluster compression %d", comp)
	}
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		return nil, fmt.Errorf("zim: decompressing cluster: %w", err)
	}
	return out.Bytes(), nil
}

// clusterCache is a byte-bounded LRU of decompressed clusters that also
// collapses concurrent loads of the same cluster into one (a page and
// its images often live side by side and are requested in parallel).
type clusterCache struct {
	mu       sync.Mutex
	maxBytes int
	curBytes int
	ll       *list.List
	items    map[uint32]*list.Element
	loading  map[uint32]*loadCall
}

type cacheItem struct {
	key  uint32
	data []byte
}

type loadCall struct {
	wg   sync.WaitGroup
	data []byte
	err  error
}

func newClusterCache(maxBytes int) *clusterCache {
	return &clusterCache{
		maxBytes: maxBytes,
		ll:       list.New(),
		items:    make(map[uint32]*list.Element),
		loading:  make(map[uint32]*loadCall),
	}
}

func (c *clusterCache) get(key uint32, load func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		data := el.Value.(*cacheItem).data
		c.mu.Unlock()
		return data, nil
	}
	if call, ok := c.loading[key]; ok {
		c.mu.Unlock()
		call.wg.Wait()
		return call.data, call.err
	}
	call := &loadCall{}
	call.wg.Add(1)
	c.loading[key] = call
	c.mu.Unlock()

	call.data, call.err = load()

	c.mu.Lock()
	delete(c.loading, key)
	if call.err == nil && len(call.data) <= c.maxBytes/2 {
		el := c.ll.PushFront(&cacheItem{key: key, data: call.data})
		c.items[key] = el
		c.curBytes += len(call.data)
		for c.curBytes > c.maxBytes {
			back := c.ll.Back()
			it := back.Value.(*cacheItem)
			c.ll.Remove(back)
			delete(c.items, it.key)
			c.curBytes -= len(it.data)
		}
	}
	c.mu.Unlock()
	call.wg.Done()
	return call.data, call.err
}
