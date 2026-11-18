// Package wikitext turns MediaWiki markup into HTML, offline.
//
// Wikipedia pages are written in wikitext and lean heavily on templates
// whose real definitions are Lua programs on Wikimedia's servers. A dump
// has the markup but not that machinery, so this package renders the
// language itself faithfully (headings, links, lists, tables, quotes,
// references, images) and gives the templates that matter to a reader
// (infoboxes, citations, hatnotes, dates, conversions, quotations...)
// their own small implementations. Maintenance and navigation templates
// are dropped. Everything is escaped and sanitised: only a whitelist of
// HTML survives.
package wikitext

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Context is what the renderer needs to know about the page and wiki.
type Context struct {
	Title    string
	Site     *dump.Siteinfo
	PageURL  func(title string) string           // URL of another page on this wiki
	ImageURL func(file string, width int) string // "" to show no pictures
	Now      time.Time
}

// Heading is an entry of the table of contents.
type Heading struct {
	Level  int
	Text   string
	Anchor string
}

// Output is a rendered page.
type Output struct {
	HTML         string
	Categories   []string
	ShortDesc    string
	Headings     []Heading
	DisplayTitle string
}

// Render converts a page's wikitext into HTML.
func Render(src string, ctx *Context) *Output {
	if ctx.Now.IsZero() {
		ctx.Now = time.Now()
	}
	r := &renderer{ctx: ctx, out: &Output{}, refGroups: map[string]*refGroup{}, anchors: map[string]int{}}
	body := r.blocks(r.prepare(src))
	// References nobody placed go at the end, as MediaWiki does.
	for _, name := range r.refOrder {
		g := r.refGroups[name]
		if !g.placed && len(g.refs) > 0 {
			body += r.resolve(r.refListPH(name, true))
		}
	}
	html := r.resolve(body)
	if len(r.out.Headings) >= 4 && !r.noTOC {
		html = strings.Replace(html, tocMarker, r.toc(), 1)
	}
	html = strings.Replace(html, tocMarker, "", -1)
	r.out.HTML = balance(html)
	return r.out
}

const tocMarker = "\x01toc\x02"

type renderer struct {
	ctx     *Context
	out     *Output
	ph      []placeholder
	depth   int
	noTOC   bool
	tocDone bool
	anchors map[string]int

	refGroups map[string]*refGroup
	refOrder  []string
}

type placeholder struct {
	html  string
	block bool
	lazy  func() string
}

// Placeholders keep finished HTML out of the way of later passes. They
// are written \x01<kind><n>\x02; \x01 and \x02 cannot occur in XML text.
func (r *renderer) inlinePH(html string) string {
	r.ph = append(r.ph, placeholder{html: html})
	return "\x01i" + strconv.Itoa(len(r.ph)-1) + "\x02"
}

func (r *renderer) blockPH(html string) string {
	r.ph = append(r.ph, placeholder{html: html, block: true})
	return "\x01b" + strconv.Itoa(len(r.ph)-1) + "\x02"
}

func (r *renderer) lazyBlockPH(f func() string) string {
	r.ph = append(r.ph, placeholder{block: true, lazy: f})
	return "\x01b" + strconv.Itoa(len(r.ph)-1) + "\x02"
}

var phRe = regexp.MustCompile("\x01([ib])([0-9]+)\x02")

// resolve substitutes placeholders, recursively.
func (r *renderer) resolve(s string) string {
	for i := 0; i < 8 && strings.IndexByte(s, '\x01') >= 0; i++ {
		s = phRe.ReplaceAllStringFunc(s, func(m string) string {
			n, _ := strconv.Atoi(m[2 : len(m)-1])
			if n >= len(r.ph) {
				return ""
			}
			p := &r.ph[n]
			if p.lazy != nil {
				p.html, p.lazy = p.lazy(), nil
			}
			return p.html
		})
	}
	return s
}

var (
	commentRe   = regexp.MustCompile(`(?s)<!--.*?(-->|$)`)
	magicWordRe = regexp.MustCompile(`__[A-ZÀ-ÿ_]+__`)
	paramRe     = regexp.MustCompile(`(?s)\{\{\{([^{}|]*)(\|([^{}]*))?\}\}\}`)
)

// prepare runs the passes that work on raw source: comments, special
// tags, references and templates.
func (r *renderer) prepare(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = commentRe.ReplaceAllString(src, "")
	if strings.Contains(src, "__NOTOC__") {
		r.noTOC = true
	}
	src = magicWordRe.ReplaceAllString(src, "")
	src = paramRe.ReplaceAllString(src, "$3")
	src = r.extractTags(src)
	return r.expand(src)
}

// fragment renders a piece of markup (a template parameter, a table
// cell): block level when it spans lines, inline otherwise.
func (r *renderer) fragment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = r.expand(s)
	if strings.Contains(s, "\n") || startsBlock(s) {
		h := r.blocks(s)
		if strings.HasPrefix(h, "<p>") && strings.HasSuffix(h, "</p>\n") && strings.Count(h, "<p>") == 1 {
			h = h[3 : len(h)-5]
		}
		return h
	}
	return r.inline(s)
}

func startsBlock(s string) bool {
	return len(s) > 0 && strings.ContainsRune("*#:;", rune(s[0])) || strings.HasPrefix(s, "{|")
}

// ---------------------------------------------------------------------
// Block level

var headingRe = regexp.MustCompile(`^(={1,6})\s*(.*?)\s*(={1,6})\s*$`)
var blockTagRe = regexp.MustCompile(`(?i)^\s*</?(div|table|blockquote|center|h[1-6]|p|ul|ol|dl|li|tr|td|th|hr|pre|caption)\b`)

func (r *renderer) blocks(text string) string {
	if strings.Contains(text, "{{") {
		text = r.expand(text)
	}
	if strings.Contains(text, "[[") {
		text = r.multilineFiles(text)
	}
	lines := strings.Split(text, "\n")
	var b strings.Builder
	var para []string
	var pre []string
	var list []byte // open list types, e.g. "*#"

	flushPara := func() {
		if len(para) > 0 {
			h := strings.TrimSpace(r.inline(strings.Join(para, "\n")))
			if h != "" {
				b.WriteString("<p>" + h + "</p>\n")
			}
			para = nil
		}
	}
	flushPre := func() {
		if len(pre) > 0 {
			b.WriteString("<pre>" + r.inline(strings.Join(pre, "\n")) + "</pre>\n")
			pre = nil
		}
	}
	closeList := func(to int) {
		for len(list) > to {
			c := list[len(list)-1]
			b.WriteString(itemClose(c) + listClose(c))
			list = list[:len(list)-1]
		}
	}
	flushAll := func() { flushPara(); flushPre(); closeList(0) }

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "{|"):
			flushAll()
			depth, j := 0, i
			for ; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if strings.HasPrefix(t, "{|") {
					depth++
				} else if strings.HasPrefix(t, "|}") {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if j >= len(lines) {
				j = len(lines) - 1
			}
			b.WriteString(r.table(lines[i : j+1]))
			i = j
			continue

		case headingRe.MatchString(line) && strings.HasPrefix(line, "="):
			flushAll()
			m := headingRe.FindStringSubmatch(line)
			lvl := len(m[1])
			if len(m[3]) < lvl {
				lvl = len(m[3])
			}
			textPart := m[2]
			if len(m[1]) > lvl {
				textPart = strings.Repeat("=", len(m[1])-lvl) + textPart
			}
			if len(m[3]) > lvl {
				textPart += strings.Repeat("=", len(m[3])-lvl)
			}
			h := r.inline(textPart)
			plain := stripTags(r.resolve(h))
			anchor := r.uniqueAnchor(Anchor(plain))
			if !r.tocDone && lvl >= 2 {
				b.WriteString(tocMarker)
				r.tocDone = true
			}
			r.out.Headings = append(r.out.Headings, Heading{Level: lvl, Text: plain, Anchor: anchor})
			tag := "h" + strconv.Itoa(lvl)
			b.WriteString("<" + tag + ` id="` + escAttr(anchor) + `">` + h + "</" + tag + ">\n")
			continue

		case strings.HasPrefix(trimmed, "----"):
			flushAll()
			b.WriteString("<hr>\n")
			if rest := strings.TrimLeft(trimmed, "-"); rest != "" {
				para = append(para, rest)
			}
			continue

		case len(line) > 0 && strings.ContainsRune("*#:;", rune(line[0])):
			flushPara()
			flushPre()
			n := 0
			for n < len(line) && strings.ContainsRune("*#:;", rune(line[n])) {
				n++
			}
			prefix := []byte(line[:n])
			content := strings.TrimSpace(line[n:])
			common := 0
			for common < len(list) && common < len(prefix) && sameList(list[common], prefix[common]) {
				common++
			}
			closeList(common)
			if common == len(prefix) && common > 0 {
				// sibling item at the same depth
				c := list[len(list)-1]
				b.WriteString(itemClose(c))
				list[len(list)-1] = prefix[len(prefix)-1]
				b.WriteString(itemOpen(prefix[len(prefix)-1]))
			} else {
				for k := common; k < len(prefix); k++ {
					b.WriteString(listOpen(prefix[k]) + itemOpen(prefix[k]))
					list = append(list, prefix[k])
				}
			}
			last := prefix[len(prefix)-1]
			if last == ';' {
				if k := defSplit(content); k >= 0 {
					b.WriteString(r.inline(strings.TrimSpace(content[:k])) + "</dt><dd>" + r.inline(strings.TrimSpace(content[k+1:])))
					list[len(list)-1] = ':'
					continue
				}
			}
			b.WriteString(r.inline(content))
			continue

		case trimmed == "":
			flushAll()
			continue

		case line[0] == ' ' && !isBlockOnly(trimmed) && !blockTagRe.MatchString(line):
			flushPara()
			closeList(0)
			pre = append(pre, line[1:])
			continue

		case isBlockOnly(trimmed) || blockTagRe.MatchString(line):
			flushAll()
			b.WriteString(r.inline(trimmed) + "\n")
			continue
		}
		flushPre()
		closeList(0)
		para = append(para, line)
	}
	flushAll()
	return b.String()
}

// multilineFiles renders [[File:...]] links whose captions run over
// several lines (even blank ones) before the text is split into
// paragraphs, as MediaWiki does.
func (r *renderer) multilineFiles(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "[[")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := matchBracketsML(s, i)
		if end < 0 || !strings.Contains(s[i:end], "\n") {
			b.WriteString(s[:i+2])
			s = s[i+2:]
			continue
		}
		inner := s[i+2 : end]
		target := strings.TrimSpace(strings.SplitN(inner, "|", 2)[0])
		if c := strings.IndexByte(target, ':'); c > 0 {
			if ns, ok := r.ctx.Site.NamespaceKey(target[:c]); ok && ns == 6 {
				b.WriteString(s[:i])
				out, _ := r.internalLink(inner, "")
				b.WriteString(out)
				s = s[end+2:]
				continue
			}
		}
		b.WriteString(s[:i+2])
		s = s[i+2:]
	}
}

// matchBracketsML is matchBrackets allowing newlines anywhere.
func matchBracketsML(s string, i int) int {
	depth := 0
	for j := i; j < len(s)-1; j++ {
		if s[j] == '[' && s[j+1] == '[' {
			depth++
			j++
		} else if s[j] == ']' && s[j+1] == ']' {
			depth--
			if depth == 0 {
				return j
			}
			j++
		}
	}
	return -1
}

// isBlockOnly: the line holds nothing but block placeholders.
func isBlockOnly(s string) bool {
	if !strings.Contains(s, "\x01b") {
		return false
	}
	rest := phRe.ReplaceAllStringFunc(s, func(m string) string {
		if m[1] == 'b' {
			return ""
		}
		return m
	})
	return strings.TrimSpace(rest) == ""
}

func sameList(a, b byte) bool {
	return a == b || (a == ';' || a == ':') && (b == ';' || b == ':')
}

func listOpen(c byte) string {
	switch c {
	case '*':
		return "<ul>"
	case '#':
		return "<ol>"
	}
	return "<dl>"
}

func listClose(c byte) string {
	switch c {
	case '*':
		return "</ul>\n"
	case '#':
		return "</ol>\n"
	}
	return "</dl>\n"
}

func itemOpen(c byte) string {
	switch c {
	case ';':
		return "<dt>"
	case ':':
		return "<dd>"
	}
	return "<li>"
}

func itemClose(c byte) string {
	switch c {
	case ';':
		return "</dt>"
	case ':':
		return "</dd>"
	}
	return "</li>"
}

// defSplit finds the ':' separating "; term : definition", skipping
// colons inside links and URLs.
func defSplit(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "[["):
			depth++
			i++
		case strings.HasPrefix(s[i:], "]]"):
			depth--
			i++
		case s[i] == ':' && depth == 0 && !strings.HasPrefix(s[i:], "://"):
			return i
		}
	}
	return -1
}

func (r *renderer) uniqueAnchor(a string) string {
	if a == "" {
		a = "section"
	}
	r.anchors[a]++
	if n := r.anchors[a]; n > 1 {
		return a + "_" + strconv.Itoa(n)
	}
	return a
}

func (r *renderer) toc() string {
	return `<nav class="toc" aria-label="Contents"><details open><summary>Contents</summary>` + TOCList(r.out.Headings) + "</details></nav>"
}

// TOCList renders headings as a nested, numbered list of links.
func TOCList(hs []Heading) string {
	var b strings.Builder
	b.WriteString("<ol>")
	min := 6
	for _, h := range hs {
		if h.Level < min {
			min = h.Level
		}
	}
	level, open := min, false
	for _, h := range hs {
		l := h.Level
		if l > level+1 && open {
			l = level + 1 // never skip a level ("==" then "====")
		}
		if l > level {
			for level < l {
				b.WriteString("<ol>")
				level++
			}
		} else {
			if open {
				b.WriteString("</li>")
			}
			for level > l {
				b.WriteString("</ol></li>")
				level--
			}
		}
		b.WriteString(`<li><a href="#` + escAttr(h.Anchor) + `">` + escText(h.Text) + "</a>")
		open = true
	}
	if open {
		b.WriteString("</li>")
	}
	for level > min {
		b.WriteString("</ol></li>")
		level--
	}
	b.WriteString("</ol>")
	return b.String()
}

// ---------------------------------------------------------------------
// Inline level

func (r *renderer) inline(s string) string {
	if s == "" {
		return ""
	}
	r.depth++
	defer func() { r.depth-- }()
	if r.depth > 40 {
		return escText(s)
	}
	if strings.Contains(s, "{{") {
		s = r.expand(s)
	}
	s = r.links(s)
	s = r.escapeAndTags(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = doQuotes(l)
	}
	return strings.Join(lines, "\n")
}

var (
	extLinkRe = regexp.MustCompile(`^\[((?:https?:|ftp:|mailto:|//)[^\s\]\x01\x02<>"]+)(?:[ \t]+([^\]\n]*))?\]`)
	bareURLRe = regexp.MustCompile(`(?:https?|ftp)://[^\s<>\[\]"\x01\x02{}|]+`)
)

// links replaces [[internal]] and [external] links with placeholders.
func (r *renderer) links(s string) string {
	if !strings.ContainsRune(s, '[') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "[[") {
			end := matchBrackets(s, i)
			if end > 0 {
				inner := s[i+2 : end]
				j := end + 2
				// Link trail: "[[Babylonia]]n" -> "Babylonian".
				k := j
				for k < len(s) && (s[k] >= 'a' && s[k] <= 'z') {
					k++
				}
				out, usedTrail := r.internalLink(inner, s[j:k])
				b.WriteString(out)
				if usedTrail {
					i = k
				} else {
					i = j
				}
				continue
			}
		}
		if s[i] == '[' {
			if m := extLinkRe.FindStringSubmatch(s[i:]); m != nil {
				b.WriteString(r.externalLink(m[1], m[2]))
				i += len(m[0])
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// matchBrackets returns the index of the "]]" closing the "[[" at i.
func matchBrackets(s string, i int) int {
	depth := 0
	for j := i; j < len(s)-1; j++ {
		switch {
		case s[j] == '[' && s[j+1] == '[':
			depth++
			j++
		case s[j] == ']' && s[j+1] == ']':
			depth--
			if depth == 0 {
				return j
			}
			j++
		case s[j] == '\n' && depth == 1 && j > i+2 && !strings.Contains(s[i:j], "|"):
			return -1 // links do not span lines (captions may)
		}
	}
	return -1
}

func (r *renderer) externalLink(url, label string) string {
	href := url
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	var text string
	if strings.TrimSpace(label) == "" {
		text = "[↗]"
	} else {
		text = r.inline(label)
	}
	return r.inlinePH(`<a class="external" rel="nofollow noopener" href="` + escAttr(href) + `">` + text + "</a>")
}

// escapeAndTags escapes text, keeps whitelisted HTML (sanitised) and
// valid entities, and turns bare URLs into links.
func (r *renderer) escapeAndTags(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/8)
	text := func(t string) {
		for {
			loc := bareURLRe.FindStringIndex(t)
			if loc == nil {
				b.WriteString(escText(t))
				return
			}
			b.WriteString(escText(t[:loc[0]]))
			u := strings.TrimRight(t[loc[0]:loc[1]], ".,;:!?)'")
			b.WriteString(`<a class="external" rel="nofollow noopener" href="` + escAttr(u) + `">` + escText(u) + "</a>")
			t = t[loc[0]+len(u):]
		}
	}
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			if m := tagRe.FindStringSubmatch(s[i:]); m != nil {
				name := strings.ToLower(m[2])
				if allowedTags[name] {
					text(s[start:i])
					if m[1] == "/" {
						b.WriteString("</" + name + ">")
					} else if voidTags[name] {
						b.WriteString("<" + name + ">")
					} else {
						b.WriteString("<" + name + sanitizeAttrs(m[3]) + ">")
					}
					i += len(m[0]) - 1
					start = i + 1
				}
			}
		case '&':
			if m := entityRe.FindString(s[i:]); m != "" {
				text(s[start:i])
				b.WriteString(m)
				i += len(m) - 1
				start = i + 1
			}
		}
	}
	text(s[start:])
	return b.String()
}

var quoteRe = regexp.MustCompile(`''+`)

// doQuotes implements MediaWiki's ”italic” / ”'bold”' rules.
func doQuotes(line string) string {
	if !strings.Contains(line, "''") {
		return line
	}
	idx := quoteRe.FindAllStringIndex(line, -1)
	type tok struct {
		text string
		q    int
	}
	var toks []tok
	last := 0
	for _, p := range idx {
		t := line[last:p[0]]
		n := p[1] - p[0]
		switch {
		case n == 4:
			t += "'"
			n = 3
		case n > 5:
			t += strings.Repeat("'", n-5)
			n = 5
		}
		toks = append(toks, tok{text: t}, tok{q: n})
		last = p[1]
	}
	toks = append(toks, tok{text: line[last:]})
	nI, nB := 0, 0
	for _, t := range toks {
		switch t.q {
		case 2:
			nI++
		case 3:
			nB++
		case 5:
			nI++
			nB++
		}
	}
	if nI%2 == 1 && nB%2 == 1 {
		// Treat one bold as an apostrophe + italic, preferring one
		// after a single-letter word ("l'''amour").
		pick := -1
		for i, t := range toks {
			if t.q != 3 || i == 0 {
				continue
			}
			prev := toks[i-1].text
			if len(prev) >= 2 && prev[len(prev)-1] != ' ' && prev[len(prev)-2] == ' ' {
				pick = i
				break
			}
			if pick < 0 && len(prev) > 0 && prev[len(prev)-1] != ' ' {
				pick = i
			}
		}
		if pick < 0 {
			for i, t := range toks {
				if t.q == 3 {
					pick = i
					break
				}
			}
		}
		if pick > 0 {
			toks[pick-1].text += "'"
			toks[pick].q = 2
		}
	}
	var out strings.Builder
	state, buffer := "", ""
	for _, t := range toks {
		if t.q == 0 {
			if state == "both" {
				buffer += t.text
			} else {
				out.WriteString(t.text)
			}
			continue
		}
		switch t.q {
		case 2:
			switch state {
			case "i":
				out.WriteString("</i>")
				state = ""
			case "bi":
				out.WriteString("</i>")
				state = "b"
			case "ib":
				out.WriteString("</b></i><b>")
				state = "b"
			case "both":
				out.WriteString("<b><i>" + buffer + "</i>")
				state = "b"
			default:
				out.WriteString("<i>")
				state += "i"
			}
		case 3:
			switch state {
			case "b":
				out.WriteString("</b>")
				state = ""
			case "bi":
				out.WriteString("</i></b><i>")
				state = "i"
			case "ib":
				out.WriteString("</b>")
				state = "i"
			case "both":
				out.WriteString("<i><b>" + buffer + "</b>")
				state = "i"
			default:
				out.WriteString("<b>")
				state += "b"
			}
		case 5:
			switch state {
			case "b":
				out.WriteString("</b><i>")
				state = "i"
			case "i":
				out.WriteString("</i><b>")
				state = "b"
			case "bi":
				out.WriteString("</i></b>")
				state = ""
			case "ib":
				out.WriteString("</b></i>")
				state = ""
			case "both":
				out.WriteString("<i><b>" + buffer + "</b></i>")
				state = ""
			default:
				buffer = ""
				state = "both"
			}
		}
	}
	if state == "b" || state == "ib" {
		out.WriteString("</b>")
	}
	if state == "i" || state == "bi" || state == "ib" {
		out.WriteString("</i>")
	}
	if state == "bi" {
		out.WriteString("</b>")
	}
	if state == "both" && buffer != "" {
		out.WriteString("<b><i>" + buffer + "</i></b>")
	}
	return out.String()
}
