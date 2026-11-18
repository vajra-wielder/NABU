package dump

import (
	"bytes"
	"compress/bzip2"
	"container/list"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Page is one wiki page as stored in the dump.
type Page struct {
	Title     string
	NS        int
	ID        uint64
	Redirect  string // target title when the page is a redirect
	Text      string // wikitext
	Timestamp string
}

// Reader reads pages from an indexed dump.
type Reader struct {
	Files *Files
	Index *Index
	data  []ReaderAtCloser

	streams *streamCache
}

// NewReader opens the data files of f for reading with ix.
func NewReader(f *Files, ix *Index) (*Reader, error) {
	r := &Reader{Files: f, Index: ix, streams: newStreamCache(24 << 20)}
	for _, p := range f.Pairs {
		d, _, err := OpenData(p.Data)
		if err != nil {
			r.Close()
			return nil, err
		}
		r.data = append(r.data, d)
	}
	return r, nil
}

// Close releases all files.
func (r *Reader) Close() error {
	for _, d := range r.data {
		d.Close()
	}
	if r.Index != nil {
		r.Index.Close()
	}
	return nil
}

// Site is the wiki's siteinfo.
func (r *Reader) Site() *Siteinfo { return r.Index.Site() }

// Load reads the page an index entry points to.
func (r *Reader) Load(e Entry) (*Page, error) {
	pair, start, end, err := r.Index.streamRange(e.Stream)
	if err != nil {
		return nil, err
	}
	if pair >= len(r.data) {
		return nil, ErrStale
	}
	data, err := r.streams.get(e.Stream, func() ([]byte, error) {
		zr := bzip2.NewReader(io.NewSectionReader(r.data[pair], start, end-start))
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, zr); err != nil {
			return nil, fmt.Errorf("decompressing the dump (is the file complete?): %w", err)
		}
		return buf.Bytes(), nil
	})
	if err != nil {
		return nil, err
	}
	return findPage(data, e.ID)
}

// Lookup finds and reads a page by title (normalized first).
func (r *Reader) Lookup(title string) (*Page, Entry, error) {
	e, _, err := r.Index.Find(r.Site().Normalize(title))
	if err != nil {
		return nil, Entry{}, err
	}
	p, err := r.Load(e)
	return p, e, err
}

type xmlPage struct {
	Title    string `xml:"title"`
	NS       int    `xml:"ns"`
	ID       uint64 `xml:"id"`
	Redirect struct {
		Title string `xml:"title,attr"`
	} `xml:"redirect"`
	Revision struct {
		Timestamp string `xml:"timestamp"`
		Text      string `xml:"text"`
	} `xml:"revision"`
}

var (
	pageOpen  = []byte("<page>")
	pageClose = []byte("</page>")
	idOpen    = []byte("<id>")
	idClose   = []byte("</id>")
)

// findPage locates the <page> with the given id in a decompressed
// stream. Only that page is XML-decoded.
func findPage(data []byte, id uint64) (*Page, error) {
	want := []byte(strconv.FormatUint(id, 10))
	for len(data) > 0 {
		i := bytes.Index(data, pageOpen)
		if i < 0 {
			break
		}
		data = data[i:]
		j := bytes.Index(data, pageClose)
		if j < 0 {
			break
		}
		chunk := data[:j+len(pageClose)]
		data = data[j+len(pageClose):]
		// The first <id> in a page is the page id.
		a := bytes.Index(chunk, idOpen)
		if a < 0 {
			continue
		}
		b := bytes.Index(chunk[a:], idClose)
		if b < 0 || !bytes.Equal(chunk[a+len(idOpen):a+b], want) {
			continue
		}
		var xp xmlPage
		if err := xml.Unmarshal(chunk, &xp); err != nil {
			return nil, fmt.Errorf("dump: page %d: %w", id, err)
		}
		return &Page{Title: xp.Title, NS: xp.NS, ID: xp.ID, Redirect: xp.Redirect.Title,
			Text: xp.Revision.Text, Timestamp: xp.Revision.Timestamp}, nil
	}
	return nil, errors.New("dump: page missing from its stream (index and dump do not match)")
}

// streamCache keeps recently decompressed streams, bounded by bytes,
// and makes concurrent requests for one stream share a single
// decompression.
type streamCache struct {
	mu      sync.Mutex
	max     int
	cur     int
	ll      *list.List
	items   map[uint32]*list.Element
	loading map[uint32]*loadCall
}

type cached struct {
	key  uint32
	data []byte
}

type loadCall struct {
	wg   sync.WaitGroup
	data []byte
	err  error
}

func newStreamCache(max int) *streamCache {
	return &streamCache{max: max, ll: list.New(), items: map[uint32]*list.Element{}, loading: map[uint32]*loadCall{}}
}

func (c *streamCache) get(k uint32, load func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if el, ok := c.items[k]; ok {
		c.ll.MoveToFront(el)
		d := el.Value.(*cached).data
		c.mu.Unlock()
		return d, nil
	}
	if call, ok := c.loading[k]; ok {
		c.mu.Unlock()
		call.wg.Wait()
		return call.data, call.err
	}
	call := &loadCall{}
	call.wg.Add(1)
	c.loading[k] = call
	c.mu.Unlock()

	call.data, call.err = load()

	c.mu.Lock()
	delete(c.loading, k)
	if call.err == nil && len(call.data) < c.max/2 {
		c.items[k] = c.ll.PushFront(&cached{k, call.data})
		c.cur += len(call.data)
		for c.cur > c.max {
			b := c.ll.Back()
			it := b.Value.(*cached)
			c.ll.Remove(b)
			delete(c.items, it.key)
			c.cur -= len(it.data)
		}
	}
	c.mu.Unlock()
	call.wg.Done()
	return call.data, call.err
}

// StreamCount is the number of bzip2 streams (of up to 100 pages).
func (ix *Index) StreamCount() int { return len(ix.streams) }

// StreamPages decompresses stream s and returns all its pages. It
// bypasses the cache, so whole-dump scans don't evict what a reader is
// looking at.
func (r *Reader) StreamPages(s uint32) ([]*Page, error) {
	pair, start, end, err := r.Index.streamRange(s)
	if err != nil {
		return nil, err
	}
	if pair >= len(r.data) {
		return nil, ErrStale
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, bzip2.NewReader(io.NewSectionReader(r.data[pair], start, end-start))); err != nil {
		return nil, fmt.Errorf("decompressing the dump (is the file complete?): %w", err)
	}
	data := buf.Bytes()
	var pages []*Page
	for {
		i := bytes.Index(data, pageOpen)
		if i < 0 {
			break
		}
		data = data[i:]
		j := bytes.Index(data, pageClose)
		if j < 0 {
			break
		}
		var xp xmlPage
		if err := xml.Unmarshal(data[:j+len(pageClose)], &xp); err == nil {
			pages = append(pages, &Page{Title: xp.Title, NS: xp.NS, ID: xp.ID, Redirect: xp.Redirect.Title,
				Text: xp.Revision.Text, Timestamp: xp.Revision.Timestamp})
		}
		data = data[j+len(pageClose):]
	}
	return pages, nil
}
