package fulltext

import (
	"strings"

	"github.com/vajra-wielder/nabu/internal/dump"
)

// Sections at the end of an article that list its sources and links
// elsewhere are left out of the index: they are in nearly every article
// and say nothing of its subject ("Official website", "Profile",
// publishers, "com"). In Simple English they hold 2.3% of the words.
// "See also" sections stay: they name related subjects.
var boilerplate = map[string]string{
	"en": "references|notes|footnotes|citations|sources|bibliography|works cited|notes and references|references and notes|" +
		"external links|other websites|further reading|external sources|web links",
	"fr": "references|notes|notes et references|sources|bibliographie|liens externes|lien externe",
	"de": "einzelnachweise|anmerkungen|quellen|literatur|weblinks|belege",
	"es": "referencias|notas|bibliografia|fuentes|enlaces externos",
	"pt": "referencias|notas|bibliografia|fontes|ligacoes externas",
	"it": "note|bibliografia|fonti|collegamenti esterni",
	"ru": "примечания|литература|ссылки|источники",
	"nl": "referenties|bronnen|literatuur|externe links|externe link",
	"id": "referensi|catatan|pranala luar|daftar pustaka|bacaan lanjutan",
	"da": "referencer|kilder|litteratur|eksterne henvisninger",
}

// The headings of "See also" sections: the links under them stay, the
// heading's own words ("related", "pages") go.
var seeAlso = map[string]string{
	"en": "see also|related pages", "fr": "voir aussi|articles connexes", "de": "siehe auch",
	"es": "vease tambien|veanse tambien", "pt": "ver tambem", "it": "voci correlate", "ru": "см. также",
	"nl": "zie ook", "id": "lihat pula|lihat juga", "da": "se ogsa",
}

var boilerSets, seeAlsoSets = sets(boilerplate), sets(seeAlso)

func sets(lists map[string]string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for lang, list := range lists {
		m := map[string]bool{}
		for _, h := range strings.Split(list, "|") {
			m[dump.Fold(h)] = true
		}
		out[lang] = m
	}
	return out
}

// withoutBoilerplate returns src without those sections: from such a
// heading to the next heading of the same level or higher.
func withoutBoilerplate(src, lang string) string {
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	if lang == "simple" {
		lang = "en"
	}
	set, also := boilerSets[lang], seeAlsoSets[lang]
	if set == nil || !strings.Contains(src, "\n=") {
		return src
	}
	var b strings.Builder
	skip := 0 // level of the section being left out, 0 for none
	start := 0
	for start < len(src) {
		end := strings.IndexByte(src[start:], '\n')
		if end < 0 {
			end = len(src)
		} else {
			end += start + 1
		}
		line := src[start:end]
		drop := false
		if lvl, name := heading(line); lvl > 0 {
			if skip > 0 && lvl <= skip {
				skip = 0
			}
			f := dump.Fold(name)
			if skip == 0 && set[f] {
				skip = lvl
			}
			drop = also[f]
		}
		if skip == 0 && !drop {
			b.WriteString(line)
		}
		start = end
	}
	return b.String()
}

// heading returns the level and text of a "== Heading ==" line.
func heading(line string) (int, string) {
	l := strings.TrimSpace(line)
	lvl := 0
	for lvl < len(l) && l[lvl] == '=' {
		lvl++
	}
	if lvl < 2 || lvl > 6 || !strings.HasSuffix(l, strings.Repeat("=", lvl)) || len(l) <= 2*lvl {
		return 0, ""
	}
	return lvl, strings.TrimSpace(l[lvl : len(l)-lvl])
}
