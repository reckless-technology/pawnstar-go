---
name: project-capture-history-wip
description: "IN FLIGHT: capture history implemented in BOTH engines, uncommitted, awaiting SPRT; matches chained on the Mac"
metadata:
  type: project
---

**WIP as of 2026-07-02 late evening — code UNCOMMITTED in both repos, awaiting SPRT.**

**Capture history** (first strength feature since bit-parity was reached) is implemented identically in pawnstar-go (working tree, on top of `fbf0844`) and pawnstar C++ (`src/search_state.h`, on top of `fca64ca`): per-thread `captureHistory[move.pieceTo()*7 + captured]` int16, saturating ++ at 16384 on good captures (recorded beside continuation history in recordGood / the 5 C++ RecordContinuationHistory sites — NOT in qsearch, matching both engines), used in scoreAndSortMoves/ScoreAndSortMoves as a **[0,99]-clamped bonus added to the SEE sort** (SEE quantum is 100 cp so it refines order only *within* a SEE class). **Verified bit-identical across engines: both bench 7,295,483** (old signature 7,007,401), and depth-12 `PAWNSTAR_BENCH_DEBUG` output identical line-for-line (659 lines). C++ side is clang-format-18 clean.

**Why uncommitted:** the Go golden test (`benchGoldenNodes` = 7007401) fails by design until the SPRT justifies adoption. **On SPRT pass:** bump `benchGoldenNodes` to 7295483, update the signature mentions in CLAUDE.md/README (+ [[project-cross-engine-bit-parity]] memory), commit Go (push ok) and C++ (commit; push only when told), and re-verify cross-engine bench. **On SPRT fail:** revert both working trees, record the negative result here.

**PARITY MATCH RESULT (2026-07-02, 300 games @ 10+0.1, Threads=1 Hash=64 both, C++ = DEBUGX-off release):
Go +5.79 ± 23.72 Elo (50.83%, W81 L76 D143) — statistical parity, point estimate in Go's favor.**
The arm64 NEON work closed the playing-strength gap (history: −62 → −32 on amd64 → +6±24 on arm64).

**Still running:** the **capture-history SPRT** (`TC=10+0.1 CONCURRENCY=8 tools/sprt.sh ./pawnstar-go ./pawnstar-go-baseline 500`, bounds [0,10]), auto-launched by the chain. Results accumulate in `~/pawnstar_nnue/results_summary.txt` (+ `caphist_sprt.log`).

**Harness (set up this session):** fastchess built from source → `~/.local/bin/fastchess`; openings `~/pawnstar_nnue/openings.epd` → symlink to `UHO_Lichess_4852_v1.epd` (downloaded from official-stockfish/books; the Linux box's original file was unavailable — note if strict continuity with old SPRT numbers matters). Baseline binary `./pawnstar-go-baseline` = 7,007,401 build. **Gotcha found: a default C++ cmake build has DEBUGX=ON (~8% slower, bench 1.15 vs 1.24 Mnps release) — always compare against `-DPAWNSTAR_DEBUGX=OFF`.** True arm64 speed ratio vs release C++ is ~0.91×, not the ~0.98× recorded against the DEBUGX build in [[reference-pawnstar-perf-measurement]].

v0.1.166 was released earlier this session (release pipeline verified end-to-end).
