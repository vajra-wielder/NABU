<p align="center"><img src="internal/web/assets/nabu.svg" width="96" alt=""></p>

<h1 align="center">NABU</h1>
<p align="center"><em>Wikipedia on a pen drive, read straight from Wikimedia's own compressed dumps. Plug it into almost any computer. No internet, no installation.</em></p>

---

Nabu was the Babylonian god of scribes, writing and wisdom. His symbol was a single
wedge pressed into clay, the stylus that wrote the first libraries. His temples kept
the tablets so knowledge outlived the people who wrote it.

This NABU keeps Wikipedia. Copy NABU and a Wikipedia dump onto a USB stick. On any
Windows, Mac or Linux laptop, double-click **Start NABU** and the encyclopedia opens in
the browser, with Wikipedia's look: serif headings, infoboxes, footnotes, tables and
links that work. When the laptop is online, NABU asks Wikimedia for the latest dump and
offers to fetch it.

## Contents

- [Why read the raw dumps](#why-read-the-raw-dumps)
- [Languages: ten that cover 91% of the land](#languages-ten-that-cover-91-of-the-land)
- [Using a NABU drive](#using-a-nabu-drive)
- [Making a NABU drive](#making-a-nabu-drive)
- [Requisites (pinned, kept locally)](#requisites-pinned-kept-locally)
- [How it works](#how-it-works)
- [Computers it runs on](#computers-it-runs-on)
- [Command line](#command-line)
- [Developing](#developing)
- [Limits](#limits)
- [Credits and licences](#credits-and-licences)

## Why read the raw dumps

Wikimedia publishes every Wikipedia, about twice a month, as
`<wiki>-<date>-pages-articles-multistream.xml.bz2`. That is the wikitext of every
page, compressed with bzip2 in independent streams of 100 pages, plus an index saying
which stream holds which page. NABU reads that file **as is**:

| | Wikimedia dump (NABU) | Kiwix ZIM "maxi" |
|---|---|---|
| English Wikipedia | **25.4 GB** + 0.3 GB index | ~110 GB |
| Conversion needed | none: the downloaded file is the library | — |
| Fresh | the day Wikimedia publishes | weeks to months later |
| Pictures | from Wikimedia when online; labelled frames offline | included |

To open an article, NABU finds the title in its search index, seeks to the right stream
in the `.bz2` and decompresses about half a megabyte. That takes 20–40 ms, even from a
USB stick. The program is about 10 MB and uses only the Go standard library, with no
third-party code at all.

## Languages: ten that cover 91% of the land

NABU offers the **smallest set of languages that together cover the most land**, not
the most speakers. A country counts as covered when one of its official or co-official
languages is in the set. Languages are picked greedily by the uncovered land area they
add (set cover over 171 countries, ≈99% of the land outside Antarctica). Ten languages
reach 91%:

| # | Language | Adds | Cumulative | Main areas | Dump size (Nov 2026) |
|---|---|---|---|---|---|
| 1 | English | 43.6 M km² | 31.9% | Canada, USA, Australia, India, Sudan, South Africa… | 25.4 GB |
| 2 | Russian | 20.4 M km² | 46.9% | Russia, Kazakhstan, Belarus, Kyrgyzstan, Tajikistan | 5.9 GB |
| 3 | Arabic | 12.8 M km² | 56.3% | Algeria, Saudi Arabia, Libya, Chad, Egypt… | 2.1 GB |
| 4 | Spanish | 12.0 M km² | 65.1% | Argentina, Mexico, Peru, Colombia, Bolivia… | 5.1 GB |
| 5 | Portuguese | 10.7 M km² | 72.9% | Brazil, Angola, Mozambique, Portugal | 2.7 GB |
| 6 | Chinese | 9.6 M km² | 80.0% | China, Taiwan | 3.4 GB |
| 7 | French | 8.7 M km² | 86.3% | DR Congo, Niger, Mali, France, CAR, Madagascar… | 6.9 GB |
| 8 | Persian | 2.3 M km² | 88.0% | Iran, Afghanistan | 1.5 GB |
| 9 | Danish | 2.2 M km² | 89.6% | Greenland, Denmark | 0.5 GB |
| 10 | Indonesian | 1.9 M km² | 91.0% | Indonesia | 1.3 GB |
| | **All ten** | | **91%** | | **≈55 GB, fits a 64 GB stick** |

Reproduce it with `python3 docs/language_coverage.py` (`--all` keeps going: Mongolian,
Amharic, Turkish, Swedish… each adds under 1.2%). Danish is there purely for Greenland's
2.2 million km², which is the honest result of counting area rather than people.

The reader itself is language-independent. Namespace names ("Datei", "پرونده"), the
main page, right-to-left layout and the site name all come from each dump's own header.
Any other Wikimedia dump copied into `library/` works too.

## Using a NABU drive

| Computer | Start it |
|---|---|
| Windows | Double-click **`Start NABU (Windows).bat`** |
| Mac | Double-click **`Start NABU (Mac).command`**. The first time, right-click it, choose **Open**, then **Open** (NABU is not notarised by Apple). |
| Linux | `sh start-nabu-linux.sh`, or double-click it and choose *Run in Terminal* |

The browser opens on your library. Search from the box at the top of every page (press
<kbd>/</kbd>); capitals and accents don't matter, so "ecriture" finds "Écriture".
Close the small NABU window, or click **Turn off NABU**, when you are done.

**Reading.** Articles have Wikipedia's structure in NABU's colours: lapis blue, terracotta
and parchment, with a Night theme for the evening.

- **Contents sidebar.** On wide screens it follows you down the page.
- **Link previews.** Hover a link to see how that article begins.
- **Reading settings.** The **Aa** button sets the theme (Auto, Day, Night), text size,
  page width, and serif or sans-serif body text. Settings are remembered per browser.
- **Saved & recent.** **☆ Save** keeps an article for later. Saved articles and reading
  history are stored *on the drive*, so they move with the stick to any computer. Only
  reading on the computer running NABU is remembered, never that of devices reading
  over Wi-Fi. History can be switched off or cleared.
- **Home page.** It offers *Continue reading*, your saved articles and a few random
  articles to explore.
- **Installable.** In Chrome and Edge, NABU can be installed as an app window.

**No Wikipedia yet?** The **Get & update** page lists the ten languages with the date
and size of Wikimedia's newest dump, and downloads with one click. Downloads resume
after dropped connections or restarts, and every file is checked against Wikimedia's
SHA-1 before it is used. Files over 4 GB are written in 4 GB pieces, so FAT32 sticks
work. Already have the two files? Copy them into `library/`.

**First use of a new dump:** NABU sorts its titles into a search index on the drive
(`library/.nabu/index/`, about 2–3% of the dump's size). It takes seconds for small
wikis and a few minutes for English, up to about 15 on an old laptop. It runs in the
background with a progress bar, once per dump. You can also do it ahead of time on a
fast machine with `nabu prepare`.

**Drive format:** exFAT is best (every OS reads and writes it). FAT32 works too.
Avoid NTFS: Macs can't write to it, so updates fail there.

## Making a NABU drive

You need a Unix-like shell (Linux, macOS, or Git Bash/WSL on Windows), `curl`, `unzip`
and, optionally, `zip`.

```bash
scripts/fetch-requisites.sh
```

```bash
scripts/build.sh
```

The first command fetches the two pinned Go toolchains for your computer and checks
their SHA-256. The second builds 11 programs into `dist/NABU/` (plus
`dist/NABU-<version>.zip`, about 45 MB). Copy the *contents* of `dist/NABU/` to the root
of the stick:

```
NABU drive/
├── Start NABU (Windows).bat
├── Start NABU (Mac).command
├── start-nabu-linux.sh
├── READ ME FIRST.txt
├── nabu/            11 programs: Windows, macOS, Linux × Intel/AMD/ARM (+ legacy)
├── library/         the dumps (NABU can download them)
└── licenses/
```

Then add a Wikipedia from the **Get & update** page, or headless:

```bash
nabu/nabu-linux-amd64 download en --library library
```

Pushing a tag such as `v1.0.0` makes GitHub Actions test on Windows, macOS and Linux
(Go 1.24 and 1.20) and attach the drive zip to a release.

## Requisites (pinned, kept locally)

Everything needed to build NABU is pinned to an exact version and either **committed
here** or **fetched and checksum-verified** by `scripts/fetch-requisites.sh`. After that
one fetch, building needs no network at all. Machine-readable list:
[`requisites/requisites.lock`](requisites/requisites.lock).

| Requisite | Version | Why | Kept where | Licence |
|---|---|---|---|---|
| Go toolchain | **1.24.7** | Builds the programs for Windows 10+, macOS 11+, all Linux | `requisites/toolchains/go1.24.7/`, fetched from the Go module proxy, SHA-256 in the lock file | BSD-3-Clause |
| Go toolchain (legacy) | **1.20.14** | The last Go that runs on Windows 7/8 and macOS 10.13–10.15; builds the `-legacy` programs | `requisites/toolchains/go1.20.14/` | BSD-3-Clause |
| Libertinus Serif (`@fontsource/libertinus-serif`) | **5.3.0** | Wikipedia's heading face (Linux Libertine's successor), embedded in the program | committed in [`internal/web/assets/fonts/`](internal/web/assets/fonts) | SIL OFL 1.1 |
| Go libraries | none | bzip2, XML, HTTP, SHA-1 all come from Go's standard library | — | — |
| Test dumps | — | Small synthetic multistream dumps (English and right-to-left Persian) | committed in [`testdata/`](testdata), generator `testdata/make_dumps.py` (Python's built-in `bz2`) | — |

`go.mod` declares `go 1.20`, so nothing newer than the legacy toolchain slips in. CI
runs the tests under both versions.

| Toolchain zip (SHA-256) | linux-amd64 | darwin-arm64 | windows-amd64 |
|---|---|---|---|
| go1.24.7 | `4c2a63c0…7b8031e` | `b529787f…ac604f63` | `615b3423…ddfae337` |
| go1.20.14 | `725ca027…54e7d26d` | `597bb4d3…9925967b` | `50b3d782…f0a58e02` |

## How it works

```
 browser ──HTTP──▶ nabu  (one static program, listening on 127.0.0.1:8080)
                    ├─ /enwiki/wiki/<Title>   index lookup → seek → bunzip2 one stream
                    │                          → find <page> → wikitext → HTML
                    ├─ /enwiki/search, /suggest  binary search of the sorted title index
                    ├─ /, /nabu/updates        library, downloads, settings
                    └─ updater                 RSS + dumpstatus.json on dumps.wikimedia.org
                                               → resumable download → SHA-1 → 4 GB pieces
 library/*.xml.bz2 + *-index.txt.bz2   (exactly as Wikimedia publishes them)
 library/.nabu/index/*.nidx            NABU's sorted title index, built once per dump
```

- **Index** (`internal/dump`). Wikimedia's index lists pages in file order. NABU
  re-sorts it by a search key (lower case, accents removed) with an external merge
  sort, so memory stays around 150 MB even for English's ~30 million titles. Titles are
  front-coded in blocks of 64. Only a directory of block starts lives in RAM (~15 MB
  for English); everything else stays on the stick. An index is rebuilt automatically
  if its dump changes. Small wikis ship as one file pair; English also ships as ~70
  numbered parts, and NABU reads both layouts.
- **Pages.** One bzip2 stream (100 pages) is read and decompressed; a small cache keeps
  recent streams, and parallel requests for the same stream share the work. Only the
  matching `<page>` is XML-decoded.
- **Wikitext** (`internal/wikitext`). NABU renders the language itself faithfully:
  headings and contents, links with trails and pipe tricks, lists, tables (nested,
  with attributes), bold and italic by MediaWiki's own algorithm, footnotes with named
  and grouped references, galleries, poems, multi-line captions. Wikipedia's templates
  run on Lua servers that a dump doesn't include, so the ones a reader needs have small
  built-in implementations: **infoboxes** (any of them, as a label/value table),
  **citations** (in all ten languages' parameter names), hatnotes, dates and ages, unit
  conversion, coordinates, quotations, lists, footnote variants, flags and table-cell
  helpers. Maintenance banners and navigation boxes are dropped. Everything is escaped,
  and only a whitelist of HTML and CSS survives. Rendering a page takes about 0.5 ms
  (median), under 100 ms worst case. It was tested on thousands of real articles from
  Simple English, Abkhaz and Yiddish Wikipedia without a single crash.
- **Security.** NABU listens on `127.0.0.1` unless `--lan`. It refuses requests addressed
  to domain names (DNS rebinding). Actions that change the drive need a local client, a
  custom header and a matching `Origin`.
- **Why Go and not Assembly?** Assembly would mean rewriting NABU for every processor
  *and* every operating system. Go compiles one readable codebase into static programs
  for 11 targets with no runtime to install. The bottleneck is the USB stick, not the
  language.

## Computers it runs on

| | Programs on the drive | Oldest supported |
|---|---|---|
| Windows | `windows-amd64`, `windows-386` (32-bit), `windows-arm64` | Windows 10; **Windows 7 SP1 / 8 / 8.1** via `windows-386-legacy` |
| macOS | `darwin-arm64` (Apple silicon), `darwin-amd64` (Intel) | macOS 11; **10.13–10.15** via `darwin-amd64-legacy` |
| Linux | `linux-amd64`, `linux-386`, `linux-arm64`, `linux-arm` (Raspberry Pi, ARMv6+) | kernel 3.2; no libc needed (static) |

The launchers pick the right program from the CPU and OS version. On Linux, USB sticks
are often mounted so programs can't run from them; the launcher then runs a temporary
copy from `/tmp`. The Mac launcher clears the download quarantine flag. The 32-bit
Linux and Windows 7 programs need no SSE2, so they run on CPUs back to the Pentium III.
Internet Explorer 11 gets stylesheets it understands, and on Windows NABU opens an
installed modern browser rather than IE.

**Coverage: about 97% of desktop and laptop computers** (StatCounter web-use share,
mid-2026), and above 94% even after allowing for machines that forbid unsigned programs.
NABU needs about 1 GB of RAM: opening an article took 23 ms median on one core, and
preparing a 6-million-title index peaked at 264 MB. The full analysis, measurements,
gaps and release checklist are in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md).

## Command line

```text
nabu [flags]              start NABU and open the browser
  --library DIR           folder with the dumps (launchers pass the drive's library/)
  --port 8080             first port to try (next free one if busy)
  --lan                   share with phones and computers on the same network
  --no-browser            don't open a browser (servers, Raspberry Pi kiosks)
  --offline               never touch the network (no update checks, no pictures)
  --mirror URL            a Wikimedia dump mirror instead of dumps.wikimedia.org
nabu catalog [wiki…]      newest dump date and size of each offered Wikipedia
nabu download <wiki>      download a dump: en, fr, … or enwiki, zh_yuewiki, …
                          (Ctrl+C pauses; run again to resume)
nabu prepare              build the search index of every dump now
nabu info                 list the dumps in the library and their state
nabu version
```

## Developing

```bash
go test ./...
```

```bash
go run ./cmd/nabu --library testdata --offline
```

To test against real dumps, put a few small ones (for example `simplewiki`, `abwiki`,
`yiwiki`) in a folder and run:

```bash
NABU_REAL_DUMPS=/path/to/dumps go test ./internal/dump ./internal/wikitext -run Real -v
```

This renders a sample of real articles and reports speed and any markup left
unrendered.

- `internal/dump`: siteinfo and namespaces, title normalisation and folding, index
  builder (external sort) and reader, stream cache, page extraction.
- `internal/wikitext`: the renderer: blocks, inline, links and files, tables, extension
  tags and references, templates (`handlers.go`, `cite.go`), sanitising.
- `internal/library`: finds dumps, prepares them in the background.
- `internal/update`: Wikimedia release lookup, resumable verified downloads, settings.
- `internal/server`, `internal/web`: pages, API, saved/history store, link previews,
  templates; `theme.css` (palette and shared bar), `nabu.css` (NABU pages), `wiki.css`
  (articles), `bar.js` (search and reading settings), `reader.js` (save, previews,
  contents sidebar), fonts.
- `docs/language_coverage.py`: the language selection.

## Limits

- **Pictures need the internet.** Dumps contain text only. When online (and the
  setting is on), pictures load from Wikimedia; offline, a frame shows each picture's
  name and caption.
- **Templates are approximated.** The common reader-facing ones are implemented. Rare
  ones show their main text or are left out. Navigation boxes are not shown. Maths
  appears as its TeX source.
- **Search covers titles, not full text.** It forgives case and accents. Full-text
  search would need an index of every word, roughly another 10–20% of the dump.
- **Category pages** show their description but not member lists, which aren't in the
  articles dump.
- **Unsigned programs.** Windows SmartScreen may warn (*More info → Run anyway*). On a
  Mac, right-click → Open the first time.

## Credits and licences

- NABU: MIT, see [LICENSE](LICENSE).
- Wikipedia content: [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/) by
  Wikipedia's volunteer authors. Dumps published by the Wikimedia Foundation.
- Libertinus Serif: SIL Open Font License 1.1.
