package wikitext

import (
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	src := `{{Infobox settlement|name=Babylon|country=[[Iraq]]|population={{formatnum:1000}}}}
'''Babylon''' is in [[Mesopotamia|ancient Mesopotamia]] near the [[euphrates]]<ref>{{cite web|title=Hidden}}</ref>.
{{coord|32|32|11|N|44|25|15|E|display=title}} {{coord|33.0|44.0}}
[[File:Ishtar Gate.jpg|thumb|The [[Ishtar Gate]]]] [[Category:Ancient cities]] [[fr:Babylone]] [[wikt:city]]
{{glossary}}
{{term|Ziggurat}}
{{defn|A stepped temple tower.}}
{{glossary end}}
{{glossary|Cuneiform|Wedge-shaped writing}}
{| class="wikitable"
! King !! Reign
|-
| Hammurabi || 1792–1750 BC
|}`
	d := Extract("Babylon", src, site(t))
	check := func(name string, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
	check("links", d.Links, "Ishtar Gate", "Iraq", "Mesopotamia", "Euphrates")
	check("categories", d.Categories, "Ancient cities")
	check("media", d.Media, "Ishtar Gate.jpg")
	if len(d.Coordinates) != 2 || !d.Coordinates[0].Primary || d.Coordinates[0].Lat < 32.5 || d.Coordinates[0].Lon < 44.4 {
		t.Errorf("coordinates: %+v", d.Coordinates)
	}
	if c, ok := PrimaryCoord(src); !ok || !c.Primary || c.Lat < 32.5 {
		t.Errorf("PrimaryCoord: %+v %v", c, ok)
	}
	if len(d.Infoboxes) != 1 || d.Infoboxes[0].Params["country"] != "[[Iraq]]" {
		t.Errorf("infobox: %+v", d.Infoboxes)
	}
	if len(d.Glossary) != 2 || d.Glossary[0].Term != "Ziggurat" || d.Glossary[0].Definition != "A stepped temple tower." || d.Glossary[1].Term != "Cuneiform" {
		t.Errorf("glossary: %+v", d.Glossary)
	}
	if len(d.Tables) != 1 || !strings.Contains(d.Tables[0], "Hammurabi") {
		t.Errorf("tables: %q", d.Tables)
	}
	for _, want := range []string{"Infobox settlement", "coord", "glossary", "term", "defn", "formatnum:1000"} {
		found := false
		for _, tp := range d.Templates {
			found = found || tp == want
		}
		if !found {
			t.Errorf("template %q not listed: %q", want, d.Templates)
		}
	}
	txt := PlainText(src, site(t))
	for _, want := range []string{"Babylon", "ancient Mesopotamia", "euphrates", "Hammurabi", "1792"} {
		if !strings.Contains(txt, want) {
			t.Errorf("plain text lacks %q: %q", want, txt)
		}
	}
	for _, bad := range []string{"Hidden", "Category", "Babylone", "wikitable", "{{", "[["} {
		if strings.Contains(txt, bad) {
			t.Errorf("plain text has %q: %q", bad, txt)
		}
	}
	o := render(t, src)
	has(t, o.HTML, `<dt id="Ziggurat">Ziggurat</dt>`, "<dd>A stepped temple tower.</dd>", "<dt>Cuneiform</dt><dd>Wedge-shaped writing</dd>")
	if len(o.Coords) != 2 || len(o.Templates) == 0 {
		t.Errorf("render output coords %v templates %v", o.Coords, o.Templates)
	}
}
