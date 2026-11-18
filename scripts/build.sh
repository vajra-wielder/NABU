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
mkdir -p "$OUT/nabu" "$OUT/library" "$OUT/licenses"

build() { # goos goarch output [go]
  local goos=$1 goarch=$2 out=$3 gobin=${4:-$GO} extra=()
  [ "$goarch" = arm ] && export GOARM=6 || unset GOARM
  printf '  %-34s' "$(basename "$out")"
  GOOS=$goos GOARCH=$goarch "$gobin" build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$out" ./cmd/nabu
  printf '%6s KB\n' "$(( $(wc -c <"$out") / 1024 ))"
}

echo "Building NABU $VERSION with $("$GO" version | cut -d' ' -f3)"
build windows amd64 "$OUT/nabu/nabu-windows-amd64.exe"
build windows arm64 "$OUT/nabu/nabu-windows-arm64.exe"
build windows 386   "$OUT/nabu/nabu-windows-386.exe"
build darwin  arm64 "$OUT/nabu/nabu-darwin-arm64"
build darwin  amd64 "$OUT/nabu/nabu-darwin-amd64"
build linux   amd64 "$OUT/nabu/nabu-linux-amd64"
build linux   arm64 "$OUT/nabu/nabu-linux-arm64"
build linux   386   "$OUT/nabu/nabu-linux-386"
build linux   arm   "$OUT/nabu/nabu-linux-arm"

if [ -z "${NO_LEGACY:-}" ]; then
  if [ -n "$GO_LEGACY" ]; then
    echo "Legacy builds (Windows 7/8, macOS 10.13-10.15) with $("$GO_LEGACY" version | cut -d' ' -f3)"
    build windows 386   "$OUT/nabu/nabu-windows-386-legacy.exe" "$GO_LEGACY"
    build darwin  amd64 "$OUT/nabu/nabu-darwin-amd64-legacy" "$GO_LEGACY"
  else
    echo "WARNING: no Go 1.20 toolchain found, skipping Windows 7/8 and old macOS builds."
    echo "         Run scripts/fetch-requisites.sh first, or set GO_LEGACY."
  fi
fi

cp launchers/"Start NABU (Windows).bat" launchers/"Start NABU (Mac).command" \
   launchers/start-nabu-linux.sh launchers/"READ ME FIRST.txt" "$OUT/"
chmod +x "$OUT/Start NABU (Mac).command" "$OUT/start-nabu-linux.sh" "$OUT"/nabu/nabu-darwin-* "$OUT"/nabu/nabu-linux-*
cp LICENSE "$OUT/licenses/NABU-MIT.txt"
cp internal/web/assets/fonts/OFL-LICENSE.txt "$OUT/licenses/Libertinus-OFL.txt"

if command -v zip >/dev/null; then
  (cd dist && zip -qr "NABU-$VERSION.zip" NABU)
  echo "Packed dist/NABU-$VERSION.zip ($(( $(wc -c <"dist/NABU-$VERSION.zip") / 1048576 )) MB)"
fi
echo
echo "Done. Copy everything inside dist/NABU/ to the root of your pen drive,"
echo "then download a Wikipedia from NABU's \"Get & update\" page, or copy a"
echo "Wikimedia dump (…-pages-articles-multistream.xml.bz2 + its -index.txt.bz2)"
echo "into its library/ folder."
