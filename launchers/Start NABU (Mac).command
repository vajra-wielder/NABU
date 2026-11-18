#!/bin/sh
# NABU - offline Wikipedia. Double-click this file to start.
# If macOS says it cannot be opened: on macOS 15 and newer go to System
# Settings > Privacy & Security and click "Open Anyway"; on older macOS
# right-click this file, choose Open, then Open.
DIR="$(cd "$(dirname "$0")" && pwd)"

case "$(uname -m)" in
  arm64) ARCH=arm64 ;;
  *)     ARCH=amd64 ;;
esac
BIN="$DIR/programs/mac/nabu-darwin-$ARCH"

# macOS 10.13 to 10.15 need the legacy build.
MAJOR=$(sw_vers -productVersion 2>/dev/null | cut -d. -f1)
if [ -n "$MAJOR" ] && [ "$MAJOR" -lt 11 ] 2>/dev/null; then
  BIN="$DIR/programs/mac/nabu-darwin-amd64-legacy"
fi

if [ ! -f "$BIN" ]; then
  echo "NABU could not find $BIN"
  echo "This copy of NABU has no programs for this computer. Copy the"
  echo "\"programs/mac\" folder from a complete NABU drive next to this file."
  exit 1
fi

# Files copied from a downloaded zip carry a quarantine flag that stops
# them running; NABU is unsigned, so lift it.
xattr -d com.apple.quarantine "$BIN" 2>/dev/null

# Some drives cannot run programs directly. Then run a copy from the
# computer's temporary folder (nothing is installed).
if ! "$BIN" version >/dev/null 2>&1; then
  TMP="${TMPDIR:-/tmp}/nabu-$(id -u)"
  mkdir -p "$TMP" && cp "$BIN" "$TMP/nabu.$$" && chmod +x "$TMP/nabu.$$" && mv -f "$TMP/nabu.$$" "$TMP/nabu"
  xattr -c "$TMP/nabu" 2>/dev/null
  BIN="$TMP/nabu"
fi

exec "$BIN" --drive "$DIR" "$@"
