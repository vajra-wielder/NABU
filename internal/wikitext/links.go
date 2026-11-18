package wikitext

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// interwiki prefixes that lead to other Wikimedia projects (online).
var interwiki = map[string]string{
	"wikt": "wiktionary.org", "wiktionary": "wiktionary.org",
	"w": "wikipedia.org", "wikipedia": "wikipedia.org",
	"s": "wikisource.org", "wikisource": "wikisource.org",
	"q": "wikiquote.org", "wikiquote": "wikiquote.org",
	"b": "wikibooks.org", "wikibooks": "wikibooks.org",
	"n": "wikinews.org", "wikinews": "wikinews.org",
	"v": "wikiversity.org", "wikiversity": "wikiversity.org",
	"voy": "wikivoyage.org", "wikivoyage": "wikivoyage.org",
	"commons": "commons.wikimedia.org", "c": "commons.wikimedia.org",
	"d": "www.wikidata.org", "wikidata": "www.wikidata.org",
	"species": "species.wikimedia.org", "wikispecies": "species.wikimedia.org",
	"m": "meta.wikimedia.org", "meta": "meta.wikimedia.org",
	"mw": "www.mediawiki.org", "foundation": "foundation.wikimedia.org",
}

var langCodeRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]{2,10}){0,2}$`)

// internalLink renders the inside of [[...]]. trail is the run of
// letters right after "]]"; it joins the link text when used.
func (r *renderer) internalLink(inner, trail string) (string, bool) {
	parts := splitTop(inner, '|')
	target := strings.TrimSpace(parts[0])
	colon := strings.HasPrefix(target, ":")
	target = strings.TrimSpace(strings.TrimPrefix(target, ":"))
	if target == "" {
		if len(parts) > 1 {
			return r.inline(strings.Join(parts[1:], "|")), false
		}
		return "", false
	}
	if strings.ContainsAny(target, "{}<>\x01") {
		return r.inline(strings.Join(parts[len(parts)-1:], "")), false
	}
	site := r.ctx.Site

	if i := strings.IndexByte(target, ':'); i > 0 {
		prefix := strings.TrimSpace(target[:i])
		rest := strings.TrimSpace(target[i+1:])
		if ns, ok := site.NamespaceKey(prefix); ok {
			switch {
			case ns == 6 && !colon:
				return r.file(rest, parts[1:]), false
			case ns == 14 && !colon:
				r.out.Categories = append(r.out.Categories, site.Normalize(rest))
				return "", false
			case ns == -2:
				return r.inlinePH(r.onlineLink("https://"+site.Host()+"/wiki/Special:FilePath/"+url.PathEscape(strings.ReplaceAll(rest, " ", "_")), labelOf(parts, rest, r))), false
			}
		} else if host, ok := interwiki[strings.ToLower(prefix)]; ok {
			if !strings.Contains(host, ".wikimedia.org") && !strings.HasPrefix(host, "www.") {
				host = site.Lang + "." + host
			}
			return r.inlinePH(r.onlineLink("https://"+host+"/wiki/"+wikiPath(rest), labelOf(parts, target, r))), false
		} else if langCodeRe.MatchString(strings.ToLower(prefix)) && len(prefix) <= 12 {
			if !colon {
				return "", false // interlanguage link: shown by Wikipedia in the sidebar
			}
			return r.inlinePH(r.onlineLink("https://"+strings.ToLower(prefix)+".wikipedia.org/wiki/"+wikiPath(rest), labelOf(parts, target, r))), false
		}
	}

	page, frag := target, ""
	if i := strings.IndexByte(target, '#'); i >= 0 {
		page, frag = strings.TrimSpace(target[:i]), strings.TrimSpace(target[i+1:])
	}
	var label string
	usedTrail := false
	if len(parts) > 1 {
		l := strings.Join(parts[1:], "|")
		if strings.TrimSpace(l) == "" {
			label = escText(pipeTrick(page))
		} else {
			label = r.inline(l)
		}
	} else {
		label = escText(target)
	}
	if trail != "" {
		label += trail
		usedTrail = true
	}
	var href string
	norm := ""
	if page == "" {
		href = "#" + Anchor(frag)
	} else {
		norm = site.Normalize(page)
		if norm == r.ctx.Title && frag == "" {
			return r.inlinePH(`<strong class="selflink">` + label + "</strong>"), usedTrail
		}
		href = r.ctx.PageURL(norm)
		if frag != "" {
			href += "#" + url.PathEscape(Anchor(frag))
		}
	}
	return r.inlinePH(`<a href="` + escAttr(href) + `" title="` + escAttr(norm) + `">` + label + "</a>"), usedTrail
}

func labelOf(parts []string, def string, r *renderer) string {
	if len(parts) > 1 && strings.TrimSpace(strings.Join(parts[1:], "|")) != "" {
		return r.inline(strings.Join(parts[1:], "|"))
	}
	return escText(def)
}

func (r *renderer) onlineLink(href, label string) string {
	return `<a class="external" rel="noopener" href="` + escAttr(href) + `">` + label + "</a>"
}

func wikiPath(t string) string {
	return url.PathEscape(strings.ReplaceAll(strings.TrimSpace(t), " ", "_"))
}

var parenSuffix = regexp.MustCompile(`\s*\([^()]*\)$`)

// pipeTrick derives the label for [[Paris (Texas)|]] -> "Paris".
func pipeTrick(t string) string {
	if i := strings.IndexByte(t, ':'); i > 0 {
		t = t[i+1:]
	}
	if i := strings.IndexByte(t, ','); i > 0 {
		t = t[:i]
	}
	return strings.TrimSpace(parenSuffix.ReplaceAllString(t, ""))
}

// splitTop splits s at sep, ignoring separators nested in [[ ]], {{ }}
// or placeholders.
func splitTop(s string, sep byte) []string {
	var parts []string
	depthSq, depthBr := 0, 0
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '[' && i+1 < len(s) && s[i+1] == '[':
			depthSq++
			i++
		case c == ']' && i+1 < len(s) && s[i+1] == ']' && depthSq > 0:
			depthSq--
			i++
		case c == '{' && i+1 < len(s) && s[i+1] == '{':
			depthBr++
			i++
		case c == '}' && i+1 < len(s) && s[i+1] == '}' && depthBr > 0:
			depthBr--
			i++
		case c == sep && depthSq == 0 && depthBr == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// Image option keywords, in English and the languages NABU offers.
var imageWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`thumb thumbnail frame framed frameless border left right center centre none
		upright baseline middle sub super text-top text-bottom top bottom
		mini miniatur hochkant links rechts zentriert ohne rahmenlos rahmen
		vignette gauche droite centré néant sans_cadre cadre redresse encadré
		miniatura miniaturadeimagen izquierda derecha centro ninguno sinmarco marco enmarcado vertical
		miniaturadaimagem esquerda direita nenhum semmoldura moldura
		мини миниатюра слева справа центр безрамки обрамить рамка сверху
		缩略图 縮圖 左 右 居中 无 无框 有框 边框
		مصغر تصغير يسار يمين مركز لاشيء بدون إطار إطار
		بندانگشتی چپ راست وسط هیچ قاب بیقاب
		miniature venstre højre centreret ingen ramme
		jmpl kiri kanan tengah tiada bingkai`) {
		imageWords[w] = true
	}
}

var sizeRe = regexp.MustCompile(`^(\d*)(?:x(\d+))?\s*\pL*$`)

func isImageOption(p string) bool {
	l := strings.ToLower(strings.TrimSpace(p))
	if imageWords[l] {
		return true
	}
	if i := strings.IndexByte(l, '='); i > 0 && i < 16 {
		return true // alt=, link=, upright=, class=, lang=, page=, ...
	}
	if sizeRe.MatchString(l) && strings.ContainsAny(l, "0123456789") {
		return true
	}
	return false
}

var mediaExt = regexp.MustCompile(`(?i)\.(ogg|oga|ogv|webm|mp3|mp4|wav|flac|mid|midi|opus)$`)

// file renders [[File:Name.jpg|thumb|...|caption]].
func (r *renderer) file(name string, opts []string) string {
	thumb, align, width, upright := false, "", 0, 0.0
	frameless := false
	caption := ""
	for _, o := range opts {
		t := strings.TrimSpace(o)
		l := strings.ToLower(t)
		switch {
		case l == "thumb" || l == "thumbnail" || l == "frame" || l == "framed" || l == "mini" || l == "miniatur" ||
			l == "vignette" || l == "miniatura" || l == "мини" || l == "миниатюра" || l == "缩略图" || l == "縮圖" ||
			l == "مصغر" || l == "تصغير" || l == "بندانگشتی" || l == "miniature" || l == "jmpl" || l == "miniaturadeimagen" || l == "miniaturadaimagem":
			thumb = true
		case l == "frameless" || l == "rahmenlos" || l == "sans_cadre" || l == "sinmarco" || l == "безрамки":
			frameless = true
		case l == "left" || l == "links" || l == "gauche" || l == "izquierda" || l == "esquerda" || l == "слева" || l == "左" || l == "يسار" || l == "چپ" || l == "venstre" || l == "kiri":
			align = "left"
		case l == "right" || l == "rechts" || l == "droite" || l == "derecha" || l == "direita" || l == "справа" || l == "右" || l == "يمين" || l == "راست" || l == "højre" || l == "kanan":
			align = "right"
		case l == "center" || l == "centre" || l == "zentriert" || l == "centré" || l == "centro" || l == "центр" || l == "居中" || l == "مركز" || l == "وسط" || l == "centreret" || l == "tengah":
			align = "center"
		case l == "none" || l == "ohne" || l == "néant" || l == "ninguno" || l == "nenhum" || l == "无" || l == "ingen" || l == "tiada":
			align = "none"
		case strings.HasPrefix(l, "upright") || strings.HasPrefix(l, "hochkant") || strings.HasPrefix(l, "redresse") || strings.HasPrefix(l, "vertical"):
			upright = 0.75
			if i := strings.IndexByte(l, '='); i > 0 {
				if f, err := strconv.ParseFloat(strings.TrimSpace(l[i+1:]), 64); err == nil && f > 0 && f < 5 {
					upright = f
				}
			}
		case isImageOption(t):
			if m := sizeRe.FindStringSubmatch(l); m != nil && m[1] != "" {
				width, _ = strconv.Atoi(m[1])
			}
		default:
			caption = o
		}
	}
	if !thumb && !frameless && width == 0 {
		width = 0 // natural size: icons, flags
	}
	if thumb || frameless {
		if width == 0 {
			width = 220
			if upright > 0 {
				width = int(220*upright/10+0.5) * 10
			}
		}
	}
	if width > 400 {
		width = 400
	}
	if thumb {
		if align == "" {
			align = "right"
			if r.ctx.Site.RTL() {
				align = "left"
			}
		}
		capHTML := ""
		if strings.TrimSpace(caption) != "" {
			capHTML = "<figcaption>" + r.inline(caption) + "</figcaption>"
		}
		return r.blockPH(`<figure class="thumb t` + align + `" style="width:` + strconv.Itoa(width+8) + `px">` +
			r.picture(name, width, caption) + capHTML + "</figure>")
	}
	if width > 0 && width < 40 {
		return "" // decorative icon or flag
	}
	pic := r.picture(name, width, caption)
	if align == "left" || align == "right" || align == "center" {
		return r.blockPH(`<figure class="float t` + align + `">` + pic + "</figure>")
	}
	return r.inlinePH(pic)
}

// picture is an image fetched from Wikimedia when the computer is
// online. Offline (or with pictures turned off) a labelled frame shows
// where it would be.
func (r *renderer) picture(name string, width int, alt string) string {
	name = strings.TrimSpace(name)
	label := `<span class="pic-ph" title="` + escAttr(name) + `">🖼 ` + escText(name) + "</span>"
	if r.ctx.ImageURL == nil || mediaExt.MatchString(name) {
		if mediaExt.MatchString(name) {
			label = `<span class="pic-ph">🔊 ` + escText(name) + "</span>"
		}
		return label
	}
	w := width
	if w == 0 {
		w = 220
	}
	src := r.ctx.ImageURL(name, w)
	if src == "" {
		return label
	}
	return `<span class="pic"><img src="` + escAttr(src) + `" loading="lazy" decoding="async" alt="` +
		escAttr(plainWiki(alt)) + `" style="max-width:` + strconv.Itoa(w) + `px">` + label + "</span>"
}

var (
	plainLinkRe = regexp.MustCompile(`\[\[(?:[^\]|]*\|)?([^\]]*)\]\]`)
	plainExtRe  = regexp.MustCompile(`\[(?:https?:)?//\S+\s*([^\]]*)\]`)
)

// plainWiki reduces markup to plain text, for alt attributes.
func plainWiki(s string) string {
	s = phRe.ReplaceAllString(s, "")
	for strings.Contains(s, "{{") {
		i := strings.Index(s, "{{")
		j := matchBraces(s, i)
		if j < 0 {
			break
		}
		s = s[:i] + s[j+2:]
	}
	s = plainLinkRe.ReplaceAllString(s, "$1")
	s = plainExtRe.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "''", "")
	return stripTags(s)
}
