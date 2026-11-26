package fulltext

import (
	"strings"
)

// WikiSnippet is Snippet for an article's wikitext. Turning a whole long
// article into text takes most of the time a results page needs, so the
// words are looked for in the wikitext first, and only the paragraph they
// are in is turned into text (plain), outside templates, tables,
// references and comments. When that does not show them (words written
// with accents, say, or only in the infobox), the whole article is.
func WikiSnippet(src string, terms []string, width int, plain func(string) string) string {
	if len(src) > 6000 {
		for _, para := range paragraphsWith(src, terms, 4) {
			if s, ok := snippet(plain(para), terms, width); ok {
				return s
			}
		}
	}
	s, _ := snippet(plain(src), terms, width)
	return s
}

// paragraphsWith returns up to max stretches of the article around the
// paragraphs of running text (outside markup) in which one of terms
// appears, in the order they come: from the paragraph before to about
// 3000 bytes on, so a snippet has the words around them too.
func paragraphsWith(src string, terms []string, max int) []string {
	var ascii []string
	for _, t := range terms {
		if t != "" && isASCII(t) {
			ascii = append(ascii, t)
		}
	}
	if len(ascii) == 0 {
		return nil
	}
	var starts []int // paragraph starts at depth 0
	var hits []int   // indexes in starts of paragraphs with a term
	depth := 0       // inside {{ }}, {| |}, <ref>, <!-- -->
	text := 0        // start of the running text being read (at depth 0)
	n := len(src)
	at := func(i int, s string) bool { return i+len(s) <= n && src[i:i+len(s)] == s }
	// upTo looks for the terms in the running text before i.
	upTo := func(i int) {
		if depth == 0 && i > text && hasTerm(src[text:i], ascii) {
			if k := len(starts) - 1; len(hits) == 0 || hits[len(hits)-1] != k {
				hits = append(hits, k)
			}
		}
	}
	starts = append(starts, 0)
	for i := 0; i < n; i++ {
		c := src[i]
		switch {
		case c == '{' && (at(i, "{{") || at(i, "{|")):
			upTo(i)
			depth++
			i++
		case depth > 0 && (at(i, "}}") || at(i, "|}")):
			depth--
			i++
			text = i + 1
		case c == '<' && at(i, "<!--"):
			upTo(i)
			if e := strings.Index(src[i:], "-->"); e >= 0 {
				i += e + 2
			} else {
				i = n
			}
			text = i + 1
		case c == '<' && depth == 0 && at(i, "<ref"):
			// <ref name="x" /> or <ref ...>...</ref>
			upTo(i)
			gt := strings.IndexByte(src[i:], '>')
			switch {
			case gt < 0:
				i = n
			case src[i+gt-1] == '/':
				i += gt
			default:
				if e := strings.Index(src[i:], "</ref>"); e >= 0 {
					i += e + 5
				} else {
					i = n
				}
			}
			text = i + 1
		case c == '\n' && depth == 0 && i+1 < n && (src[i+1] == '\n' || strings.IndexByte("=*#:;", src[i+1]) >= 0):
			// a blank line, or headings and lists: a paragraph of their own
			upTo(i)
			starts = append(starts, i+1)
			text = i + 1
		}
	}
	upTo(n)
	var out []string
	for _, k := range hits {
		if len(out) == max {
			break
		}
		from := starts[k]
		if k > 0 {
			from = starts[k-1]
		}
		to := n
		for j := k + 1; j < len(starts); j++ {
			if starts[j] >= starts[k]+3000 {
				to = starts[j]
				break
			}
		}
		out = append(out, src[from:to])
	}
	return out
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// hasTerm reports whether one of terms (lower case) appears in s as a
// whole word, whatever its case.
func hasTerm(s string, terms []string) bool {
	for _, t := range terms {
		for from := 0; ; {
			i := indexFold(s[from:], t)
			if i < 0 {
				break
			}
			i += from
			j := i + len(t)
			if (i == 0 || !asciiWord(s[i-1])) && (j == len(s) || !asciiWord(s[j])) {
				return true
			}
			from = i + 1
		}
	}
	return false
}

func asciiWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// indexFold is strings.Index ignoring ASCII case; t is in lower case.
func indexFold(s, t string) int {
	if len(t) == 0 {
		return 0
	}
	first := t[0]
	up := first
	if up >= 'a' && up <= 'z' {
		up -= 'a' - 'A'
	}
	for i := 0; i+len(t) <= len(s); i++ {
		if c := s[i]; c != first && c != up {
			continue
		}
		ok := true
		for k := 1; k < len(t); k++ {
			c := s[i+k]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != t[k] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
