#!/usr/bin/env bash
# UCI protocol regression test. Drives the actual engine binary over stdin/stdout with synchronized I/O
# (read the engine's responses before sending the next command, so `quit` never cancels a running search)
# and asserts on the protocol behaviour. Usage: test/uci_test.sh [path-to-engine]   (default ./pawnstar-go)
#
# Ported from the C++ Pawnstar test/uci_test.sh. These features (seldepth / currmove / searchmoves, the
# uciok/readyok handshake, bench determinism) are about the engine's behaviour *as a UCI process*, so the
# only faithful test drives the built binary the way a GUI does — hence a script, not a `go test`.
set -u

ENGINE="${1:-./pawnstar-go}"
if [ ! -x "$ENGINE" ]; then
    echo "[FAIL] UCI: engine binary '$ENGINE' not found or not executable (run 'make build' first)"
    exit 1
fi

fails=0
pass() { echo "[PASS] $1"; }
fail() {
    echo "[FAIL] $1"
    fails=$((fails + 1))
}

# Drive the engine through a coprocess so responses can be read synchronously. Pin to one core / one
# thread so searches are deterministic and cheap.
coproc ENG { GOMAXPROCS=1 PAWNSTAR_THREADS=1 "$ENGINE" 2>/dev/null; }
send() { printf '%s\n' "$1" >&"${ENG[1]}"; }

# read_until <extended-regex> <timeout-seconds>: echo every line received until one matches the regex
# (then stop). Returns non-zero if the timeout elapses without a match.
read_until() {
    local re="$1" timeout="${2:-15}" line
    while IFS= read -r -t "$timeout" line <&"${ENG[0]}"; do
        printf '%s\n' "$line"
        [[ "$line" =~ $re ]] && return 0
    done
    return 1
}

# 1) uci handshake: id + option advertisements + uciok
send "uci"
out=$(read_until '^uciok' 10)
echo "$out" | grep -q '^id name Pawnstar' && pass "uci: id name" || fail "uci: missing 'id name Pawnstar'"
echo "$out" | grep -q '^uciok' && pass "uci: uciok" || fail "uci: missing uciok"
echo "$out" | grep -q 'option name Threads' && pass "uci: advertises Threads option" || fail "uci: no Threads option"

# 2) isready / readyok handshake
send "isready"
read_until '^readyok' 10 >/dev/null && pass "isready: readyok" || fail "isready: no readyok"

# Deterministic, real searches: single thread and no opening book (otherwise `go` returns a book move
# instantly, before any search, and emits no info lines).
send "setoption name Threads value 1"
send "setoption name OwnBook value false"

# 3) info seldepth: present on a real search and >= the nominal depth (quiescence extends beyond it).
send "ucinewgame"
send "position startpos"
send "go depth 12"
out=$(read_until '^bestmove' 30)
last_info=$(echo "$out" | grep '^info depth' | tail -1)
if [ -n "$last_info" ]; then
    d=$(echo "$last_info" | sed -E 's/.*info depth +([0-9]+).*/\1/')
    s=$(echo "$last_info" | sed -E 's/.*seldepth +([0-9]+).*/\1/')
    if [ -n "$s" ] && [ "$s" -ge "$d" ]; then
        pass "info seldepth present and >= depth (seldepth $s >= depth $d)"
    else
        fail "info seldepth bad/absent: '$last_info'"
    fi
else
    fail "go depth 12 produced no info lines (book not disabled? search not run?)"
fi

# 4) go searchmoves restricts the root. Force a move the engine would never choose (a2a3); if the filter
#    works the bestmove must be exactly that. (A normal search from startpos returns a real developing move.)
send "position startpos"
send "go searchmoves a2a3 depth 10"
out=$(read_until '^bestmove' 30)
bm=$(echo "$out" | grep '^bestmove')
echo "$bm" | grep -q '^bestmove a2a3' && pass "go searchmoves restricts root (forced a2a3)" || fail "searchmoves: expected 'bestmove a2a3', got '$bm'"

# 5) info currmove / currmovenumber: emitted once a search runs past the ~3s reporting gate. Use a
#    movetime well beyond the gate (6s) so there is a comfortable window of root iterations emitting it.
send "position startpos"
send "go movetime 6000"
out=$(read_until '^bestmove' 30)
echo "$out" | grep -q 'currmove [a-h][1-8][a-h][1-8].* currmovenumber [0-9]' &&
    pass "info currmove/currmovenumber emitted on a long search" || fail "currmove/currmovenumber not emitted"

# 6) pondering: `go ... ponder` searches on the opponent's time and must NOT emit a bestmove until
#    `ponderhit` (or `stop`). Then ponderhit converts it into a timed search that returns a bestmove.
#    Use a position not in the book and advertise the Ponder option.
send "uci"
out=$(read_until '^uciok' 10)
echo "$out" | grep -q 'option name Ponder' && pass "uci: advertises Ponder option" || fail "uci: no Ponder option"
send "position fen r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1"
send "go wtime 3000 btime 3000 ponder"
sleep 0.3                       # let the ponder search run; it must not resolve on its own this fast
send "isready"                  # handled concurrently with the running ponder search
out=$(read_until '^readyok' 10) # everything the engine emitted while pondering, up to readyok
if echo "$out" | grep -q '^bestmove'; then
    fail "ponder: bestmove emitted before ponderhit"
else
    pass "ponder: no bestmove while pondering"
fi
send "ponderhit"                # convert to a timed search (~120ms budget from wtime 3000)
out=$(read_until '^bestmove' 30)
echo "$out" | grep -qE '^bestmove [a-h][1-8][a-h][1-8]' && pass "ponder: bestmove after ponderhit" || fail "ponder: no bestmove after ponderhit"

send "quit"
wait "$ENG_PID" 2>/dev/null

# 7) bench determinism: single-threaded bench is deterministic, so two runs must agree (guards the
#    uninitialised-read class of bug that made node counts wobble). Run as a plain pipe — bench is
#    synchronous on the main thread and quits cleanly.
b1=$(printf 'bench\nquit\n' | GOMAXPROCS=1 "$ENGINE" 2>/dev/null | grep -oE '^[0-9]+ nodes' | grep -oE '^[0-9]+')
b2=$(printf 'bench\nquit\n' | GOMAXPROCS=1 "$ENGINE" 2>/dev/null | grep -oE '^[0-9]+ nodes' | grep -oE '^[0-9]+')
if [ -n "$b1" ] && [ "$b1" = "$b2" ]; then
    pass "bench is deterministic across runs ($b1 nodes)"
else
    fail "bench nondeterministic: run1='$b1' run2='$b2'"
fi

if [ "$fails" -eq 0 ]; then
    echo "[PASS] UCI: protocol handshakes, seldepth, searchmoves, currmove, bench determinism"
    exit 0
fi
echo "[FAIL] UCI: $fails check(s) failed"
exit 1
