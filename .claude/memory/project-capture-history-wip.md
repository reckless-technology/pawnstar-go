---
name: project-capture-history-wip
description: "Ordering experiments 2026-07: capture-history v1+v2 REJECTED; BUTTERFLY HISTORY ADOPTED (+~7 Elo both TCs, probe -17.7%, golden now 6,888,983); probe protocol standing"
metadata:
  type: project
---

**CONCLUDED 2026-07-03: REJECTED and reverted.** Pooled result over two 10+0.1 runs (1000 + 1700 games):
**2700 games, W1184 L1236 D280, 49.04% → −6.7 Elo, 95% CI [−19.1, +5.7]**; the extension run alone hit the
SPRT H0-accept (LLR −2.60 at 1658 games, bounds [0,10]). The first 1000 games read +6.3 ± 18.5 — a noise
head-fake; the extension caught it. Both working trees reverted; both engines verified back at the golden
7,007,401 signature. **Lesson for the next attempt:** the [0,99] within-SEE-class tiebreak is too timid to
gain and adds enough ordering churn to cost a few Elo — try the aggressive form instead (capture history as
a first-class ordering term across SEE classes, and/or gravity-style bonus+malus updates), and use tighter
SPRT bounds ([0,5]) or plan ≥3000 games for small effects. The implementation below is known bit-identical
across engines (both benched 7,295,483, 659/659 diagnostics identical) — the dual-implementation workflow
itself is proven and repeatable.

Original implementation notes (for reproducing the next variant):

**Capture history** (first strength feature since bit-parity was reached) is implemented identically in pawnstar-go (working tree, on top of `fbf0844`) and pawnstar C++ (`src/search_state.h`, on top of `fca64ca`): per-thread `captureHistory[move.pieceTo()*7 + captured]` int16, saturating ++ at 16384 on good captures (recorded beside continuation history in recordGood / the 5 C++ RecordContinuationHistory sites — NOT in qsearch, matching both engines), used in scoreAndSortMoves/ScoreAndSortMoves as a **[0,99]-clamped bonus added to the SEE sort** (SEE quantum is 100 cp so it refines order only *within* a SEE class). **Verified bit-identical across engines: both bench 7,295,483** (old signature 7,007,401), and depth-12 `PAWNSTAR_BENCH_DEBUG` output identical line-for-line (659 lines). C++ side is clang-format-18 clean.

**Why uncommitted:** the Go golden test (`benchGoldenNodes` = 7007401) fails by design until the SPRT justifies adoption. **On SPRT pass:** bump `benchGoldenNodes` to 7295483, update the signature mentions in CLAUDE.md/README (+ [[project-cross-engine-bit-parity]] memory), commit Go (push ok) and C++ (commit; push only when told), and re-verify cross-engine bench. **On SPRT fail:** revert both working trees, record the negative result here.

**PARITY MATCH RESULT (2026-07-02, 300 games @ 10+0.1, Threads=1 Hash=64 both, C++ = DEBUGX-off release):
Go +5.79 ± 23.72 Elo (50.83%, W81 L76 D143) — statistical parity, point estimate in Go's favor.**
The arm64 NEON work closed the playing-strength gap (history: −62 → −32 on amd64 → +6±24 on arm64).

**Still running:** the **capture-history SPRT** (`TC=10+0.1 CONCURRENCY=8 tools/sprt.sh ./pawnstar-go ./pawnstar-go-baseline 500`, bounds [0,10]), auto-launched by the chain. Results accumulate in `~/pawnstar_nnue/results_summary.txt` (+ `caphist_sprt.log`).

**Harness (set up this session):** fastchess built from source → `~/.local/bin/fastchess`; openings `~/pawnstar_nnue/openings.epd` → symlink to `UHO_Lichess_4852_v1.epd` (downloaded from official-stockfish/books; the Linux box's original file was unavailable — note if strict continuity with old SPRT numbers matters). Baseline binary `./pawnstar-go-baseline` = 7,007,401 build. **Gotcha found: a default C++ cmake build has DEBUGX=ON (~8% slower, bench 1.15 vs 1.24 Mnps release) — always compare against `-DPAWNSTAR_DEBUGX=OFF`.** True arm64 speed ratio vs release C++ is ~0.91×, not the ~0.98× recorded against the DEBUGX build in [[reference-pawnstar-perf-measurement]].

v0.1.166 was released earlier this session (release pipeline verified end-to-end).


---

**QUEUED NEXT (2026-07-03, user-approved): butterfly history experiment.** Replace the per-ply main
history `counts[maxPly][4096]` with a standard **butterfly table `counts[2][4096]` keyed by
side-to-move** (from-to). Rationale: per-ply fragments the cross-tree statistics history exists to
pool — sparse near the root where positions are coherent, saturated-but-unselective deep; ply parity
was only working as an accidental colour proxy (and null-move subtrees flip parity, contaminating it) —
the butterfly makes colour explicit. 32× smaller (1 MiB → 32 KB/thread). **Coupled change (required):**
the LMR tier-3 gate `history.count(ply,m)==0` loses meaning post-pooling; replace with a pure lateness
gate extending the doubling ladder: −1 at i>3 (depth>2), −2 at i>6 (depth>3), **−3 at i>12** (inside
the depth>3 branch). Go: historyTable→[2][4096]uint32, recordGoodMove/count take stm colour
(ss.currentPosition().colorToMove); C++ mirror: kTableSize=2*4096, RecordGoodMove/GetCount take colour.
Same workflow: implement both engines on the settled base (after the variant-2 verdict), verify
bit-identical bench signature + depth-12 PAWNSTAR_BENCH_DEBUG, SPRT [0,5] at 10+0.1 vs the new baseline.


---

**PRE-SPRT ORDERING PROBE (2026-07-03, user-suggested — now the standing gate for ordering changes):**
before any SPRT, measure the fixed-depth tree: (1) the `bench` signature delta is free (candidate vs
7,007,401-class baseline); (2) BK-24 @ depth 12 cand-vs-base via the scratchpad driver (~2 min, node
counts are load-independent). **A real ordering improvement should show node ratio ≤ ~1.00; ratio >
~1.02 is a red flag** — the change is redirecting search, not improving it, and any TC Elo is
move-quality luck paying a depth tax. Data: capture-history v1 bench +4.1% → SPRT −6.7; v2 BK-24
+4.1% nodes (huge per-position variance: pos03 −45%, pos10 +126%, 8/24 bestmoves changed) while its
SPRT read ~+9 ±11 (LLR ~0.9, no formal pass at 2500 games) — mechanism ≠ ordering efficiency, so
weak evidence. Verdict standard: no formal SPRT pass + probe red flag → reject.


---

**VARIANT 2 FINAL (2026-07-03): REJECTED per the pre-registered standard.** 3000 games @ 10+0.1,
bounds [0,5]: **+9.38 ± 10.60, LLR 1.06 — no formal pass** (needed +2.94). Borderline-positive
(~95% LOS) BUT the fixed-depth probe red-flagged the mechanism (+4.1% BK-24 nodes, +14.7% bench-13,
huge per-position variance, 8/24 bestmoves changed): not an ordering improvement, just redirected
search — variant-1's head-fake profile. Reverted in both repos; both re-verified at 7,007,401.
**If ever revisited:** only via a probe-clean variant (node ratio ≤ 1.00); the borderline TC result
means a well-designed capture-history probably has a few Elo in it, but this formulation isn't it.


---

**BUTTERFLY HISTORY: ADOPTED 2026-07-03 (user decision, stopping the 12+0.12 run early).** Evidence:
probe bench −1.7% / BK-24 @ d12 **−17.7% nodes, −21% time**; 10+0.1 3000 games **+7.8 ± 9.8**;
12+0.12 stopped at 2140 games **+6.8 ± 11.7** (consistent ~+7 across TCs; pooled ~5140 games CI > 0).
Adoption: golden signature **7,007,401 → 6,888,983** (benchGoldenNodes bumped); BK accepted sets
regenerated in Go (GEN_BK — remember `-v` + tee, t.Logf is silent otherwise) — 4 positions' choices
changed; C++ BK sets were already broad enough (24/24 untouched). Cross-language verified at depth 13:
both engines 6,888,983 with 667/667 identical per-position bestmove/node/diagnostic lines. The Go
pieceTo() also adopted C++'s single-shift form (identity-preserving, test-pinned). Committed in both
repos. The first strength feature landed since bit-parity was established.
