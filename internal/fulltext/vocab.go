package fulltext

import (
	_ "embed"
	"hash/fnv"
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

// keepWord decides for a word in df >= minCommonDocs articles, titleDocs
// of them having it in the title.
func keepWord(t, lang string, df, titleDocs uint32) bool {
	if strings.Trim(t, "0123456789") == "" {
		return true
	}
	ratio := float64(titleDocs) / float64(df)
	if ratio >= titleShare {
		return true
	}
	for _, f := range formsOf(t, lang) {
		if survival[f] && (lang == "en" || lang == "simple") {
			return true
		}
	}
	return false
}

// phraseKey is the term of a pair of words.
func phraseKey(a, b string) string { return phraseMark + a + " " + b }

func phraseHash(a, b string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(a))
	h.Write([]byte{' '})
	h.Write([]byte(b))
	return h.Sum64()
}

// phraseSet holds the pairs of words that are titles, as hashes.
type phraseSet []uint64

func (s phraseSet) has(a, b string) bool {
	if len(s) == 0 {
		return false
	}
	h := phraseHash(a, b)
	i := sort.Search(len(s), func(i int) bool { return s[i] >= h })
	return i < len(s) && s[i] == h
}

// titlePhrases collects the titles (redirects too) of two words, little
// words aside.
func titlePhrases(ix *dump.Index, rules Rules) phraseSet {
	if ix == nil {
		return nil
	}
	var out phraseSet
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	u := out[:0]
	for i, h := range out {
		if i == 0 || h != out[i-1] {
			u = append(u, h)
		}
	}
	return u
}
