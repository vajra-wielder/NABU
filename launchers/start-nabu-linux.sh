#!/bin/sh
# NABU - offline Wikipedia. Start it with:  sh start-nabu-linux.sh
# (or double-click and choose "Run" / "Run in terminal").
DIR="$(cd "$(dirname "$0")" && pwd)"

case "$(uname -m)" in
  x86_64|amd64)          ARCH=amd64 ;;
  aarch64|arm64|armv8*)  ARCH=arm64 ;;
  i386|i486|i586|i686)   ARCH=386 ;;
  armv6*|armv7*|arm*)    ARCH=arm ;;
  *) echo "Sorry, NABU has no build for this processor ($(uname -m))."; exit 1 ;;
esac
BIN="$DIR/nabu/nabu-linux-$ARCH"

if [ ! -f "$BIN" ]; then
  echo "NABU could not find $BIN"
  echo "The drive may be incomplete. Copy the whole NABU folder again."
  exit 1
fi

# USB drives are often mounted "noexec" on Linux, or lose the executable
# bit on FAT/exFAT. Then run a copy from a temporary folder instead.
if ! "$BIN" version >/dev/null 2>&1; then
  TMP="${XDG_RUNTIME_DIR:-/tmp}/nabu-$(id -u)"
  mkdir -p "$TMP" && cp "$BIN" "$TMP/nabu.$$" && chmod +x "$TMP/nabu.$$" && mv -f "$TMP/nabu.$$" "$TMP/nabu"
  BIN="$TMP/nabu"
fi

exec "$BIN" --library "$DIR/library" "$@"
