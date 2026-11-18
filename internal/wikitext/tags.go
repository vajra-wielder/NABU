package wikitext

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// tagSpanRe finds the next extension tag: <name attrs>...</name> or
// <name attrs/>.
var tagStartRe = regexp.MustCompile(`(?i)<(nowiki|pre|math|chem|ce|syntaxhighlight|source|ref|references|gallery|poem|templatestyles|indicator|timeline|graph|mapframe|maplink|score|imagemap|categorytree|section|includeonly|noinclude|onlyinclude|hiero|inputbox|charinsert|templatedata|langconvert|phonos|rss|code)(\s[^>]*?)?(/?)>`)

// extractTags replaces extension tags with placeholders (or removes
// them) before templates are expanded, so their contents are not
// mistaken for markup.
func (r *renderer) extractTags(s string) string {
	var b strings.Builder
	for {
		loc := tagStartRe.FindStringSubmatchIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:loc[0]])
		name := strings.ToLower(s[loc[2]:loc[3]])
		attrs := ""
		if loc[4] >= 0 {
			attrs = s[loc[4]:loc[5]]
		}
		selfClosing := loc[7] > loc[6]
		rest := s[loc[1]:]
		content := ""
		if !selfClosing {
			closeRe := closeTagRe(name)
			if c := closeRe.FindStringIndex(rest); c != nil {
				content = rest[:c[0]]
				rest = rest[c[1]:]
			} else if name == "noinclude" || name == "onlyinclude" || name == "includeonly" {
				// unclosed wrapper tags: just drop the tag
			} else {
				content = rest
				rest = ""
			}
		}
		b.WriteString(r.tagOutput(name, attrs, content, selfClosing))
		s = rest
	}
}

func (r *renderer) tagOutput(name, attrs, content string, self bool) string {
	switch name {
	case "nowiki":
		return r.inlinePH(escText(html.UnescapeString(content)))
	case "code":
		// <code> may contain wikitext; render it but keep the tag.
		return r.inlinePH("<code>" + r.inline(r.expand(r.extractTags(content))) + "</code>")
	case "pre":
		return r.blockPH("<pre>" + escText(content) + "</pre>")
	case "syntaxhighlight", "source":
		if strings.Contains(attrs, "inline") {
			return r.inlinePH("<code>" + escText(content) + "</code>")
		}
		return r.blockPH(`<pre class="code">` + escText(strings.Trim(content, "\n")) + "</pre>")
	case "math", "chem", "ce":
		tex := strings.TrimSpace(content)
		cls := "math"
		if strings.Contains(attrs, "block") {
			return r.blockPH(`<div class="math-block"><code class="` + cls + `">` + escText(tex) + "</code></div>")
		}
		return r.inlinePH(`<code class="` + cls + `" title="TeX">` + escText(tex) + "</code>")
	case "hiero":
		return r.inlinePH(`<code class="hiero">` + escText(content) + "</code>")
	case "ref":
		return r.ref(attrs, content, self)
	case "references":
		group := attrValue(attrs, "group")
		if content != "" {
			// list-defined references: register their text
			r.extractTags(content)
		}
		return r.refListPH(group, false)
	case "gallery":
		return r.gallery(content)
	case "poem":
		inner := r.expand(r.extractTags(content))
		lines := strings.Split(strings.Trim(inner, "\n"), "\n")
		for i, l := range lines {
			lines[i] = r.inline(l)
		}
		return r.blockPH(`<div class="poem"><p>` + strings.Join(lines, "<br>\n") + "</p></div>")
	case "noinclude", "onlyinclude":
		return r.extractTags(content)
	case "imagemap":
		// First line is the picture; the clickable areas need the server.
		first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(content), "\n", 2)[0])
		return "[[" + first + "]]"
	}
	// includeonly, templatestyles, indicator, timeline, graph, maps,
	// score, categorytree, section, inputbox, ...: nothing to show offline.
	return ""
}

var attrValRe = regexp.MustCompile(`(?i)([a-z-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'/>]+))`)

func attrValue(attrs, key string) string {
	for _, m := range attrValRe.FindAllStringSubmatch(attrs, -1) {
		if strings.EqualFold(m[1], key) {
			return strings.TrimSpace(m[2] + m[3] + m[4])
		}
	}
	return ""
}

// ---------------------------------------------------------------------
// References

type ref struct {
	n    int
	name string
	html string
	uses int
}

type refGroup struct {
	name   string
	refs   []*ref
	byName map[string]*ref
	placed bool
}

func (r *renderer) group(name string) *refGroup {
	g := r.refGroups[name]
	if g == nil {
		g = &refGroup{name: name, byName: map[string]*ref{}}
		r.refGroups[name] = g
		r.refOrder = append(r.refOrder, name)
	}
	return g
}

func (r *renderer) ref(attrs, content string, self bool) string {
	return r.addRef(attrValue(attrs, "group"), attrValue(attrs, "name"), content, self)
}

// addRef records a footnote and returns its superscript marker.
func (r *renderer) addRef(groupName, name, content string, reuse bool) string {
	g := r.group(groupName)
	var rf *ref
	if name != "" {
		rf = g.byName[name]
	}
	content = strings.TrimSpace(content)
	if rf == nil {
		rf = &ref{n: len(g.refs) + 1, name: name}
		g.refs = append(g.refs, rf)
		if name != "" {
			g.byName[name] = rf
		}
	}
	if rf.html == "" && content != "" {
		rf.html = r.fragment(r.extractTags(content))
	}
	rf.uses++
	label := refLabel(groupName, rf.n)
	id := "cite_note-" + Anchor(groupName) + strconv.Itoa(rf.n)
	return r.inlinePH(`<sup class="reference" id="cite_ref-` + escAttr(Anchor(groupName)) + strconv.Itoa(rf.n) + "-" + strconv.Itoa(rf.uses) +
		`"><a href="#` + escAttr(id) + `">[` + escText(label) + `]</a></sup>`)
}

func refLabel(group string, n int) string {
	switch group {
	case "lower-alpha", "note", "nb", "n":
		return alpha(n, 'a')
	case "upper-alpha":
		return alpha(n, 'A')
	case "lower-roman":
		return strings.ToLower(roman(n))
	case "upper-roman":
		return roman(n)
	case "":
		return strconv.Itoa(n)
	}
	return group + " " + strconv.Itoa(n)
}

func alpha(n int, base byte) string {
	s := ""
	for n > 0 {
		n--
		s = string(rune(base+byte(n%26))) + s
		n /= 26
	}
	return s
}

func roman(n int) string {
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	s := ""
	for i, v := range vals {
		for n >= v {
			s += syms[i]
			n -= v
		}
	}
	return s
}

// refListPH places a group's reference list; it is built at the end so
// it includes references used after it.
func (r *renderer) refListPH(group string, withHeading bool) string {
	g := r.group(group)
	g.placed = true
	return r.lazyBlockPH(func() string {
		if len(g.refs) == 0 {
			return ""
		}
		var b strings.Builder
		if withHeading {
			b.WriteString(`<h2 id="nabu-references">` + "References" + "</h2>\n")
		}
		b.WriteString(`<ol class="references">`)
		for _, rf := range g.refs {
			id := "cite_note-" + Anchor(group) + strconv.Itoa(rf.n)
			back := "#cite_ref-" + Anchor(group) + strconv.Itoa(rf.n) + "-1"
			b.WriteString(`<li id="` + escAttr(id) + `" value="` + strconv.Itoa(rf.n) + `"><a class="ref-back" href="` + escAttr(back) + `" aria-label="Back">^</a> `)
			if group != "" && group != "lower-alpha" && group != "upper-alpha" {
				b.WriteString("<b>" + escText(refLabel(group, rf.n)) + "</b> ")
			}
			b.WriteString(`<span class="ref-text">` + r.resolve(rf.html) + "</span></li>")
		}
		b.WriteString("</ol>\n")
		return b.String()
	})
}

// gallery renders <gallery> lines ("File:X.jpg|caption") as a grid.
func (r *renderer) gallery(content string) string {
	var items []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := splitTop(line, '|')
		name := strings.TrimSpace(parts[0])
		if ns, rest := r.ctx.Site.SplitTitle(name); ns == 6 {
			name = rest
		}
		caption := ""
		for _, p := range parts[1:] {
			if !isImageOption(strings.TrimSpace(p)) {
				caption = p
			}
		}
		items = append(items, `<figure class="gallery-item">`+r.picture(name, 180, caption)+
			`<figcaption>`+r.inline(r.expand(caption))+`</figcaption></figure>`)
	}
	if len(items) == 0 {
		return ""
	}
	return r.blockPH(`<div class="gallery">` + strings.Join(items, "") + "</div>")
}

// closeRes is filled once at start-up and only read afterwards, so
// concurrent renders can share it.
var closeRes = func() map[string]*regexp.Regexp {
	m := map[string]*regexp.Regexp{}
	for _, n := range strings.Split("nowiki pre math chem ce syntaxhighlight source ref references gallery poem templatestyles indicator timeline graph mapframe maplink score imagemap categorytree section includeonly noinclude onlyinclude hiero inputbox charinsert templatedata langconvert phonos rss code", " ") {
		m[n] = regexp.MustCompile(`(?i)</` + n + `\s*>`)
	}
	return m
}()

func closeTagRe(name string) *regexp.Regexp { return closeRes[name] }
