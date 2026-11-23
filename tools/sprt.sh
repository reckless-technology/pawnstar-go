#!/usr/bin/env bash
# SPRT self-play between two pawnstar-go binaries via fastchess.
#
# Unlike the C++ engine (net-vs-net, one binary + EvalFile), the Go port compares two BINARIES:
# a candidate against a baseline. Snapshot the baseline with `make baseline` before a change,
# build the candidate with `make build`, then run this.
#
# Usage: tools/sprt.sh [candidate_bin] [baseline_bin] [rounds]
#   defaults: ./pawnstar-go  vs  ./pawnstar-go-baseline,  500 rounds (1000 games)
#
# Env:
#   DEPTH        fixed search depth (default 8). Isolates eval quality; immune to core
#                oversubscription. This is the default mode.
#   TC           time control e.g. "8+0.08". If set, overrides DEPTH and measures real
#                strength including a heavier eval's speed cost.
#   OPENINGS     an opening file to use instead of fetching the pinned set. It must still match the
#                SHA-256 in tools/openings.pin; the run fails otherwise. By default the set pinned
#                there (Pawnstar's nnue/openings.epd at one commit) is fetched over https and cached.
#   ELO0 ELO1    SPRT hypothesis bounds (default 0 / 10).
#   CONCURRENCY  parallel games (default nproc).
set -euo pipefail

CAND="${1:-./pawnstar-go}"
BASE="${2:-./pawnstar-go-baseline}"
ROUNDS="${3:-500}"
ELO0="${ELO0:-0}"
ELO1="${ELO1:-10}"
CONCURRENCY="${CONCURRENCY:-$(nproc)}"

for f in "$CAND" "$BASE"; do
    [ -x "$f" ] || { echo "error: engine binary not found/executable: $f" >&2; exit 1; }
done
command -v fastchess >/dev/null || { echo "error: fastchess not on PATH" >&2; exit 1; }
# Fetch (if need be) and verify the pinned opening set; a failure here stops the run (set -e).
OPENINGS="$("$(dirname "${BASH_SOURCE[0]}")/fetch-openings.sh")"

# fastchess's EPD reader rejects the '#' provenance header and blank lines, so it is handed a scratch
# copy without them. The hash was checked above, on the fetched file; the copy is never hashed and the
# pinned original is left as fetched.
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
grep -v '^[[:space:]]*#' "$OPENINGS" | grep -v '^[[:space:]]*$' > "$scratch/openings.epd" || true
[ -s "$scratch/openings.epd" ] || { echo "error: no positions left in $OPENINGS after stripping comments" >&2; exit 1; }

echo "openings: $OPENINGS (comments stripped into $scratch/openings.epd, $(wc -l < "$scratch/openings.epd") positions)" >&2

# Each engine is pinned to one core so CONCURRENCY concurrent games don't oversubscribe.
# GOMAXPROCS=1 is the Go equivalent of the C++ harness's PAWNSTAR_THREADS=1: the search still
# spawns worker goroutines but they all run on a single OS thread.
export GOMAXPROCS=1

if [ -n "${TC:-}" ]; then
    clock_args=(tc="$TC")
    echo "SPRT  cand=$CAND  base=$BASE  TC=$TC  rounds=$ROUNDS  elo[$ELO0,$ELO1]"
else
    clock_args=(depth="${DEPTH:-8}")
    echo "SPRT  cand=$CAND  base=$BASE  depth=${DEPTH:-8}  rounds=$ROUNDS  elo[$ELO0,$ELO1]"
fi

# -games 2 -repeat plays each opening twice with colors reversed. Draw/resign adjudication
# ends decided games early. -sprt stops as soon as elo0 vs elo1 is decided at alpha/beta=0.05.
# Not exec'd: the EXIT trap has to remove the scratch copy; the script exits with fastchess's status.
fastchess \
    -engine cmd="$CAND" name=cand \
    -engine cmd="$BASE" name=base \
    -each proto=uci "${clock_args[@]}" \
    -rounds "$ROUNDS" -games 2 -repeat \
    -openings file="$scratch/openings.epd" format=epd order=random \
    -draw movenumber=40 movecount=8 score=20 \
    -resign movecount=3 score=600 \
    -sprt elo0="$ELO0" elo1="$ELO1" alpha=0.05 beta=0.05 \
    -concurrency "$CONCURRENCY"
