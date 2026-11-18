package wikitext

import (
	"html"
	"regexp"
	"strings"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Coord is a geographical position given by {{coord}}.
type Coord struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Primary bool    `json:"primary,omitempty"` // display=title: the article's own location
}

// Infobox is an infobox template with its parameters.
type Infobox struct {
	Name   string            `json:"name"`
	Params map[string]string `json:"params"`
	Order  []string          `json:"order"`
}

// GlossaryTerm is a term and its definition.
type GlossaryTerm struct {
	Term       string `json:"term"`
	Definition string `json:"definition"`
}

// PageData is the structured content of a page: everything NABU v0.1's
// database stored per article (links, glossary, coordinates, tables,
// media, infoboxes, categories and templates), taken from the wikitext
// without rendering it.
type PageData struct {
	Title       string         `json:"title"`
	Links       []string       `json:"links"`
	Categories  []string       `json:"categories"`
	Templates   []string       `json:"templates"`
	Coordinates []Coord        `json:"coordinates"`
	Infoboxes   []Infobox      `json:"infoboxes"`
	Tables      []string       `json:"tables"`
	Media       []string       `json:"media"`
	Glossary    []GlossaryTerm `json:"glossary"`
}

// Primary returns the article's own location: the {{coord}} marked
// display=title, else the first one in an infobox, else none.
func (d *PageData) Primary() (Coord, bool) {
	for _, c := range d.Coordinates {
		if c.Primary {
			return c, true
		}
	}
	if len(d.Coordinates) > 0 && len(d.Infoboxes) > 0 {
		return d.Coordinates[0], true
	}
	return Coord{}, false
}

type extractor struct {
	site *dump.Siteinfo
	d    *PageData
	seen map[string]map[string]bool
	term string // a {{term}} waiting for its {{defn}}
}

func (x *extractor) add(kind string, list *[]string, v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	if x.seen[kind] == nil {
		x.seen[kind] = map[string]bool{}
	}
	if !x.seen[kind][v] {
		x.seen[kind][v] = true
		*list = append(*list, v)
	}
}

// Extract reads a page's structured data.
func Extract(title, src string, site *dump.Siteinfo) *PageData {
	d := &PageData{Title: title, Links: []string{}, Categories: []string{}, Templates: []string{},
		Coordinates: []Coord{}, Infoboxes: []Infobox{}, Tables: []string{}, Media: []string{}, Glossary: []GlossaryTerm{}}
	x := &extractor{site: site, d: d, seen: map[string]map[string]bool{}}
	src = commentRe.ReplaceAllString(src, "")
	src = refBlockRe.ReplaceAllString(src, " ")
	x.templates(src, 0)
	x.links(src)
	x.tables(src)
	return d
}

// templates walks every {{template}}, nested ones included.
func (x *extractor) templates(s string, depth int) {
	if depth > 20 {
		return
	}
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			return
		}
		end := matchBraces(s, i)
		if end < 0 {
			return
		}
		inner := s[i+2 : end]
		s = s[end+2:]
		if strings.HasPrefix(inner, "{") { // {{{parameter}}}
			continue
		}
		parts := splitTop(inner, '|')
		name := strings.TrimSpace(strings.ReplaceAll(parts[0], "_", " "))
		if name == "" || strings.HasPrefix(name, "#") || strings.ContainsAny(name, "{}[]<>") {
			x.templates(inner, depth+1)
			continue
		}
		x.add("tpl", &x.d.Templates, name)
		n := normName(name)
		var pos []string
		named := map[string]string{}
		var order []string
		for _, p := range parts[1:] {
			if k := strings.IndexByte(p, '='); k > 0 && !strings.ContainsAny(p[:k], "[{<") {
				key := strings.TrimSpace(p[:k])
				named[key] = strings.TrimSpace(p[k+1:])
				order = append(order, key)
			} else {
				pos = append(pos, strings.TrimSpace(p))
			}
		}
		switch {
		case n == "coord" || n == "coor" || n == "coordinates" || n == "coordinate" || n == "coor dms" || n == "coordenadas" || n == "координаты":
			if lat, lon, ok := ParseCoord(pos); ok {
				disp := named["display"]
				x.d.Coordinates = append(x.d.Coordinates, Coord{Lat: lat, Lon: lon,
					Primary: strings.Contains(disp, "title")})
			}
		case isInfobox(n):
			x.d.Infoboxes = append(x.d.Infoboxes, Infobox{Name: name, Params: named, Order: order})
		case n == "glossary" && len(pos) >= 2: // {{glossary|term|definition}}, NABU v0.1's form
			x.d.Glossary = append(x.d.Glossary, GlossaryTerm{Term: plain(pos[0]), Definition: plain(pos[1])})
		case n == "term" || n == "glossary term" || n == "gloss term":
			t := firstNonEmpty(named["term"], named["1"])
			if t == "" && len(pos) > 0 {
				t = pos[0]
			}
			x.term = plain(t)
		case n == "defn" || n == "glossary definition" || n == "gloss defn":
			def := firstNonEmpty(named["defn"], named["1"])
			if def == "" && len(pos) > 0 {
				def = pos[0]
			}
			if x.term != "" {
				x.d.Glossary = append(x.d.Glossary, GlossaryTerm{Term: x.term, Definition: plain(def)})
				x.term = ""
			}
		}
		x.templates(inner[len(parts[0]):], depth+1)
	}
}

// links sorts every [[link]] into articles, media and categories.
func (x *extractor) links(s string) {
	for {
		i := strings.Index(s, "[[")
		if i < 0 {
			return
		}
		end := matchBracketsML(s, i)
		if end < 0 {
			s = s[i+2:]
			continue
		}
		inner := s[i+2 : end]
		if strings.Contains(inner[min(len(inner), 1):], "[[") {
			x.links(inner[2:]) // caption links inside [[File:...]]
		}
		s = s[end+2:]
		target := strings.TrimSpace(strings.SplitN(inner, "|", 2)[0])
		colon := strings.HasPrefix(target, ":")
		target = strings.TrimSpace(strings.TrimPrefix(target, ":"))
		if target == "" || strings.ContainsAny(target, "{}<>") {
			continue
		}
		if c := strings.IndexByte(target, ':'); c > 0 {
			if ns, ok := x.site.NamespaceKey(target[:c]); ok {
				switch {
				case ns == 6 && !colon, ns == -2:
					x.add("media", &x.d.Media, strings.TrimSpace(target[c+1:]))
					continue
				case ns == 14 && !colon:
					x.add("cat", &x.d.Categories, x.site.Normalize(target[c+1:]))
					continue
				}
			} else if _, iw := interwiki[strings.ToLower(target[:c])]; iw || langCodeRe.MatchString(strings.ToLower(target[:c])) {
				continue // other wikis and languages
			}
		}
		if h := strings.IndexByte(target, '#'); h >= 0 {
			target = target[:h]
		}
		if target != "" {
			x.add("link", &x.d.Links, x.site.Normalize(target))
		}
	}
}

// tables collects every {| ... |} table as wikitext.
func (x *extractor) tables(s string) {
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "{|") {
			continue
		}
		depth, j := 0, i
		for ; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "{|") {
				depth++
			} else if strings.HasPrefix(t, "|}") {
				if depth--; depth == 0 {
					break
				}
			}
		}
		if j >= len(lines) {
			j = len(lines) - 1
		}
		x.d.Tables = append(x.d.Tables, strings.Join(lines[i:j+1], "\n"))
		i = j
	}
}

var (
	refBlockRe  = regexp.MustCompile(`(?is)<ref[^>/]*>.*?</ref\s*>|<ref[^>]*/>`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
	extLabelRe  = regexp.MustCompile(`\[(?:https?:|ftp:|//)[^\s\]]*\s*([^\]]*)\]`)
	bareURLText = regexp.MustCompile(`(?:https?|ftp)://\S+`)
	headEqRe    = regexp.MustCompile(`(?m)^=+|=+\s*$`)
	quoteRunRe  = regexp.MustCompile(`''+`)
	tableMarkRe = regexp.MustCompile(`(?m)^\s*(\{\||\|\}|\|-.*$|\|\+)`)
	cellAttrRe  = regexp.MustCompile(`(?m)(^|\|\||!!|^[|!])\s*[a-z-]+="[^"]*"[^|\n]*\|`)
)

// plain reduces a short piece of markup to text.
func plain(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(PlainText(s, nil)), " "))
}

// PlainText is the readable text of a page, without templates, tables'
// markup, references or tags: what full-text search indexes. site may
// be nil for fragments.
func PlainText(src string, site *dump.Siteinfo) string {
	s := commentRe.ReplaceAllString(src, "")
	s = refBlockRe.ReplaceAllString(s, " ")
	// Templates go entirely (infoboxes, navigation, citations).
	var b strings.Builder
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
		b.WriteString(s[:i])
		b.WriteByte(' ')
		s = s[end+2:]
	}
	s = b.String()
	// Links: keep the visible text, drop files, categories, languages.
	b.Reset()
	for {
		i := strings.Index(s, "[[")
		if i < 0 {
			b.WriteString(s)
			break
		}
		end := matchBracketsML(s, i)
		if end < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		inner := s[i+2 : end]
		s = s[end+2:]
		parts := splitTop(inner, '|')
		target := strings.TrimSpace(strings.TrimPrefix(parts[0], ":"))
		if c := strings.IndexByte(target, ':'); c > 0 && site != nil && !strings.HasPrefix(parts[0], ":") {
			if _, ok := site.NamespaceKey(target[:c]); ok {
				continue
			}
			if langCodeRe.MatchString(strings.ToLower(target[:c])) {
				continue
			}
		}
		b.WriteString(parts[len(parts)-1])
	}
	s = b.String()
	s = extLabelRe.ReplaceAllString(s, "$1")
	s = bareURLText.ReplaceAllString(s, " ")
	s = tableMarkRe.ReplaceAllString(s, " ")
	s = cellAttrRe.ReplaceAllString(s, "$1 ")
	s = tagStripRe.ReplaceAllString(s, " ")
	s = headEqRe.ReplaceAllString(s, " ")
	s = quoteRunRe.ReplaceAllString(s, "")
	s = magicWordRe.ReplaceAllString(s, " ")
	return html.UnescapeString(s)
}

var coordStartRe = regexp.MustCompile(`(?i)\{\{\s*(coord|coor|coordinates|coordenadas|координаты)\s*\|`)

// PrimaryCoord finds a page's own location quickly, without a full
// extraction: the {{coord}} with display=title, else the first one.
func PrimaryCoord(src string) (Coord, bool) {
	var first *Coord
	for _, loc := range coordStartRe.FindAllStringIndex(src, 40) {
		end := matchBraces(src, loc[0])
		if end < 0 {
			continue
		}
		parts := splitTop(src[loc[0]+2:end], '|')
		var pos []string
		display := ""
		for _, p := range parts[1:] {
			if k := strings.IndexByte(p, '='); k > 0 {
				if strings.TrimSpace(p[:k]) == "display" {
					display = p[k+1:]
				}
				continue
			}
			pos = append(pos, p)
		}
		lat, lon, ok := ParseCoord(pos)
		if !ok {
			continue
		}
		c := Coord{Lat: lat, Lon: lon, Primary: strings.Contains(display, "title")}
		if c.Primary {
			return c, true
		}
		if first == nil {
			first = &c
		}
	}
	if first != nil {
		return *first, true
	}
	return Coord{}, false
}
