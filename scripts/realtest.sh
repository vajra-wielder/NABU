#!/usr/bin/env bash
# Unattended test of NABU against a real, full-size dump.
#
#   scripts/realtest.sh [folder] [step...]   steps: prepare check serve (default: all)
#   scripts\realtest.bat [folder] [step...]  the same, from a Windows terminal
#
# folder is the NABU folder with the dumps (or set NABU_DRIVE). Without one,
# the checkout's test folder ("Nabu Test system") is used.
#
# Environment:
#   NABU_BIN    the nabu program            (default: build ./cmd/nabu)
#   NABU_DRIVE  the NABU folder with dumps  (dump already downloaded)
#   WIKI        the wiki to test            (default: enwiki)
#   CRAWL       random articles to open     (default: 3000)
#   PORT        port for the test server    (default: 8765)
#
# Shows each step live, writes a short report to <folder>/realtest/report.txt
# (meant to be read whole) and full logs next to it.
set -u
ROOT=$(cd "$(dirname "$0")/.." && pwd)
if [ $# -gt 0 ] && [ -d "$1" ]; then NABU_DRIVE=$1; shift; fi
if [ -z "${NABU_DRIVE:-}" ]; then
	for d in "$ROOT"/*/; do
		n=$(basename "$d")
		if echo "$n" | grep -qiE '^nabu[ _-]*test'; then NABU_DRIVE=${d%/}; break; fi
	done
fi
[ -n "${NABU_DRIVE:-}" ] || { echo "No NABU folder: give it as the first argument, or set NABU_DRIVE."; exit 2; }
command -v cygpath >/dev/null && NABU_DRIVE=$(cygpath -u "$NABU_DRIVE") # C:\... from a Windows terminal
DRIVE=$NABU_DRIVE
WIKI=${WIKI:-enwiki}
CRAWL=${CRAWL:-3000}
PORT=${PORT:-8765}
OUT=$DRIVE/realtest
mkdir -p "$OUT"
REPORT=$OUT/report.txt
STEPS=${*:-prepare check serve}

case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) WIN=1 ;; *) WIN= ;; esac
if [ -z "${NABU_BIN:-}" ] && [ -n "$WIN" ] && [ -x "$DRIVE/programs/windows/nabu-windows-amd64.exe" ]; then
	NABU_BIN=$DRIVE/programs/windows/nabu-windows-amd64.exe # test the program on the drive
fi
if [ -z "${NABU_BIN:-}" ]; then
	NABU_BIN=$OUT/nabu${WIN:+.exe}
	GO=go; for g in "$ROOT"/requisites/toolchains/go1.24.7/bin/go "$ROOT"/requisites/toolchains/go1.24.7/bin/go.exe; do [ -x "$g" ] && GO=$g && break; done
	(cd "$ROOT" && CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" build -o "$NABU_BIN" ./cmd/nabu) || { echo "build failed"; exit 1; }
fi

say() { echo "$*" | tee -a "$REPORT"; }
now() { date +%s.%N; }
secs() { awk -v a="$1" -v b="$2" 'BEGIN{printf "%.1f", b-a}'; }

: > "$REPORT"
say "NABU real-data test  $(date -u +%F' '%H:%MZ)  $(uname -s)  $("$NABU_BIN" version 2>&1 | head -1)"
say "drive $DRIVE  wiki $WIKI"
"$NABU_BIN" info --drive "$DRIVE" > "$OUT/info.log" 2>&1
say "$(head -5 "$OUT/info.log")"

run_step() { # name, command...
	local name=$1; shift
	local t0=$(now)
	echo "[$name] running... (live below; also in $OUT/$name.log)"
	"$@" 2>&1 | tee "$OUT/$name.log"
	local rc=${PIPESTATUS[0]}
	echo
	local res=PASS; [ $rc -ne 0 ] && res="FAIL (exit $rc)"
	say "[$name] $res in $(secs "$t0" "$(now)") s"
	[ $rc -ne 0 ] && tr '\r' '\n' < "$OUT/$name.log" | grep -v '^\s*$' | tail -5 | sed 's/^/    /' | tee -a "$REPORT"
	return $rc
}

for step in $STEPS; do case $step in
prepare)
	run_step prepare "$NABU_BIN" prepare --full-text --drive "$DRIVE"
	say "    title index $(du -sh "$DRIVE/indexes" 2>/dev/null | cut -f1), full-text $(du -sh "$DRIVE/search" 2>/dev/null | cut -f1), dumps $(du -sh "$DRIVE/wikipedia" | cut -f1)"
	;;
check)
	run_step check "$NABU_BIN" check --drive "$DRIVE" "$WIKI"
	say "    $(tr '\r' '\n' < "$OUT/check.log" | grep -v '^\s*$' | tail -1)"
	;;
serve)
	# NABU_FROM_COPY: serve from this very program, not a copy it starts
	# apart (one that would outlive the test and keep the port).
	NABU_FROM_COPY=1 "$NABU_BIN" --drive "$DRIVE" --offline --no-browser --port "$PORT" > "$OUT/server.log" 2>&1 &
	SRV=$!
	trap 'kill $SRV 2>/dev/null' EXIT
	t0=$(now)
	for _ in $(seq 600); do curl -s -o /dev/null "http://127.0.0.1:$PORT/nabu/api/ping" && break; sleep 0.5; done
	say "[startup] server answering after $(secs "$t0" "$(now)") s"
	BASE=http://127.0.0.1:$PORT/$WIKI

	# get URL -> "code seconds bytes"; follows NABU's redirects (a title
	# like "UK" leads to its article, the way a reader's click does)
	get() { curl -s -L -o "$OUT/last.html" -w '%{http_code} %{time_total} %{size_download}' --max-time 60 "$1"; }
	# percent-encode UTF-8 bytes (no python3 needed: Windows has none by default)
	enc() { local LC_ALL=C s=$1 i c o=; for ((i=0; i<${#s}; i++)); do c=${s:i:1}
		case $c in [A-Za-z0-9._~/-]) o+=$c ;; *) printf -v c '%%%02X' "'$c"; o+=${c: -3} ;; esac; done; echo "$o"; }
	# stats over a file of seconds -> "median max"
	stats() { sort -n "$1" | awk '{a[NR]=$1} END{if(NR==0){print "- -";exit} printf "%.3f %.3f", a[int((NR+1)/2)], a[NR]}'; }

	timed_set() { # label, kind(wiki|search|full), items...
		local label=$1 kind=$2; shift 2
		: > "$OUT/t_$label"; local fails=0 n=0 url
		for it in "$@"; do
			n=$((n+1))
			case $kind in
			wiki) url="$BASE/wiki/$(enc "${it// /_}")" ;;
			search) url="$BASE/search?list=1&q=$(enc "$it")" ;;
			full) url="$BASE/search?q=$(enc "$it")" ;;
			esac
			read -r code t size <<< "$(get "$url")"
			echo "$t" >> "$OUT/t_$label"
			if [ "$code" = 404 ]; then
				echo "$label $it" >> "$OUT/missing.log" # not in this wiki: fine
			elif [ "$code" != 200 ] || [ "${size:-0}" -lt 500 ]; then
				fails=$((fails+1)); echo "$label FAIL $code $size $it" >> "$OUT/failures.log"
			fi
		done
		read -r med max <<< "$(stats "$OUT/t_$label")"
		say "[$label] $n requests, $fails failed, median ${med}s, max ${max}s"
	}
	: > "$OUT/failures.log"; : > "$OUT/missing.log"

	timed_set titles search "Paris" "albert einstein" "The Beatles" "quantum mech" "Mount Everest" \
		"New York City" "zebra" "Dostoevsky" "Москва" "café" "C++" "AC/DC" "World War II" \
		"photosynthesis" "Tokyo" "Python (programming language)" "Ω" "1984" "Ludwig van" "q"
	timed_set fulltext full "theory of relativity" "largest desert" "invented the telephone" \
		"capital of Mongolia" "boiling point of water" "first moon landing" "chlorophyll" \
		"migration of birds" "Silk Road trade" "volcanic eruption"
	timed_set long wiki "United States" "World War II" "List of common misconceptions" "Donald Trump" \
		"India" "COVID-19 pandemic" "Barack Obama" "Elizabeth II" "Russian invasion of Ukraine" "Albert Einstein"
	timed_set odd wiki "C++" "AC/DC" "Ω" "%" "Æon Flux" "São Paulo" "Tōkyō" "UK" "USA" "Main Page" \
		"Lord of the Rings" "Schrödinger's cat" "Sign (mathematics)" "Brazil national football team"

	# random crawl: follow /random, record failures, timings and slow pages
	: > "$OUT/t_crawl"; : > "$OUT/slow.log"; cf=0
	for i in $(seq "$CRAWL"); do
		r=$(curl -s -L -o "$OUT/last.html" -w '%{http_code} %{time_total} %{size_download} %{url_effective}' --max-time 60 "$BASE/random")
		read -r code t size url <<< "$r"
		echo "$t" >> "$OUT/t_crawl"
		if [ "$code" != 200 ] || [ "${size:-0}" -lt 500 ] || grep -q 'goroutine [0-9]* \[\|runtime error: ' "$OUT/last.html"; then
			cf=$((cf+1)); echo "crawl FAIL $code $size $url" >> "$OUT/failures.log"
		fi
		awk -v t="$t" 'BEGIN{exit !(t>2)}' && echo "slow $t $url" >> "$OUT/slow.log"
		[ $((i % 50)) -eq 0 ] || [ "$i" = "$CRAWL" ] && printf '\r  crawl: %d / %d articles, %d failed ' "$i" "$CRAWL" "$cf"
	done
	echo
	read -r med max <<< "$(stats "$OUT/t_crawl")"
	say "[crawl] $CRAWL random articles, $cf failed, median ${med}s, max ${max}s, $(wc -l < "$OUT/slow.log" 2>/dev/null || echo 0) over 2 s"

	nf=$(wc -l < "$OUT/failures.log")
	say "[failures] $nf total$([ "$nf" -gt 0 ] && echo ", first ones:")"
	head -10 "$OUT/failures.log" | sed 's/^/    /' | tee -a "$REPORT"
	[ -s "$OUT/missing.log" ] && say "[missing] not in this wiki (not a failure): $(cut -d' ' -f2- "$OUT/missing.log" | paste -sd, -)"
	errs=$(grep -ci 'panic\|error' "$OUT/server.log")
	say "[server log] $errs error/panic lines$([ "$errs" -gt 0 ] && echo ", first ones:")"
	grep -i 'panic\|error' "$OUT/server.log" | head -5 | cut -c1-200 | sed 's/^/    /' | tee -a "$REPORT"
	if [ -r "/proc/$SRV/winpid" ]; then # Git Bash: ask Windows for the peak working set
		peak=$(powershell.exe -NoProfile -Command "[math]::Round((Get-Process -Id $(cat /proc/$SRV/winpid)).PeakWorkingSet64/1MB)" 2>/dev/null | tr -d '\r')
	else
		peak=$(awk '/VmHWM/{print int($2/1024)}' "/proc/$SRV/status" 2>/dev/null)
	fi
	say "[memory] server peak RSS ${peak:-n/a}${peak:+ MB}"
	kill $SRV 2>/dev/null
	;;
*) echo "unknown step $step"; exit 2 ;;
esac; done
say "done. Full logs in $OUT"
