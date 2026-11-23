#!/usr/bin/env bash
# Build the NABU pen-drive folder: dist/NABU/
#
# Uses the pinned toolchains from requisites/toolchains/ when present
# (scripts/fetch-requisites.sh), otherwise the `go` on your PATH. Builds
# offline: NABU uses only the Go standard library.
#
#   scripts/build.sh                 build every platform into dist/NABU
#   VERSION=1.0.0 scripts/build.sh   stamp a version
#   NO_LEGACY=1 scripts/build.sh     skip Windows 7 / old macOS builds
#   WITH_SOURCE=1 scripts/build.sh   also put the source code and the pinned
#                                    toolchain archives in NABU/source/
#
# Each part of the drive is its own folder (programs/windows, programs/mac,
# programs/linux, wikipedia, search, pictures, ...), so a smaller copy is
# made by leaving folders out. See README, "What is on the drive".
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
GO="${GO:-}"
if [ -z "$GO" ]; then
  if [ -x requisites/toolchains/go1.24.7/bin/go ]; then GO="$PWD/requisites/toolchains/go1.24.7/bin/go"; else GO=go; fi
fi
GO_LEGACY="${GO_LEGACY:-}"
if [ -z "$GO_LEGACY" ] && [ -x requisites/toolchains/go1.20.14/bin/go ]; then
  GO_LEGACY="$PWD/requisites/toolchains/go1.20.14/bin/go"
fi
export CGO_ENABLED=0 GOTOOLCHAIN=local

OUT=dist/NABU
rm -rf dist
W="$OUT/programs/windows" M="$OUT/programs/mac" L="$OUT/programs/linux"
mkdir -p "$W" "$M" "$L" "$OUT/wikipedia" "$OUT/licenses"

build() { # goos goarch output [go]
  local goos=$1 goarch=$2 out=$3 gobin=${4:-$GO} extra=()
  [ "$goarch" = arm ] && export GOARM=6 || unset GOARM
  printf '  %-34s' "$(basename "$out")"
  GOOS=$goos GOARCH=$goarch GO386="${GO386:-sse2}" "$gobin" build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$out" ./cmd/nabu
  printf '%6s KB\n' "$(( $(wc -c <"$out") / 1024 ))"
}

echo "Building NABU $VERSION with $("$GO" version | cut -d' ' -f3)"
build windows amd64 "$W/nabu-windows-amd64.exe"
build windows arm64 "$W/nabu-windows-arm64.exe"
build windows 386   "$W/nabu-windows-386.exe"
build darwin  arm64 "$M/nabu-darwin-arm64"
build darwin  amd64 "$M/nabu-darwin-amd64"
build linux   amd64 "$L/nabu-linux-amd64"
build linux   arm64 "$L/nabu-linux-arm64"
GO386=softfloat build linux 386 "$L/nabu-linux-386"  # no SSE2 needed: Pentium III, Athlon XP
build linux   arm   "$L/nabu-linux-arm"

if [ -z "${NO_LEGACY:-}" ]; then
  if [ -n "$GO_LEGACY" ]; then
    echo "Legacy builds (Windows 7/8, macOS 10.13-10.15) with $("$GO_LEGACY" version | cut -d' ' -f3)"
    GO386=softfloat build windows 386 "$W/nabu-windows-386-legacy.exe" "$GO_LEGACY"
    build darwin  amd64 "$M/nabu-darwin-amd64-legacy" "$GO_LEGACY"
  else
    echo "WARNING: no Go 1.20 toolchain found, skipping Windows 7/8 and old macOS builds."
    echo "         Run scripts/fetch-requisites.sh first, or set GO_LEGACY."
  fi
fi

cp launchers/"Start NABU (Windows).bat" launchers/"Start NABU (Mac).command" \
   launchers/start-nabu-linux.sh launchers/"READ ME FIRST.txt" \
   launchers/"Set up NABU (Windows).bat" launchers/"Set up NABU (Mac).command" \
   launchers/set-up-nabu-linux.sh "$OUT/"
# Windows reads batch files with Windows line endings only reliably (with
# bare LF it can drop or garble characters), whatever the checkout did;
# the read-me too, for old Notepad.
for f in "$OUT"/*.bat "$OUT/READ ME FIRST.txt"; do
  sed -i.tmp -e 's/\r*$/\r/' "$f" && rm -f "$f.tmp"
done
chmod +x "$OUT/Start NABU (Mac).command" "$OUT/start-nabu-linux.sh" "$OUT/Set up NABU (Mac).command" "$OUT/set-up-nabu-linux.sh" "$M"/nabu-darwin-* "$L"/nabu-linux-*
cp LICENSE "$OUT/licenses/NABU-MIT.txt"
cp internal/web/assets/fonts/OFL-LICENSE.txt "$OUT/licenses/Libertinus-OFL.txt"
cp internal/web/assets/WORLD-MAP-LICENSE.txt "$OUT/licenses/World-map.txt"

# Each folder says what it is for and whether it can be left out.
"$GO" run ./cmd/nabu about-folders --drive "$OUT" >/dev/null

if [ -n "${WITH_SOURCE:-}" ]; then
  mkdir -p "$OUT/source"
  git archive --format=zip -o "$OUT/source/nabu-source-$VERSION.zip" HEAD
  if [ -d requisites/downloads ]; then cp -r requisites/downloads "$OUT/source/requisites"; fi
  cp requisites/requisites.lock "$OUT/source/"
  echo "Source and requisites: $(du -sh "$OUT/source" | cut -f1)"
fi

if command -v zip >/dev/null; then
  (cd dist && zip -qr "NABU-$VERSION.zip" NABU)
  echo "Packed dist/NABU-$VERSION.zip ($(( $(wc -c <"dist/NABU-$VERSION.zip") / 1048576 )) MB)"
fi
echo
echo "Done. Copy everything inside dist/NABU/ to the root of your pen drive,"
echo "then download a Wikipedia from NABU's \"Get & update\" page, or copy a"
echo "Wikimedia dump (…-pages-articles-multistream.xml.bz2 + its -index.txt.bz2)"
echo "into its wikipedia/ folder."
