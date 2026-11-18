package wikitext

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type handler func(r *renderer, c *call) string

var handlers = map[string]handler{}

func reg(h handler, names ...string) {
	for _, n := range names {
		handlers[n] = h
	}
}

func drop(*renderer, *call) string { return "" }

func first(_ *renderer, c *call) string { return c.arg(0) }

func wrap(tag string) handler {
	return func(r *renderer, c *call) string {
		return r.inlinePH("<" + tag + ">" + r.inline(c.arg(0)) + "</" + tag + ">")
	}
}

func text(s string) handler { return func(*renderer, *call) string { return s } }

func init() {
	// Things a reader of an offline copy does not need.
	reg(drop, "short description", "authority control", "taxonbar", "coord missing", "featured article",
		"good article", "featured list", "pp", "pp-protected", "pp-semi", "pp-move", "pp-vandalism",
		"protection padlock", "italic title", "lowercase title", "displaytitle", "defaultsort", "toc limit",
		"tocright", "toc", "clear", "clr", "-", "commons", "commons category", "commonscat", "wiktionary",
		"wikiquote", "wikisource", "wikispecies", "wikibooks", "wikivoyage", "sister project links",
		"subject bar", "portal", "portal bar", "portals", "spoken wikipedia", "listen", "audio", "cbignore",
		"dead link", "citation needed", "cn", "fact", "clarify", "when", "who", "which", "by whom", "according to whom",
		"vague", "dubious", "update", "update inline", "failed verification", "better source needed", "page needed",
		"refimprove", "more citations needed", "unreferenced", "multiple issues", "notability", "orphan",
		"one source", "expand section", "copy edit", "tone", "advert", "pov", "cleanup", "inflation-fn",
		"div col", "div col end", "colbegin", "col-begin", "col-break", "col-end", "col-2", "col-3", "refbegin",
		"refend", "reflist-talk", "nowrap begin", "nowrap end", "end", "s-end", "s-start", "s-bef", "s-aft",
		"s-ttl", "s-off", "s-reg", "s-hou", "use dmy dates", "use mdy dates", "engvar", "anchor",
		"sfn whitelist", "efn whitelist", "hidden begin", "hidden end", "collapse top", "collapse bottom",
		"wikidata", "official website", "webarchive", "verify credibility", "self-published inline",
		"bots", "nobots", "redirect category shell", "rcat shell", "r from", "esborrany", "ébauche", "esbozo",
		"запрос источника", "нет ап", "citation nécessaire", "référence nécessaire", "cita requerida", "carece de fontes",
		"كاريكاتير", "بحاجة لمصدر", "مصدر", "منبع", "需要来源", "來源請求")
	reg(func(r *renderer, c *call) string { r.out.ShortDesc = c.arg(0); return "" }, "short description")

	// Punctuation and spacing helpers.
	reg(text("|"), "!")
	reg(text("="), "=")
	reg(text("||"), "!!")
	reg(text("&nbsp;"), "nbsp", "spaces", "sp", "space")
	reg(text("&nbsp;– "), "snd", "spnd", "sndash")
	reg(text("–"), "ndash", "en dash", "dash", "–")
	reg(text("—"), "mdash", "em dash", "—")
	reg(text("&nbsp;· "), "·", "dot", "middot", "dot-separator", "•", "bull", "bullet")
	reg(text("'"), "'", "apostrophe")
	reg(text(`"`), "\"", "quotation mark")
	reg(text("&#124;"), "pipe")
	reg(text("<br>"), "break", "br")
	reg(func(r *renderer, c *call) string { return r.blockPH(`<div style="clear:both"></div>`) }, "clear", "clr", "-")

	// Inline formatting wrappers.
	reg(first, "nowrap", "nobr", "nobreak", "j", "nowr", "visible anchor", "vanchor", "sic", "not a typo",
		"proper name", "lang-rtl", "ltr", "rtl", "keypress", "hlist-item", "trim", "nobold", "noitalic",
		"plain link", "plainlink", "unbulleted list item", "ill-wd", "silence", "notatypo", "lang2", "linktext")
	reg(wrap("small"), "small", "smaller", "smallcaps", "sc", "small caps", "midsize")
	reg(wrap("big"), "big", "larger", "large", "huge")
	reg(wrap("code"), "code", "mono", "monospace", "tt", "param", "para")
	reg(wrap("kbd"), "kbd", "key press")
	reg(wrap("sup"), "sup", "su")
	reg(wrap("sub"), "sub")
	reg(wrap("var"), "var", "mvar", "math variable")
	reg(wrap("em"), "em", "emphasis")
	reg(wrap("strong"), "strong")
	reg(wrap("del"), "strike", "s", "del")
	reg(func(r *renderer, c *call) string { return "''" + c.arg(0) + "''" }, "italic", "i", "taxon", "species", "genus")
	reg(func(r *renderer, c *call) string { return "'''" + c.arg(0) + "'''" }, "bold", "b")
	reg(func(r *renderer, c *call) string {
		return r.inlinePH(`<span class="texhtml">` + r.inline(c.arg(0)) + "</span>")
	}, "math", "tmath", "bigmath")
	reg(func(r *renderer, c *call) string {
		return r.inlinePH(`<abbr title="` + escAttr(stripTags(r.inline(c.arg(1)))) + `">` + r.inline(c.arg(0)) + "</abbr>")
	}, "abbr", "abbrlink", "tooltip", "hover title", "tip")
	reg(func(r *renderer, c *call) string {
		col := c.arg(0)
		if strings.ContainsAny(col, ";:\"'<>()") {
			return c.arg(1)
		}
		return r.inlinePH(`<span style="color:` + escAttr(col) + `">` + r.inline(c.arg(1)) + "</span>")
	}, "color", "colour", "font color", "fgcolor")
	reg(func(r *renderer, c *call) string {
		return r.inlinePH("{{" + escText(c.arg(0)) + "}}")
	}, "tl", "tlx", "tlc", "tlp", "template link")

	// Languages and pronunciation.
	reg(func(r *renderer, c *call) string {
		t := c.arg(1)
		if t == "" {
			t = c.get("text")
		}
		return r.inlinePH(`<span lang="` + escAttr(c.arg(0)) + `">` + r.inline(t) + "</span>")
	}, "lang", "langx", "script", "transliteration", "transl", "language", "yue-hant")
	reg(func(r *renderer, c *call) string {
		var ps []string
		for _, p := range c.pos {
			switch strings.ToLower(p) {
			case "", "lang", "pron", "local", "us", "uk", "also", "ipa", "audio", "-":
				continue
			}
			ps = append(ps, p)
		}
		return "/" + strings.Join(ps, "") + "/"
	}, "ipac-en", "ipac-es", "ipac-fr", "ipac-de", "ipac-pt", "ipac-ru", "ipac-ar")
	reg(func(r *renderer, c *call) string { return "[" + c.arg(0) + "]" }, "ipa", "ipa2", "ipa audio")
	reg(func(r *renderer, c *call) string {
		var ps []string
		for _, p := range c.pos {
			if p != "" && !strings.Contains(p, "=") {
				ps = append(ps, p)
			}
		}
		return "''" + strings.Join(ps, "-") + "''"
	}, "respell")
	reg(func(r *renderer, c *call) string {
		// {{nihongo|English|漢字|romaji}}
		out := c.arg(0)
		var extra []string
		for _, p := range c.pos[min(1, len(c.pos)):] {
			if p != "" {
				extra = append(extra, p)
			}
		}
		if len(extra) > 0 {
			out += " (" + strings.Join(extra, ", ") + ")"
		}
		return out
	}, "nihongo", "nihongo2", "nihongo3", "nihongo foot", "zh", "lang-zh", "chinese", "korean")
	reg(func(r *renderer, c *call) string {
		// {{ill|Local title|fr|Foreign title}}: interlanguage link
		return "[[" + c.arg(0) + "|" + firstNonEmpty(c.get("lt"), c.arg(0)) + "]]"
	}, "ill", "interlanguage link", "link-interwiki", "仮リンク", "ill2")

	// Hatnotes.
	hat := func(label string) handler {
		return func(r *renderer, c *call) string {
			var links []string
			for _, p := range c.pos {
				if p != "" {
					links = append(links, "[["+p+"]]")
				}
			}
			if len(links) == 0 {
				return ""
			}
			return r.blockPH(`<div class="hatnote">` + label + r.inline(joinAnd(links)) + "</div>")
		}
	}
	reg(hat("Main article: "), "main", "main article", "main page", "principal", "artículo principal", "article détaillé", "artigo principal", "основная статья", "مقالة مفصلة", "مقاله اصلی", "主条目", "hovedartikel", "artikel utama")
	reg(hat("See also: "), "see also", "seealso", "see", "véase también", "voir aussi", "ver também", "см. также", "انظر أيضا", "همچنین ببینید", "参见", "se også", "lihat pula")
	reg(hat("Further information: "), "further", "further information", "details", "more")
	reg(func(r *renderer, c *call) string {
		var links []string
		for _, p := range c.pos {
			if p != "" {
				links = append(links, "[["+p+"]]")
			}
		}
		if len(links) == 0 {
			return ""
		}
		return r.blockPH(`<div class="hatnote">Not to be confused with ` + r.inline(joinAnd(links)) + ".</div>")
	}, "distinguish")
	reg(func(r *renderer, c *call) string {
		if c.arg(0) == "" {
			return ""
		}
		s := "This article is about " + c.arg(0) + "."
		if c.arg(1) != "" && c.arg(2) != "" {
			s += " For " + c.arg(1) + ", see [[" + c.arg(2) + "]]."
		}
		return r.blockPH(`<div class="hatnote">` + r.inline(s) + "</div>")
	}, "about")

	// References in template form.
	reg(func(r *renderer, c *call) string { return r.refListPH(c.get("group"), false) },
		"reflist", "references", "refs", "reference list", "listaref", "referencias", "références", "referências",
		"примечания", "مراجع", "المراجع", "پانویس", "reflist|2", "脚注ヘルプ", "notes", "listaderefs")
	reg(func(r *renderer, c *call) string { return r.refListPH("lower-alpha", false) }, "notelist", "notes list", "notefoot")
	reg(func(r *renderer, c *call) string { return r.refListPH("upper-alpha", false) }, "notelist-ua")
	reg(func(r *renderer, c *call) string {
		return r.addRef("lower-alpha", c.get("name"), r.extractTags(firstNonEmpty(c.arg(0), c.get("text"))), false)
	}, "efn", "efn-la", "note", "nb")
	reg(func(r *renderer, c *call) string {
		return r.addRef("upper-alpha", c.get("name"), r.extractTags(c.arg(0)), false)
	}, "efn-ua")
	reg(func(r *renderer, c *call) string {
		return r.addRef(c.get("group"), c.get("name"), r.extractTags(c.arg(0)), false)
	}, "refn", "ref")
	reg(func(r *renderer, c *call) string {
		// {{sfn|Smith|2001|p=12}}: short footnote
		var who []string
		for _, p := range c.pos {
			if p != "" {
				who = append(who, p)
			}
		}
		if len(who) == 0 {
			return ""
		}
		year := ""
		if len(who) > 1 {
			year = who[len(who)-1]
			who = who[:len(who)-1]
		}
		t := joinAnd(who)
		if year != "" {
			t += " " + year
		}
		if p := c.get("p", "page"); p != "" {
			t += ", p. " + p
		} else if pp := c.get("pp", "pages"); pp != "" {
			t += ", pp. " + pp
		} else if l := c.get("loc"); l != "" {
			t += ", " + l
		}
		t += "."
		return r.addRef("", "sfn:"+t, t, false)
	}, "sfn", "sfnp", "sfnm", "harvnb", "harvp", "harv", "sfnref")
	reg(func(r *renderer, c *call) string {
		var who []string
		for _, p := range c.pos {
			if p != "" {
				who = append(who, p)
			}
		}
		if len(who) > 1 {
			return joinAnd(who[:len(who)-1]) + " (" + who[len(who)-1] + ")"
		}
		return strings.Join(who, "")
	}, "harvtxt", "harvnb text")
	reg(func(r *renderer, c *call) string {
		p := firstNonEmpty(c.arg(0), c.get("p", "page", "pp", "pages"))
		return r.inlinePH(`<sup class="reference">:` + escText(p) + "</sup>")
	}, "rp", "r")
	reg(func(r *renderer, c *call) string { return r.inlinePH(r.cite(c)) },
		"citation", "cite", "harvc", "internetquelle", "literatur", "lien web", "ouvrage", "article", "lien brisé",
		"cita web", "cita libro", "cita publicación", "cita noticia", "citar web", "citar livro", "citar periódico",
		"citar jornal", "книга", "статья", "публикация", "cite web", "استشهاد بويب", "استشهاد بكتاب", "یادکرد وب", "یادکرد کتاب")
	reg(func(r *renderer, c *call) string { return "ISBN " + c.arg(0) }, "isbn", "isbnt")
	reg(func(r *renderer, c *call) string {
		d := c.arg(0)
		return r.inlinePH(`<a class="external" href="https://doi.org/` + escAttr(d) + `">doi:` + escText(d) + "</a>")
	}, "doi")
	reg(func(r *renderer, c *call) string {
		u := c.arg(0)
		if u == "" {
			return ""
		}
		if !strings.Contains(u, "://") {
			u = "https://" + u
		}
		label := firstNonEmpty(c.arg(1), strings.TrimPrefix(strings.TrimPrefix(c.arg(0), "https://"), "http://"))
		return r.inlinePH(`<a class="external" rel="nofollow noopener" href="` + escAttr(u) + `">` + r.inline(label) + "</a>")
	}, "url", "official url", "official", "official website", "website", "sitio web", "site officiel", "site oficial", "официальный сайт")

	// Quotations.
	reg(func(r *renderer, c *call) string {
		q := firstNonEmpty(c.get("text", "quote", "quotetext", "1"), c.arg(0))
		who := firstNonEmpty(c.get("author", "sign", "person", "2"), c.arg(1))
		src := firstNonEmpty(c.get("source", "title", "3"), c.arg(2))
		h := `<blockquote class="quote">` + r.fragment(q)
		if who != "" || src != "" {
			h += `<p class="quote-source">— ` + r.inline(strings.Trim(who+", "+src, ", ")) + "</p>"
		}
		return r.blockPH(h + "</blockquote>")
	}, "quote", "blockquote", "cquote", "quote box", "rquote", "quotation", "centered pull quote", "cita", "citation bloc", "début citation", "цитата", "اقتباس", "نقل قول")
	reg(func(r *renderer, c *call) string { return "“" + c.arg(0) + "”" }, "\"", "qq", "quote inline", "qi", "«", "lang-quote")

	// Lists.
	list := func(tag, class string) handler {
		return func(r *renderer, c *call) string {
			var items []string
			body := ""
			for _, p := range c.pos {
				if strings.Contains(p, "\n*") || strings.HasPrefix(strings.TrimSpace(p), "*") {
					body += p
					continue
				}
				if p != "" {
					items = append(items, "<li>"+r.fragment(p)+"</li>")
				}
			}
			if body != "" {
				h := r.blocks(body)
				return r.blockPH(`<div class="` + class + `">` + h + "</div>")
			}
			if len(items) == 0 {
				return ""
			}
			return r.inlinePH(`<` + tag + ` class="` + class + `">` + strings.Join(items, "") + "</" + tag + ">")
		}
	}
	reg(list("ul", "plainlist"), "plainlist", "plain list", "unbulleted list", "ubl", "ubil", "unbulleted indent list", "startplainlist")
	reg(list("ul", "hlist"), "flatlist", "hlist", "flat list", "horizontal list", "cslist", "comma separated entries")
	reg(list("ul", "bulleted"), "bulleted list", "bulletlist", "collapsible list", "columns-list", "div col list")
	reg(list("ol", "ordered"), "ordered list")

	// Numbers, dates and measures.
	reg(func(r *renderer, c *call) string { return formatNum(c.arg(0)) }, "formatnum", "nts", "ntsh", "num", "number")
	reg(func(r *renderer, c *call) string {
		switch len(c.pos) {
		case 1:
			return "1⁄" + c.arg(0)
		case 2:
			return c.arg(0) + "⁄" + c.arg(1)
		case 3:
			return c.arg(0) + " " + c.arg(1) + "⁄" + c.arg(2)
		}
		return ""
	}, "frac", "fraction", "sfrac")
	reg(func(r *renderer, c *call) string { return "c. " + c.arg(0) }, "circa", "c.", "ca.")
	reg(func(r *renderer, c *call) string { return "fl. " + c.arg(0) }, "floruit", "fl.", "fl")
	reg(func(r *renderer, c *call) string { return "r. " + strings.Join(nonEmpty(c.pos), "–") }, "reign", "r.")
	reg(func(r *renderer, c *call) string { return "As of " + dateText(c.pos, c) }, "as of")
	reg(func(r *renderer, c *call) string { return dateText(c.pos, c) }, "date", "start date", "end date", "dts",
		"birth date", "death date", "film date", "release date", "fecha", "date de naissance", "data de nascimento", "дата рождения")
	reg(func(r *renderer, c *call) string {
		d := dateText(c.pos, c)
		if age, ok := ageOf(c.pos, r.ctx.Now); ok {
			return d + " (age&nbsp;" + strconv.Itoa(age) + ")"
		}
		return d
	}, "birth date and age", "bda", "birth date and age2", "start date and age", "fecha de inicio y edad")
	reg(func(r *renderer, c *call) string {
		if len(c.pos) < 6 {
			return dateText(c.pos, c)
		}
		d := dateText(c.pos[:3], c)
		y1, _ := strconv.Atoi(c.pos[3])
		m1, _ := strconv.Atoi(c.pos[4])
		d1, _ := strconv.Atoi(c.pos[5])
		if age, ok := ageOf(c.pos[3:6], time.Date(atoiOr(c.pos[0]), time.Month(atoiOr(c.pos[1])), atoiOr(c.pos[2]), 0, 0, 0, 0, time.UTC)); ok && y1 > 0 && m1 > 0 && d1 > 0 {
			return d + " (aged&nbsp;" + strconv.Itoa(age) + ")"
		}
		return d
	}, "death date and age", "dda")
	reg(func(r *renderer, c *call) string {
		y := atoiOr(c.arg(0))
		if y == 0 {
			return c.arg(0)
		}
		return c.arg(0) + " (age&nbsp;" + strconv.Itoa(r.ctx.Now.Year()-y-1) + "–" + strconv.Itoa(r.ctx.Now.Year()-y) + ")"
	}, "birth year and age", "byam")
	reg(func(r *renderer, c *call) string {
		if age, ok := ageOf(c.pos, r.ctx.Now); ok {
			return strconv.Itoa(age)
		}
		return ""
	}, "age", "age in years", "years ago", "edad")
	reg(func(r *renderer, c *call) string {
		years := nonEmpty(c.pos[min(1, len(c.pos)):])
		if len(years) == 0 {
			return c.arg(0)
		}
		return c.arg(0) + " (m.&nbsp;" + strings.Join(years, "–") + ")<br>"
	},
		"marriage", "married")
	reg(func(r *renderer, c *call) string {
		name := c.arg(0)
		if len(c.pos) >= 2 {
			name = "[[" + c.arg(0) + " " + c.arg(1) + "]]"
		}
		return name
	}, "sortname")
	reg(func(r *renderer, c *call) string { return firstNonEmpty(c.arg(1), c.arg(0)) }, "sort", "sortkey", "hs", "ntsh2")
	reg(convert, "convert", "cvt", "convertir", "conversion", "unité", "conv")
	reg(coord, "coord", "coor", "coordinates", "coor dms", "coordinate", "coord-nolink", "coordenadas", "координаты")
	reg(func(r *renderer, c *call) string {
		v := c.arg(0)
		if e := c.get("e"); e != "" {
			v += "×10<sup>" + e + "</sup>"
		}
		if u := firstNonEmpty(c.get("u", "ul"), c.arg(1)); u != "" {
			v += "&nbsp;" + u
		}
		return v
	}, "val")
	reg(text("▲"), "increase", "up", "gain")
	reg(text("▼"), "decrease", "down", "loss")
	reg(text("▬"), "steady", "nochange")

	// Table cells: these expand to "attributes | text".
	cellT := func(bg, label string) handler {
		return func(r *renderer, c *call) string {
			t := firstNonEmpty(c.arg(0), label)
			return `style="background:` + bg + `;text-align:center" | ` + t
		}
	}
	reg(cellT("#9EFF9E", "Yes"), "yes", "y", "yes2", "won", "sí", "oui", "sim", "да")
	reg(cellT("#FFC7C7", "No"), "no", "n", "no2", "lost", "non", "não", "нет")
	reg(cellT("#FFB", "Partial"), "partial", "some", "maybe", "partially")
	reg(cellT("#ececec", "N/A"), "n/a", "na", "n.a.", "nonapplicable")
	reg(cellT("#ececec", "TBA"), "tba", "tbd", "unknown", "dunno", "?")

	// Flags and countries: the name, not the picture.
	reg(drop, "flagicon", "flag icon", "flagicon image", "bandera", "drapeau", "bandeira", "флагификация")
	reg(func(r *renderer, c *call) string { return "[[" + c.arg(0) + "]]" }, "flag", "flagcountry", "flagu", "flag country", "flagathlete", "flagdeco", "bandera2", "pays", "país", "drapeau2")

	// Images in template form.
	reg(func(r *renderer, c *call) string {
		var items []string
		for i := 1; i <= 12; i++ {
			n := strconv.Itoa(i)
			img := c.named["image"+n]
			if img == "" {
				continue
			}
			cap := c.named["caption"+n]
			items = append(items, `<figure class="gallery-item">`+r.picture(img, 180, cap)+"<figcaption>"+r.inline(cap)+"</figcaption></figure>")
		}
		if len(items) == 0 {
			return ""
		}
		h := `<div class="gallery">` + strings.Join(items, "")
		if t := c.get("footer"); t != "" {
			h += `<div class="gallery-footer">` + r.inline(t) + "</div>"
		}
		return r.blockPH(h + "</div>")
	}, "multiple image", "image array", "gallery", "imagen múltiple", "image multiple", "imagem múltipla")
	reg(func(r *renderer, c *call) string {
		return r.file(c.arg(0), []string{"thumb", "center", c.arg(2)})
	}, "wide image", "panorama", "largeimage")
	reg(func(r *renderer, c *call) string {
		img := firstNonEmpty(c.get("image"), c.arg(0))
		if img == "" {
			return ""
		}
		return r.inlinePH(r.picture(img, atoiOr(c.get("bsize")), ""))
	}, "css image crop", "image crop")
	reg(drop, "infobox mapframe", "maplink", "location map", "location map+", "location map many", "osm location map", "mapframe")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func nonEmpty(vs []string) []string {
	var out []string
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " and " + xs[1]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + ", and " + xs[len(xs)-1]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func atoiOr(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// dateText formats Y|M|D parameters ("1990|5|17" -> "17 May 1990"),
// day-first when the template asks for it (df=y) and month-first
// otherwise, as English Wikipedia does.
func dateText(pos []string, c *call) string {
	ps := nonEmpty(pos)
	var nums []int
	for _, p := range ps {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return strings.Join(ps, " ")
	}
	y := nums[0]
	if len(nums) == 1 || nums[1] < 1 || nums[1] > 12 {
		return strconv.Itoa(y)
	}
	m := time.Month(nums[1]).String()
	if len(nums) == 2 {
		return m + " " + strconv.Itoa(y)
	}
	df := c != nil && c.get("df") != "" // day first only when asked (df=y)
	if df {
		return strconv.Itoa(nums[2]) + " " + m + " " + strconv.Itoa(y)
	}
	return m + " " + strconv.Itoa(nums[2]) + ", " + strconv.Itoa(y)
}

func ageOf(pos []string, now time.Time) (int, bool) {
	ps := nonEmpty(pos)
	if len(ps) == 0 {
		return 0, false
	}
	y := atoiOr(ps[0])
	if y == 0 {
		return 0, false
	}
	m, d := 1, 1
	if len(ps) > 1 {
		m = atoiOr(ps[1])
	}
	if len(ps) > 2 {
		d = atoiOr(ps[2])
	}
	age := now.Year() - y
	if int(now.Month()) < m || (int(now.Month()) == m && now.Day() < d) {
		age--
	}
	return age, age >= 0 && age < 200
}

// --- {{convert}} ---------------------------------------------------------

type unit struct {
	sym    string
	to     string  // default target unit
	factor float64 // to SI-ish base
	offset float64
	kind   string
}

var units = map[string]unit{
	"km": {"km", "mi", 1000, 0, "len"}, "m": {"m", "ft", 1, 0, "len"}, "cm": {"cm", "in", 0.01, 0, "len"},
	"mm": {"mm", "in", 0.001, 0, "len"}, "mi": {"mi", "km", 1609.344, 0, "len"}, "ft": {"ft", "m", 0.3048, 0, "len"},
	"in": {"in", "cm", 0.0254, 0, "len"}, "yd": {"yd", "m", 0.9144, 0, "len"}, "nmi": {"nmi", "km", 1852, 0, "len"},
	"km2": {"km²", "sqmi", 1e6, 0, "area"}, "sqkm": {"km²", "sqmi", 1e6, 0, "area"}, "sqmi": {"sq mi", "km2", 2589988.11, 0, "area"},
	"mi2": {"sq mi", "km2", 2589988.11, 0, "area"}, "ha": {"ha", "acre", 1e4, 0, "area"}, "acre": {"acres", "ha", 4046.856, 0, "area"},
	"m2": {"m²", "sqft", 1, 0, "area"}, "sqft": {"sq ft", "m2", 0.09290304, 0, "area"},
	"kg": {"kg", "lb", 1, 0, "mass"}, "g": {"g", "oz", 0.001, 0, "mass"}, "lb": {"lb", "kg", 0.45359237, 0, "mass"},
	"oz": {"oz", "g", 0.028349523, 0, "mass"}, "t": {"t", "ST", 1000, 0, "mass"}, "ST": {"short tons", "t", 907.18474, 0, "mass"},
	"C": {"°C", "F", 1, 273.15, "temp"}, "F": {"°F", "C", 5.0 / 9, 255.372222, "temp"}, "K": {"K", "C", 1, 0, "temp"},
	"km/h": {"km/h", "mph", 1 / 3.6, 0, "speed"}, "mph": {"mph", "km/h", 0.44704, 0, "speed"}, "kn": {"kn", "km/h", 0.514444, 0, "speed"},
	"m/s": {"m/s", "ft/s", 1, 0, "speed"}, "ft/s": {"ft/s", "m/s", 0.3048, 0, "speed"},
	"l": {"L", "USgal", 0.001, 0, "vol"}, "L": {"L", "USgal", 0.001, 0, "vol"}, "USgal": {"US gal", "L", 0.003785412, 0, "vol"},
	"m3": {"m³", "cuft", 1, 0, "vol"}, "cuft": {"cu ft", "m3", 0.0283168, 0, "vol"},
}

func convert(r *renderer, c *call) string {
	if len(c.pos) < 2 {
		return strings.Join(c.pos, " ")
	}
	vals := []string{c.pos[0]}
	i := 1
	sep := ""
	for i+1 < len(c.pos) {
		switch c.pos[i] {
		case "to", "-", "–", "and", "or", "by", "x", "×", "+/-", "to(-)":
			sep = c.pos[i]
			vals = append(vals, c.pos[i+1])
			i += 2
			continue
		}
		break
	}
	if i >= len(c.pos) {
		return strings.Join(c.pos, " ")
	}
	from := c.pos[i]
	u, ok := units[from]
	fromSym := from
	if ok {
		fromSym = u.sym
	}
	if sep == "-" {
		sep = "–"
	}
	if sep == "" {
		sep = " "
	} else {
		sep = " " + sep + " "
	}
	in := strings.Join(vals, sep) + "&nbsp;" + fromSym
	if !ok {
		return in
	}
	to := u.to
	if i+1 < len(c.pos) && c.pos[i+1] != "" {
		to = strings.Fields(c.pos[i+1])[0]
	}
	tu, ok := units[to]
	if !ok || tu.kind != u.kind {
		return in
	}
	var outs []string
	for _, v := range vals {
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
		if err != nil {
			return in
		}
		base := f*u.factor + u.offset
		res := (base - tu.offset) / tu.factor
		outs = append(outs, roundLike(res, v))
	}
	return in + " (" + strings.Join(outs, sep) + "&nbsp;" + tu.sym + ")"
}

// roundLike rounds a converted value to a sensible precision.
func roundLike(v float64, orig string) string {
	digits := 0
	if i := strings.IndexByte(orig, '.'); i >= 0 {
		digits = len(orig) - i - 1
	}
	if math.Abs(v) < 10 && digits < 1 {
		digits = 1
	}
	s := strconv.FormatFloat(v, 'f', digits, 64)
	if digits > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return formatNum(s)
}

// --- {{coord}} -----------------------------------------------------------

func coord(r *renderer, c *call) string {
	var nums []float64
	var hemis []string
	for _, p := range c.pos {
		p = strings.TrimSpace(p)
		switch strings.ToUpper(p) {
		case "N", "S", "E", "W", "O":
			hemis = append(hemis, strings.ToUpper(p))
			continue
		}
		if strings.Contains(p, ":") {
			continue // "type:city", "region:..."
		}
		if f, err := strconv.ParseFloat(p, 64); err == nil {
			nums = append(nums, f)
		}
	}
	var lat, lon float64
	switch {
	case len(hemis) == 2 && len(nums) >= 2:
		k := len(nums) / 2
		lat, lon = dms(nums[:k]), dms(nums[k:])
		if hemis[0] == "S" {
			lat = -lat
		}
		if hemis[1] == "W" || hemis[1] == "O" {
			lon = -lon
		}
	case len(nums) >= 2:
		lat, lon = nums[0], nums[1]
	default:
		return ""
	}
	label := fmtDMS(lat, "N", "S") + " " + fmtDMS(lon, "E", "W")
	href := fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.5f&mlon=%.5f#map=10/%.5f/%.5f", lat, lon, lat, lon)
	h := `<a class="external coord" rel="noopener" href="` + escAttr(href) + `">` + label + "</a>"
	if strings.Contains(c.get("display"), "title") && !strings.Contains(c.get("display"), "inline") {
		return r.blockPH(`<div class="coord-title">` + h + "</div>")
	}
	return r.inlinePH(h)
}

func dms(p []float64) float64 {
	v := 0.0
	for i, x := range p {
		v += x / math.Pow(60, float64(i))
	}
	return v
}

func fmtDMS(v float64, pos, neg string) string {
	h := pos
	if v < 0 {
		h, v = neg, -v
	}
	d := math.Floor(v)
	m := math.Floor((v - d) * 60)
	s := math.Round(((v-d)*60 - m) * 60)
	if s == 60 {
		s, m = 0, m+1
	}
	return fmt.Sprintf("%.0f°%02.0f′%02.0f″%s", d, m, s, h)
}
