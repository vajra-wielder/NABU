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
	article map[string]bool // one-word titles of articles (not redirects, not disambiguation pages)
}

func newVocab(lang string, article map[string]bool) *vocab {
	return &vocab{lang: lang, class: map[string]wordClass{}, df: map[string]uint32{}, article: article}
}

// word decides for a word in df >= minCommonDocs articles, titleDocs of
// them having it in the title. Words come in byte order, so a plural
// ("vegetables") comes after its singular and is kept with it.
func (v *vocab) word(t string, df, titleDocs uint32) wordClass {
	c := classify(t, v.lang, df, titleDocs)
	if c == general && v.article[t] && float64(titleDocs) >= articleShare*float64(df) {
		// an article of its own, and in titles now and then: "platinum",
		// "stomach" (not "official": in titles too seldom for how common)
		c = naming
	}
	if c == general {
		for _, f := range formsOf(t, v.lang) {
			if f < t && v.class[f] != general {
				c = kept
				break
			}
		}
	}
	v.class[t], v.df[t] = c, df
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
func (v *vocab) pair(a, b string, df uint32) bool {
	ca, oka := v.class[a]
	cb, okb := v.class[b]
	if !oka || !okb || df < minPhraseDocs || len(a) < 2 || len(b) < 2 {
		return false
	}
	if ca != naming && cb != naming {
		return false
	}
	return 2*df <= v.df[a] && 2*df <= v.df[b]
}

// keepWord reports whether a word would be indexed by itself (tests).
func keepWord(t, lang string, df, titleDocs uint32) bool {
	return classify(t, lang, df, titleDocs) != general
}

// phraseKey is the term of a pair of words.
func phraseKey(a, b string) string { return phraseMark + a + " " + b }

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
	return phraseSet{u, st}
}

func (s phraseSet) has(a, b string) bool {
	if len(s.h) == 0 {
		return false
	}
	h := phraseHash(a, b)
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

// titlePhrases collects the titles (redirects too) of two words, little
// words aside.
func titlePhrases(ix *dump.Index, rules Rules) phraseSet {
	if ix == nil {
		return phraseSet{}
	}
	var out []uint64
	ix.Each(func(e dump.Entry) bool {
		if e.NS != dump.NSMain {
			return true
		}
		var ws [3]string
		n := 0
		TokenizeCase(e.Title, func(w string, c Case) {
			if rules.Keep(w, c) && !junk(w) {
				if n < 3 {
					ws[n] = w
				}
				n++
			}
		})
		if n == 2 && ws[0] != ws[1] {
			out = append(out, phraseHash(ws[0], ws[1]))
		}
		return true
	})
	return newPhraseSet(out)
}

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
