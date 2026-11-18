// Package fulltext is NABU's optional search inside article text: an
// inverted index built by scanning a whole dump, plus the location of
// every geotagged article for "places nearby".
package fulltext

import (
	"unicode"
	"unicode/utf8"

	"github.com/vajra-wielder/nabu/internal/dump"
)

const maxTermBytes = 48

// isCJK: scripts written without spaces between words. They are indexed
// as overlapping pairs of characters, the usual approach for Chinese and
// Japanese search.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Thai, r) || unicode.Is(unicode.Lao, r) || unicode.Is(unicode.Khmer, r) || unicode.Is(unicode.Myanmar, r)
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) }

// Tokenize calls emit for every search term in s, in order: folded words
// of two or more characters (case and accents removed, as in title
// search), and character pairs for CJK text.
func Tokenize(s string, emit func(term string)) {
	word := make([]rune, 0, 32)
	var cjk []rune
	flushWord := func() {
		if len(word) >= 2 {
			t := dump.Fold(string(word))
			if len(t) <= maxTermBytes {
				emit(t)
			}
		}
		word = word[:0]
	}
	flushCJK := func() {
		switch {
		case len(cjk) == 1:
			emit(string(cjk))
		case len(cjk) > 1:
			for i := 0; i+1 < len(cjk); i++ {
				emit(string(cjk[i : i+2]))
			}
		}
		cjk = cjk[:0]
	}
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case isWord(r):
			flushCJK()
			word = append(word, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
}

// Terms returns the distinct terms of a query, in order.
func Terms(q string) []string {
	var out []string
	seen := map[string]bool{}
	Tokenize(q, func(t string) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	})
	return out
}
