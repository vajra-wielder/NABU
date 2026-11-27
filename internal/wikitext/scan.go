package wikitext

import "strings"

// Hand-written forms of the regular expressions PlainText used: the same
// replacements, many times faster (they were a third of the time of
// building search inside articles). plain_test.go checks them against
// the expressions on real articles.

// dropComments is commentRe.ReplaceAllString(s, ""): <!-- ... --> (to the
// end when not closed).
func dropComments(s string) string {
	i := strings.Index(s, "<!--")
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i >= 0 {
		b.WriteString(s[:i])
		e := strings.Index(s[i+4:], "-->")
		if e < 0 {
			return b.String()
		}
		s = s[i+4+e+3:]
		i = strings.Index(s, "<!--")
	}
	b.WriteString(s)
	return b.String()
}

// hasPrefixFold reports whether s starts with p (lower case ASCII),
// whatever its case.
func hasPrefixFold(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != p[i] {
			return false
		}
	}
	return true
}

// indexFold is strings.Index ignoring ASCII case; p is lower case.
func indexFold(s, p string) int {
	for i := 0; i+len(p) <= len(s); i++ {
		if c := s[i]; c == p[0] || c == p[0]-('a'-'A') {
			if hasPrefixFold(s[i:], p) {
				return i
			}
		}
	}
	return -1
}

// refMatch is the length of refBlockRe's match at the start of s (which
// starts with "<ref", any case), or -1:
// (?is)<ref[^>/]*>.*?</ref\s*>|<ref[^>]*/>
func refMatch(s string) int {
	// <ref[^>/]*>.*?</ref\s*>
	j := 4
	for j < len(s) && s[j] != '>' && s[j] != '/' {
		j++
	}
	if j < len(s) && s[j] == '>' {
		for k := j + 1; ; {
			e := indexFold(s[k:], "</ref")
			if e < 0 {
				break
			}
			e += k + 5
			m := e
			for m < len(s) && isSpaceRe(s[m]) {
				m++
			}
			if m < len(s) && s[m] == '>' {
				return m + 1
			}
			k = e - 4
		}
	}
	// <ref[^>]*/>
	if g := strings.IndexByte(s[4:], '>'); g >= 0 {
		g += 4
		if s[g-1] == '/' {
			return g + 1
		}
	}
	return -1
}

// isSpaceRe is \s of Go's regexp: [\t\n\f\r ].
func isSpaceRe(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// dropRefs is refBlockRe.ReplaceAllString(s, " ").
func dropRefs(s string) string {
	i := indexFold(s, "<ref")
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i >= 0 {
		n := refMatch(s[i:])
		if n < 0 {
			b.WriteString(s[:i+1])
			s = s[i+1:]
		} else {
			b.WriteString(s[:i])
			b.WriteByte(' ')
			s = s[i+n:]
		}
		i = indexFold(s, "<ref")
	}
	b.WriteString(s)
	return b.String()
}

// dropBareURLs is bareURLText.ReplaceAllString(s, " "):
// (?:https?|ftp)://\S+
func dropBareURLs(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for i := 0; i < len(s); i++ {
		var n int
		switch {
		case strings.HasPrefix(s[i:], "http://"):
			n = 7
		case strings.HasPrefix(s[i:], "https://"):
			n = 8
		case strings.HasPrefix(s[i:], "ftp://"):
			n = 6
		default:
			continue
		}
		j := i + n
		if j >= len(s) || isSpaceRe(s[j]) {
			continue // \S+ needs a character
		}
		for j < len(s) && !isSpaceRe(s[j]) {
			j++
		}
		b.WriteString(s[last:i])
		b.WriteByte(' ')
		last = j
		i = j - 1
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// dropTableMarks is tableMarkRe.ReplaceAllString(s, " "):
// (?m)^\s*(\{\|.*$|\|\}|\|-.*$|\|\+)
// At a line start, after spaces (which may run over blank lines), "{|" or
// "|-" goes with the rest of its line, "|}" or "|+" alone; the spaces go
// too.
func dropTableMarks(s string) string {
	if !strings.ContainsAny(s, "{|") {
		return s
	}
	var b strings.Builder
	last := 0
	for p := 0; p < len(s); {
		j := p
		for j < len(s) && isSpaceRe(s[j]) {
			j++
		}
		end := -1
		if j+1 < len(s) {
			switch {
			case s[j] == '{' && s[j+1] == '|', s[j] == '|' && s[j+1] == '-':
				end = j + 2
				for end < len(s) && s[end] != '\n' {
					end++
				}
			case s[j] == '|' && (s[j+1] == '}' || s[j+1] == '+'):
				end = j + 2
			}
		}
		from := p
		if end >= 0 {
			if last == 0 {
				b.Grow(len(s))
			}
			b.WriteString(s[last:p])
			b.WriteByte(' ')
			last = end
			from = end
		}
		// the next line start
		nl := strings.IndexByte(s[from:], '\n')
		if nl < 0 {
			break
		}
		p = from + nl + 1
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// dropCellAttrs is cellAttrRe.ReplaceAllString(s, "$1 "):
// (?m)(^|\|\||!!|^[|!])\s*[a-z-]+="[^"]*"[^|\n]*\|
func dropCellAttrs(s string) string {
	if !strings.Contains(s, `="`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	// attr matches \s*[a-z-]+="[^"]*"[^|\n]*\| at s[j:], returning its end.
	attr := func(j int) int {
		for j < len(s) && isSpaceRe(s[j]) {
			j++
		}
		k := j
		for k < len(s) && (s[k] >= 'a' && s[k] <= 'z' || s[k] == '-') {
			k++
		}
		if k == j || k+1 >= len(s) || s[k] != '=' || s[k+1] != '"' {
			return -1
		}
		q := strings.IndexByte(s[k+2:], '"')
		if q < 0 {
			return -1
		}
		m := k + 2 + q + 1
		for m < len(s) && s[m] != '|' && s[m] != '\n' {
			m++
		}
		if m < len(s) && s[m] == '|' {
			return m + 1
		}
		return -1
	}
	for i := 0; i < len(s); i++ {
		lineStart := i == 0 || s[i-1] == '\n'
		pre := -1 // length of $1
		end := -1
		if lineStart {
			if e := attr(i); e >= 0 {
				pre, end = 0, e
			}
		}
		if end < 0 && i+1 < len(s) && (s[i] == '|' && s[i+1] == '|' || s[i] == '!' && s[i+1] == '!') {
			if e := attr(i + 2); e >= 0 {
				pre, end = 2, e
			}
		}
		if end < 0 && lineStart && (s[i] == '|' || s[i] == '!') {
			if e := attr(i + 1); e >= 0 {
				pre, end = 1, e
			}
		}
		if end < 0 {
			continue
		}
		b.WriteString(s[last : i+pre])
		b.WriteByte(' ')
		last = end
		i = end - 1
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// dropHeadingMarks is headEqRe.ReplaceAllString(s, " "):
// (?m)^=+|=+\s*$
func dropHeadingMarks(s string) string {
	if !strings.Contains(s, "=") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '=' {
			continue
		}
		j := i
		for j < len(s) && s[j] == '=' {
			j++
		}
		if i == 0 || s[i-1] == '\n' {
			// ^=+
			b.WriteString(s[last:i])
			b.WriteByte(' ')
			last = j
			i = j - 1
			continue
		}
		// =+\s*$ : spaces (newlines too), then the end of a line; like
		// the regexp, take as many spaces as still end at a line's end.
		k := j
		for k < len(s) && isSpaceRe(s[k]) {
			k++
		}
		for k > j && !(k == len(s) || s[k] == '\n') {
			k--
		}
		if k == len(s) || s[k] == '\n' {
			b.WriteString(s[last:i])
			b.WriteByte(' ')
			last = k
			i = k - 1
			continue
		}
		i = j - 1
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}
