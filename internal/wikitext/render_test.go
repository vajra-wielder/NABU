package wikitext

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vajra-wielder/nabu/internal/dump"
)

func site(t *testing.T) *dump.Siteinfo {
	si, err := dump.ParseSiteinfo(strings.NewReader(`<mediawiki xml:lang="en"><siteinfo><sitename>Wikipedia</sitename><dbname>enwiki</dbname>
<base>https://en.wikipedia.org/wiki/Main_Page</base><case>first-letter</case><namespaces>
<namespace key="0" case="first-letter" /><namespace key="6" case="first-letter">File</namespace>
<namespace key="10" case="first-letter">Template</namespace><namespace key="14" case="first-letter">Category</namespace>
</namespaces></siteinfo>`))
	if err != nil {
		t.Fatal(err)
	}
	return si
}

func render(t *testing.T, src string) *Output {
	t.Helper()
	return Render(src, &Context{
		Title:    "Test page",
		Site:     site(t),
		PageURL:  func(s string) string { return "/w/" + url.PathEscape(strings.ReplaceAll(s, " ", "_")) },
		ImageURL: func(f string, w int) string { return "https://img.example/" + f },
		Now:      time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
	})
}

func has(t *testing.T, html string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(html, w) {
			t.Errorf("missing %q in:\n%s", w, html)
		}
	}
}

func hasNot(t *testing.T, html string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(html, b) {
			t.Errorf("unexpected %q in:\n%s", b, html)
		}
	}
}

func TestQuotes(t *testing.T) {
	cases := map[string]string{
		"''i''":            "<i>i</i>",
		"'''b'''":          "<b>b</b>",
		"'''''bi'''''":     "<i><b>bi</b></i>",
		"'''b ''bi'' b'''": "<b>b <i>bi</i> b</b>",
		"l'''amour'' x":    "l'<i>amour</i> x",
		"''unclosed":       "<i>unclosed</i>",
		"rock 'n' roll":    "rock 'n' roll",
	}
	for in, want := range cases {
		if got := doQuotes(in); got != want {
			t.Errorf("doQuotes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinksAndBlocks(t *testing.T) {
	o := render(t, `Intro with [[Babylon]], [[Babylonia|the empire]], [[Nabu]]s and [[test page]].
A [https://example.org site] and https://bare.example/x.

== First ==
* one
** nested
* two
# a
# b
; term : definition
 preformatted
----
[[Category:Cities]] [[fr:Babylone]] [[:Category:Cities|cat link]] [[wikt:stylus]]`)
	h := o.HTML
	has(t, h,
		`<a href="/w/Babylon" title="Babylon">Babylon</a>`,
		`<a href="/w/Babylonia" title="Babylonia">the empire</a>`,
		`title="Nabu">Nabus</a>`,
		`<strong class="selflink">test page</strong>`,
		`href="https://example.org">site</a>`,
		`href="https://bare.example/x">`,
		`<h2 id="First">First</h2>`,
		"<ul><li>one<ul><li>nested</li></ul>\n</li><li>two</li></ul>",
		"<ol><li>a</li><li>b</li></ol>",
		"<dl><dt>term</dt><dd>definition</dd></dl>",
		"<pre>preformatted</pre>",
		"<hr>",
		`title="Category:Cities">cat link</a>`,
		`https://en.wiktionary.org/wiki/stylus`)
	hasNot(t, h, "Babylone", "[[", "]]")
	if len(o.Categories) != 1 || o.Categories[0] != "Cities" {
		t.Errorf("categories: %v", o.Categories)
	}
}

func TestTemplatesAndRefs(t *testing.T) {
	o := render(t, `{{Short description|A test}}
{{Infobox person
| name = Ada
| image = Ada.jpg
| caption = Portrait
| birth_date = {{birth date and age|1990|5|17}}
| leader_title1 = Office
| leader_name1 = [[Chief]]
| empty =
}}
Text.<ref name="a">{{cite book |last=Smith |first=J. |title=Tablets |year=2001 |publisher=Clay |isbn=123}}</ref> Again.<ref name="a"/> {{sfn|Jones|1999|p=4}}
Length {{convert|10|km|mi}}, heat {{convert|100|C}}. {{nowrap|kept text}} {{citation needed|date=x}}
{{Main|Babylon}}
{{Navbox|title=Hidden}}
{{lang|fr|bonjour}} {{!}} {{formatnum:1234567}} {{lc:ABC}} {{#if: x | yes | no}} {{#switch: b | a=1 | b=2 | #default=3}}
== Notes ==
{{Reflist}}`)
	h := o.HTML
	if o.ShortDesc != "A test" {
		t.Errorf("short desc %q", o.ShortDesc)
	}
	has(t, h, `class="infobox"`, `>Ada</th>`, `Birth date</th><td>May 17, 1990 (age&nbsp;36)`,
		`>Office</th><td><a href="/w/Chief"`, `img.example/Ada.jpg`,
		`[1]</a></sup>`, `cite_ref-1-2`, `[2]</a></sup>`,
		`Smith, J. (2001)`, `<i>Tablets</i>`, `ISBN 123`, `Jones 1999, p. 4.`,
		`10&nbsp;km (6.2&nbsp;mi)`, `100&nbsp;°C (212&nbsp;°F)`, `kept text`,
		`Main article: <a href="/w/Babylon"`, `<span lang="fr">bonjour</span>`, `| 1,234,567 abc yes 2`,
		`<ol class="references">`)
	hasNot(t, h, "Hidden", "citation needed", "{{", "Empty", "Portrait</th>")
}

func TestTables(t *testing.T) {
	h := render(t, `{| class="wikitable" onclick="evil()"
|+ Caption
! A !! B
|-
| style="color:red" | 1 || [[Link|x||y]]
|-
| multi
line
* list
|}`).HTML
	has(t, h, `<table class="wikitable">`, "<caption>Caption</caption>", "<th>A</th><th>B</th>",
		`<td style="color:red">1</td>`, `>x||y</a>`, "<li>list</li>")
	hasNot(t, h, "onclick", "evil")
}

func TestSanitising(t *testing.T) {
	h := render(t, `<script>alert(1)</script> <img src=x onerror=alert(2)> <span style="background:url(javascript:x)" onmouseover="y">s</span>
<div class="ok" id="k">d</div> [javascript:alert(3) bad] <a href="javascript:alert(4)">a</a> <nowiki>[[not a link]]</nowiki>
<math>x^2</math> </div></div> <b>unclosed`).HTML
	// Dangerous markup may only survive as visible, escaped text.
	hasNot(t, h, "<script", "<img", "onmouseover", "url(", "<a href=\"javascript")
	has(t, h, "&lt;script&gt;", `<div class="ok" id="w-k">d</div>`, "[[not a link]]", `x^2`, "<b>unclosed</b>")
	if strings.Count(h, "<div") != strings.Count(h, "</div>") {
		t.Errorf("unbalanced divs:\n%s", h)
	}
}

func TestFilesAndTOC(t *testing.T) {
	o := render(t, `[[File:Map.png|thumb|right|250px|A [[map]]
over two lines]]
[[Image:Flag.svg|20px]]
== A ==
== B ==
=== B1 ===
== C ==
<gallery>
File:One.jpg|First
Two.jpg|Second
</gallery>`)
	h := o.HTML
	has(t, h, `<figure class="thumb tright" style="width:258px">`, "img.example/Map.png", `<a href="/w/Map"`,
		`over two lines</figcaption>`, `class="toc"`, `<a href="#B1">B1</a>`, `class="gallery"`, "First", "img.example/Two.jpg")
	hasNot(t, h, "Flag.svg") // decorative icons are dropped
	if len(o.Headings) != 4 {
		t.Errorf("headings %v", o.Headings)
	}
	// Offline: a labelled frame instead of the picture.
	off := Render(`[[File:Map.png|thumb|Caption]]`, &Context{Title: "x", Site: site(t), PageURL: func(s string) string { return s }})
	has(t, off.HTML, `class="pic-ph"`, "Map.png")
	hasNot(t, off.HTML, "<img")
}

func TestNoTOCAndDepth(t *testing.T) {
	o := render(t, "__NOTOC__\n== a ==\n== b ==\n== c ==\n== d ==")
	hasNot(t, o.HTML, `class="toc"`, "__NOTOC__")
	// Pathological nesting must not hang or crash.
	deep := strings.Repeat("{{x|", 200) + strings.Repeat("}}", 200) + strings.Repeat("[[", 300) + strings.Repeat("''", 500)
	render(t, deep)
}
