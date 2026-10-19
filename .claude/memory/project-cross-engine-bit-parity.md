---
name: project-cross-engine-bit-parity
description: "Go, C++ AND Rust pawnstar are kept bit-for-bit identical at fixed depth; bench=6,601,454 is the signature (6,888,983 before the depth-weighted history update 2026-07-05; 7,007,401 before butterfly history)"
metadata: 
  node_type: memory
  type: project
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

pawnstar-go (Go, `/home/jonny/work/pawnstar-go`) is a faithful port of pawnstar (C++, `/home/jonny/work/pawnstar`, GitHub `jonny-reckless/pawnstar`). There is ALSO a **Rust port, pawnstar-rs** (`/home/jonny/work/pawnstar-rs`), kept in the same lockstep. All THREE are maintained **bit-for-bit identical at fixed depth**: the 16-position `bench` command reports the same node total — **6,601,454** since the depth-weighted history bonus/malus + gravity update (2026-07-05; 6,888,983 with the earlier butterfly history table; 7,007,401 before that) — and the same per-position bestmove + node count in all three engines. Enforced by `benchGoldenNodes` in the Go UCI test and `GOLDEN_TOTAL`/`GOLDEN` in the Rust `tests/bench_golden_test.rs` (C++ has no golden constant — its UCI test only checks bench determinism). This is the standing faithfulness gate for any search / move-gen / eval / TT change.

**Why:** Any divergence signals a real bug in one engine. Example (fixed 2026-07): C++ had no `kPawnSinglePush` move type, so a single pawn push was encoded as `kNonCapture` and *incremented* the reversible-move (50-move) clock instead of resetting it; since the Zobrist hash excludes the clock, `ScaleByRule50` then scaled a deep static eval differently, flipping one quiescence stand-pat cutoff at int8 resolution (C++ commit 452145e; Go was already correct).

**Verified through depth 12 on arm64 (2026-07-02):** `bench 12` totals 4,231,499 in both engines with `PAWNSTAR_BENCH_DEBUG=1` output **identical line-for-line** (per-position bestmove + nodes + every non-zero DEBUGX diagnostic counter, 664 normalized lines); and all 24 BK positions via UCI at `go depth 12` (Clear Hash per position, Threads 1, Hash 64) matched on depth-reached/bestmove/score/nodes — 6,133,129 total nodes both, including the pos-1 mate-in-3 early stop at depth 4. Relative speed on Apple Silicon at those identical trees: Go ~0.95× C++ (round range 0.89–0.98).

**How to apply:** After any change touching search, move generation, eval, or the TT, run `bench` in **all three** engines and confirm the current golden signature — 6,601,454 (and equal per-position counts via `PAWNSTAR_BENCH_DEBUG=1`, present in Go and C++). Changes often need mirroring across all three repos; the mechanical acceptance test for a mirror is "bench matches the reference per-position table exactly" (a subtly-wrong port cannot reproduce 16 depth-13 node counts). **Bench parity does NOT cover lint: after any C++/Rust edit run the formatter or CI fails on it — C++ `clang-format-18 --dry-run -Werror src/*.h src/*.cpp test/*.cpp tools/*.cpp` (CI pins v18; if not installed, grab the binary from the PyPI `clang-format==18.1.8` wheel — it's a zip), Rust `cargo fmt --check`, Go `gofmt -l` + `go vet` (2026-07-05 the mirror agents shipped bit-correct but unformatted code and the C++ clang-format + Rust cargo-fmt CI jobs failed). NOTE pawnstar-go and pawnstar-rs are PRIVATE repos (unauthenticated GitHub Actions API returns 404 — can't observe their CI without a token); pawnstar (C++) is public.** Go: direct push to main is fine ([[feedback-direct-push-main-pawnstar-go]]); C++ and Rust: confirm per task, and commit ≠ push (the user says "push" separately). BK accepted-move sets are per-engine and blessed independently (Go depths 7–8, C++/Rust depths 8–11) — a strength change shifts some and each engine's set is regenerated separately. See [[project-capture-history-wip]] (the +17 Elo modern-history change 2026-07-05), [[feedback-root-cause-not-benign]] and [[reference-pawnstar-perf-measurement]].


---

**UCI ROBUSTNESS + PONDER FIXES LANDED IN ALL THREE (2026-07-13/14).** Not search changes — bench stayed
6,601,454 with byte-identical per-position counts AND DEBUGX counters across Go/C++/Rust. What was wrong:
(1) **Malformed FEN crashed C++ (SIGSEGV, + ASan-confirmed OOB *writes* = silent memory corruption) and
Rust (panic)**: a KINGLESS position is unrepresentable — kingSquare() on an empty king bitboard returns 64,
one past every 64-entry attack table. Fix in all three: parse into a fallible helper, and if !hasBothKings()
fall back to the start position; validate each placement (unknown letter / x>7 / y<0 aborts); guard the
checkers computation; make square-from-string total. No-op for valid FENs — that is what preserved parity.
(2) **All three emitted `bestmove` WHILE PONDERING** (UCI violation + desynced their own position stack).

**THE DESIGN LESSON (worth remembering): don't overload one flag.** `isCancelPending` meant BOTH "GUI said
stop" AND "main thread done, helpers wind up" — and the root search sets it at the end of EVERY search, so
the ponder-wait keyed on it was dead code. The tempting fix — save/restore the flag around the helper join —
is RACY: a `stop` landing during the join is clobbered by the restore, nothing then clears isPondering, so
the ponder-wait spins while stopThinking blocks in join → **PERMANENT HANG. Measured 6 hangs / 72 runs.** I
shipped that racy version to Go before catching it. Correct fix, now in all three: a dedicated
`isStopRequested` written ONLY by the UCI thread (stopThinking sets, startThinking clears), never by a search
thread; the ponder-wait keys off it; searchRootNode keeps its plain unconditional store(true). 0 hangs / 72.

**CI (gh IS NOW INSTALLED — use it; go/rs are private so the unauthenticated API 404s, but gh works):**
after ANY signature change, check CI in ALL THREE repos. pawnstar-rs CI was **RED for three commits** and
nobody noticed: its workflow hardcoded a SECOND copy of the bench signature (`test "$nodes" = "6888983"`)
that drifted when GOLDEN_TOTAL was re-blessed to 6,601,454. Now derived from GOLDEN_TOTAL — one source of
truth. Lesson: grep for the OLD signature across .github/ too, not just source/tests.

---

**LOST-STOP RACE + MALFORMED `go` VALUES, FIXED IN ALL THREE (2026-10-18).** Not search changes — bench
stayed 6,601,454 with the identical per-position table in all three. (1) The root search
(SearchRootNode / search_root_node / searchRootNode) cleared `isCancelPending` as it began, ON THE WORKER.
`go infinite` + `stop` (or `quit`) written back to back reached stopThinking before the worker got there,
so the stop was lost: the search ran on and stopThinking blocked in join/wg.Wait for ever. Measured through
the binaries: **20/20 stops and 10/10 quits hung in EACH engine, Go included** (Go's coverage tests had
never sent stop that fast; the C++/Rust coverage tests slept 100-300 ms after `go` to dodge it). Fix, the
same in all three: the root search never clears the flag; only the code that STARTS a search clears it,
before it starts it (startThinking before spawning; SetPosition/newGameFromFen/runBench for the direct
callers — every direct caller already set the position first). Race-free because nothing on a search
thread ever writes false: a stop landing before the worker starts, during, or after is seen or finds the
search over; helpers inherit the flag. After: 0 hangs in 5,000 stops + 200 quits per engine. The rule
generalises the ponder lesson above: a flag the UCI thread sets must never be cleared by a search thread.
(2) C++ `go depth abc` reached std::stoi and terminated the engine; Rust read it as 0 (atoi); Go set the
keyword's mode with a stale value (`go nodes abc` = an UNBOUNDED search). Now one rule in all three: a
numeric UCI value must be a whole integer (C++ ParseInteger on from_chars, Rust parse_integer =
str::parse::<i64>, Go strconv.Atoi); a malformed or missing value is ignored WITH its keyword (the line
reads as a plain `go`), a numeric setoption is left as it was, bench keeps its default depth.
(3) Go's -race run of the new test caught `go` rewriting timeControl while the PREVIOUS search still read
it (`go infinite` + `go depth 1`): handleUciGo (and the C++ handle_go, handle_position, handle_bench) now
stop the running search FIRST, as Rust's game_mut always did. Go/C++ setoption still don't stop first
(Rust's does) - GUIs never send it mid-search; left as is.
Regression tests in each: an in-process watchdogged loop (go+stop, go ponder+stop, go+go, go+ucinewgame,
go+quit; Threads 3) and binary loops (uci_test.sh in C++/Rust, uci_robustness_test.rs, uci_test.go).

---

**CASTLING ADVANCES THE FIFTY-MOVE CLOCK (Go, 2026-10-19, PR #13).** makeMove reset `halfMove` on
castling - the reasoning was "castling is irreversible". Irreversibility matters to REPETITION alone, and
the hash already carries it there (castling removes rights). FIDE Article 9.3 and FEN's halfmove clock
reset only on a pawn move or a capture (en passant included), so castling now increments. Bench stayed
6,601,454 with every per-position line and DEBUGX counter identical: the bench positions start at clock 0
and no depth-13 line castles and then goes 20 plies with no pawn move or capture (scaleByRule50's first
read). So a fix that leaves bench unchanged keeps parity, and the C++ and Rust engines need the same
one-line change: **pawnstar-rs src/position.rs `MoveType::Castling` still sets reversible_move_count = 0**
(seen 2026-10-19 at 16bc0ed), and the C++ engine was found doing the same. Tests:
TestCastlingAdvancesFiftyMoveClock (fen_test.go), TestDrawByFiftyMovesAcrossCastling (draw_test.go).
Still open in all three: the uint8 clock wraps past 255 (a FEN clock over 255, or a GUI that never
adjudicates).

**CHECKMATE TAKES PRECEDENCE OVER THE FIFTY-MOVE DRAW (all three, 2026-10-19: pawnstar #20, Go #14, Rust
#14).** The node returned the fifty-move draw at clock >= 100 before asking whether the side to move was
mated; FIDE 5.1.1 (9.6.2 for the 75-move rule) lets a mate on the hundredth ply stand. Each engine now,
at node entry after the material draw, tests the clock on its own: in check with no legal move ->
checkmatedScore + ply (counting `checkmates`), else the draw; repetition after. Same point, same order in
all three, so the trees stay one. Bench 6,601,454 and the stripped BENCH_DEBUG transcripts identical
across the three (no bench line reaches clock 100). The eval's fifty-move test needed nothing: in the
search it only sees positions the node already ruled on, or capture children at clock 0. Test FEN, shared
with zenith and stoopid-chess: `6k1/5ppp/8/8/8/8/8/R5K1 w - - 99 80` (a1a8 mate 1; g1f1 at cp 0 before).
zenith had the same bug (fixed, zenith #55); stoopid-chess never did (tests added, #12). The same PRs made
the Windows binaries import system DLLs alone (C++ /MT, Rust +crt-static; Go was already kernel32 only),
held by tools/windows-dependents.ps1 in CI and the release.
