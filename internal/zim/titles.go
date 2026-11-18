package zim

import (
	"encoding/binary"
	"math/rand"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// titleIndex is an on-disk array of uint32 entry indices sorted by
// (namespace, title). Modern archives carry a front-articles-only list
// at X/listing/titleOrdered/v1, which is the best index for search and
// random pages; every archive has the full list the header points to.
type titleIndex struct {
	base       int64
	count      uint32
	articles   bool // true when the list holds only front articles
	fallbackNS byte // namespace to restrict to when !articles
	ok         bool
}

func (a *Archive) titleIdx() titleIndex {
	a.titlesOnce.Do(func() {
		if a.NewNamespaceScheme {
			if e, err := a.Find('X', "listing/titleOrdered/v1"); err == nil {
				if base, n, ok := a.uncompressedBlobSpan(e); ok && n%4 == 0 {
					a.titles = titleIndex{base: base, count: uint32(n / 4), articles: true, ok: true}
					return
				}
			}
		}
		if a.Hdr.TitlePtrPos != 0 && a.Hdr.TitlePtrPos+4*uint64(a.Hdr.EntryCount) <= uint64(a.size) {
			a.titles = titleIndex{base: int64(a.Hdr.TitlePtrPos), count: a.Hdr.EntryCount,
				fallbackNS: a.ContentNamespace(), ok: true}
		}
	})
	return a.titles
}

// uncompressedBlobSpan returns the absolute file offset and length of a
// blob stored in an uncompressed cluster.
func (a *Archive) uncompressedBlobSpan(e *Entry) (int64, int64, bool) {
	off, n, compressed, err := a.blobSpan(e)
	if err != nil || compressed {
		return 0, 0, false
	}
	return off, n, true
}

func (a *Archive) titleEntryAt(t titleIndex, i uint32) (*Entry, error) {
	var b [4]byte
	if _, err := a.r.ReadAt(b[:], t.base+4*int64(i)); err != nil {
		return nil, err
	}
	return a.EntryAt(binary.LittleEndian.Uint32(b[:]))
}

// lowerBoundTitle finds the first position whose title is >= prefix
// (restricted to ns for full lists).
func (a *Archive) lowerBoundTitle(t titleIndex, ns byte, prefix string) (uint32, error) {
	var ferr error
	i := sort.Search(int(t.count), func(i int) bool {
		if ferr != nil {
			return true
		}
		e, err := a.titleEntryAt(t, uint32(i))
		if err != nil {
			ferr = err
			return true
		}
		if t.articles {
			return e.Title() >= prefix
		}
		return compareKey(e.Namespace, e.Title(), ns, prefix) >= 0
	})
	return uint32(i), ferr
}

// Result is one search hit.
type Result struct {
	Title  string // the title that matched (may be a redirect's)
	Entry  *Entry // the matched entry, redirects resolved
	Exact  bool   // title equals the query (modulo case variants tried)
	Via    string // set when Title is a redirect to Entry
	Weight int
}

// SearchTitles returns up to limit articles whose title starts with the
// query. Wikipedia titles are case sensitive and normally capitalised,
// so several capitalisations of the query are tried and merged.
func (a *Archive) SearchTitles(query string, limit int) ([]Result, error) {
	query = strings.TrimSpace(strings.Join(strings.Fields(query), " "))
	if query == "" || limit <= 0 {
		return nil, nil
	}
	t := a.titleIdx()
	if !t.ok {
		return nil, nil
	}
	ns := t.fallbackNS
	seen := map[uint32]bool{}
	var out []Result
	for vi, q := range caseVariants(query) {
		pos, err := a.lowerBoundTitle(t, ns, q)
		if err != nil {
			return out, err
		}
		scanned := 0
		for i := pos; i < t.count && scanned < limit*4 && len(out) < limit*3; i++ {
			e, err := a.titleEntryAt(t, i)
			if err != nil {
				return out, err
			}
			if !t.articles && e.Namespace != ns {
				break
			}
			title := e.Title()
			if !strings.HasPrefix(title, q) {
				break
			}
			scanned++
			target, err := a.Resolve(e)
			if err != nil || !isArticle(target) {
				continue
			}
			if seen[target.Index] {
				continue
			}
			seen[target.Index] = true
			r := Result{Title: title, Entry: target, Exact: title == q}
			if e.IsRedirect() && target.Title() != title {
				r.Via = title
				r.Title = target.Title()
			}
			// Rank: exact matches first, then the variant order (as typed
			// beats re-capitalised), then shorter titles.
			r.Weight = vi*1000 + len(title)
			if r.Exact {
				r.Weight -= 100000
			}
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight < out[j].Weight })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// FindTitle returns the article whose title is exactly query under one
// of the usual capitalisations, following redirects.
func (a *Archive) FindTitle(query string) (*Entry, bool) {
	res, err := a.SearchTitles(query, 5)
	if err != nil {
		return nil, false
	}
	for _, r := range res {
		if r.Exact {
			return r.Entry, true
		}
	}
	return nil, false
}

func isArticle(e *Entry) bool {
	if e.IsRedirect() || e.IsSpecial() {
		return false
	}
	m := e.MimeType()
	return strings.HasPrefix(m, "text/html")
}

// caseVariants yields distinct capitalisations of q, as typed first.
func caseVariants(q string) []string {
	var vs []string
	add := func(s string) {
		for _, v := range vs {
			if v == s {
				return
			}
		}
		vs = append(vs, s)
	}
	add(q)
	add(upperFirst(q))
	words := strings.Split(q, " ")
	for i, w := range words {
		words[i] = upperFirst(strings.ToLower(w))
	}
	add(strings.Join(words, " "))
	add(upperFirst(strings.ToLower(q)))
	add(strings.ToLower(q))
	add(strings.ToUpper(q))
	return vs
}

func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// RandomArticle picks a random article (not a redirect, not an asset).
func (a *Archive) RandomArticle() (*Entry, error) {
	t := a.titleIdx()
	if t.ok && t.articles && t.count > 0 {
		for tries := 0; tries < 32; tries++ {
			e, err := a.titleEntryAt(t, uint32(rand.Int63n(int64(t.count))))
			if err != nil {
				return nil, err
			}
			if e, err = a.Resolve(e); err == nil && isArticle(e) {
				return e, nil
			}
		}
	}
	lo, hi, err := a.NamespaceRange(a.ContentNamespace())
	if err != nil {
		return nil, err
	}
	if hi <= lo {
		return nil, ErrNotFound
	}
	for tries := 0; tries < 200; tries++ {
		e, err := a.EntryAt(lo + uint32(rand.Int63n(int64(hi-lo))))
		if err != nil {
			return nil, err
		}
		if isArticle(e) && !strings.HasPrefix(e.URL, "_") && !strings.HasPrefix(e.URL, "-/") {
			return e, nil
		}
	}
	return a.MainEntry()
}

// ArticleCount estimates the number of articles: the size of the front
// article listing when present, otherwise the M/Counter metadata.
func (a *Archive) ArticleCount() int64 {
	if t := a.titleIdx(); t.ok && t.articles {
		return int64(t.count)
	}
	for _, part := range strings.Split(a.Metadata("Counter"), ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 && strings.HasPrefix(kv[0], "text/html") {
			var n int64
			for _, c := range kv[1] {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int64(c-'0')
			}
			return n
		}
	}
	return 0
}
