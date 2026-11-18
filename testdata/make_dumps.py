#!/usr/bin/env python3
"""Write small Wikimedia-style multistream dumps for NABU's tests.

Exactly the real layout: a bzip2 stream holding the <siteinfo> header,
then streams of 100 <page>s each, then a closing stream; plus the
"offset:pageid:title" index, bzip2-compressed. Two wikis are made:

  testwiki  English, with templates, tables, refs, redirects, files
  fawiki    Persian: right-to-left, localized namespace names

    python3 testdata/make_dumps.py testdata
"""
import bz2
import os
import random
import sys
from xml.sax.saxutils import escape

SITEINFO = """<mediawiki xmlns="http://www.mediawiki.org/xml/export-0.11/" version="0.11" xml:lang="{lang}">
  <siteinfo>
    <sitename>{sitename}</sitename>
    <dbname>{db}</dbname>
    <base>https://{lang}.wikipedia.org/wiki/{main}</base>
    <generator>MediaWiki 1.45.0-wmf.1</generator>
    <case>first-letter</case>
    <namespaces>
{namespaces}
    </namespaces>
  </siteinfo>
"""

PAGE = """  <page>
    <title>{title}</title>
    <ns>{ns}</ns>
    <id>{id}</id>{redirect}
    <revision>
      <id>{rev}</id>
      <timestamp>2026-10-01T12:00:00Z</timestamp>
      <model>wikitext</model>
      <format>text/x-wiki</format>
      <text bytes="{bytes}" xml:space="preserve">{text}</text>
    </revision>
  </page>
"""

EN_NS = {-2: "Media", -1: "Special", 0: "", 1: "Talk", 6: "File", 10: "Template", 14: "Category", 4: "Wikipedia", 828: "Module"}
FA_NS = {-2: "مدیا", -1: "ویژه", 0: "", 1: "بحث", 6: "پرونده", 10: "الگو", 14: "رده", 4: "ویکی‌پدیا", 828: "پودمان"}

BABYLON = """{{Short description|Ancient city in Mesopotamia}}
{{Infobox settlement
| name = Babylon
| image = Ishtar Gate.jpg
| caption = The [[Ishtar Gate]]
| country = [[Iraq]]
| founded = {{circa|2300 BC}}
| area_km2 = {{convert|9|km2|sqmi}}
| leader_title1 = King
| leader_name1 = [[Hammurabi]]
| empty_value =
}}
'''Babylon''' was a city of [[Mesopotamia|ancient Mesopotamia]] on the [[Euphrates]].<ref name="brit">{{cite web |url=https://example.org/babylon |title=Babylon |website=Encyclopaedia |access-date=2026-01-01}}</ref> Its god of scribes was [[Nabu]]s.<ref name="brit"/>

== History ==
[[File:Babylon map.png|thumb|left|upright=1.2|Map of [[Babylonia]]
with a caption over two lines]]
Hammurabi ruled from {{start date|1792}}.<ref>Second source with ''italic''.</ref>

=== Neo-Babylonian ===
* [[Nebuchadnezzar II]]
** built the [[Hanging Gardens of Babylon|gardens]]
* restored the city
# first
# second

{| class="wikitable"
|+ Kings
! King !! Reign
|-
| [[Hammurabi]] || 1792–1750 BC
|-
| style="color:red" | Nabopolassar || 626–605 BC
|}

== See also ==
{{Main|Ishtar Gate}}
{{Navbox|title=Mesopotamia}}
<script>alert(1)</script><span style="background:url(javascript:x)">safe text</span>
[[Category:Ancient cities]]
[[fr:Babylone]]

== References ==
{{Reflist}}
"""


def pages_en():
    yield ("Main Page", 0, None, "Welcome to the '''test wiki'''. See [[Babylon]].")
    yield ("Babylon", 0, None, BABYLON)
    yield ("Babylonia", 0, "Babylon", "#REDIRECT [[Babylon]]")
    yield ("Nabu", 0, None, "'''Nabu''' was the god of scribes.\n\n== Worship ==\nIn [[Babylon]].")
    yield ("Écriture", 0, None, "French word for writing.")
    yield ("God of scribes", 0, "Nabu", "#REDIRECT [[Nabu#Worship]]")
    yield ("Template:Infobox settlement", 10, None, "<includeonly>...</includeonly>")
    yield ("Category:Ancient cities", 14, None, "Cities of antiquity.")
    rnd = random.Random(7)
    words = "tablet stylus cuneiform temple river city king law star clay archive".split()
    for i in range(240):
        t = f"{rnd.choice(words).capitalize()} {rnd.choice(words)} {i}"
        yield (t, 0, None, f"'''{t}''' is about [[{rnd.choice(words).capitalize()}]]. " * 5)


def pages_fa():
    yield ("صفحهٔ اصلی", 0, None, "به '''ویکی‌پدیا''' خوش آمدید. [[بابل]]")
    yield ("بابل", 0, None, "'''بابل''' شهری باستانی بود.\n\n[[پرونده:Babylon.jpg|بندانگشتی|نقشه]]\n\n== تاریخ ==\nدر [[عراق]].\n[[رده:شهرها]]")
    yield ("عراق", 0, None, "کشوری در خاورمیانه.")
    for i in range(130):
        yield (f"مقاله {i}", 0, None, f"متن شماره {i}.")


def write(out_dir, db, lang, sitename, main, ns, pages):
    data = os.path.join(out_dir, f"{db}-20261101-pages-articles-multistream.xml.bz2")
    index = os.path.join(out_dir, f"{db}-20261101-pages-articles-multistream-index.txt.bz2")
    nslines = "\n".join(
        f'      <namespace key="{k}" case="first-letter">{escape(v)}</namespace>' if v
        else f'      <namespace key="{k}" case="first-letter" />' for k, v in sorted(ns.items()))
    header = SITEINFO.format(lang=lang, sitename=escape(sitename), db=db, main=main.replace(" ", "_"), namespaces=nslines)
    blob = bz2.compress(header.encode())
    lines = []
    pages = list(pages)
    for start in range(0, len(pages), 100):
        offset = len(blob)
        chunk = []
        for j, (title, nsk, redirect, text) in enumerate(pages[start:start + 100]):
            pid = start + j + 10
            full = (ns[nsk] + ":" + title.split(":", 1)[-1]) if nsk else title
            red = f'\n    <redirect title="{escape(redirect)}" />' if redirect else ""
            chunk.append(PAGE.format(title=escape(full), ns=nsk, id=pid, redirect=red, rev=pid * 7,
                                     bytes=len(text.encode()), text=escape(text)))
            lines.append(f"{offset}:{pid}:{full}")
        blob += bz2.compress("".join(chunk).encode())
    blob += bz2.compress(b"</mediawiki>\n")
    with open(data, "wb") as f:
        f.write(blob)
    with open(index, "wb") as f:
        f.write(bz2.compress(("\n".join(lines) + "\n").encode()))


if __name__ == "__main__":
    out = sys.argv[1] if len(sys.argv) > 1 else "."
    write(out, "testwiki", "en", "Wikipedia", "Main Page", EN_NS, pages_en())
    write(out, "fawiki", "fa", "ویکی‌پدیا", "صفحهٔ اصلی", FA_NS, pages_fa())
