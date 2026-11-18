// Package dump reads Wikimedia "pages-articles-multistream" dumps: the
// bzip2-compressed XML that Wikimedia publishes for every wiki, in every
// language, together with its offset index.
//
// A multistream dump is many small bzip2 streams laid end to end, each
// holding 100 pages. The index file lists "offset:pageid:title" for every
// page, so one article can be read by seeking to its stream and
// decompressing a few hundred kilobytes, never the whole file. NABU turns
// that index into a compact, sorted, searchable one the first time it
// sees a dump (see build.go).
package dump

import (
	"bytes"
	"compress/bzip2"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Namespace is one of the wiki's page namespaces (Talk, File, Category, ...).
type Namespace struct {
	Key  int    `json:"key"`
	Name string `json:"name"` // localized, e.g. "Datei" on German Wikipedia
	Case string `json:"case"` // "first-letter" or "case-sensitive"
}

// Siteinfo is the header at the start of every dump.
type Siteinfo struct {
	Sitename   string      `json:"sitename"`
	DBName     string      `json:"dbname"` // e.g. "dewiki"
	Base       string      `json:"base"`   // URL of the main page
	Generator  string      `json:"generator"`
	Case       string      `json:"case"`
	Lang       string      `json:"lang"`
	Namespaces []Namespace `json:"namespaces"`

	byName map[string]int // lower-cased names and aliases -> key
	byKey  map[int]Namespace
}

// Namespace keys used by the renderer.
const (
	NSMain     = 0
	NSFile     = 6
	NSTemplate = 10
	NSCategory = 14
)

// canonical English names work on every wiki, alongside the local ones.
var canonical = map[string]int{
	"media": -2, "special": -1, "talk": 1, "user": 2, "user talk": 3,
	"project": 4, "project talk": 5, "file": 6, "image": 6, "file talk": 7,
	"image talk": 7, "mediawiki": 8, "template": 10, "template talk": 11,
	"help": 12, "category": 14, "category talk": 15, "portal": 100,
	"draft": 118, "module": 828, "timedtext": 710,
}

// Common local aliases for File, which older articles use a lot.
var fileAliases = []string{"bild", "imagen", "immagine", "imagem", "изображение",
	"файл", "afbeelding", "grafika", "画像", "图像", "文件", "صورة", "ملف", "תמונה",
	"קובץ", "fichier", "plik", "soubor", "kuva", "fil", "dosya", "resim", "tập tin", "hình"}

// ParseSiteinfo reads the <siteinfo> element from the first stream of a
// dump (everything before the first page).
func ParseSiteinfo(r io.Reader) (*Siteinfo, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	si := &Siteinfo{}
	var stack []string
	var cur *Namespace
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			if si.DBName != "" {
				break
			}
			return nil, errors.New("dump: no <siteinfo> header found")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			text.Reset()
			switch t.Name.Local {
			case "mediawiki":
				for _, a := range t.Attr {
					if a.Name.Local == "lang" {
						si.Lang = a.Value
					}
				}
			case "namespace":
				cur = &Namespace{}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "key":
						cur.Key = atoi(a.Value)
					case "case":
						cur.Case = a.Value
					}
				}
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			v := strings.TrimSpace(text.String())
			switch t.Name.Local {
			case "sitename":
				si.Sitename = v
			case "dbname":
				si.DBName = v
			case "base":
				si.Base = v
			case "generator":
				si.Generator = v
			case "case":
				si.Case = v
			case "namespace":
				if cur != nil {
					cur.Name = v
					si.Namespaces = append(si.Namespaces, *cur)
					cur = nil
				}
			case "siteinfo":
				si.init()
				return si, nil
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			text.Reset()
		}
	}
	si.init()
	return si, nil
}

func (si *Siteinfo) init() {
	si.byName = map[string]int{}
	si.byKey = map[int]Namespace{}
	for k, v := range canonical {
		si.byName[k] = v
	}
	for _, a := range fileAliases {
		si.byName[a] = NSFile
	}
	for _, ns := range si.Namespaces {
		si.byKey[ns.Key] = ns
		if ns.Name != "" {
			si.byName[strings.ToLower(ns.Name)] = ns.Key
		}
	}
	if p, ok := si.byKey[4]; ok && p.Name != "" {
		si.byName[strings.ToLower(p.Name)] = 4
	}
	if si.Lang == "" {
		si.Lang = langFromDB(si.DBName)
	}
}

// SiteinfoFromDump decompresses the header stream: the bytes before the
// first page stream.
func SiteinfoFromDump(r io.ReaderAt, firstOffset int64) (*Siteinfo, error) {
	if firstOffset <= 0 || firstOffset > 1<<20 {
		firstOffset = 1 << 20
	}
	buf := make([]byte, firstOffset)
	n, err := r.ReadAt(buf, 0)
	if err != nil && !(errors.Is(err, io.EOF) && n > 0) {
		return nil, err
	}
	return ParseSiteinfo(bzip2.NewReader(bytes.NewReader(buf[:n])))
}

func langFromDB(db string) string {
	for _, suf := range []string{"wiktionary", "wikiquote", "wikibooks", "wikisource", "wikivoyage", "wikinews", "wikiversity", "wiki"} {
		if strings.HasSuffix(db, suf) {
			l := strings.ReplaceAll(strings.TrimSuffix(db, suf), "_", "-")
			if l == "be-x-old" {
				return "be-tarask"
			}
			return l
		}
	}
	return "en"
}

// Host is the wiki's web address, e.g. "de.wikipedia.org".
func (si *Siteinfo) Host() string {
	if u, err := url.Parse(si.Base); err == nil && u.Host != "" {
		return u.Host
	}
	return si.Lang + ".wikipedia.org"
}

// MainPage is the title of the wiki's front page.
func (si *Siteinfo) MainPage() string {
	if u, err := url.Parse(si.Base); err == nil {
		if i := strings.Index(u.Path, "/wiki/"); i >= 0 {
			return strings.ReplaceAll(u.Path[i+6:], "_", " ")
		}
	}
	return "Main Page"
}

// RTL reports whether the wiki's language is written right to left.
func (si *Siteinfo) RTL() bool { return IsRTL(si.Lang) }

// IsRTL reports whether a language code is a right-to-left script.
func IsRTL(lang string) bool {
	switch strings.SplitN(lang, "-", 2)[0] {
	case "ar", "arc", "arz", "azb", "ckb", "dv", "fa", "glk", "he", "ks", "lrc",
		"mzn", "nqo", "pnb", "ps", "sd", "syc", "ug", "ur", "yi", "ary", "skr":
		return true
	}
	return false
}

// NamespaceKey resolves a namespace prefix ("Datei", "File", "category")
// to its key.
func (si *Siteinfo) NamespaceKey(prefix string) (int, bool) {
	k, ok := si.byName[strings.ToLower(strings.TrimSpace(strings.ReplaceAll(prefix, "_", " ")))]
	return k, ok
}

// NamespaceName is the local name of a namespace key.
func (si *Siteinfo) NamespaceName(key int) string {
	if ns, ok := si.byKey[key]; ok {
		return ns.Name
	}
	for k, v := range canonical {
		if v == key {
			return strings.Title(k) //nolint:staticcheck // ASCII only
		}
	}
	return ""
}

// SplitTitle separates a full title into namespace key and the rest.
func (si *Siteinfo) SplitTitle(title string) (int, string) {
	if i := strings.IndexByte(title, ':'); i > 0 {
		if k, ok := si.NamespaceKey(title[:i]); ok {
			return k, strings.TrimSpace(title[i+1:])
		}
	}
	return NSMain, title
}

// Normalize turns user or link input into the canonical page title, the
// way MediaWiki does: underscores become spaces, runs of spaces collapse,
// namespace names take their local spelling and the first letter is
// capitalised where the wiki is case-insensitive there.
func (si *Siteinfo) Normalize(t string) string {
	t = strings.ReplaceAll(t, "_", " ")
	t = strings.Join(strings.Fields(t), " ")
	t = strings.TrimPrefix(t, ":")
	t = strings.TrimSpace(t)
	if i := strings.IndexByte(t, '#'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	key, rest := si.SplitTitle(t)
	caseRule := si.Case
	if ns, ok := si.byKey[key]; ok && ns.Case != "" {
		caseRule = ns.Case
	}
	if caseRule != "case-sensitive" {
		rest = UpperFirst(rest)
	}
	if key != NSMain {
		name := si.NamespaceName(key)
		if name != "" {
			return name + ":" + rest
		}
	}
	return rest
}

// UpperFirst capitalises the first letter.
func UpperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	u := unicode.ToUpper(r)
	if r == 'ß' || u == r {
		return s
	}
	return string(u) + s[n:]
}

func atoi(s string) int {
	n, neg := 0, false
	for i, c := range s {
		if i == 0 && c == '-' {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}

// Fold maps a title to its search key: lower case, accents removed,
// underscores as spaces. "Écriture" and "ecriture" fold to the same key.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == '_' || unicode.IsSpace(r) {
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		r = unicode.ToLower(r)
		if r >= 0xc0 && r < 0x250 {
			if m, ok := deaccent[r]; ok {
				b.WriteString(m)
				continue
			}
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

var deaccent = func() map[rune]string {
	m := map[rune]string{}
	add := func(base string, chars string) {
		for _, r := range chars {
			m[r] = base
		}
	}
	add("a", "àáâãäåāăą")
	add("c", "çćĉċč")
	add("d", "ďđ")
	add("e", "èéêëēĕėęě")
	add("g", "ĝğġģ")
	add("h", "ĥħ")
	add("i", "ìíîïĩīĭįı")
	add("j", "ĵ")
	add("k", "ķ")
	add("l", "ĺļľŀł")
	add("n", "ñńņňŉ")
	add("o", "òóôõöøōŏő")
	add("r", "ŕŗř")
	add("s", "śŝşšș")
	add("t", "ţťŧț")
	add("u", "ùúûüũūŭůűų")
	add("w", "ŵ")
	add("y", "ýÿŷ")
	add("z", "źżž")
	m['ß'] = "ss"
	m['æ'] = "ae"
	m['œ'] = "oe"
	m['þ'] = "th"
	m['ð'] = "d"
	return m
}()
