# Which computers can run NABU

**Short answer: about 97% of the world's desktop and laptop computers, measured by
web use (StatCounter, mid-2026). It stays above 90% even after generously
subtracting machines whose owners or administrators forbid running programs from a
USB stick.** Every phone and tablet can *read* from a NABU computer over Wi-Fi.

This document explains how that figure is built, what NABU itself needs (measured,
not guessed), which machines are left out, and what would close each remaining gap.

## 1. What NABU needs from a computer

NABU sits on a stack of four layers, and a computer must pass all four.

| Layer | NABU needs | Why |
|---|---|---|
| Operating system + processor | One of NABU's 11 programs runs on it | NABU is a native program (Go, statically linked, no runtime) |
| Policy | The OS allows an unsigned program to run from a USB stick | NABU is not code-signed (yet) |
| Browser | Any browser from 2016 onwards, or Internet Explorer 11 | NABU shows pages in the computer's own browser |
| Memory, disk | About 1 GB of RAM; a stick large enough for the dump | See the measurements below |

### Operating systems and processors (the 11 programs)

| OS | Programs | Oldest version | Limit set by |
|---|---|---|---|
| Windows | 64-bit x86, 32-bit x86, ARM64 | **Windows 10**, plus **Windows 7 SP1 / 8 / 8.1** through `windows-386-legacy` (built with Go 1.20) | Go 1.21+ needs Windows 10; Go 1.20 is the last for 7/8 ([Go 1.20 notes](https://golang.google.cn/doc/go1.20)) |
| macOS | Apple silicon, Intel | **macOS 11**, plus **10.13 High Sierra – 10.15** through `darwin-amd64-legacy` | Go 1.20 is the last for 10.13/10.14 ([Go 1.20 notes](https://golang.google.cn/doc/go1.20)) |
| Linux | x86-64, x86 (32-bit), ARM64, ARMv6+ | **kernel 3.2** (2012); no C library needed | Go 1.24 minimum ([Go wiki](https://golang.google.cn/wiki/MinimumRequirements)) |

The **processor floor** is as low as Go allows. The 32-bit Linux and Windows 7
programs are built with `GO386=softfloat`, so they need no SSE2 and run on a
Pentium III or Athlon XP (around 2000). ARM goes down to the ARMv6 in the
original Raspberry Pi and Pi Zero. The launchers pick the right program from the
processor type and OS version, so nobody has to choose.

### Browsers

NABU's pages are plain HTML. They use CSS custom properties (Chrome 49, Firefox 31,
Safari 9.1 and Edge 15, all 2014–2017) and ES5 JavaScript that runs even in old
browsers. **Internet Explorer 11** is the built-in browser of Windows 7 and 8.1 and
lacks CSS custom properties. NABU detects it and serves stylesheets with every
variable resolved. On Windows, if IE is still the default browser but Chrome,
Firefox, Edge, Opera, Brave or Yandex is installed, NABU opens that one instead.
Search suggestions understand IE's old key names. Everything else on the page
degrades gracefully (no sticky bar, contents always expanded).

Today's desktop browser market is Chrome, Edge, Safari, Firefox and Opera at ~96%
together ([StatCounter, Sept 2026](https://gs.statcounter.com/browser-market-share/desktop-tablet-console/worldwide/)).
All of them have supported everything NABU uses for about ten years.

### Memory, speed and storage: measured

Measured on this project's build machine with NABU limited to **one CPU core**
(`GOMAXPROCS=1`). "6M titles" is a synthetic index one fifth the size of English
Wikipedia's ~30 million titles. Simple English is a real Wikimedia dump.

| Task | 64-bit program | 32-bit program |
|---|---|---|
| Idle, two Wikipedias open (563k + 6M titles) | 33 MB | 24 MB |
| After opening 300 random articles | 143 MB (peak 147 MB) | 124 MB (peak 131 MB) |
| Open an article (bunzip2 one stream + render) | median **23 ms**, 95% under 70 ms | median **25 ms**, 95% under 73 ms |
| Search-as-you-type on 6M titles | 0.2 ms | 0.4 ms |
| Prepare the index, 563k titles | 1.9 s, peak 148 MB | 2.3 s, peak 109 MB |
| Prepare the index, 6M titles | 21 s, peak **264 MB** | 25 s, peak **102 MB** |

Preparation memory is capped by a sorting budget (128 MB on 64-bit, 48 MB on 32-bit)
with an external merge sort. Bigger wikis use more temporary files on the stick, not
more memory. **English** (~5× the synthetic set) should therefore prepare in about 2
minutes on a modern core and perhaps 10–15 on an old netbook, in the same memory.
Its index is about 0.7 GB.

**What this means for old hardware:** a 2008-era netbook (Intel Atom, 1 GB RAM)
is perhaps 4–6× slower per core than the test machine. Articles should still open in
about 0.1–0.2 s, and memory stays well inside 1 GB. Windows 7 itself needs 1 GB
(32-bit) or 2 GB (64-bit), so **any machine that runs a supported OS has enough memory
for NABU**. USB 2.0 sticks add a few milliseconds per article: about half a megabyte is
read per page.

**Storage:** English 25.4 GB plus 0.7 GB index fits a **32 GB** stick. All ten offered
languages (≈55 GB) fit a **64 GB** stick. FAT32 works (big files are split into 4 GB
pieces), exFAT is best, and NTFS is read-only on Macs.

## 2. How much of the world that covers

### The data

Desktop/laptop operating systems by share of web traffic, StatCounter, **August
2026** ([source](https://gs.statcounter.com/os-market-share/desktop-operating-systems/worldwide)):

| OS | Share |
|---|---|
| Windows | 63.03% |
| macOS (StatCounter's "macOS" 15.08% + "OS X" 11.57%) | 26.65% |
| Linux | 8.76% |
| ChromeOS | 1.56% |

Windows versions, desktop, **July 2026** ([source](https://gs.statcounter.com/windows-version-market-share/desktop/)):
Windows 11 68.93%, Windows 10 29.83%, **Windows 7 1.01%**, Windows 8.1 0.09%,
Windows 8 0.02%, **Windows XP 0.09%**, others ≈0.03%.

macOS versions: StatCounter's per-version macOS figures can't be used, because
browsers have reported "10.15" on every Mac since 2020 (Apple froze the version in
the user agent), so "Catalina" shows 47–55%
([source](https://gs.statcounter.com/macos-version-market-share/desktop/worldwide)).
The hardware cut-off is used instead: macOS 10.13 runs on Macs from late 2009/2010
onwards. Macs older than that and still in use are estimated here at no more than 2%
of Macs.

### The calculation

| OS | Share of desktops | NABU covers | Of all desktops |
|---|---|---|---|
| Windows | 63.03% | 7, 8, 8.1, 10, 11 = 99.88% of Windows (not XP/Vista) | **62.95%** |
| macOS | 26.65% | 10.13 and newer, est. ≥98% of Macs | **≈26.1%** |
| Linux | 8.76% | x86, x86-64, ARM, ARM64, kernel ≥3.2, est. ≥99% (not RISC-V, PowerPC, …) | **≈8.7%** |
| ChromeOS | 1.56% | only with Linux turned on: counted as **0** | 0 |
| **Total** | | | **≈97.7%** |

**After policy blocks.** Some computers that *can* run NABU are set up to refuse it:

- **Windows Smart App Control** blocks unsigned programs outright. It is only enabled
  on some clean installs of Windows 11, in some regions, and Microsoft publishes no
  adoption figure ([Microsoft](https://learn.microsoft.com/en-ie/windows/apps/develop/smart-app-control/overview)).
- **Windows in S mode** runs only Store apps.
- **Managed school and office PCs** often block programs from USB sticks (AppLocker,
  device control).

No reliable figure exists for any of these. Subtracting a deliberately generous 5%
of all Windows machines (3.2 points) still leaves **≈94.6%**. The "more than 90%"
target holds even under pessimistic assumptions.

Not blocked by policy: **SmartScreen** ("Windows protected your PC") is triggered by
the "downloaded from the internet" mark, which FAT32 and exFAT sticks cannot even
store. A drive prepared on another computer therefore doesn't trigger it. **Gatekeeper** on
macOS only stops quarantined files. The Mac launcher removes the flag from NABU's
programs, and the launcher itself needs one approval if the zip was downloaded on a
Mac (macOS 15+: *System Settings → Privacy & Security → Open Anyway*;
[details](https://mjtsai.com/blog/2024/07/05/sequoia-removes-gatekeeper-contextual-menu-override)).

### Caveats on the data

- StatCounter counts **web traffic, not installed computers**. Old, rarely-online
  machines, which are exactly the ones NABU is for, are under-counted. That is why
  the Windows 7/8 and macOS 10.13–10.15 programs exist even though their measured
  share is about 1%.
- StatCounter's month-to-month figures swing (an "Unknown" bucket of up to 21%
  appeared in June 2026). The August snapshot used here has none. The conclusion
  doesn't depend on a single month: Windows 7+ is above 99.8% of Windows in every
  2026 snapshot.

## 3. Who is left out, and what would help

| Not covered | Share (est.) | Why | What could change it |
|---|---|---|---|
| Machines that forbid unsigned programs (Smart App Control, S mode, managed PCs) | unknown, assumed ≤3% | policy | **Code signing**: Windows Authenticode (e.g. Azure Trusted Signing, about $10/month) and an Apple Developer ID with notarisation ($99/year). This is the single biggest remaining improvement. Until then: run NABU on another machine with `--lan`. |
| ChromeOS | 1.56% | runs no native programs | Works today if the *Linux development environment* is on (see *READ ME FIRST*). School-managed Chromebooks usually have it off, so read over `--lan` from another computer. |
| Macs older than 2009/10 (macOS ≤10.12) | ≤0.5% | Go 1.20 needs 10.13 | Would need a separate, older toolchain and code base; not worth it. |
| Windows XP / Vista | ≈0.1% | Go dropped them in 2018 | Same; Go 1.10 lacks features NABU uses. |
| Linux on RISC-V, PowerPC, LoongArch; kernels before 3.2 | ≪0.1% | not built | RISC-V and LoongArch are one line each in `scripts/build.sh` once there is demand. |

**Phones and tablets** are not NABU hosts. A phone can't run a program from a USB stick
in a supported way. But any phone, tablet, Chromebook or locked PC with a browser can
**read** from a computer running `nabu --lan` on the same Wi-Fi. That is how one
stick can serve a whole classroom.

## 4. Verified vs. estimated

Verified in this project:

- Builds for all 11 targets; tests pass with Go 1.24 and Go 1.20, as 64-bit and as
  32-bit softfloat (no SSE2), and under the race detector.
- The Linux launcher works from a drive whose program has lost its executable bit.
- Real downloads from dumps.wikimedia.org verify against Wikimedia's SHA-1.
- All the measurements in section 1.
- The IE11 stylesheet contains no CSS variables (automated test).

Not verified on real hardware (no such machines were available):

- Windows 7, macOS 10.13, Windows on ARM, Raspberry Pi, a ChromeOS Linux container,
  Internet Explorer 11 itself.
- How long an old netbook takes to prepare English Wikipedia.

The CI workflow tests on current Windows, macOS and Linux. The table below is the
manual checklist for a release:

| Machine | Check |
|---|---|
| Windows 7 SP1, 32-bit, IE11 default, no other browser | launcher picks `windows-386-legacy`; pages styled in IE11 |
| Windows 7 SP1, 64-bit, Chrome installed, IE default | Chrome opens, not IE |
| Windows 11 ARM | `windows-arm64` runs |
| macOS 10.13 Intel | `darwin-amd64-legacy` runs |
| macOS 15 Apple silicon, zip downloaded from the web | one *Open Anyway*, then runs |
| Ubuntu 16.04 i386 on a Pentium M | `linux-386` (softfloat) runs from a `noexec` stick |
| Raspberry Pi Zero (ARMv6) | `linux-arm` runs; article opens in < 2 s |
| Chromebook with Linux on | `--lan`, opens at `penguin.linux.test:8080` |

## Sources

- StatCounter: [desktop OS share](https://gs.statcounter.com/os-market-share/desktop-operating-systems/worldwide),
  [Windows versions](https://gs.statcounter.com/windows-version-market-share/desktop/),
  [macOS versions](https://gs.statcounter.com/macos-version-market-share/desktop/worldwide),
  [desktop browsers](https://gs.statcounter.com/browser-market-share/desktop-tablet-console/worldwide/)
- Go: [1.20 release notes](https://golang.google.cn/doc/go1.20), [minimum requirements](https://golang.google.cn/wiki/MinimumRequirements)
- Microsoft: [Smart App Control](https://learn.microsoft.com/en-ie/windows/apps/develop/smart-app-control/overview)
- macOS Sequoia Gatekeeper change: [Michael Tsai](https://mjtsai.com/blog/2024/07/05/sequoia-removes-gatekeeper-contextual-menu-override)
- Steam Hardware Survey (memory context: 4 GB machines ≈1.4%, 8 GB ≈8% of gaming PCs, mid-2026),
  via [Tech Insider](https://tech-insider.org/steam-hardware-survey-august-2026/)
