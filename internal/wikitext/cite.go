package wikitext

import (
	"strconv"
	"strings"
)

// Parameter names of citation templates in the languages NABU offers.
var citeKeys = map[string][]string{
	"title":     {"title", "titel", "titre", "título", "titolo", "заглавие", "название", "عنوان", "العنوان", "judul", "titulo"},
	"url":       {"url", "chapter-url", "chapterurl", "ссылка", "مسار", "نشانی", "URL"},
	"author":    {"author", "authors", "autor", "auteur", "autore", "автор", "مؤلف", "المؤلف", "نویسنده", "penulis", "vauthors", "people", "host"},
	"last":      {"last", "last1", "surname", "surname1", "apellido", "apellidos", "nom", "nachname", "sobrenome", "cognome", "الأخير", "نام خانوادگی"},
	"first":     {"first", "first1", "given", "given1", "nombre", "prénom", "vorname", "nome", "الأول", "نام"},
	"date":      {"date", "datum", "fecha", "data", "дата", "تاريخ", "التاريخ", "تاریخ", "tanggal", "publication-date"},
	"year":      {"year", "jahr", "année", "año", "anno", "ano", "год", "سنة", "سال", "tahun"},
	"work":      {"work", "website", "newspaper", "journal", "magazine", "periodical", "encyclopedia", "encyclopaedia", "werk", "site", "obra", "sito", "publicado", "périodique", "journal", "издание", "عمل", "الموقع", "وبگاه", "situs"},
	"publisher": {"publisher", "hrsg", "verlag", "éditeur", "editorial", "editore", "editora", "издательство", "الناشر", "ناشر", "penerbit", "agency", "via"},
	"location":  {"location", "place", "ort", "lieu", "ubicación", "lugar", "città", "local", "место", "المكان", "مکان", "lokasi"},
	"volume":    {"volume", "band", "tome", "volumen", "том", "المجلد", "جلد"},
	"issue":     {"issue", "number", "nummer", "numéro", "número", "numero", "номер", "العدد", "شماره"},
	"pages":     {"pages", "page", "pp", "p", "seiten", "seite", "páginas", "página", "pagine", "страницы", "الصفحات", "صفحه", "halaman", "at"},
	"isbn":      {"isbn", "ISBN"},
	"doi":       {"doi"},
	"accessed":  {"access-date", "accessdate", "abruf", "zugriff", "consulté le", "fechaacceso", "accesso", "acessodata", "access_date", "дата обращения", "تاريخ الوصول", "تاریخ بازدید", "tanggal-akses"},
	"archive":   {"archive-url", "archiveurl", "archiv-url", "archive-url ", "مسار الأرشيف", "archivo-url", "arquivourl"},
	"archived":  {"archive-date", "archivedate", "archiv-datum", "fechaarchivo", "arquivodata", "تاريخ الأرشيف"},
	"chapter":   {"chapter", "kapitel", "chapitre", "capítulo", "capitolo", "contribution", "entry", "глава"},
	"edition":   {"edition", "auflage", "édition", "edición", "edizione", "edição"},
	"language":  {"language", "lang", "sprache", "langue", "idioma", "lingua", "язык", "اللغة", "زبان"},
	"quote":     {"quote", "zitat", "citation", "cita", "citação", "цитата"},
}

func (c *call) cite(k string) string {
	for _, key := range citeKeys[k] {
		if v := strings.TrimSpace(c.named[strings.ToLower(key)]); v != "" {
			return v
		}
	}
	return ""
}

// cite formats any citation template in a compact, readable style:
// Authors (date). "Title". Work. Place: Publisher. p. X. ISBN. Retrieved.
func (r *renderer) cite(c *call) string {
	isBook := strings.Contains(c.name, "book") || strings.Contains(c.name, "libro") || strings.Contains(c.name, "livro") ||
		c.name == "literatur" || c.name == "ouvrage" || c.name == "книга" || strings.Contains(c.name, "کتاب") || strings.Contains(c.name, "بكتاب")
	var authors []string
	if a := c.cite("author"); a != "" {
		authors = append(authors, a)
	}
	for i := 1; i <= 9; i++ {
		n := strconv.Itoa(i)
		last := c.get("last"+n, "surname"+n, "author"+n)
		if i == 1 && last == "" {
			last = c.cite("last")
		}
		if last == "" {
			continue
		}
		first := c.get("first" + n)
		if i == 1 && first == "" {
			first = c.cite("first")
		}
		if first != "" {
			last += ", " + first
		}
		authors = append(authors, last)
	}
	authors = dedupe(authors)
	if len(authors) > 4 {
		authors = append(authors[:3], "et al.")
	}
	var parts []string
	if len(authors) > 0 {
		parts = append(parts, r.inline(strings.Join(authors, "; ")))
	}
	if d := firstNonEmpty(c.cite("date"), c.cite("year")); d != "" {
		if len(parts) > 0 {
			parts[len(parts)-1] += " (" + r.inline(d) + ")"
		} else {
			parts = append(parts, "("+r.inline(d)+")")
		}
	}
	title := c.cite("title")
	if title == "" && len(c.pos) > 0 {
		title = c.pos[0]
	}
	if ch := c.cite("chapter"); ch != "" {
		parts = append(parts, "“"+ch+"”. In")
	}
	if title != "" {
		t := r.inline(title)
		if isBook || c.cite("work") == "" && c.cite("chapter") != "" {
			t = "<i>" + t + "</i>"
		} else {
			t = "“" + t + "”"
		}
		if u := c.cite("url"); u != "" && (strings.HasPrefix(u, "http") || strings.HasPrefix(u, "//")) {
			t = `<a class="external" rel="nofollow noopener" href="` + escAttr(u) + `">` + t + "</a>"
		}
		parts = append(parts, t)
	}
	if e := c.cite("edition"); e != "" {
		parts = append(parts, e+" ed.")
	}
	if w := c.cite("work"); w != "" {
		v := "<i>" + r.inline(w) + "</i>"
		if vol := c.cite("volume"); vol != "" {
			v += " <b>" + r.inline(vol) + "</b>"
		}
		if is := c.cite("issue"); is != "" {
			v += " (" + r.inline(is) + ")"
		}
		parts = append(parts, v)
	}
	pub := c.cite("publisher")
	if loc := c.cite("location"); loc != "" && pub != "" {
		pub = loc + ": " + pub
	} else if loc != "" {
		pub = loc
	}
	if pub != "" {
		parts = append(parts, r.inline(pub))
	}
	if p := c.cite("pages"); p != "" {
		if strings.ContainsAny(p, "–-,") {
			parts = append(parts, "pp. "+r.inline(p))
		} else {
			parts = append(parts, "p. "+r.inline(p))
		}
	}
	if l := c.cite("language"); l != "" {
		parts = append(parts, "(in "+r.inline(l)+")")
	}
	if i := c.cite("isbn"); i != "" {
		parts = append(parts, "ISBN "+r.inline(i))
	}
	if d := c.cite("doi"); d != "" {
		parts = append(parts, `<a class="external" href="https://doi.org/`+escAttr(d)+`">doi:`+r.inline(d)+"</a>")
	}
	if a := c.cite("archive"); a != "" {
		s := `<a class="external" rel="nofollow noopener" href="` + escAttr(a) + `">Archived</a>`
		if d := c.cite("archived"); d != "" {
			s += " " + r.inline(d)
		}
		parts = append(parts, s)
	}
	if d := c.cite("accessed"); d != "" {
		parts = append(parts, "Retrieved "+r.inline(d))
	}
	out := strings.Join(parts, ". ")
	out = strings.ReplaceAll(out, ". In. ", ". In ")
	if out != "" && !strings.HasSuffix(out, ".") {
		out += "."
	}
	if q := c.cite("quote"); q != "" {
		out += " “" + r.inline(q) + "”"
	}
	return `<cite class="citation">` + out + "</cite>"
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
