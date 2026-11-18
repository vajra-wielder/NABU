package server

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/vajra-wielder/nabu/internal/web"
)

// Internet Explorer 11 (still the built-in browser of Windows 7 and 8.1)
// does not understand CSS custom properties, which NABU's stylesheets
// use for colours. For it, NABU serves the same stylesheets with every
// var(--name) replaced by its light-theme value.

var (
	rootBlockRe = regexp.MustCompile(`(?s):root\s*\{([^}]*)\}`)
	cssVarDefRe = regexp.MustCompile(`--([\w-]+)\s*:\s*([^;]+);`)
	cssVarUseRe = regexp.MustCompile(`var\(--([\w-]+)\)`)
	darkBlockRe = regexp.MustCompile(`(?s)@media \(prefers-color-scheme: dark\)\s*\{.*?\n\}`)

	legacyMu  sync.Mutex
	legacyCSS = map[string][]byte{}
)

func isOldIE(r *http.Request) bool {
	ua := r.Header.Get("User-Agent")
	return strings.Contains(ua, "Trident/") || strings.Contains(ua, "MSIE ")
}

// flattenCSS resolves custom properties using the first :root block.
func flattenCSS(css string) string {
	vars := map[string]string{}
	if m := rootBlockRe.FindStringSubmatch(css); m != nil {
		for _, d := range cssVarDefRe.FindAllStringSubmatch(m[1], -1) {
			vars[d[1]] = strings.TrimSpace(d[2])
		}
	}
	css = darkBlockRe.ReplaceAllString(css, "")
	for i := 0; i < 3; i++ { // variables defined via other variables
		css = cssVarUseRe.ReplaceAllStringFunc(css, func(m string) string {
			if v, ok := vars[cssVarUseRe.FindStringSubmatch(m)[1]]; ok {
				return v
			}
			return "inherit"
		})
	}
	return css
}

// serveLegacyCSS answers a stylesheet request from Internet Explorer.
func (s *Server) serveLegacyCSS(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(r.URL.Path, "/nabu/static/")
	if !strings.HasSuffix(name, ".css") || !isOldIE(r) {
		return false
	}
	legacyMu.Lock()
	b, ok := legacyCSS[name]
	if !ok {
		src, err := fs.ReadFile(web.Assets, "assets/"+name)
		if err != nil {
			legacyMu.Unlock()
			return false
		}
		b = []byte(flattenCSS(string(src)))
		legacyCSS[name] = b
	}
	legacyMu.Unlock()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Vary", "User-Agent")
	w.Write(b)
	return true
}
