package fulltext

import "strings"

// forms lists the forms of a word searched with it: the word itself, and
// its plural or singular where the language makes them by adding an
// ending ("bird", "birds"; "city", "cities"; "box", "boxes"). Only forms
// in the index are used, so a wrong guess costs nothing. Short words and
// abbreviations are searched as typed.
func (ix *Index) forms(t string) []string {
	out := []string{t}
	if len(t) < 4 || !isASCII(t) && !strings.ContainsAny(t, "abcdefghijklmnopqrstuvwxyz") {
		return out
	}
	lang := ix.Meta.Lang
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	add := func(f string) {
		if len(f) >= 3 && f != t {
			out = append(out, f)
		}
	}
	switch lang {
	case "en", "simple":
		switch {
		case strings.HasSuffix(t, "ies"):
			add(t[:len(t)-3] + "y")
		case strings.HasSuffix(t, "oes"):
			add(t[:len(t)-2])
			add(t[:len(t)-1])
		case strings.HasSuffix(t, "ches") || strings.HasSuffix(t, "shes") || strings.HasSuffix(t, "sses") || strings.HasSuffix(t, "xes") || strings.HasSuffix(t, "zes"):
			add(t[:len(t)-2])
		case strings.HasSuffix(t, "ss") || strings.HasSuffix(t, "us") || strings.HasSuffix(t, "is"):
			add(t + "es")
		case strings.HasSuffix(t, "s"):
			add(t[:len(t)-1])
		case strings.HasSuffix(t, "y") && !strings.ContainsAny(t[len(t)-2:len(t)-1], "aeiou"):
			add(t[:len(t)-1] + "ies")
		case strings.HasSuffix(t, "ch") || strings.HasSuffix(t, "sh") || strings.HasSuffix(t, "x") || strings.HasSuffix(t, "z"):
			add(t + "es")
		case strings.HasSuffix(t, "o"):
			add(t + "es")
			add(t + "s")
		default:
			add(t + "s")
		}
	case "fr", "es", "pt", "it", "ca", "nl", "da", "sv", "no", "nb":
		// plurals by -s (and French -x)
		switch {
		case strings.HasSuffix(t, "s") || strings.HasSuffix(t, "x") && lang == "fr":
			add(t[:len(t)-1])
		default:
			add(t + "s")
		}
	}
	return out
}

// correct finds the word of the index one letter away from w (a letter
// missing, extra, wrong or two swapped) that is in the most articles,
// for "did you mean". Only for words in Latin letters.
func (ix *Index) correct(w string) string {
	if len(w) < 4 || len(w) > 24 {
		return ""
	}
	for i := 0; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return ""
		}
	}
	best, bestDF := "", uint32(0)
	try := func(c string) {
		if c == w {
			return
		}
		if row, ok, err := ix.lookup(c); err == nil && ok && row.df > bestDF && !ix.common[c] {
			best, bestDF = c, row.df
		}
	}
	for i := 0; i <= len(w); i++ {
		if i < len(w) {
			try(w[:i] + w[i+1:]) // missing
			if i+1 < len(w) {
				try(w[:i] + string(w[i+1]) + string(w[i]) + w[i+2:]) // swapped
			}
		}
		for c := byte('a'); c <= 'z'; c++ {
			try(w[:i] + string(c) + w[i:]) // extra
			if i < len(w) && c != w[i] {
				try(w[:i] + string(c) + w[i+1:]) // wrong
			}
		}
	}
	return best
}
