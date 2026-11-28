#!/usr/bin/env bash
# Every test on real data, in order, each with its time, the results
# written into docs/realtest/ in the source code, and at the end an offer
# to upload them (git commit and push). From a Windows terminal:
#
#   scripts\test-all.bat              English (enwiki)
#   set WIKI=simplewiki & scripts\test-all.bat
#
# It uses the test folder (NABU-test beside the source code, or the one
# NABU_TEST names) and changes nothing in it but what NABU itself writes.
# English takes about an hour: the drive check (20 min) and the studies.
#
# Steps:
#   1. the unit tests (quick)
#   2. the new readers (wikitext scanners, dump pages) against the old
#      ones on real articles
#   3. realtest: title index and search inside articles ready, drive
#      check, searches and 3,000 random articles through NABU itself
#   4. the studies of the search index (size, search, snippets, build,
#      words, what titles name)
#   5. upload
set -u
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
export WIKI=${WIKI:-enwiki}
export GOTOOLCHAIN=local
GO=go
for g in requisites/toolchains/go1.24.7/bin/go requisites/toolchains/go1.24.7/bin/go.exe; do
	[ -x "$g" ] && GO=$ROOT/$g && break
done
mkdir -p docs/realtest
SUM=docs/realtest/test-all-$WIKI.txt
LOG=docs/realtest/test-all-$WIKI.log
: > "$LOG"
{
	echo "NABU tests on real data, $WIKI, $(date -u +%F' '%H:%MZ), $(uname -s)"
	echo "source code at $(git log -1 --format='%h %s' 2>/dev/null)"
} > "$SUM"
fails=0
now() { date +%s; }
step() { # name, command...
	local name=$1; shift
	local t0=$(now)
	echo
	echo "=== $name ==="
	echo "=== $name ===" >> "$LOG"
	"$@" 2>&1 | tee -a "$LOG"
	local rc=${PIPESTATUS[0]}
	local took=$(( $(now) - t0 ))
	local res=PASS
	[ $rc -ne 0 ] && res="FAIL (exit $rc)" && fails=$((fails+1))
	printf '%-44s %-14s %dm%02ds\n' "$name" "$res" $((took/60)) $((took%60)) | tee -a "$SUM"
}

step "1. unit tests" "$GO" test ./...
step "2. new readers against the old, on real articles" "$GO" test ./internal/wikitext ./internal/dump -run 'ScannersOnReal|FastPageReal' -v
step "3. realtest (NABU itself)" bash scripts/realtest.sh
step "4. studies of the search index" "$GO" test ./internal/fulltext -run 'StudySize|StudySearch|StudySnippets|StudyBuild|StudyWords|StudyTitleVocab' -v -timeout 4h

echo
echo "=== summary ==="
cat "$SUM"
[ -f "docs/realtest/realtest-$WIKI/report.txt" ] && echo && cat "docs/realtest/realtest-$WIKI/report.txt"
echo
echo "Results: docs/realtest/ (this summary in $SUM, everything printed in $LOG)."
[ $fails -gt 0 ] && echo "$fails step(s) failed: the log says why."

echo
answer=n
if [ -t 0 ]; then
	read -r -p "Upload the results now (git commit and push)? [Y/n] " answer
fi
case "$answer" in
[nN]*) echo "Not uploaded. Later: git add docs/realtest && git commit -m \"Test results\" && git push" ;;
*)
	git add docs/realtest && git commit -q -m "Test results: $WIKI, $(date -u +%F)" && git push && echo "Uploaded."
	;;
esac
[ $fails -eq 0 ]
