#!/usr/bin/env bash
# Download the pinned Go toolchains listed in requisites/requisites.lock
# into requisites/toolchains/, verifying each against its SHA-256.
# After this, NABU builds with no network access at all: the libraries
# are in vendor/ and the typeface is in internal/web/assets/fonts/.
#
#   scripts/fetch-requisites.sh            toolchains for this computer
#   scripts/fetch-requisites.sh --all      every platform (for an archive)
set -euo pipefail
cd "$(dirname "$0")/.."

case "$(uname -s)" in
  Linux) os=linux ;; Darwin) os=darwin ;; MINGW*|MSYS*|CYGWIN*) os=windows ;;
  *) echo "unsupported host $(uname -s)"; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported host CPU $(uname -m)"; exit 1 ;;
esac
want="$os-$arch"
[ "${1:-}" = "--all" ] && want="*"

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

mkdir -p requisites/downloads requisites/toolchains
# A go.mod here keeps `go build ./...` in NABU from wandering into the
# toolchains' own source code.
printf 'module nabu-requisites-toolchains\n' > requisites/toolchains/go.mod
grep '^toolchain ' requisites/requisites.lock | while read -r _ _ ver plat sum url; do
  # shellcheck disable=SC2053
  [[ "$plat" == $want ]] || continue
  zip="requisites/downloads/go$ver.$plat.zip"
  if [ ! -f "$zip" ] || [ "$(sha256 "$zip")" != "$sum" ]; then
    echo "Downloading Go $ver for $plat ..."
    curl -fL --retry 4 -o "$zip.part" "$url"
    mv "$zip.part" "$zip"
  fi
  got="$(sha256 "$zip")"
  if [ "$got" != "$sum" ]; then
    echo "CHECKSUM MISMATCH for $zip"; echo "  expected $sum"; echo "  got      $got"; exit 1
  fi
  echo "ok  go$ver $plat  $sum"
  if [ "$plat" = "$os-$arch" ]; then
    dest="requisites/toolchains/go$ver"
    if [ ! -x "$dest/bin/go" ] && [ ! -x "$dest/bin/go.exe" ]; then
      tmp="$(mktemp -d)"
      unzip -q "$zip" -d "$tmp"
      rm -rf "$dest"
      mv "$tmp"/golang.org/toolchain@*/ "$dest"
      rm -rf "$tmp"
      chmod +x "$dest"/bin/* "$dest"/pkg/tool/*/* 2>/dev/null || true
    fi
    echo "    installed at $dest"
  fi
done
echo "Requisites ready. Build with: scripts/build.sh"
