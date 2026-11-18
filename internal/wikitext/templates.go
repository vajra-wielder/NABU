package wikitext

import (
	"strconv"
	"strings"
	"unicode"
)

// call is one template invocation.
type call struct {
	name  string            // normalized: lower case, spaces, no namespace
	raw   string            // as written
	pos   []string          // positional parameters (trimmed)
	named map[string]string // named parameters, keys lower-cased
	keys  []string          // named keys in source order
	block bool              // alone on its line(s)
}

func (c *call) arg(i int) string {
	if i < len(c.pos) {
		return c.pos[i]
	}
	return ""
}

// get returns the first non-empty named parameter among keys.
func (c *call) get(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(c.named[k]); v != "" {
			return v
		}
	}
	return ""
}

// expand replaces every top-level {{template}} in s.
func (r *renderer) expand(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	r.depth++
	defer func() { r.depth-- }()
	if r.depth > 30 {
		return s
	}
	var b strings.Builder
	line := "" // output since the last newline
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			b.WriteString(s)
			break
		}
		end := matchBraces(s, i)
		if end < 0 {
			b.WriteString(s)
			break
		}
		before := s[:i]
		b.WriteString(before)
		if k := strings.LastIndexByte(before, '\n'); k >= 0 {
			line = before[k+1:]
		} else {
			line += before
		}
		inner := s[i+2 : end]
		after := s[end+2:]
		// A template alone on its line is block-level.
		prefix := line
		nl := strings.IndexByte(after, '\n')
		suffix := after
		if nl >= 0 {
			suffix = after[:nl]
		}
		block := strings.TrimSpace(prefix) == "" && strings.TrimSpace(suffix) == ""
		out := r.callTemplate(inner, block)
		if strings.Contains(out, "{{") {
			out = r.expand(out)
		}
		b.WriteString(out)
		if k := strings.LastIndexByte(out, '\n'); k >= 0 {
			line = out[k+1:]
		} else {
			line += out
		}
		s = after
		if block && out == "" && strings.HasPrefix(s, "\n") && strings.TrimSpace(prefix) == "" {
			s = s[1:] // swallow the empty line a dropped template leaves
		}
	}
	return b.String()
}

// matchBraces finds the "}}" closing the "{{" at i.
func matchBraces(s string, i int) int {
	depth := 0
	for j := i; j < len(s)-1; j++ {
		if s[j] == '{' && s[j+1] == '{' {
			depth++
			j++
		} else if s[j] == '}' && s[j+1] == '}' {
			depth--
			if depth == 0 {
				return j
			}
			j++
		}
	}
	return -1
}

func normName(n string) string {
	n = strings.ReplaceAll(n, "_", " ")
	n = strings.ToLower(strings.Join(strings.Fields(n), " "))
	for _, p := range []string{"subst:", "safesubst:", "msgnw:", "template:", "plantilla:", "vorlage:", "modèle:", "predefinição:", "шаблон:", "قالب:", "الگو:", "skabelon:", "templat:", "模板:", "樣板:"} {
		n = strings.TrimPrefix(n, p)
	}
	return strings.TrimSpace(n)
}

func (r *renderer) callTemplate(inner string, block bool) string {
	parts := splitTop(inner, '|')
	nameRaw := strings.TrimSpace(parts[0])
	if nameRaw == "" {
		return ""
	}
	// Parser functions and magic words: {{#if:...}}, {{lc:...}}
	if i := strings.IndexByte(nameRaw, ':'); i > 0 {
		fn := strings.ToLower(strings.TrimSpace(nameRaw[:i]))
		if out, ok := r.parserFunc(fn, strings.TrimSpace(nameRaw[i+1:]), parts[1:]); ok {
			return out
		}
	}
	c := &call{raw: nameRaw, name: normName(nameRaw), named: map[string]string{}, block: block}
	for _, p := range parts[1:] {
		if k := strings.IndexByte(p, '='); k > 0 && !strings.ContainsAny(p[:k], "[{<\x01") {
			key := strings.ToLower(strings.TrimSpace(p[:k]))
			if _, dup := c.named[key]; !dup {
				c.keys = append(c.keys, key)
			}
			c.named[key] = strings.TrimSpace(p[k+1:])
			if n, err := strconv.Atoi(key); err == nil && n >= 1 {
				for len(c.pos) < n {
					c.pos = append(c.pos, "")
				}
				c.pos[n-1] = c.named[key]
			}
			continue
		}
		c.pos = append(c.pos, strings.TrimSpace(p))
	}
	if h, ok := handlers[c.name]; ok {
		return h(r, c)
	}
	return r.fallback(c)
}

// magic variables with no arguments.
func (r *renderer) magic(name string) (string, bool) {
	switch strings.ToUpper(name) {
	case "PAGENAME", "FULLPAGENAME", "BASEPAGENAME", "SUBJECTPAGENAME", "ROOTPAGENAME":
		return r.ctx.Title, true
	case "PAGENAMEE":
		return wikiPath(r.ctx.Title), true
	case "CURRENTYEAR":
		return strconv.Itoa(r.ctx.Now.Year()), true
	case "CURRENTMONTHNAME":
		return r.ctx.Now.Month().String(), true
	case "CURRENTDAY":
		return strconv.Itoa(r.ctx.Now.Day()), true
	case "SITENAME":
		return r.ctx.Site.Sitename, true
	case "NAMESPACE":
		return "", true
	case "!":
		return "|", true
	case "=":
		return "=", true
	}
	return "", false
}

func (r *renderer) parserFunc(fn, first string, rest []string) (string, bool) {
	arg := func(i int) string {
		if i < len(rest) {
			return strings.TrimSpace(rest[i])
		}
		return ""
	}
	switch fn {
	case "#if":
		if strings.TrimSpace(r.expand(first)) != "" {
			return arg(0), true
		}
		return arg(1), true
	case "#ifeq":
		if strings.TrimSpace(r.expand(first)) == strings.TrimSpace(r.expand(arg(0))) {
			return arg(1), true
		}
		return arg(2), true
	case "#switch":
		v := strings.TrimSpace(r.expand(first))
		def := ""
		fall := false
		for _, p := range rest {
			k, val, hasEq := strings.Cut(p, "=")
			k = strings.TrimSpace(k)
			if !hasEq {
				if k == v {
					fall = true
				}
				def = k
				continue
			}
			if fall || k == v {
				return strings.TrimSpace(val), true
			}
			if k == "#default" {
				def = strings.TrimSpace(val)
			}
		}
		return def, true
	case "#tag":
		tag := strings.ToLower(first)
		if tag == "ref" {
			name, group := "", ""
			var content []string
			for _, p := range rest {
				k, v, ok := strings.Cut(p, "=")
				switch strings.TrimSpace(strings.ToLower(k)) {
				case "name":
					if ok {
						name = strings.Trim(strings.TrimSpace(v), `"`)
						continue
					}
				case "group":
					if ok {
						group = strings.Trim(strings.TrimSpace(v), `"`)
						continue
					}
				}
				content = append(content, p)
			}
			return r.addRef(group, name, r.extractTags(strings.Join(content, "|")), false), true
		}
		return "", true
	case "lc":
		return strings.ToLower(first), true
	case "uc":
		return strings.ToUpper(first), true
	case "lcfirst":
		if first == "" {
			return "", true
		}
		rs := []rune(first)
		rs[0] = unicode.ToLower(rs[0])
		return string(rs), true
	case "ucfirst":
		return upperFirstRune(first), true
	case "formatnum":
		return formatNum(first), true
	case "displaytitle":
		r.out.DisplayTitle = stripTags(r.inline(first))
		return "", true
	case "plural":
		return arg(0), true
	case "padleft", "padright", "urlencode", "anchorencode":
		return first, true
	case "#invoke", "#expr", "#time", "#property", "#statements", "#ifexist", "#iferror",
		"#ifexpr", "defaultsort", "defaultsortkey", "defaultcategorysort", "#language",
		"int", "fullurl", "localurl", "filepath", "#related", "#section", "#lst", "#lsth",
		"#coordinates", "#target", "#categorytree", "#babel", "noexternallanglinks", "#titleparts":
		return "", true
	}
	return "", false
}

// fallback handles templates without their own implementation.
func (r *renderer) fallback(c *call) string {
	n := c.name
	if v, ok := r.magic(c.raw); ok {
		return v
	}
	switch {
	case isInfobox(n):
		return r.infobox(c)
	case strings.HasPrefix(n, "cite ") || strings.HasPrefix(n, "cita ") || strings.HasPrefix(n, "citar ") || strings.HasPrefix(n, "vcite"):
		return r.inlinePH(r.cite(c))
	case strings.HasPrefix(n, "lang-") || strings.HasPrefix(n, "ipa-") || strings.HasPrefix(n, "transl-"):
		return langText(c)
	case strings.HasSuffix(n, "stub") || strings.Contains(n, "navbox") || strings.Contains(n, "sidebar") ||
		strings.HasPrefix(n, "portal") || strings.HasPrefix(n, "pp-") || strings.HasPrefix(n, "use "):
		return ""
	case c.block:
		return "" // banners, navigation, maintenance notices
	case len(c.pos) == 1 && len(c.named) == 0:
		// Unknown inline wrapper such as {{nowrap|text}}: keep the text.
		return c.pos[0]
	}
	return ""
}

func isInfobox(n string) bool {
	for _, p := range []string{"infobox", "ficha", "info/", "карточка", "基礎情報", "صندوق معلومات", "جعبه اطلاعات", "taxobox", "automatic taxobox", "speciesbox", "chembox", "drugbox", "geobox", "kotak info", "faktaboks", "boîte", "infoboks"} {
		if strings.HasPrefix(n, p) || strings.Contains(n, " "+p) {
			return true
		}
	}
	return strings.Contains(n, "infobox")
}

func upperFirstRune(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

func formatNum(s string) string {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, hasFrac := strings.Cut(s, ".")
	if _, err := strconv.Atoi(intPart); err != nil || len(intPart) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if hasFrac {
		out += "." + frac
	}
	if neg {
		out = "−" + out
	}
	return out
}

// infobox renders any infobox as a two-column table of its parameters.
func (r *renderer) infobox(c *call) string {
	title := c.get("name", "title", "nombre", "nome", "titre", "titel", "название", "名称", "اسم", "عنوان", "nama", "navn")
	if title == "" {
		title = r.ctx.Title
	}
	var b strings.Builder
	b.WriteString(`<table class="infobox"><tr><th colspan="2" class="infobox-title">` + r.fragment(title) + "</th></tr>")
	// The image comes first.
	for _, k := range []string{"image", "image_name", "logo", "flag", "imagen", "immagine", "imagem", "bild", "image1", "изображение", "صورة", "تصویر", "gambar", "billede", "image_flag", "map_image", "image_map"} {
		if v := c.named[k]; strings.TrimSpace(v) != "" {
			name := strings.TrimSpace(v)
			if strings.Contains(name, "{{") || strings.Contains(name, "<") || strings.Contains(name, "\x01") {
				// a map or cropping template: render it; keep it only if
				// it produced something
				if h := r.fragment(name); strings.TrimSpace(stripTags(h)) != "" || strings.Contains(h, "<img") {
					b.WriteString(`<tr><td colspan="2" class="infobox-image">` + h + "</td></tr>")
				}
				break
			}
			if strings.HasPrefix(name, "[[") {
				b.WriteString(`<tr><td colspan="2" class="infobox-image">` + r.fragment(name) + "</td></tr>")
				break
			}
			if ns, rest := r.ctx.Site.SplitTitle(name); ns == 6 {
				name = rest
			}
			capt := c.get(k+"_caption", "caption", "image_caption", "pie", "legenda", "légende", "bildbeschreibung", "подпись", "تعليق")
			b.WriteString(`<tr><td colspan="2" class="infobox-image">` + r.picture(name, 250, capt))
			if capt != "" {
				b.WriteString(`<div class="infobox-caption">` + r.fragment(capt) + "</div>")
			}
			b.WriteString("</td></tr>")
			break
		}
	}
	rows := 0
	// Generic {{Infobox}} uses headerN / labelN / dataN.
	if _, ok := c.named["data1"]; ok || c.named["label1"] != "" {
		for i := 1; i <= 120; i++ {
			n := strconv.Itoa(i)
			if h := c.named["header"+n]; strings.TrimSpace(h) != "" {
				b.WriteString(`<tr><th colspan="2" class="infobox-header">` + r.fragment(h) + "</th></tr>")
			}
			d := strings.TrimSpace(c.named["data"+n])
			if d == "" {
				continue
			}
			l := strings.TrimSpace(c.named["label"+n])
			if l == "" {
				b.WriteString(`<tr><td colspan="2" class="infobox-full">` + r.fragment(d) + "</td></tr>")
			} else {
				b.WriteString(`<tr><th scope="row">` + r.fragment(l) + "</th><td>" + r.fragment(d) + "</td></tr>")
			}
			rows++
		}
	} else {
		used := map[string]bool{}
		for _, k := range c.keys {
			if used[k] || skipInfoboxKey(k) {
				continue
			}
			v := strings.TrimSpace(c.named[k])
			label := humanKey(k)
			// "leader_title1 = President" + "leader_name1 = X" -> President: X
			if i := strings.Index(k, "_title"); i > 0 {
				nameKey := k[:i] + "_name" + k[i+len("_title"):]
				if nv, ok := c.named[nameKey]; ok {
					used[nameKey] = true
					label, v = "", strings.TrimSpace(nv)
					if lh := r.fragment(c.named[k]); strings.TrimSpace(stripTags(lh)) != "" {
						r.infoRow(&b, lh, v)
						rows++
					}
					continue
				}
			}
			// "established_event1 = Baptism" + "established_date1 = 496"
			if i := strings.Index(k, "_event"); i > 0 {
				dateKey := k[:i] + "_date" + k[i+len("_event"):]
				if dv, ok := c.named[dateKey]; ok {
					used[dateKey] = true
					if lh := r.fragment(v); strings.TrimSpace(stripTags(lh)) != "" {
						r.infoRow(&b, lh, dv)
						rows++
					}
					continue
				}
			}
			// "languages_type = Official language" labels "languages"
			if t, ok := c.named[k+"_type"]; ok && strings.TrimSpace(t) != "" {
				used[k+"_type"] = true
				r.infoRow(&b, r.fragment(t), v)
				rows++
				continue
			}
			if strings.HasSuffix(k, "_type") {
				if _, ok := c.named[strings.TrimSuffix(k, "_type")]; ok {
					continue
				}
			}
			if v == "" {
				continue
			}
			if r.infoRow(&b, escText(label), v) {
				rows++
			}
		}
	}
	b.WriteString("</table>")
	return r.blockPH(b.String())
}

// infoRow adds a label/value row unless the value renders to nothing.
func (r *renderer) infoRow(b *strings.Builder, labelHTML, value string) bool {
	h := r.fragment(value)
	if strings.TrimSpace(stripTags(r.resolve(h))) == "" && !strings.Contains(h, "<img") && !strings.Contains(r.resolve(h), "<img") {
		return false
	}
	b.WriteString(`<tr><th scope="row">` + labelHTML + "</th><td>" + h + "</td></tr>")
	return true
}

func skipInfoboxKey(k string) bool {
	if strings.Contains(k, "caption") || strings.Contains(k, "footnote") || strings.HasSuffix(k, "_ref") {
		return true
	}
	switch k {
	case "name", "title", "image", "image_name", "logo", "caption", "image_caption", "alt", "image_alt",
		"image_size", "imagesize", "image_width", "size", "width", "upright", "bodystyle", "embed", "child",
		"module", "signature", "signature_alt", "signature_size", "nombre", "imagen", "pie", "tamaño",
		"nome", "immagine", "imagem", "legenda", "bild", "bildbeschreibung", "titre", "légende", "titel",
		"название", "изображение", "подпись", "ширина", "مسار", "صورة", "تعليق", "اسم", "flag", "map_image",
		"image_map", "pushpin_map", "mapframe", "coordinates_footnotes", "image1", "image_flag":
		return true
	}
	for _, suf := range []string{"style", "_size", "_alt", "_width", "_upright", "class", "_caption", "_image", "image_"} {
		if strings.HasSuffix(k, suf) || strings.HasPrefix(k, suf) {
			return true
		}
	}
	return strings.HasPrefix(k, "pushpin") || strings.HasPrefix(k, "mapframe")
}

func humanKey(k string) string {
	k = strings.ReplaceAll(strings.ReplaceAll(k, "_", " "), "-", " ")
	return upperFirstRune(strings.TrimSpace(k))
}

func langText(c *call) string {
	t := c.arg(0)
	if t == "" {
		t = c.get("text", "1")
	}
	if strings.HasPrefix(c.name, "ipa") {
		return "[" + t + "]"
	}
	return "''" + t + "''"
}
