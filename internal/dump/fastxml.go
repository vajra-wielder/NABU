package dump

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

// fastPage reads a <page>...</page> of a dump without encoding/xml, which
// took a third of the time of reading a bundle: dumps always write a page
// the same way (title, ns, id, maybe redirect, revision with its
// timestamp and text). Anything else (CDATA, a missing element) is left
// to encoding/xml: ok is false.
func fastPage(chunk []byte) (p *Page, ok bool) {
	if bytes.Contains(chunk, []byte("<![CDATA[")) {
		return nil, false
	}
	elem := func(b []byte, name string) ([]byte, bool) {
		open := "<" + name + ">"
		i := bytes.Index(b, []byte(open))
		if i < 0 {
			return nil, false
		}
		i += len(open)
		j := bytes.Index(b[i:], []byte("</"+name+">"))
		if j < 0 {
			return nil, false
		}
		return b[i : i+j], true
	}
	title, ok1 := elem(chunk, "title")
	nsb, ok2 := elem(chunk, "ns")
	idb, ok3 := elem(chunk, "id")
	if !ok1 || !ok2 || !ok3 {
		return nil, false
	}
	ns, err1 := strconv.Atoi(string(bytes.TrimSpace(nsb)))
	id, err2 := strconv.ParseUint(string(bytes.TrimSpace(idb)), 10, 64)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	p = &Page{NS: ns, ID: id}
	if p.Title, ok = unescapeXML(title); !ok {
		return nil, false
	}
	if r := bytes.Index(chunk, []byte("<redirect")); r >= 0 {
		rest := chunk[r:]
		a := bytes.Index(rest, []byte(`title="`))
		gt := bytes.IndexByte(rest, '>')
		if a < 0 || gt < a {
			return nil, false
		}
		a += len(`title="`)
		q := bytes.IndexByte(rest[a:], '"')
		if q < 0 {
			return nil, false
		}
		if p.Redirect, ok = unescapeXML(rest[a : a+q]); !ok {
			return nil, false
		}
	}
	rev := bytes.Index(chunk, []byte("<revision>"))
	if rev < 0 {
		return nil, false
	}
	revb := chunk[rev:]
	if ts, ok := elem(revb, "timestamp"); ok {
		p.Timestamp = string(ts)
	}
	t := bytes.Index(revb, []byte("<text"))
	if t < 0 {
		return nil, false
	}
	t += len("<text")
	if t >= len(revb) || (revb[t] != ' ' && revb[t] != '>' && revb[t] != '/') {
		return nil, false
	}
	gt := bytes.IndexByte(revb[t:], '>')
	if gt < 0 {
		return nil, false
	}
	gt += t
	if revb[gt-1] == '/' {
		return p, true // <text ... />: empty
	}
	end := bytes.Index(revb[gt+1:], []byte("</text>"))
	if end < 0 {
		return nil, false
	}
	if p.Text, ok = unescapeXML(revb[gt+1 : gt+1+end]); !ok {
		return nil, false
	}
	return p, true
}

// unescapeXML decodes character data as encoding/xml does: the five
// entities and character references, \r\n and \r as \n. Anything else
// (another entity, a stray "<") is not ok.
func unescapeXML(b []byte) (string, bool) {
	if bytes.IndexByte(b, '&') < 0 && bytes.IndexByte(b, '\r') < 0 {
		if bytes.IndexByte(b, '<') >= 0 || !utf8.Valid(b) {
			return "", false
		}
		return string(b), true
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch c {
		case '<':
			return "", false
		case '\r':
			sb.WriteByte('\n')
			if i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
			continue
		case '&':
		default:
			sb.WriteByte(c)
			continue
		}
		semi := bytes.IndexByte(b[i:], ';')
		if semi < 0 || semi > 12 {
			return "", false
		}
		name := string(b[i+1 : i+semi])
		switch name {
		case "lt":
			sb.WriteByte('<')
		case "gt":
			sb.WriteByte('>')
		case "amp":
			sb.WriteByte('&')
		case "quot":
			sb.WriteByte('"')
		case "apos":
			sb.WriteByte('\'')
		default:
			if len(name) < 2 || name[0] != '#' {
				return "", false
			}
			var v uint64
			var err error
			if name[1] == 'x' {
				v, err = strconv.ParseUint(name[2:], 16, 32)
			} else {
				v, err = strconv.ParseUint(name[1:], 10, 32)
			}
			if err != nil || !utf8.ValidRune(rune(v)) {
				return "", false
			}
			sb.WriteRune(rune(v))
		}
		i += semi
	}
	s := sb.String()
	if !utf8.ValidString(s) {
		return "", false
	}
	return s, true
}
