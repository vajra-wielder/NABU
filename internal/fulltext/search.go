package fulltext

import (
	"math"
	"sort"
	"strings"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Hit is a search result.
type Hit struct {
	Doc
	N     uint32
	Score float64
}

// Result is the outcome of a search.
type Result struct {
	Hits    []Hit
	Total   int      // articles found
	Terms   []string // words searched
	Marks   []string // the words and the forms of them found, to mark in snippets
	Ignored []string // too common to search
	General []string // of those, the ones not indexed for naming nothing ("official")
	Phrases []string // pairs of the words searched as one ("first aid")
	// Partial: no article has every word, so these are the articles with
	// the most of them (Missing: the words they lack).
	Partial bool
	Missing []string
	// Suggest is the search spelled as words of the index, when some word
	// is in no article ("photosyntesis" → "photosynthesis"). Corrected:
	// these are the results for Suggest, as the search as typed found
	// nothing (or only articles with some of the words).
	Suggest   string
	Corrected bool
}

const bm25K1 = 1.2

// bm25B is how much an article's length counts (0: not at all; 1: fully).
// titleBoost is added for each searched word in the title; rerank is how
// many of the best are looked at again with their titles.
var bm25B, titleBoost, rerank = 0.3, 4.0, 400

type qword struct {
	term  string
	forms []string // the forms of it in the index
	rows  []dictRow
	list  []posting // every article with one of the forms
}

// Search finds the articles containing every word of q (in any of their
// forms: "bird" finds "birds"), best first; when none has them all, the
// ones with the most of them. An article in first (the one whose title
// is q, say) comes first when it has the words.
func (ix *Index) Search(q string, offset, limit int, first ...Doc) (*Result, error) {
	res, err := ix.search(q, offset, limit, first)
	if err != nil || res.Suggest == "" || (res.Total > 0 && !res.Partial) {
		return res, err
	}
	// Spelt as the index has it, the search finds more: show that.
	better, err := ix.search(res.Suggest, offset, limit, first)
	if err != nil {
		return nil, err
	}
	if better.Total > 0 && (!better.Partial || res.Total == 0) {
		better.Suggest, better.Corrected = res.Suggest, true
		return better, nil
	}
	return res, nil
}

func (ix *Index) search(q string, offset, limit int, first []Doc) (*Result, error) {
	res := &Result{}
	// A little word is searched only as it is indexed: "US" in capitals,
	// not "us".
	var order, seq []string // seq: the words as typed, little words aside, for the pairs
	keep := map[string]bool{}
	TokenizeCase(q, func(t string, c Case) {
		if _, seen := keep[t]; !seen {
			order = append(order, t)
		}
		keep[t] = keep[t] || (ix.rules.Keep(t, c) && !ix.common[t] && !ix.general[t])
		if ix.rules.Keep(t, c) && !junk(t) {
			seq = append(seq, t)
		}
	})
	var words []*qword
	for _, t := range order {
		if !keep[t] {
			res.Ignored = append(res.Ignored, t)
			if ix.general[t] {
				res.General = append(res.General, t)
			}
			continue
		}
		w := &qword{term: t}
		for _, f := range ix.forms(t) {
			row, ok, err := ix.lookup(f)
			if err != nil {
				return nil, err
			}
			if ok {
				w.forms = append(w.forms, f)
				w.rows = append(w.rows, row)
			}
		}
		res.Terms = append(res.Terms, t)
		res.Marks = append(res.Marks, w.forms...)
		words = append(words, w)
	}
	if len(words) == 0 {
		return res, nil
	}
	var present []*qword
	suggest := map[string]string{}
	for _, w := range words {
		if len(w.rows) == 0 {
			if s := ix.correct(w.term); s != "" {
				suggest[w.term] = s
			}
			continue
		}
		var err error
		if w.list, err = ix.union(w.rows); err != nil {
			return nil, err
		}
		present = append(present, w)
	}
	if len(suggest) > 0 {
		var out []string
		for _, t := range order {
			if s, ok := suggest[t]; ok {
				t = s
			}
			out = append(out, t)
		}
		res.Suggest = strings.Join(out, " ")
	}
	if len(present) == 0 {
		return res, nil
	}

	N := float64(ix.Meta.Docs)
	idf := func(df int) float64 { return math.Log(1 + (N-float64(df)+0.5)/(float64(df)+0.5)) }
	// BM25: a word's weight in an article grows with how often it is
	// there, less and less, and less in a long article (format 3 knows
	// their lengths; format 2 counts every article as average).
	var norm [256]float64
	for i := range norm {
		norm[i] = bm25K1
		if ix.lens != nil && ix.Meta.AvgLen > 0 {
			norm[i] = bm25K1 * (1 - bm25B + bm25B*byteLen(byte(i))/ix.Meta.AvgLen)
		}
	}
	tfw := func(doc uint32, tf uint8) float64 {
		k := bm25K1
		if ix.lens != nil {
			k = norm[ix.lens[doc]]
		}
		return float64(tf) / (float64(tf) + k)
	}
	and := func(ws []*qword) []cand {
		ws = append([]*qword(nil), ws...)
		sort.Slice(ws, func(i, j int) bool { return len(ws[i].list) < len(ws[j].list) })
		cands := make([]cand, len(ws[0].list))
		wt := idf(len(ws[0].list))
		for i, p := range ws[0].list {
			cands[i] = cand{p.doc, wt * tfw(p.doc, p.tf)}
		}
		for _, w := range ws[1:] {
			wt := idf(len(w.list))
			out := cands[:0]
			j := 0
			for _, c := range cands {
				for j < len(w.list) && w.list[j].doc < c.doc {
					j++
				}
				if j < len(w.list) && w.list[j].doc == c.doc {
					c.score += wt * tfw(c.doc, w.list[j].tf)
					out = append(out, c)
				}
			}
			cands = out
			if len(cands) == 0 {
				break
			}
		}
		return cands
	}
	var cands []cand
	used := present
	if len(present) == len(words) {
		cands = and(present)
	}
	if len(cands) == 0 {
		// No article has every word: the ones with the most of them, the
		// rarer (more telling) words kept first.
		used, cands = bestPart(present, and, func(w *qword) float64 { return idf(len(w.list)) })
		if len(cands) > 0 {
			res.Partial = true
			in := map[*qword]bool{}
			for _, w := range used {
				in[w] = true
			}
			for _, w := range words {
				if !in[w] {
					res.Missing = append(res.Missing, w.term)
				}
			}
		}
	}
	res.Total = len(cands)
	if len(cands) == 0 {
		return res, nil
	}
	// Pairs of the words indexed as one: the articles that have them
	// together come first.
	for i := 0; i+1 < len(seq); i++ {
		a := seq[i]
		var rows []dictRow
		var found string
		for _, f := range ix.forms(seq[i+1]) {
			if row, ok, err := ix.lookup(phraseKey(a, f)); err == nil && ok {
				rows = append(rows, row)
				if found == "" {
					found = a + " " + f
				}
			}
		}
		if len(rows) == 0 {
			continue
		}
		list, err := ix.union(rows)
		if err != nil {
			return nil, err
		}
		res.Phrases = append(res.Phrases, found)
		res.Marks = append(res.Marks, a, seq[i+1])
		wt := 2 * idf(len(list))
		j := 0
		for k := range cands {
			for j < len(list) && list[j].doc < cands[k].doc {
				j++
			}
			if j < len(list) && list[j].doc == cands[k].doc {
				cands[k].score += wt * tfw(cands[k].doc, list[j].tf)
			}
		}
	}
	// cands are in article order: the ones asked to come first are found
	// by number.
	lead := map[uint32]bool{}
	for _, d := range first {
		if n, ok := ix.DocOf(d.Stream, d.PageID); ok {
			if i := sort.Search(len(cands), func(i int) bool { return cands[i].doc >= n }); i < len(cands) && cands[i].doc == n {
				cands[i].score += 1000
				lead[n] = true
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	// Re-rank the best with their titles: a title containing the words,
	// or equal to the search, comes first.
	top := offset + limit + rerank
	if top > len(cands) {
		top = len(cands)
	}
	if lim := offset + limit; top > 400 && top > lim {
		top = 400
		if lim > top {
			top = lim
		}
	}
	fq := dump.Fold(q)
	form := map[string]bool{}
	for _, w := range used {
		for _, f := range w.forms {
			form[f] = true
		}
	}
	hits := make([]Hit, 0, top)
	for _, c := range cands[:top] {
		d, err := ix.Doc(c.doc)
		if err != nil {
			return nil, err
		}
		h := Hit{Doc: d, N: c.doc, Score: c.score}
		if lead[c.doc] {
			// already first
		} else if ft := dump.Fold(d.Title); ft == fq {
			h.Score += 100
		} else if form[ft] {
			// the article of one of the words: "Desert" for "largest desert"
			h.Score += 2 * titleBoost
		} else {
			inTitle := 0
			Tokenize(d.Title, func(t string) {
				if form[t] {
					inTitle++
				}
			})
			h.Score += titleBoost * float64(inTitle)
		}
		hits = append(hits, h)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if offset > len(hits) {
		offset = len(hits)
	}
	end := offset + limit
	if end > len(hits) {
		end = len(hits)
	}
	res.Hits = hits[offset:end]
	return res, nil
}

type cand struct {
	doc   uint32
	score float64
}

// bestPart finds the largest set of the words some article has all of,
// among sets of the same size the one whose words weigh most.
func bestPart(ws []*qword, and func([]*qword) []cand, weight func(*qword) float64) ([]*qword, []cand) {
	n := len(ws)
	if n > 6 {
		// Too many to try every set: leave out the commonest words one
		// by one.
		ws = append([]*qword(nil), ws...)
		sort.Slice(ws, func(i, j int) bool { return len(ws[i].list) < len(ws[j].list) })
		for k := len(ws); k > 0; k-- {
			if c := and(ws[:k]); len(c) > 0 {
				return ws[:k], c
			}
		}
		return nil, nil
	}
	for size := n; size > 0; size-- {
		var best []*qword
		var bestC []cand
		bestW := -1.0
		for mask := 1; mask < 1<<uint(n); mask++ {
			if popcount(mask) != size {
				continue
			}
			var set []*qword
			w := 0.0
			for i := 0; i < n; i++ {
				if mask&(1<<uint(i)) != 0 {
					set = append(set, ws[i])
					w += weight(ws[i])
				}
			}
			if w <= bestW {
				continue
			}
			if c := and(set); len(c) > 0 {
				best, bestC, bestW = set, c, w
			}
		}
		if best != nil {
			return best, bestC
		}
	}
	return nil, nil
}

func popcount(x int) int {
	n := 0
	for ; x != 0; x &= x - 1 {
		n++
	}
	return n
}

// union reads the lists of a word's forms as one: every article with
// any of them, with the highest weight.
func (ix *Index) union(rows []dictRow) ([]posting, error) {
	var out []posting
	for i, row := range rows {
		ps, err := ix.postings(row)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			out = ps
			continue
		}
		merged := make([]posting, 0, len(out)+len(ps))
		a, b := 0, 0
		for a < len(out) || b < len(ps) {
			switch {
			case b == len(ps) || a < len(out) && out[a].doc < ps[b].doc:
				merged = append(merged, out[a])
				a++
			case a == len(out) || ps[b].doc < out[a].doc:
				merged = append(merged, ps[b])
				b++
			default:
				p := out[a]
				if ps[b].tf > p.tf {
					p.tf = ps[b].tf
				}
				merged = append(merged, p)
				a++
				b++
			}
		}
		out = merged
	}
	return out, nil
}
