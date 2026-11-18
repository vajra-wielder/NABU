#!/usr/bin/env python3
"""Build a small but realistic Wikipedia-shaped ZIM file for testing NABU.

It uses the official libzim writer (pip install libzim), so NABU's own
reader is checked against the reference implementation, not against
itself. The result mimics what Kiwix's mwoffliner produces: HTML articles
in namespace C, a stylesheet, images, redirects, a main page, metadata
and a favicon.

    python3 testdata/make_fixture.py library/wikipedia_en_test_maxi_2026-10.zim [articles]

The copy committed at testdata/wikipedia_en_test_maxi_2026-10.zim was made
with 300 articles and libzim 9 (python-libzim 4.1.0).
"""
import random
import struct
import sys
import zlib

from libzim.writer import Creator, Hint, Item, StringProvider


def png(w, h, rgb):
    raw = b"".join(b"\x00" + bytes(rgb) * w for _ in range(h))

    def chunk(t, d):
        c = struct.pack(">I", len(d)) + t + d
        return c + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)

    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


class Page(Item):
    def __init__(self, path, title, mime, content, front=False, compress=True):
        super().__init__()
        self.path, self.title, self.mime = path, title, mime
        self.content, self.front, self.compress = content, front, compress

    def get_path(self): return self.path
    def get_title(self): return self.title
    def get_mimetype(self): return self.mime
    def get_contentprovider(self): return StringProvider(self.content)
    def get_hints(self): return {Hint.FRONT_ARTICLE: self.front, Hint.COMPRESS: self.compress}


CSS = """body{font-family:sans-serif;margin:0 auto;max-width:60em;padding:1em;line-height:1.6;color:#202122}
h1,h2{font-family:'Linux Libertine','Georgia','Times',serif;font-weight:normal;border-bottom:1px solid #a2a9b1}
a{color:#36c;text-decoration:none} a:hover{text-decoration:underline}
.infobox{float:right;border:1px solid #a2a9b1;background:#f8f9fa;margin:0 0 1em 1em;padding:.4em;width:16em;font-size:88%}
"""

WORDS = ("tablet stylus cuneiform scribe temple river city king law star wedge clay "
         "archive library wisdom writing reed palace canal harvest omen").split()


def article(title, links, img=None):
    rnd = random.Random(title)
    paras = []
    for _ in range(rnd.randint(3, 8)):
        words = [rnd.choice(WORDS) for _ in range(rnd.randint(40, 120))]
        for _ in range(3):
            t = rnd.choice(links)
            words.insert(rnd.randrange(len(words)), f'<a href="{t.replace(" ", "_")}">{t}</a>')
        paras.append("<p>" + " ".join(words).capitalize() + ".</p>")
    box = (f'<table class="infobox"><tr><td><img src="./_assets_/{img}" alt="" width="64" height="64"></td></tr>'
           f'<tr><td>{title}</td></tr></table>') if img else ""
    return (f'<!DOCTYPE html><html><head><meta charset="utf-8"><title>{title}</title>'
            f'<link rel="stylesheet" href="./_assets_/style.css"></head><body>'
            f'<h1>{title}</h1>{box}{"".join(paras)}<h2>See also</h2><ul>'
            + "".join(f'<li><a href="{t.replace(" ", "_")}">{t}</a></li>' for t in links[:5])
            + "</ul></body></html>")


def main(out, count=3000):
    titles = ["Nabu", "Babylon", "Cuneiform", "Clay tablet", "Ashurbanipal", "Library of Ashurbanipal",
              "Hammurabi", "Ishtar Gate", "Euphrates", "Tigris", "Ziggurat", "Marduk", "Uruk",
              "Epic of Gilgamesh", "Scribe", "Stylus", "Écriture", "Ελληνικά", "Москва", "東京"]
    rnd = random.Random(7)
    titles += [f"{rnd.choice(WORDS).capitalize()} {rnd.choice(WORDS)} {i}" for i in range(count)]
    with Creator(out).config_indexing(False, "eng").config_clustersize(512 * 1024) as c:
        c.set_mainpath("Main_Page")
        for k, v in {"Name": "wikipedia_en_test", "Flavour": "maxi", "Date": "2026-10-01",
                     "Title": "Wikipedia (test fixture)", "Description": "A tiny Wikipedia-shaped archive",
                     "Language": "eng", "Creator": "Wikipedia", "Publisher": "NABU tests"}.items():
            c.add_metadata(k, v)
        c.add_illustration(png(48, 48, (153, 102, 51)), 48)
        c.add_item(Page("_assets_/style.css", "", "text/css", CSS))
        c.add_item(Page("_assets_/tablet.png", "", "image/png", png(64, 64, (180, 140, 90)), compress=False))
        main_links = titles[:16]
        c.add_item(Page("Main_Page", "Main Page", "text/html",
                        article("Main Page", main_links, "tablet.png").replace(
                            "<h1>Main Page</h1>", "<h1>Welcome to the test Wikipedia</h1>"), front=True))
        for i, t in enumerate(titles):
            links = [rnd.choice(titles) for _ in range(8)]
            c.add_item(Page(t.replace(" ", "_"), t, "text/html",
                            article(t, links, "tablet.png" if i % 5 == 0 else None), front=True))
        c.add_redirection("Babylonia", "Babylonia", "Babylon", {Hint.FRONT_ARTICLE: True})
        c.add_redirection("God_of_scribes", "God of scribes", "Nabu", {Hint.FRONT_ARTICLE: True})


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "wikipedia_en_test_maxi_2026-10.zim",
         int(sys.argv[2]) if len(sys.argv) > 2 else 3000)
