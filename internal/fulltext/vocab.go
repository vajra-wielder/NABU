package fulltext

import (
	_ "embed"
	"sort"
	"strings"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Which words are indexed.
//
// A word in many articles is kept only when it names something: a word of
// the titles (in titles at least titleShare as often as in articles,
// like "desert", "cardiac", "Paris"), or a number, or one of the survival
// words and the words of questions (survival.txt). That a word is itself
// a title says little: Wikipedia has an article "Official", and one for
// nearly every common word.
// Otherwise it is a general word ("official", "largest", "became",
// "including"): in so many articles that it hardly narrows a search, it
// takes room and pulls the ranking towards long articles. It is not
// indexed, and a search says so. Rare words are all kept: they are the
// names and terms people look for.
//
// Pairs of words that are the title of an article ("first aid", "silk
// road", "heart attack") are also indexed as one term where they follow
// each other in an article (little words between them aside: "bank of
// England"), when both words are common and the pair narrows them down:
// articles with the pair rank first. Word positions would do that for
// every pair, at about the size of the index again; pairs from titles
// take a few percent.
const (
	commonShare   = 1000 // "many articles": one in this many, at least minCommon
	minCommon     = 50
	titleShare    = 0.006  // in titles at least this often per article with the word
	articleShare  = 0.002  // the same, for a word that is itself the title of an article
	pairShare     = 0.018  // for a pair, one word must be in titles at least this often
	phraseMark    = "\xff" // phrase terms start with it: after every word, in byte order
	minPhraseDocs = 5
)

//go:embed survival.txt
var survivalText string

var survival = func() map[string]bool {
	m := map[string]bool{}
	for _, line := range strings.Split(survivalText, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		Tokenize(line, func(t string) { m[t] = true })
	}
	return m
}()

// minCommonDocs is how many articles make a word common in an index of n.
func minCommonDocs(n int) uint32 {
	if m := n / commonShare; m > minCommon {
		return uint32(m)
	}
	return minCommon
}

// wordClass is what a common word is to the index.
type wordClass uint8

const (
	general wordClass = iota // not indexed
	kept                     // indexed: a year, a survival or question word, a form of a kept word
	naming                   // indexed, naming something (a word of titles)
)

// common words seen so far in a build, in byte order, with their class.
type vocab struct {
	lang    string
	class   map[string]wordClass
	df      map[string]uint32
	ratio   map[string]float32 // in titles per article with the word
	article map[string]bool    // one-word titles of articles (not redirects, not disambiguation pages)
}

func newVocab(lang string, article map[string]bool) *vocab {
	return &vocab{lang: lang, class: map[string]wordClass{}, df: map[string]uint32{}, ratio: map[string]float32{}, article: article}
}

// word decides for a word in df >= minCommonDocs articles, titleDocs of
// them having it in the title. Words come in byte order, so a plural
// ("vegetables") comes after its singular and is kept with it.
func (v *vocab) word(t string, df, titleDocs uint32) wordClass {
	c := classify(t, v.lang, df, titleDocs)
	isNumber := strings.Trim(t, "0123456789") == ""
	if c == general && !isNumber && v.article[t] && float64(titleDocs) >= articleShare*float64(df) {
		// an article of its own, and in titles now and then: "platinum",
		// "stomach" (not "official": in titles too seldom for how common)
		c = naming
	}
	if c != general && (v.lang == "en" || v.lang == "simple") && basicWords[t] {
		// "one", "two", "red", "big": in many titles, naming nothing by
		// themselves; still part of pairs and longer titles ("red cross")
		c = general
	}
	if c == general {
		for _, f := range formsOf(t, v.lang) {
			if f < t && v.class[f] != general {
				c = kept
				break
			}
		}
	}
	v.class[t], v.df[t], v.ratio[t] = c, df, float32(titleDocs)/float32(df)
	return c
}

// classify is the class of a word by itself.
func classify(t, lang string, df, titleDocs uint32) wordClass {
	if strings.Trim(t, "0123456789") == "" {
		// Years stay ("1876"). Other numbers in so many articles are dates
		// and counts ("12", "300"): searched with a name, they are found as
		// pairs from titles or by the title ("Apollo 11").
		if len(t) == 4 && t >= "1000" && t <= "2099" {
			return kept
		}
		return general
	}
	if float64(titleDocs) >= titleShare*float64(df) {
		return naming
	}
	if lang == "en" || lang == "simple" {
		for _, f := range formsOf(t, lang) {
			if survival[f] {
				return kept
			}
		}
	}
	return general
}

// pair decides for a pair of words found in df articles: both words must
// be common (otherwise the words narrow a search well enough), one at
// least naming something ("first aid", "official language"; not "two
// years", "early life", "first time"), and the pair must narrow them
// down by half.
// gram decides for a title of 2 to 4 words found in df articles: pairs as
// pair; longer ones need every word common (otherwise its words narrow a
// search well enough), one naming something strongly, the ends not
// little words of other languages, and to narrow its words by half.
func (v *vocab) gram(ws []string, df uint32) bool {
	if len(ws) == 2 {
		return v.pair(ws[0], ws[1], df)
	}
	if df < minPhraseDocs || pairLittle[ws[0]] || pairLittle[ws[len(ws)-1]] {
		return false
	}
	strong := false
	for _, w := range ws {
		c, ok := v.class[w]
		if !ok || 2*df > v.df[w] {
			return false
		}
		if c != general && strings.Trim(w, "0123456789") != "" && v.ratio[w] >= pairShare {
			strong = true
		}
	}
	return strong
}

func (v *vocab) pair(a, b string, df uint32) bool {
	ca, oka := v.class[a]
	cb, okb := v.class[b]
	if !oka || !okb || df < minPhraseDocs || len(a) < 2 || len(b) < 2 {
		return false
	}
	// One word naming something strongly: in titles at least pairShare
	// as often as in articles ("heart", "road", "apollo"; not "life",
	// "time", "people", which also start many titles), or a survival
	// word ("aid"); and neither a little word of another language ("de
	// la") nor a number paired with anything less ("10 11", "age 18").
	if pairLittle[a] || pairLittle[b] {
		return false
	}
	strong := func(w string, c wordClass) bool {
		return c != general && strings.Trim(w, "0123456789") != "" &&
			(v.ratio[w] >= pairShare || (v.lang == "en" || v.lang == "simple") && survival[w] && v.ratio[w] >= titleShare)
	}
	if !strong(a, ca) && !strong(b, cb) {
		return false
	}
	return 2*df <= v.df[a] && 2*df <= v.df[b]
}

// keepWord reports whether a word would be indexed by itself (tests).
func keepWord(t, lang string, df, titleDocs uint32) bool {
	return classify(t, lang, df, titleDocs) != general
}

// phraseKey is the term of a pair of words.
func phraseKey(a, b string) string { return gramKey([]string{a, b}) }

// phraseHash is FNV-1a of "a b", without allocating: it is asked for
// every pair of words of every article.
func phraseHash(a, b string) uint64 {
	const prime = 1099511628211
	h := uint64(14695981039346656037)
	for i := 0; i < len(a); i++ {
		h = (h ^ uint64(a[i])) * prime
	}
	h = (h ^ ' ') * prime
	for i := 0; i < len(b); i++ {
		h = (h ^ uint64(b[i])) * prime
	}
	return h
}

// phraseSet holds the pairs of words that are titles, as sorted hashes,
// with where each run of the top 16 bits starts, so a lookup searches a
// few entries only.
type phraseSet struct {
	h     []uint64
	start []uint32 // 1<<16 + 1 entries
	// filter has a bit for each hash's low bits: most words and pairs
	// looked up are in no title, and the filter answers them without
	// searching (it fits in the processor's cache where the hashes do
	// not). About 8 bits an entry, at most 16 MB.
	filter []uint64
	mask   uint64
}

func newPhraseSet(hs []uint64) phraseSet {
	sort.Slice(hs, func(i, j int) bool { return hs[i] < hs[j] })
	u := hs[:0]
	for i, h := range hs {
		if i == 0 || h != hs[i-1] {
			u = append(u, h)
		}
	}
	st := make([]uint32, 1<<16+1)
	k := 0
	for top := 0; top <= 1<<16; top++ {
		for k < len(u) && int(u[k]>>48) < top {
			k++
		}
		st[top] = uint32(k)
	}
	bits := uint64(1 << 12)
	for bits < uint64(len(u))*8 && bits < 1<<27 {
		bits <<= 1
	}
	f := make([]uint64, bits/64)
	for _, h := range u {
		f[(h&(bits-1))>>6] |= 1 << (h & 63)
	}
	return phraseSet{h: u, start: st, filter: f, mask: bits - 1}
}

func (s phraseSet) has(a, b string) bool { return s.hasHash(phraseHash(a, b)) }

// titleSets holds what titles name (redirects too): their words, and
// the titles of 2, 3 and 4 words (little words aside), as hashes.
type titleSets struct {
	words phraseSet
	grams [maxGram + 1]phraseSet
}

const maxGram = 4

// newTitleSets reads every title of the title index.
func newTitleSets(ix *dump.Index, rules Rules) *titleSets {
	ts := &titleSets{}
	if ix == nil {
		return ts
	}
	var words []uint64
	var grams [maxGram + 1][]uint64
	var ws []string
	ix.Each(func(e dump.Entry) bool {
		if e.NS != dump.NSMain {
			return true
		}
		ws = ws[:0]
		TokenizeCase(e.Title, func(w string, c Case) {
			if rules.Keep(w, c) && !junk(w) {
				ws = append(ws, w)
			}
		})
		for _, w := range ws {
			words = append(words, gramHash([]string{w}))
		}
		if n := len(ws); n >= 2 && n <= maxGram {
			grams[n] = append(grams[n], gramHash(ws))
		}
		return true
	})
	ts.words = newPhraseSet(words)
	for n := 2; n <= maxGram; n++ {
		ts.grams[n] = newPhraseSet(grams[n])
	}
	return ts
}

// titleWord reports whether w, or a form of it, is a word of a title.
func (ts *titleSets) titleWord(w, lang string) bool {
	if ts.words.hasHash(gramHash([]string{w})) {
		return true
	}
	for _, f := range formsOf(w, lang)[1:] {
		if ts.words.hasHash(gramHash([]string{f})) {
			return true
		}
	}
	return false
}

// admit decides whether a word met in an article is indexed: a word of
// the titles (or a form of one), a name (written with a capital, not just
// after the end of a sentence), a number, or a survival word. The rest
// (words of no title: "including", "however", typing slips, codes,
// "colspan") is left out as it is read. With no titles to go by (tests,
// an estimate), every word is.
func (ts *titleSets) admit(w string, c Case, afterBreak bool, lang string) bool {
	if len(ts.words.h) == 0 {
		return true
	}
	if c != Lower && !afterBreak {
		return true
	}
	if strings.Trim(w, "0123456789") == "" {
		return true
	}
	if ts.titleWord(w, lang) {
		return true
	}
	return (lang == "en" || lang == "simple") && survival[w]
}

// gramKey is the term of a sequence of words.
func gramKey(ws []string) string { return phraseMark + strings.Join(ws, " ") }

// isDisambiguation reports whether wikitext is a disambiguation page: a
// list of the articles a word may mean, not an article about it.
func isDisambiguation(text string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], "{{")
		if j < 0 {
			return false
		}
		i += j + 2
		name := text[i:]
		if k := strings.IndexAny(name, "|}"); k >= 0 {
			name = name[:k]
		}
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case strings.Contains(name, "disambig"), name == "dab", name == "dis", strings.HasSuffix(name, "dis") && len(name) <= 8,
			name == "surname", name == "given name", name == "set index article", name == "sia":
			return true
		}
	}
}

// rareNoise reports a word in df articles that is not worth indexing for
// being rare and of no use to search there: in a wiki written in Latin
// letters, a word in another script in one or two articles (English has
// a million, mostly names quoted in their own script, searched in
// English instead), and codes of letters and digits in one or two
// articles ("02ee", "1656a").
func rareNoise(t, lang string, df uint32) bool {
	if df > 2 {
		return false
	}
	letters, digits, latin := 0, 0, false
	for _, r := range t {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r < 0x250:
			letters++
			latin = true
		default:
			letters++
		}
	}
	if digits > 0 && letters > 0 {
		return true
	}
	return letters > 0 && !latin && latinScript(lang)
}

// latinScript reports languages written in Latin letters.
func latinScript(lang string) bool {
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	switch lang {
	case "en", "simple", "fr", "de", "es", "pt", "it", "nl", "da", "sv", "no", "nb", "nn", "fi", "is", "id", "ms",
		"pl", "cs", "sk", "sl", "hr", "bs", "ro", "hu", "tr", "az", "vi", "tl", "sw", "ca", "eu", "gl", "et", "lv",
		"lt", "sq", "af", "cy", "ga", "eo", "la", "uz", "ha", "yo", "zu", "xh", "so", "mt", "lb", "oc", "br", "fy":
		return true
	}
	return false
}

// pairLittle are little words of other languages, common in English
// titles ("de la", "von der"): no pair is made of them.
var pairLittle = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields("de la le les el los las del della di da do dos das du des von van der den het al el ibn bin bint san santa st saint mc mac") {
		m[w] = true
	}
	return m
}()

// gramHash is the hash of a sequence of words, as phraseHash for two.
func gramHash(ws []string) uint64 {
	const prime = 1099511628211
	h := uint64(14695981039346656037)
	for i, w := range ws {
		if i > 0 {
			h = (h ^ ' ') * prime
		}
		for k := 0; k < len(w); k++ {
			h = (h ^ uint64(w[k])) * prime
		}
	}
	return h
}

func (s phraseSet) hasHash(h uint64) bool {
	if len(s.h) == 0 || s.filter[(h&s.mask)>>6]&(1<<(h&63)) == 0 {
		return false
	}
	lo, hi := int(s.start[h>>48]), int(s.start[h>>48+1])
	for lo < hi {
		m := (lo + hi) / 2
		switch {
		case s.h[m] == h:
			return true
		case s.h[m] < h:
			lo = m + 1
		default:
			hi = m
		}
	}
	return false
}

// basicWords are English words common in titles that name nothing on
// their own: numbers, colours, sizes and the like.
var basicWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`one two three four five six seven eight nine ten eleven twelve twenty hundred thousand
		million billion second third fourth fifth next red blue green yellow black white brown grey gray
		orange pink purple big small large little great good bad best old new young high low long short hot cold
		day days week year years month time times part side top end way thing things man men woman women boy girl
		other same own real true full half`) {
		m[w] = true
	}
	return m
}()
