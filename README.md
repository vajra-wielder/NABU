<p align="center"><img src="internal/web/assets/nabu.svg" width="96" alt=""></p>

<h1 align="center">NABU</h1>
<p align="center"><em>All of Wikipedia on a pen drive. Plug it into almost any computer and read, no internet needed.</em></p>

---

Nabu was the Babylonian god of scribes, writing and wisdom. His symbol was a single
wedge pressed into clay, the stylus that wrote the first libraries. His temples kept
the tablets and his scribes copied them, so knowledge outlived the people who wrote it.

This project does the same job for Wikipedia. Copy NABU and a Wikipedia file onto a
USB stick. On any Windows, Mac or Linux laptop, double-click **Start NABU** and the
whole encyclopedia opens in the browser: articles, links, pictures, tables, infoboxes
and Wikipedia's own typography. Nothing is installed and the computer needs no
internet connection. When it does have one, NABU checks for a newer Wikipedia and
offers to download it onto the drive.

## Contents

- [What it does](#what-it-does)
- [Using a NABU drive](#using-a-nabu-drive)
- [Making a NABU drive](#making-a-nabu-drive)
- [Requisites (pinned, kept locally)](#requisites-pinned-kept-locally)
- [Where the Wikipedia comes from](#where-the-wikipedia-comes-from)
- [How it works](#how-it-works)
- [Computers it runs on](#computers-it-runs-on)
- [Command line](#command-line)
- [Developing](#developing)
- [Limits and next steps](#limits-and-next-steps)
- [Credits and licences](#credits-and-licences)

## What it does

- **Plug and play.** One small program (~10 MB, no dependencies, nothing installed)
  for each operating system and processor, plus a double-click launcher for each OS.
  The launcher picks the right program automatically.
- **Real Wikipedia pages.** NABU serves the pages exactly as Kiwix rendered them from
  Wikipedia: every internal link works, images and maths show, and the layout and
  styles are Wikipedia's own. Headings use Wikipedia's serif face (Linux Libertine,
  via the bundled Libertinus Serif), so pages look right even on machines that lack it.
- **Search as you type** from a slim bar at the top of every article (press <kbd>/</kbd>).
  Enter on an exact title opens the article, as on Wikipedia. Redirects ("Babylonia",
  "God of scribes") are followed, and capitalisation does not matter. A
  random article is one click away.
- **Checks for updates on start.** When online, NABU asks the Kiwix catalogue whether
  a newer Wikipedia snapshot exists and shows a banner. The "Get & update" page lists
  every language and size, with free space on the drive.
- **Downloads that survive bad connections.** Downloads resume after drops or
  restarts, are verified with SHA-256, and only appear in the library once
  complete. Files over 4 GB are written in 4 GB parts, so a FAT32 stick, which cannot
  hold a 4 GB file, still fits a full Wikipedia.
- **Shares with a room.** `--lan` lets every phone and laptop on the same Wi-Fi read
  from one drive. Only the computer running NABU can download, delete or stop it.
- **Respects the drive.** Nothing is written to the host computer. "Turn off NABU"
  stops it cleanly so the stick can be removed safely.
- **Light and dark** interface, phone-friendly, works in old browsers.

## Using a NABU drive

| Computer | Start it |
|---|---|
| Windows | Double-click **`Start NABU (Windows).bat`** |
| Mac | Double-click **`Start NABU (Mac).command`**. The first time, right-click it, choose **Open**, then **Open** (NABU is not notarised by Apple). |
| Linux | `sh start-nabu-linux.sh`, or double-click it and choose *Run in Terminal* |

The browser opens on your library. A small window stays open while NABU runs. Close
it, or click **Turn off NABU** at the bottom of any page, when you are done. If the
browser does not open by itself, go to <http://localhost:8080>.

**No Wikipedia yet?** The library page offers to download one. Sizes for English:

| Edition | What you get | Size |
|---|---|---|
| `wikipedia_en_all_maxi` | every article, with pictures | ~110 GB |
| `wikipedia_en_all_nopic` | every article, no pictures | ~50 GB |
| `wikipedia_en_all_mini` | the introduction of every article | ~12 GB |
| `wikipedia_en_top_maxi` | the most-read articles, with pictures | a few GB |
| `wikipedia_en_100_mini` | 100 articles, for trying NABU out | a few MB |

Other languages are smaller. Any `.zim` file from <https://download.kiwix.org/zim/>
also works (Wiktionary, Wikivoyage, Project Gutenberg, Stack Exchange…): drop it in
`library/` and click *Look for new files*.

**Which drive format?** Use **exFAT**: every Windows, Mac and Linux computer reads and
writes it. FAT32 works too, because NABU splits big files. Avoid NTFS, which Macs can
only read, so updates fail there.

## Making a NABU drive

You need a Unix-like shell (Linux, macOS, or Git Bash/WSL on Windows), `curl`, `unzip`
and, optionally, `zip`.

```bash
scripts/fetch-requisites.sh
```

```bash
scripts/build.sh
```

The first command downloads the two pinned Go toolchains for your computer and checks
their SHA-256. Everything else is already in the repository. The second builds every
platform into `dist/NABU/` and also produces `dist/NABU-<version>.zip`. Copy the
*contents* of `dist/NABU/` to the root of the stick:

```
NABU drive/
├── Start NABU (Windows).bat
├── Start NABU (Mac).command
├── start-nabu-linux.sh
├── READ ME FIRST.txt
├── nabu/            11 programs: Windows, macOS, Linux × Intel/AMD/ARM (+ legacy)
├── library/         put .zim files here (or download them from NABU)
└── licenses/
```

Then add a Wikipedia: start NABU and use **Get & update**, run
`nabu/nabu-<os>-<arch> download wikipedia_en_all_maxi --library library`, or copy a
`.zim` file into `library/` yourself.

Tagging a release (`git tag v1.0.0 && git push --tags`) makes GitHub Actions run the
tests on Windows, macOS and Linux and attach the ready-made drive zip to the release.

## Requisites (pinned, kept locally)

Every input needed to build NABU is pinned to an exact version and either **committed
in this repository** or **fetched and checksum-verified** by
`scripts/fetch-requisites.sh`. After that one fetch, building needs no network at all.
The machine-readable list is [`requisites/requisites.lock`](requisites/requisites.lock).

| Requisite | Version | Why | Kept where | Licence |
|---|---|---|---|---|
| Go toolchain | **1.24.7** | Builds the modern programs (Windows 10+, macOS 11+, all Linux) | Fetched to `requisites/toolchains/go1.24.7/` from the Go module proxy, SHA-256 in the lock file | BSD-3-Clause |
| Go toolchain (legacy) | **1.20.14** | The last Go release that runs on Windows 7/8 and macOS 10.13–10.15; builds the `-legacy` programs | Fetched to `requisites/toolchains/go1.20.14/` | BSD-3-Clause |
| `github.com/klauspost/compress` | **v1.17.9** | Zstandard decompression (modern ZIM files) | Committed in [`vendor/`](vendor/github.com/klauspost/compress), hash in `go.sum` | BSD-3-Clause, Apache-2.0, MIT (per package) |
| `github.com/ulikunitz/xz` | **v0.5.17** | XZ/LZMA2 decompression (older ZIM files) | Committed in [`vendor/`](vendor/github.com/ulikunitz/xz), hash in `go.sum` | BSD-3-Clause |
| Libertinus Serif (`@fontsource/libertinus-serif`) | **5.3.0** | Wikipedia's heading typeface, embedded in the program | Committed in [`internal/web/assets/fonts/`](internal/web/assets/fonts), integrity hash in the lock file | SIL OFL 1.1 |
| Test ZIM | libzim 9 via `python-libzim` **4.1.0** | Checks NABU's reader against the reference implementation | Committed at [`testdata/`](testdata), generator in `testdata/make_fixture.py` | — |

Both library versions declare `go 1.20`, so the same vendored code builds with both
toolchains. `go.mod` also declares `go 1.20` so nothing newer slips in by accident. CI
runs the tests under both Go versions to enforce it.

Pinned toolchain checksums (SHA-256 of the module-proxy zip):

| Toolchain | linux-amd64 | darwin-arm64 | windows-amd64 |
|---|---|---|---|
| go1.24.7 | `4c2a63c0…7b8031e` | `b529787f…ac604f63` | `615b3423…ddfae337` |
| go1.20.14 | `725ca027…54e7d26d` | `597bb4d3…9925967b` | `50b3d782…f0a58e02` |

Full hashes, plus linux-arm64 and darwin-amd64, are in the lock file.

## Where the Wikipedia comes from

You asked NABU to "ping the Wikidata page for the latest dump". Here is what actually
happens, and why:

1. **Wikimedia** (the foundation behind Wikipedia and Wikidata) publishes database
   dumps at <https://dumps.wikimedia.org>. These are raw *wikitext*: no pictures, no
   templates expanded, no rendering. Turning them into readable pages takes a full
   MediaWiki installation and days of computing. Wikidata, strictly, is the separate
   database of structured facts and has no article pages.
2. **Kiwix**, a non-profit partner of Wikimedia, renders each new dump with MediaWiki
   itself, then packs every page, picture, stylesheet and link into one compressed
   **ZIM** file per language and size. It publishes them at
   <https://download.kiwix.org/zim/wikipedia/>, typically every month or two.
3. **NABU** reads ZIM files. Each time it starts and finds the internet, it fetches
   the Kiwix catalogue, compares the date in each file name (`…_2026-10.zim`) with
   what is on the drive, and offers anything newer. With **automatic updates** on (a
   switch on the Get & update page), it downloads by itself when the new file fits.

So the dump NABU fetches is the latest Wikimedia snapshot, in the one form a laptop
can actually read offline. The catalogue address is a setting (`--catalog`), so a
school or a local mirror can point NABU elsewhere.

## How it works

```
 browser ──HTTP──▶ nabu (one static binary, listens on 127.0.0.1:8080)
                    ├─ /content/<book>/<page>  page bytes straight from the ZIM file
                    │                          (+ NABU bar injected into HTML pages)
                    ├─ /search, /suggest       title index binary search, case variants
                    ├─ /, /nabu/updates, …     NABU's own pages (embedded templates)
                    └─ updater                 Kiwix catalogue → resumable download
                                               → SHA-256 → split parts → hot reload
 library/*.zim ◀── ZIM reader: 80-byte header, pointer lists, zstd/xz clusters
```

- **The ZIM reader** (`internal/zim`) is written for slow USB sticks. Opening a
  100 GB file reads 80 bytes plus the MIME table. Finding an article is a binary
  search over on-disk pointer lists, about 25 small reads. Compressed clusters
  (zstd, xz, plus legacy zlib/bzip2) are decompressed once into a 64 MB LRU cache.
  Concurrent requests for the same cluster share one decompression. Images sit in
  uncompressed clusters and stream straight from disk, so HTTP range requests (audio,
  video) work. Old-style (`A/`, `I/`, `-/`) and modern (`C/`) namespaces are both
  supported, as are split `.zimaa/.zimab…` files.
- **Search** binary-searches the title index (the front-articles list in modern
  files) for several capitalisations of the query, follows redirects, removes
  duplicates and ranks exact matches first.
- **Security.** NABU listens only on `127.0.0.1` unless `--lan` is given. It refuses
  requests addressed to a domain name, which blocks DNS rebinding. Actions that
  change the drive need a loopback client, a custom header and a matching `Origin`,
  so a web page you visit cannot drive NABU.
- **Why Go and not Assembly?** You offered Assembly, so here is the reasoning.
  Assembly would mean rewriting the program for every processor (x86, x86-64, ARM)
  *and* every operating system's system calls, and it would still need a Zstandard
  decoder. Go compiles one readable codebase into fully static native programs for
  11 targets, with no runtime to install, at about 10 MB each. That is the property
  that makes a pen drive work on "any laptop in the world". The page you read is
  served from the file in a handful of disk reads, so the language was never the
  bottleneck; the USB stick is.

## Computers it runs on

| | Programs on the drive | Oldest supported |
|---|---|---|
| Windows | `windows-amd64`, `windows-386` (32-bit), `windows-arm64` | Windows 10; **Windows 7 / 8 / 8.1** via `windows-386-legacy` |
| macOS | `darwin-arm64` (Apple silicon), `darwin-amd64` (Intel) | macOS 11; **10.13–10.15** via `darwin-amd64-legacy` |
| Linux | `linux-amd64`, `linux-386`, `linux-arm64`, `linux-arm` (Raspberry Pi, ARMv6+) | kernel 3.2; no libc needed (static) |

The launchers pick the right file from the CPU type and OS version. On Linux, USB
sticks are often mounted so that programs cannot run from them (`noexec`, or no
executable bit on exFAT/FAT). The launcher detects this and runs a temporary copy
from `/tmp` instead. The Mac launcher also clears the download quarantine flag.

Any browser from the last decade works. The interface's script is plain ES5.

## Command line

```text
nabu [flags]                  start NABU and open the browser
  --library DIR               folder with .zim files (launchers pass the drive's library/)
  --port 8080                 first port to try (the next free one is used if busy)
  --lan                       share with other devices on the local network
  --no-browser                don't open a browser (servers, Raspberry Pi kiosks)
  --offline                   never touch the network
  --catalog URL               where to look for editions (default: Kiwix)
nabu catalog [lang]           list the newest editions online, e.g. `nabu catalog fr`
nabu download <name>          download headless, e.g. `nabu download wikipedia_en_all_nopic`
                              (Ctrl+C pauses; run again to resume)
nabu info <file.zim>          show a ZIM file's metadata
nabu version
```

Starting NABU twice is harmless: the second launch just opens the browser on the
first one.

## Developing

```bash
go test ./...
```

```bash
go run ./cmd/nabu --library testdata --offline
```

- `internal/zim`: ZIM reader. Tests build archives with an in-test writer and check
  against a file made by the official libzim writer (`testdata/`).
- `internal/library`: finds books, joins split files, hot-rescans.
- `internal/update`: catalogue parsing, update detection, resumable verified
  downloads. The tests simulate a server that drops the connection every 64 KB.
- `internal/server`: HTTP handlers, injection, API with CSRF/rebinding protection.
- `internal/web`: embedded templates, CSS, JS, fonts, logo.
- `launchers/`: the double-click scripts. `.bat` files are kept CRLF (`.gitattributes`).

To regenerate the test archive: `pip install libzim==4.1.0`, then
`python3 testdata/make_fixture.py testdata/wikipedia_en_test_maxi_2026-10.zim 300`.

## Limits and next steps

- **Search covers titles, not full text.** ZIM files carry a Xapian full-text index.
  Reading it means implementing Xapian's on-disk format, which is the largest item on
  the roadmap.
- **Unsigned programs.** Windows SmartScreen may say "Windows protected your PC";
  click *More info → Run anyway*. On a Mac, right-click → Open the first time. Signing
  certificates would remove both prompts.
- **Updates replace whole files.** Kiwix publishes no deltas, so a new edition is a
  full download. NABU resumes it across sessions and lets you delete the old one once
  the new one is verified.
- **Only Kiwix's Wikipedia directory is browsed** in the update page. Any other ZIM
  copied into `library/` is read fine, but NABU won't offer updates for it.

## Credits and licences

- NABU: MIT, see [LICENSE](LICENSE).
- Wikipedia content: [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/)
  by Wikipedia's volunteer authors. Images carry their own licences.
- ZIM format and files: [openZIM](https://openzim.org) and [Kiwix](https://kiwix.org).
- Libertinus Serif: SIL Open Font License 1.1. Go libraries: see `vendor/` and the
  `licenses/` folder on every drive.
