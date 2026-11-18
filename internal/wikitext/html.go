package wikitext

import (
	"html"
	"regexp"
	"strings"
)

// allowedTags are the HTML tags wikitext may use directly. Anything else
// is shown as text, exactly as MediaWiki does.
var allowedTags = map[string]bool{
	"b": true, "i": true, "u": true, "s": true, "strike": true, "del": true, "ins": true,
	"sub": true, "sup": true, "small": true, "big": true, "br": true, "span": true,
	"div": true, "p": true, "code": true, "tt": true, "kbd": true, "var": true, "samp": true,
	"blockquote": true, "center": true, "cite": true, "q": true, "abbr": true, "dfn": true,
	"em": true, "strong": true, "font": true, "hr": true, "ul": true, "ol": true, "li": true,
	"dl": true, "dt": true, "dd": true, "table": true, "tr": true, "td": true, "th": true,
	"caption": true, "tbody": true, "thead": true, "tfoot": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "ruby": true, "rt": true, "rp": true,
	"rb": true, "bdi": true, "bdo": true, "wbr": true, "mark": true, "time": true, "data": true,
}

var voidTags = map[string]bool{"br": true, "hr": true, "wbr": true}

var allowedAttrs = map[string]bool{
	"class": true, "style": true, "id": true, "title": true, "lang": true, "dir": true,
	"colspan": true, "rowspan": true, "align": true, "valign": true, "width": true,
	"height": true, "bgcolor": true, "scope": true, "border": true, "cellpadding": true,
	"cellspacing": true, "color": true, "size": true, "face": true, "datetime": true,
	"value": true, "start": true, "type": true, "reversed": true, "abbr": true, "headers": true,
	"nowrap": true, "clear": true,
}

var attrRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_:-]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?`)

// sanitizeAttrs keeps safe attributes and re-quotes them.
func sanitizeAttrs(s string) string {
	var b strings.Builder
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		name := strings.ToLower(m[1])
		if !allowedAttrs[name] {
			continue
		}
		val := m[2] + m[3] + m[4]
		val = html.UnescapeString(val)
		if name == "style" {
			val = sanitizeStyle(val)
			if val == "" {
				continue
			}
		}
		if name == "id" {
			val = "w-" + val // never collide with NABU's own ids
		}
		b.WriteByte(' ')
		b.WriteString(name)
		b.WriteString(`="`)
		b.WriteString(escAttr(val))
		b.WriteByte('"')
	}
	return b.String()
}

var badStyle = regexp.MustCompile(`(?i)(expression|javascript:|url\s*\(|@import|behavior|-moz-binding|position\s*:\s*(fixed|absolute|sticky))`)

func sanitizeStyle(s string) string {
	if badStyle.MatchString(s) {
		return ""
	}
	return strings.TrimSpace(s)
}

func escAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "'", "&#39;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// escText escapes text for HTML but leaves apostrophes (for ”'bold”')
// and NABU's placeholder markers alone.
func escText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

var tagRe = regexp.MustCompile(`^<(/?)([A-Za-z][A-Za-z0-9]*)((?:\s+[^<>]*?)?)\s*(/?)>`)
var entityRe = regexp.MustCompile(`^&(#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[A-Za-z][A-Za-z0-9]{1,31});`)

// balance repairs the nesting of the final HTML: stray closing tags are
// dropped and unclosed ones are closed, so a broken template can never
// swallow the rest of the page or NABU's own layout.
func balance(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 64)
	var stack []string
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		s = s[i:]
		m := tagRe.FindStringSubmatch(s)
		if m == nil {
			b.WriteString("&lt;")
			s = s[1:]
			continue
		}
		tag := strings.ToLower(m[2])
		whole := m[0]
		s = s[len(whole):]
		switch {
		case voidTags[tag] || tag == "img" || tag == "input" || m[4] == "/":
			b.WriteString(whole)
		case m[1] == "":
			stack = append(stack, tag)
			b.WriteString(whole)
		default:
			// closing: find it on the stack
			j := len(stack) - 1
			for j >= 0 && stack[j] != tag {
				j--
			}
			if j < 0 {
				continue // stray closer: drop
			}
			for k := len(stack) - 1; k >= j; k-- {
				b.WriteString("</" + stack[k] + ">")
			}
			stack = stack[:j]
		}
	}
	for k := len(stack) - 1; k >= 0; k-- {
		b.WriteString("</" + stack[k] + ">")
	}
	return b.String()
}

// stripTags returns the text of an HTML fragment.
func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}

// Anchor turns heading text into a fragment id the way MediaWiki does.
func Anchor(s string) string {
	s = strings.Join(strings.Fields(s), "_")
	return strings.NewReplacer(`"`, "", "'", "", "<", "", ">", "", "&", "").Replace(s)
}
