package fulltext

// A study of real search indexes, to see where the room and the time go.
// It reads a NABU folder's dumps and indexes and changes nothing there:
//
//	NABU_STUDY=C:\path\to\NABU-test go test ./internal/fulltext -run Study -v -timeout 6h
//
// (NABU_TEST works too.) WIKI=enwiki limits it to one wiki. Each part
// prints what it finds and writes it to docs/realtest/fts-study-<wiki>.txt
// in the checkout, ready to commit:
//
//	TestStudySize    sizes of each part of the index, and what other ways of
//	                 writing the article lists would take (reads the whole
//	                 index once: minutes)
//	TestStudySearch  time of each stage of a search, and of the snippets
//	TestStudyBuild   where the time of building goes, on a sample of the dump

import (
	"fmt"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

type studyBook struct {
	files *dump.Files
	rd    *dump.Reader
	ix    *Index
	path  string
}

func studyBooks(t *testing.T) []studyBook {
	root := os.Getenv("NABU_STUDY")
	if root == "" {
		root = os.Getenv("NABU_TEST")
	}
	if root == "" {
		t.Skip("set NABU_STUDY to a NABU folder with a search index")
	}
	root = strings.Trim(root, `"`)
	eds, _ := dump.Scan(filepath.Join(root, "wikipedia"))
	var out []studyBook
	for _, f := range eds {
		if w := os.Getenv("WIKI"); w != "" && f.DB != w {
			continue
		}
		fts := filepath.Join(root, "search", f.Key()+".nfts")
		if _, err := os.Stat(fts); err != nil {
			continue
		}
		tix, err := dump.OpenIndex(filepath.Join(root, "indexes", f.Key()+".nidx"), f)
		if err != nil {
			t.Logf("%s: no title index: %v", f.Key(), err)
			continue
		}
		rd, err := dump.NewReader(f, tix)
		if err != nil {
			t.Fatal(err)
		}
		ix, err := Open(fts, f)
		if err != nil {
			t.Fatalf("%s: %v", fts, err)
		}
		t.Cleanup(func() { ix.Close(); rd.Close() })
		out = append(out, studyBook{f, rd, ix, fts})
	}
	if len(out) == 0 {
		t.Skip("no dump with a search index in " + root)
	}
	return out
}

// report prints and keeps lines for the study file.
type report struct {
	t     *testing.T
	name  string
	lines []string
}

func (r *report) f(format string, a ...interface{}) {
	s := fmt.Sprintf(format, a...)
	r.t.Log(s)
	r.lines = append(r.lines, s)
}

// save adds the lines to docs/realtest/fts-study-<wiki>.txt, replacing an
// earlier section of the same name.
func (r *report) save(wiki string) {
	dir := filepath.Join("..", "..", "docs", "realtest")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	path := filepath.Join(dir, "fts-study-"+wiki+".txt")
	old, _ := os.ReadFile(path)
	head := "== " + r.name + " =="
	var keep []string
	skip := false
	for _, l := range strings.Split(strings.TrimRight(string(old), "\n"), "\n") {
		if strings.HasPrefix(l, "== ") {
			skip = l == head
		}
		if !skip && l != "" {
			keep = append(keep, l)
		}
	}
	keep = append(keep, head, fmt.Sprintf("(%s, %s/%s, %d CPUs)", time.Now().UTC().Format("2006-01-02 15:04Z"), runtime.GOOS, runtime.GOARCH, runtime.NumCPU()))
	keep = append(keep, r.lines...)
	os.WriteFile(path, []byte(strings.Join(keep, "\n")+"\n"), 0o644)
	r.t.Logf("written to %s", path)
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }

// Ways of writing one article list (docs ascending, tf buckets 0..3).
type coding struct {
	name string
	size func(docs []uint32, tfs []uint8) int64
}

var codings []coding

func init() { codings = codingList }

var codingList = []coding{
	{"now: varint of delta<<2|tf", func(d []uint32, tf []uint8) int64 {
		var n, last int64
		for i, x := range d {
			n += int64(uvlen(uint64(int64(x)-last)<<2 | uint64(tf[i])))
			last = int64(x)
		}
		return n
	}},
	{"varint deltas, tf 2 bits packed apart", func(d []uint32, tf []uint8) int64 {
		return varints(d) + int64(len(d)+3)/4
	}},
	{"blocks of 128 packed to the widest (FOR), tf 2 bits", func(d []uint32, tf []uint8) int64 {
		return forBlocks(d, false) + int64(len(d)+3)/4
	}},
	{"blocks of 128, width chosen with exceptions (PFOR), tf 2 bits", func(d []uint32, tf []uint8) int64 {
		return forBlocks(d, true) + int64(len(d)+3)/4
	}},
	{"Elias-Fano, tf 2 bits", func(d []uint32, tf []uint8) int64 {
		return eliasFano(d) + int64(len(d)+3)/4
	}},
	{"best of varint/PFOR/EF per list, tf 2 bits", func(d []uint32, tf []uint8) int64 {
		a, b, c := varints(d), forBlocks(d, true), eliasFano(d)
		m := a
		if b < m {
			m = b
		}
		if c < m {
			m = c
		}
		return m + 1 + int64(len(d)+3)/4
	}},
	{"best per list, tf 1 bit (once / more often)", func(d []uint32, tf []uint8) int64 {
		return bestDocs(d) + 1 + int64(len(d)+7)/8
	}},
	{"best per list, tf in 1-3 bits (once: 1 bit)", func(d []uint32, tf []uint8) int64 {
		return bestDocs(d) + 1 + (tfBits(tf)+7)/8
	}},
	{"varint or Elias-Fano per list, tf in 1-3 bits", func(d []uint32, tf []uint8) int64 {
		m := varints(d)
		if c := eliasFano(d); c < m {
			m = c
		}
		return m + 1 + (tfBits(tf)+7)/8
	}},
	{"as now under 16 articles, else varint/Elias-Fano + tf 1-3 bits", func(d []uint32, tf []uint8) int64 {
		if len(d) < 16 {
			return codings[0].size(d, tf)
		}
		m := varints(d)
		if c := eliasFano(d); c < m {
			m = c
		}
		return m + 1 + (tfBits(tf)+7)/8
	}},
	{"best per list, no tf at all", func(d []uint32, tf []uint8) int64 {
		a, b, c := varints(d), forBlocks(d, true), eliasFano(d)
		m := a
		if b < m {
			m = b
		}
		if c < m {
			m = c
		}
		return m + 1
	}},
}

func deltas(d []uint32, fn func(v uint32)) {
	last := uint32(0)
	for i, x := range d {
		if i == 0 {
			fn(x)
		} else {
			fn(x - last - 1)
		}
		last = x
	}
}

func varints(d []uint32) int64 {
	var n int64
	deltas(d, func(v uint32) { n += int64(uvlen(uint64(v))) })
	return n
}

func forBlocks(d []uint32, exceptions bool) int64 {
	var vals []uint32
	deltas(d, func(v uint32) { vals = append(vals, v) })
	var n int64
	for len(vals) >= 128 {
		blk := vals[:128]
		vals = vals[128:]
		best := int64(math.MaxInt64)
		maxw := 0
		for _, v := range blk {
			if w := bits.Len32(v); w > maxw {
				maxw = w
			}
		}
		lo := maxw
		if exceptions {
			lo = 0
		}
		for w := lo; w <= maxw; w++ {
			c := int64(1 + (128*w+7)/8)
			if exceptions {
				c++ // exception count
				for i, v := range blk {
					if bits.Len32(v) > w {
						c += 1 + int64(uvlen(uint64(v>>uint(w)))) // position, high bits
						_ = i
					}
				}
			}
			if c < best {
				best = c
			}
		}
		n += best
	}
	for _, v := range vals {
		n += int64(uvlen(uint64(v)))
	}
	return n
}

func eliasFano(d []uint32) int64 {
	if len(d) == 0 {
		return 0
	}
	u := uint64(d[len(d)-1]) + 1
	n := uint64(len(d))
	l := 0
	if u > n {
		l = bits.Len64(u/n) - 1
	}
	bitsN := n*uint64(l) + n + (u >> uint(l)) + 1
	return int64((bitsN+7)/8) + 2
}

// allRows walks the whole dictionary in order.
func allRows(t *testing.T, ix *Index, fn func(r dictRow)) {
	for i := range ix.dictTerm {
		rows, err := ix.dictBlockRows(i)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			fn(r)
		}
	}
	ix.mu.Lock()
	ix.cache = map[int][]dictRow{}
	ix.mu.Unlock()
}

func TestStudySize(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "size"}
		ix := b.ix
		st, _ := os.Stat(b.path)
		r.f("%s: index %s for a dump of %s (%.1f%%), %d articles, %d words, %d postings",
			b.files.Key(), mb(st.Size()), mb(b.files.Size), 100*float64(st.Size())/float64(b.files.Size), ix.Meta.Docs, ix.Meta.Terms, ix.Meta.Postings)
		sec := ix.Sections()
		dirBytes := ix.docsOff - ix.dictOff[len(ix.dictOff)-1]
		r.f("parts: postings %s, dictionary %s (+ directory %s), articles %s, places %d",
			mb(sec["postings"]), mb(sec["dictionary"]), mb(dirBytes), mb(sec["docs"]), ix.Meta.Geo)

		// Postings by how many articles a word is in.
		type band struct {
			lo, hi                       uint32
			terms, posts, bytes, dictLen int64
		}
		bands := []*band{{1, 1, 0, 0, 0, 0}, {2, 2, 0, 0, 0, 0}, {3, 4, 0, 0, 0, 0}, {5, 16, 0, 0, 0, 0}, {17, 256, 0, 0, 0, 0},
			{257, 4096, 0, 0, 0, 0}, {4097, 65536, 0, 0, 0, 0}, {65537, 1 << 31, 0, 0, 0, 0}}
		alt := make([]int64, len(codings))
		var tfCount [4]int64
		var termBytes, digitTerms, digitHapax, longTerms, nonLatin int64
		type big struct {
			term  string
			df    uint32
			bytes uint64
		}
		var bigs []big
		var hapaxSample []string
		var n int64
		var dm, tm []uint32
		var tb []uint8
		allRows(t, ix, func(row dictRow) {
			n++
			termBytes += int64(len(row.term))
			for _, bd := range bands {
				if row.df >= bd.lo && row.df <= bd.hi {
					bd.terms++
					bd.posts += int64(row.df)
					bd.bytes += int64(row.size)
				}
			}
			hasDigit := strings.ContainsAny(row.term, "0123456789")
			if hasDigit {
				digitTerms++
				if row.df == 1 {
					digitHapax++
				}
			}
			if len([]rune(row.term)) > 15 {
				longTerms++
			}
			if row.term[0] >= 0x80 {
				nonLatin++
			}
			if row.df == 1 && n%997 == 0 && len(hapaxSample) < 60 {
				hapaxSample = append(hapaxSample, row.term)
			}
			bigs = append(bigs, big{row.term, row.df, row.size})
			if len(bigs) > 400 {
				sort.Slice(bigs, func(i, j int) bool { return bigs[i].df > bigs[j].df })
				bigs = bigs[:100]
			}
			ps, err := ix.postings(row)
			if err != nil {
				t.Fatal(err)
			}
			dm, tb, tm = dm[:0], tb[:0], tm[:0]
			for _, p := range ps {
				dm = append(dm, p.doc)
				tb = append(tb, tfBucket(p.tf))
				tfCount[tfBucket(p.tf)]++
			}
			_ = tm
			for i, c := range codings {
				alt[i] += c.size(dm, tb)
			}
		})
		r.f("words by how many articles they are in:")
		r.f("  %-14s %10s %12s %12s %7s", "articles", "words", "postings", "bytes", "B/post")
		for _, bd := range bands {
			hi := fmt.Sprint(bd.hi)
			if bd.hi == 1<<31 {
				hi = "more"
			}
			per := 0.0
			if bd.posts > 0 {
				per = float64(bd.bytes) / float64(bd.posts)
			}
			r.f("  %-14s %10d %12d %12s %7.2f", fmt.Sprintf("%d-%s", bd.lo, hi), bd.terms, bd.posts, mb(bd.bytes), per)
		}
		tot := tfCount[0] + tfCount[1] + tfCount[2] + tfCount[3]
		ent := 0.0
		for _, c := range tfCount {
			if c > 0 {
				p := float64(c) / float64(tot)
				ent -= p * math.Log2(p)
			}
		}
		r.f("weights (once/twice/3-5/more): %.1f%% %.1f%% %.1f%% %.1f%%, %.2f bits of information each (2 kept)",
			100*float64(tfCount[0])/float64(tot), 100*float64(tfCount[1])/float64(tot), 100*float64(tfCount[2])/float64(tot), 100*float64(tfCount[3])/float64(tot), ent)
		r.f("the postings written other ways:")
		for i, c := range codings {
			r.f("  %-62s %12s  %+6.1f%%", c.name, mb(alt[i]), 100*float64(alt[i]-alt[0])/float64(alt[0]))
		}
		r.f("dictionary: %d words, %s of letters (%.1f a word); with digits %d (%d in one article only); over 15 letters %d; not Latin letters %d",
			n, mb(termBytes), float64(termBytes)/float64(n), digitTerms, digitHapax, longTerms, nonLatin)
		r.f("  some words in one article only: %s", strings.Join(hapaxSample, " "))
		sort.Slice(bigs, func(i, j int) bool { return bigs[i].df > bigs[j].df })
		var line []string
		for i, bg := range bigs {
			if i == 60 {
				break
			}
			line = append(line, fmt.Sprintf("%s %.1f%% %s", bg.term, 100*float64(bg.df)/float64(ix.Meta.Docs), mb(int64(bg.bytes))))
		}
		r.f("  the commonest words still indexed (in %% of articles, bytes): %s", strings.Join(line, ", "))
		r.f("  dropped as in over 40%% of articles: %s", strings.Join(ix.Meta.Common, " "))

		// The article table.
		var titleBytes int64
		for d := uint32(0); d < uint32(ix.Meta.Docs); d++ {
			doc, err := ix.Doc(d)
			if err != nil {
				t.Fatal(err)
			}
			titleBytes += int64(len(doc.Title))
		}
		r.f("articles: %s, of which titles %s (%.1f bytes a title)", mb(sec["docs"]), mb(titleBytes), float64(titleBytes)/float64(ix.Meta.Docs))
		r.save(b.files.DB)
	}
}

// Queries of every kind: rare and common words, several words, names.
var studyQueries = []string{
	"theory of relativity", "largest desert", "invented the telephone", "capital of Mongolia",
	"boiling point of water", "first moon landing", "chlorophyll", "migration of birds",
	"Silk Road trade", "volcanic eruption", "how to purify water", "first aid burns",
	"edible wild plants", "broken bone splint", "solar eclipse", "world war", "football club",
	"united states army", "US army", "river", "village in poland", "american politician",
	"population census", "album released", "photosynthesis light reaction", "Napoleon Bonaparte",
	"Mount Everest expedition", "penicillin discovery", "earthquake tsunami", "Einstein",
}

func TestStudySearch(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "search"}
		ix := b.ix
		r.f("%s: each query twice (the second with the file in the computer's memory); times in milliseconds", b.files.Key())
		r.f("  %-32s %8s %6s %6s %7s %7s %7s %7s %8s %8s", "query", "matches", "lists", "dict", "lists", "merge", "titles", "search", "snip1by1", "snip||")
		var sums [7]float64
		var splits [][4]float64
		ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
		for _, q := range studyQueries {
			for round := 0; round < 2; round++ {
				var dictT, postT, mergeT, docT time.Duration
				// The stages of Search, one by one.
				t0 := time.Now()
				var rows []dictRow
				ok := true
				TokenizeCase(q, func(w string, c Case) {
					if !ix.rules.Keep(w, c) || ix.common[w] {
						return
					}
					row, found, err := ix.lookup(w)
					if err != nil {
						t.Fatal(err)
					}
					if !found {
						ok = false
					}
					rows = append(rows, row)
				})
				dictT = time.Since(t0)
				var lists [][]posting
				npost := 0
				if ok {
					t1 := time.Now()
					for _, row := range rows {
						ps, err := ix.postings(row)
						if err != nil {
							t.Fatal(err)
						}
						lists = append(lists, ps)
						npost += len(ps)
					}
					postT = time.Since(t1)
				}
				t2 := time.Now()
				var cands []uint32
				if len(lists) > 0 {
					sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
					for _, p := range lists[0] {
						cands = append(cands, p.doc)
					}
					for _, l := range lists[1:] {
						out := cands[:0]
						j := 0
						for _, c := range cands {
							for j < len(l) && l[j].doc < c {
								j++
							}
							if j < len(l) && l[j].doc == c {
								out = append(out, c)
							}
						}
						cands = out
					}
				}
				mergeT = time.Since(t2)
				t3 := time.Now()
				top := len(cands)
				if top > 400 {
					top = 400
				}
				for _, c := range cands[:top] {
					if _, err := ix.Doc(c); err != nil {
						t.Fatal(err)
					}
				}
				docT = time.Since(t3)
				// The whole search, as the page does it.
				t4 := time.Now()
				res, err := ix.Search(q, 0, 20)
				if err != nil {
					t.Fatal(err)
				}
				searchT := time.Since(t4)
				// Snippets: one after another, then side by side.
				// side by side, as the results page makes them now
				plain := func(s string) string { return wikitext.PlainText(s, b.rd.Site()) }
				snip := func(h Hit) {
					if p, err := b.rd.Load(dump.Entry{Stream: h.Stream, ID: h.PageID}); err == nil {
						WikiSnippet(p.Text, res.Terms, 220, plain)
					}
				}
				b.rd.ForgetCache()
				var loadT, plainT, textT time.Duration
				var textBytes int
				t5 := time.Now()
				for _, h := range res.Hits {
					a := time.Now()
					p, err := b.rd.Load(dump.Entry{Stream: h.Stream, ID: h.PageID})
					loadT += time.Since(a)
					if err != nil {
						continue
					}
					textBytes += len(p.Text)
					a = time.Now()
					pt := wikitext.PlainText(p.Text, b.rd.Site())
					plainT += time.Since(a)
					a = time.Now()
					Snippet(pt, res.Terms, 220)
					textT += time.Since(a)
				}
				serialT := time.Since(t5)
				splits = append(splits, [4]float64{ms(loadT), ms(plainT), ms(textT), float64(textBytes) / 1e3})
				b.rd.ForgetCache()
				t6 := time.Now()
				var wg sync.WaitGroup
				sem := make(chan struct{}, runtime.NumCPU())
				for _, h := range res.Hits {
					wg.Add(1)
					sem <- struct{}{}
					go func(h Hit) { defer wg.Done(); snip(h); <-sem }(h)
				}
				wg.Wait()
				parT := time.Since(t6)
				if round == 1 {
					for i, d := range []time.Duration{dictT, postT, mergeT, docT, searchT, serialT, parT} {
						sums[i] += ms(d)
					}
				}
				label := q
				if round == 0 {
					label += " (1st)"
				}
				r.f("  %-32s %8d %6d %6.1f %7.1f %7.1f %7.1f %7.1f %8.1f %8.1f", label, res.Total, npost, ms(dictT), ms(postT), ms(mergeT), ms(docT), ms(searchT), ms(serialT), ms(parT))
			}
		}
		k := float64(len(studyQueries))
		r.f("  %-32s %8s %6s %6.1f %7.1f %7.1f %7.1f %7.1f %8.1f %8.1f", "average (2nd runs)", "", "", sums[0]/k, sums[1]/k, sums[2]/k, sums[3]/k, sums[4]/k, sums[5]/k, sums[6]/k)
		var sp [4]float64
		for i, x := range splits {
			if i%2 == 1 {
				for k := range sp {
					sp[k] += x[k] / k2(len(splits))
				}
			}
		}
		r.f("  the 20 snippets one by one (2nd runs, average): reading the articles %.1f ms, wikitext to text %.1f ms, finding the words %.1f ms; %.0f kB of wikitext",
			sp[0], sp[1], sp[2], sp[3])
		r.f("  (lists: postings read; dict/lists/merge/titles: the stages of a search; search: all of it;")
		r.f("   snip1by1: the 20 snippets one after another from whole articles, as NABU did;")
		r.f("   snip||: side by side from the paragraphs with the words, as it does now)")
		r.save(b.files.DB)
	}
}

func TestStudyBuild(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "build"}
		total := b.rd.Index.StreamCount()
		sample := 2000
		if sample > total {
			sample = total
		}
		rules := NewRules(Stopwords(b.ix.Meta.Lang), CapitalNames(b.ix.Meta.Lang))
		site := b.rd.Site()
		var readT, plainT, tokT time.Duration
		var pages, bytesIn int64
		for i := 0; i < sample; i++ {
			s := uint32(int64(i) * int64(total) / int64(sample))
			t0 := time.Now()
			ps, err := b.rd.StreamPages(s)
			if err != nil {
				t.Fatal(err)
			}
			readT += time.Since(t0)
			for _, p := range ps {
				if p.NS != dump.NSMain || p.Redirect != "" {
					continue
				}
				pages++
				bytesIn += int64(len(p.Text))
				t1 := time.Now()
				txt := wikitext.PlainText(p.Text, site)
				plainT += time.Since(t1)
				t2 := time.Now()
				counts := map[string]int{}
				TokenizeCase(txt, func(w string, c Case) {
					if rules.Keep(w, c) && !junk(w) {
						counts[w]++
					}
				})
				tokT += time.Since(t2)
			}
		}
		all := readT + plainT + tokT
		pct := func(d time.Duration) float64 { return 100 * float64(d) / float64(all) }
		r.f("%s: %d of %d bundles, %d articles, %s of wikitext, on one CPU:", b.files.Key(), sample, total, pages, mb(bytesIn))
		r.f("  reading (decompress, XML) %6.1f s  %4.1f%%", readT.Seconds(), pct(readT))
		r.f("  wikitext to plain text   %6.1f s  %4.1f%%", plainT.Seconds(), pct(plainT))
		r.f("  words and counting       %6.1f s  %4.1f%%", tokT.Seconds(), pct(tokT))
		scale := float64(total) / float64(sample)
		r.f("  so the whole dump: about %.0f min on one CPU, %.0f min on %d (the rest of a build is sorting and writing)",
			all.Minutes()*scale, all.Minutes()*scale/float64(studyCPUs()), studyCPUs())
		r.save(b.files.DB)
	}
}

func studyCPUs() int {
	if n := runtime.NumCPU() - 1; n > 1 {
		return n
	}
	return 1
}

func k2(n int) float64 { return float64(n / 2) }

func bestDocs(d []uint32) int64 {
	m := varints(d)
	if b := forBlocks(d, true); b < m {
		m = b
	}
	if c := eliasFano(d); c < m {
		m = c
	}
	return m
}

// TestStudySnippets compares snippets made from the whole article with
// those made from the paragraphs the words are in: the time, and whether
// they show the same thing.
func TestStudySnippets(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "snippets"}
		site := b.rd.Site()
		plain := func(s string) string { return wikitext.PlainText(s, site) }
		var oldT, newT time.Duration
		var n, same, bothFound int
		var diffs []string
		for _, q := range studyQueries {
			res, err := b.ix.Search(q, 0, 20)
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range res.Hits {
				p, err := b.rd.Load(dump.Entry{Stream: h.Stream, ID: h.PageID})
				if err != nil {
					continue
				}
				a := time.Now()
				old, oldOK := snippet(plain(p.Text), res.Terms, 220)
				oldT += time.Since(a)
				a = time.Now()
				nw := WikiSnippet(p.Text, res.Terms, 220, plain)
				newT += time.Since(a)
				n++
				if old == nw {
					same++
				} else if len(diffs) < 8 {
					diffs = append(diffs, fmt.Sprintf("  %q in %s:\n    whole: %s\n    paragraph: %s", q, h.Title, old, nw))
				}
				if oldOK && strings.Contains(nw, "<mark>") {
					bothFound++
				}
			}
		}
		r.f("%s: %d snippets; from the whole article %.1f ms each, from the paragraph %.1f ms each (%.0f× faster)",
			b.files.Key(), n, float64(oldT.Microseconds())/1000/float64(n), float64(newT.Microseconds())/1000/float64(n), float64(oldT)/float64(newT))
		r.f("  the same snippet %d times (%.0f%%); words marked in both %d", same, 100*float64(same)/float64(n), bothFound)
		if len(diffs) > 0 {
			r.f("  some that differ:")
			for _, d := range diffs {
				r.f("%s", d)
			}
		}
		r.save(b.files.DB)
	}
}

// TestStudyRanking shows how much the order of results would change with
// less said about how often a word is in an article: the top 10 of each
// query, kept with 4 weights (now), 2 (once / more often) and none.
func TestStudyRanking(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "ranking"}
		now := bucketTF
		top := func(q string) []string {
			res, err := b.ix.Search(q, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, h := range res.Hits {
				out = append(out, h.Title)
			}
			return out
		}
		base := map[string][]string{}
		for _, q := range studyQueries {
			base[q] = top(q)
		}
		for _, alt := range []struct {
			name string
			tf   [4]uint8
		}{{"2 weights (once / more often)", [4]uint8{1, 3, 3, 3}}, {"no weights", [4]uint8{1, 1, 1, 1}}} {
			bucketTF = alt.tf
			var kept, all int
			var changed []string
			for _, q := range studyQueries {
				was := map[string]bool{}
				for _, t := range base[q] {
					was[t] = true
				}
				got := top(q)
				same := 0
				for _, t := range got {
					if was[t] {
						same++
					}
				}
				kept += same
				all += len(base[q])
				if len(base[q]) > 0 && same < len(base[q]) && len(changed) < 6 {
					changed = append(changed, fmt.Sprintf("    %q: now %s | would be %s", q, strings.Join(base[q], "; "), strings.Join(got, "; ")))
				}
			}
			bucketTF = now
			r.f("%s, %s: %d of %d top-10 results stay in the top 10 (%.0f%%)", b.files.Key(), alt.name, kept, all, 100*float64(kept)/float64(all))
			for _, c := range changed {
				r.f("%s", c)
			}
		}
		r.save(b.files.DB)
	}
}

func tfBits(tf []uint8) int64 {
	var b int64
	for _, x := range tf {
		b += [4]int64{1, 2, 3, 3}[x]
	}
	return b
}

// TestStudyNewFormat builds the index again in the current format, next
// to the one there (as <dump>.nfts.new in the search folder; the one
// NABU uses is left alone), and compares the two: size, speed, results.
// It takes as long as building the index (English: 2-3 hours), so it
// runs only with NABU_STUDY_REBUILD=1. Run again, it uses the .new
// already built.
func TestStudyNewFormat(t *testing.T) {
	if os.Getenv("NABU_STUDY_REBUILD") == "" {
		t.Skip("set NABU_STUDY_REBUILD=1 to build the index again and compare (as long as a build)")
	}
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "new format"}
		out := b.path + ".new"
		if _, err := os.Stat(out); err != nil {
			start := time.Now()
			last := time.Now()
			err := Build(b.rd, out, filepath.Join(filepath.Dir(b.path), ".study-tmp"), Options{Progress: func(done, total int64) {
				if time.Since(last) > 30*time.Second {
					last = time.Now()
					t.Logf("  building: %.1f%% after %s", 100*float64(done)/float64(total), time.Since(start).Round(time.Second))
				}
			}})
			os.RemoveAll(filepath.Join(filepath.Dir(b.path), ".study-tmp"))
			if err != nil {
				t.Fatal(err)
			}
			r.f("%s: built in %s", b.files.Key(), time.Since(start).Round(time.Second))
		}
		nw, err := Open(out, b.files)
		if err != nil {
			t.Fatal(err)
		}
		defer nw.Close()
		so, _ := os.Stat(b.path)
		sn, _ := os.Stat(out)
		r.f("%s: format %d %s, format %d %s (%+.1f%%)", b.files.Key(), b.ix.Meta.Version, mb(so.Size()), nw.Meta.Version, mb(sn.Size()), 100*float64(sn.Size()-so.Size())/float64(so.Size()))
		for _, part := range []string{"postings", "dictionary", "docs"} {
			o, n := b.ix.Sections()[part], nw.Sections()[part]
			r.f("  %-10s %10s -> %10s (%+.1f%%)", part, mb(o), mb(n), 100*float64(n-o)/float64(o))
		}
		// Speed: every query 3 times in each, the best time.
		var to, tn time.Duration
		best := func(ix *Index, q string) time.Duration {
			var m time.Duration
			for i := 0; i < 3; i++ {
				a := time.Now()
				if _, err := ix.Search(q, 0, 20); err != nil {
					t.Fatal(err)
				}
				if d := time.Since(a); i == 0 || d < m {
					m = d
				}
			}
			return m
		}
		for _, q := range studyQueries {
			to += best(b.ix, q)
			tn += best(nw, q)
		}
		k := float64(len(studyQueries))
		r.f("  search (without snippets): %.2f ms -> %.2f ms on average", float64(to.Microseconds())/1000/k, float64(tn.Microseconds())/1000/k)
		// Results: the new ranking counts article length.
		same, all := 0, 0
		for _, q := range studyQueries {
			ro, _ := b.ix.Search(q, 0, 10)
			rn, _ := nw.Search(q, 0, 10, nw.TitleDoc(b.rd, q)...)
			if ro.Total != rn.Total {
				t.Errorf("%q: %d matches, now %d", q, ro.Total, rn.Total)
			}
			was := map[string]bool{}
			var lo, ln []string
			for _, h := range ro.Hits {
				was[h.Title] = true
				lo = append(lo, h.Title)
			}
			for _, h := range rn.Hits {
				if was[h.Title] {
					same++
				}
				ln = append(ln, h.Title)
			}
			all += len(ro.Hits)
			r.f("  %q\n    before: %s\n    now:    %s", q, strings.Join(lo, "; "), strings.Join(ln, "; "))
		}
		r.f("  %d of %d top-10 results the same (%.0f%%); the same articles match every query", same, all, 100*float64(same)/float64(all))
		r.save(b.files.DB)
	}
}

// TestStudyWords sorts the indexed words into kinds (numbers, codes,
// other scripts, words in one article only...) with the room each kind
// takes, and lists the commonest section headings: words that are in
// nearly every article for its layout, not its subject.
func TestStudyWords(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "words"}
		type kind struct {
			name                  string
			words, posts, onlyOne int64
			bytes                 int64 // dictionary (estimated) + postings
			sample                []string
		}
		kinds := map[string]*kind{}
		order := []string{"years and numbers (1-4 digits)", "letters and digits mixed", "other scripts", "over 15 letters", "words"}
		for _, k := range order {
			kinds[k] = &kind{name: k}
		}
		var n int64
		allRows(t, b.ix, func(row dictRow) {
			n++
			k := kinds["words"]
			digits, letters, segs := 0, 0, 0
			prevDigit := -1
			for _, c := range row.term {
				d := 0
				if c >= '0' && c <= '9' {
					digits++
					d = 1
				} else {
					letters++
				}
				if d != prevDigit {
					segs++
					prevDigit = d
				}
			}
			switch {
			case letters == 0:
				k = kinds["years and numbers (1-4 digits)"]
			case digits > 0:
				k = kinds["letters and digits mixed"]
			case row.term[0] >= 0x80 && !strings.ContainsAny(row.term, "abcdefghijklmnopqrstuvwxyz"):
				k = kinds["other scripts"]
			case len([]rune(row.term)) > 15:
				k = kinds["over 15 letters"]
			}
			k.words++
			k.posts += int64(row.df)
			k.bytes += int64(row.size) + int64(len(row.term))/2 + 4
			if row.df == 1 {
				k.onlyOne++
			}
			if n%(1+int64(b.ix.Meta.Terms)/2000) == 0 && len(k.sample) < 30 {
				k.sample = append(k.sample, fmt.Sprintf("%s(%d)", row.term, row.df))
			}
		})
		r.f("%s: %d words in the index", b.files.Key(), n)
		r.f("  %-34s %10s %9s %12s %12s", "kind", "words", "in one", "postings", "room")
		for _, name := range order {
			k := kinds[name]
			r.f("  %-34s %10d %8.0f%% %12d %12s", name, k.words, 100*float64(k.onlyOne)/float64(max64(k.words, 1)), k.posts, mb(k.bytes))
		}
		for _, name := range order {
			r.f("  %s, for example: %s", name, strings.Join(kinds[name].sample, " "))
		}

		// Section headings in a sample of articles.
		total := b.rd.Index.StreamCount()
		sample := 1500
		if sample > total {
			sample = total
		}
		heads := map[string]int{}
		articles := 0
		for i := 0; i < sample; i++ {
			ps, err := b.rd.StreamPages(uint32(int64(i) * int64(total) / int64(sample)))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range ps {
				if p.NS != dump.NSMain || p.Redirect != "" {
					continue
				}
				articles++
				seen := map[string]bool{}
				for _, line := range strings.Split(p.Text, "\n") {
					line = strings.TrimSpace(line)
					if len(line) > 4 && strings.HasPrefix(line, "==") && strings.HasSuffix(line, "==") {
						h := strings.ToLower(strings.TrimSpace(strings.Trim(line, "=")))
						if !seen[h] {
							seen[h] = true
							heads[h]++
						}
					}
				}
			}
		}
		type hc struct {
			h string
			n int
		}
		var hs []hc
		for h, c := range heads {
			hs = append(hs, hc{h, c})
		}
		sort.Slice(hs, func(i, j int) bool { return hs[i].n > hs[j].n })
		var line []string
		for i, h := range hs {
			if i == 40 {
				break
			}
			line = append(line, fmt.Sprintf("%s %.0f%%", h.h, 100*float64(h.n)/float64(articles)))
		}
		r.f("  commonest section headings (in %% of %d sampled articles): %s", articles, strings.Join(line, ", "))
		r.save(b.files.DB)
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// TestStudyVocab shows, on an index as it is, what the rules of vocab.go
// would do in a new build: the common words left out as general (and
// the room they take), and the pairs of words that would be indexed
// (counted on a sample of the articles).
func TestStudyVocab(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "vocabulary"}
		ix := b.ix
		lang := ix.Meta.Lang
		inTitles := map[string]uint32{}
		for d := 0; d < ix.Meta.Docs; d++ {
			doc, err := ix.Doc(uint32(d))
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			Tokenize(doc.Title, func(w string) {
				if !seen[w] {
					seen[w] = true
					inTitles[w]++
				}
			})
		}
		many := minCommonDocs(ix.Meta.Docs)
		var all, common, dropBytes int64
		type gw struct {
			w  string
			df uint32
		}
		var dropped []gw
		var keptSample, rescued []string
		// one-word titles of articles (the study cannot tell disambiguation
		// pages without reading them: it counts them too)
		one := map[string]bool{}
		for d := 0; d < ix.Meta.Docs; d++ {
			doc, _ := ix.Doc(uint32(d))
			var tw []string
			Tokenize(doc.Title, func(w string) { tw = append(tw, w) })
			if len(tw) == 1 {
				one[tw[0]] = true
			}
		}
		v := newVocab(lang, one)
		allRows(t, ix, func(row dictRow) {
			all += int64(row.size)
			if row.df < many || strings.HasPrefix(row.term, phraseMark) {
				return
			}
			common++
			c := v.word(row.term, row.df, inTitles[row.term])
			if c == kept && classify(row.term, lang, row.df, inTitles[row.term]) == general && len(rescued) < 150 {
				rescued = append(rescued, row.term)
			}
			if c != general {
				if common%97 == 0 && len(keptSample) < 80 {
					keptSample = append(keptSample, row.term)
				}
				return
			}
			dropped = append(dropped, gw{row.term, row.df})
			dropBytes += int64(row.size)
		})
		sort.Slice(dropped, func(i, j int) bool { return dropped[i].df > dropped[j].df })
		r.f("%s: %d words in %d+ articles; %d of them general (not indexed in a new build), %s of %s of lists (%.1f%%)",
			b.files.Key(), common, many, len(dropped), mb(dropBytes), mb(all), 100*float64(dropBytes)/float64(all))
		var line []string
		for i, d := range dropped {
			if i == 300 {
				break
			}
			line = append(line, d.w)
		}
		r.f("  the commonest general words: %s", strings.Join(line, " "))
		line = line[:0]
		for i := len(dropped) - 1; i >= 0 && len(line) < 150; i -= 1 + len(dropped)/150 {
			line = append(line, dropped[i].w)
		}
		r.f("  some rarer general words: %s", strings.Join(line, " "))
		r.f("  some common words kept: %s", strings.Join(keptSample, " "))
		r.f("  kept as forms of kept words: %s", strings.Join(rescued, " "))

		// Pairs, on a sample.
		phrases := titlePhrases(b.rd.Index, ix.rules)
		total := b.rd.Index.StreamCount()
		sample := 1500
		if sample > total {
			sample = total
		}
		pairDF := map[string]int{}
		docs := 0
		for i := 0; i < sample; i++ {
			ps, err := b.rd.StreamPages(uint32(int64(i) * int64(total) / int64(sample)))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range ps {
				if p.NS != dump.NSMain || p.Redirect != "" {
					continue
				}
				docs++
				seen := map[string]bool{}
				prev := ""
				TokenizeBreaks(wikitext.PlainText(withoutBoilerplate(p.Text, lang), b.rd.Site()), func(w string, c Case, brk bool) {
					if brk {
						prev = ""
					}
					if !ix.rules.Keep(w, c) {
						return
					}
					if junk(w) {
						prev = ""
						return
					}
					if prev != "" && prev != w && phrases.has(prev, w) {
						if k := prev + " " + w; !seen[k] {
							seen[k] = true
							pairDF[k]++
						}
					}
					prev = w
				})
			}
		}
		scale := float64(ix.Meta.Docs) / float64(docs)
		var kept []gw
		var posts float64
		for k, n := range pairDF {
			ab := strings.SplitN(k, " ", 2)
			est := float64(n) * scale
			if v.pair(ab[0], ab[1], uint32(est)) {
				kept = append(kept, gw{k, uint32(est)})
				posts += est
			}
		}
		sort.Slice(kept, func(i, j int) bool { return kept[i].df > kept[j].df })
		line = line[:0]
		for i, k := range kept {
			if i == 150 {
				break
			}
			line = append(line, k.w)
		}
		r.f("  pairs of words from %d titles: about %d indexed, adding about %.0f postings (%.1f%% more)", len(phrases.h), len(kept), posts, 100*posts/float64(ix.Meta.Postings))
		r.f("  the commonest: %s", strings.Join(line, ", "))
		r.save(b.files.DB)
	}
}

// TestStudyTitles measures the article table written as format 4 does
// (titles compressed by blocks with a dictionary of title words) on the
// index's own titles, and how long a block takes to read back.
func TestStudyTitles(t *testing.T) {
	for _, b := range studyBooks(t) {
		r := &report{t: t, name: "titles"}
		ix := b.ix
		tw := titleWords{}
		var all []Doc
		var raw int64
		for d := 0; d < ix.Meta.Docs; d++ {
			doc, err := ix.Doc(uint32(d))
			if err != nil {
				t.Fatal(err)
			}
			tw.add(uint32(d), doc.Title)
			all = append(all, doc)
			raw += int64(len(doc.Title))
		}
		dict := tw.dict()
		var size int64
		var blk []byte
		var sample [][]byte
		for i := 0; i < len(all); i += docBlock {
			j := i + docBlock
			if j > len(all) {
				j = len(all)
			}
			var err error
			if blk, err = encodeDocBlock(blk[:0], all[i:j], dict); err != nil {
				t.Fatal(err)
			}
			size += int64(len(blk))
			if (i/docBlock)%97 == 0 && len(sample) < 200 {
				sample = append(sample, append([]byte(nil), blk...))
			}
		}
		start := time.Now()
		for _, s := range sample {
			if _, ok := decodeDocBlock(s, docBlock, true, dict); !ok {
				t.Fatal("a block does not read back")
			}
		}
		per := time.Since(start) / time.Duration(len(sample))
		was := ix.Sections()["docs"]
		r.f("%s: article table %s (titles %s); written as format 4: %s (%+.0f%%), a block of %d read in %s",
			b.files.Key(), mb(was), mb(raw), mb(size), 100*float64(size-was)/float64(was), docBlock, per.Round(time.Microsecond))
		r.save(b.files.DB)
	}
}
