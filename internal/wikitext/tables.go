package wikitext

import (
	"regexp"
	"strings"
)

type cell struct {
	header  bool
	attrs   string
	content []string
}

type row struct {
	attrs string
	cells []*cell
}

var attrsOnlyRe = regexp.MustCompile(`^\s*([A-Za-z][\w:-]*\s*(=\s*("[^"]*"|'[^']*'|[^\s"'|]+))?\s*)+$`)

// table renders a {| ... |} block (lines include both delimiters).
func (r *renderer) table(lines []string) string {
	first := strings.TrimSpace(lines[0])
	tableAttrs := sanitizeAttrs(strings.TrimPrefix(first, "{|"))
	var caption []string
	inCaption := false
	var rows []*row
	var cur *cell
	depth := 0

	curRow := func() *row {
		if len(rows) == 0 {
			rows = append(rows, &row{})
		}
		return rows[len(rows)-1]
	}
	addCells := func(text string, header bool) {
		seps := []string{"||"}
		if header {
			seps = []string{"!!", "||"}
		}
		parts := []string{text}
		for _, sep := range seps {
			var next []string
			for _, p := range parts {
				next = append(next, splitTopStr(p, sep)...)
			}
			parts = next
		}
		for _, p := range parts {
			c := &cell{header: header}
			if i := cellAttrSplit(p); i >= 0 {
				c.attrs, p = p[:i], p[i+1:]
			}
			c.content = []string{p}
			curRow().cells = append(curRow().cells, c)
			cur = c
		}
	}

	for _, raw := range lines[1:] {
		t := strings.TrimSpace(raw)
		if depth > 0 {
			// inside a nested table: everything belongs to the cell
			if strings.HasPrefix(t, "{|") {
				depth++
			} else if strings.HasPrefix(t, "|}") {
				depth--
			}
			if cur != nil {
				cur.content = append(cur.content, raw)
			}
			continue
		}
		switch {
		case strings.HasPrefix(t, "|}"):
			goto done
		case strings.HasPrefix(t, "{|"):
			depth = 1
			if cur == nil {
				addCells("", false)
			}
			cur.content = append(cur.content, raw)
		case strings.HasPrefix(t, "|+"):
			caption = []string{t[2:]}
			if i := cellAttrSplit(caption[0]); i >= 0 {
				caption[0] = caption[0][i+1:]
			}
			inCaption = true
			cur = nil
		case strings.HasPrefix(t, "|-"):
			rows = append(rows, &row{attrs: strings.TrimLeft(t[2:], "-")})
			cur, inCaption = nil, false
		case strings.HasPrefix(t, "!"):
			inCaption = false
			addCells(t[1:], true)
		case strings.HasPrefix(t, "|"):
			inCaption = false
			addCells(t[1:], false)
		default:
			switch {
			case inCaption:
				caption = append(caption, raw)
			case cur != nil:
				cur.content = append(cur.content, raw)
			}
		}
	}
done:
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table` + tableAttrs + ">")
	if len(caption) > 0 {
		b.WriteString("<caption>" + r.fragment(strings.Join(caption, "\n")) + "</caption>")
	}
	b.WriteString("<tbody>")
	for _, rw := range rows {
		if len(rw.cells) == 0 {
			continue
		}
		b.WriteString("<tr" + sanitizeAttrs(rw.attrs) + ">")
		for _, c := range rw.cells {
			tag := "td"
			if c.header {
				tag = "th"
			}
			b.WriteString("<" + tag + sanitizeAttrs(c.attrs) + ">" + r.fragment(strings.Join(c.content, "\n")) + "</" + tag + ">")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>\n")
	return b.String()
}

// cellAttrSplit finds the "|" separating cell attributes from content
// ("style="x" | text"), or -1.
func cellAttrSplit(s string) int {
	parts := splitTop(s, '|')
	if len(parts) < 2 {
		return -1
	}
	if strings.TrimSpace(parts[0]) == "" || !attrsOnlyRe.MatchString(parts[0]) || strings.Contains(parts[0], "\x01") {
		return -1
	}
	return len(parts[0])
}

// splitTopStr splits at a two-character separator outside [[ ]].
func splitTopStr(s, sep string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s)-1; i++ {
		switch {
		case s[i] == '[' && s[i+1] == '[':
			depth++
			i++
		case s[i] == ']' && s[i+1] == ']' && depth > 0:
			depth--
			i++
		case depth == 0 && s[i:i+2] == sep:
			parts = append(parts, s[start:i])
			start = i + 2
			i++
		}
	}
	return append(parts, s[start:])
}
