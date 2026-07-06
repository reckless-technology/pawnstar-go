---
name: project-capture-history-wip
description: "Ordering experiments 2026-07: capture-history v1+v2 REJECTED; BUTTERFLY HISTORY ADOPTED (+~7 Elo both TCs, probe -17.7%, golden now 6,888,983); probe protocol standing"
metadata: 
  node_type: memory
  type: project
  originSessionId: 4f58b693-7492-4c16-97a8-ee8793ba8b86
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


---

**MODERN HISTORY UPDATE: SPRT PASSED +17 Elo 2026-07-05 — the node probe was a FALSE NEGATIVE.**
**Result: TC 10+0.1, SPRT elo[0,5], 3295 games, 1200-1037-1058 (52.5%), Elo +17.2 ± 9.8, LOS 100%,
LLR 2.96 → H1 ACCEPTED.** Candidate = modern history with malus, NO history-guided LMR
(PAWNSTAR_MODERN_HISTORY=1, all other knobs default). Baseline = same binary flag-off (legacy ++).
**KEY LESSON: the fixed-depth node probe REJECTED this (every ablation +4.8% to +13.7% nodes over
legacy — see below) yet it WON at time control by ~+17 Elo.** The probe protocol assumes node-inflation
⟹ weaker (true for the capture-history head-fakes), but it has a FALSE-NEGATIVE mode: a change that
trades more-nodes-per-depth for better MOVE QUALITY looks bad on the node probe and still gains at TC
because quality > depth here. Node probe is necessary-not-sufficient; TC SPRT is the arbiter. Do not
kill an ordering change on the node probe alone again — if the mechanism plausibly improves move choice
(not just redirects search), SPRT it. Claude initially recommended rejecting on the probe; the user's
"SPRT anyway" was correct.

Harness note (this env): fastchess ABSENT here; used **cutechess-cli** (/usr/local/bin) instead.
Re-downloaded UHO_Lichess_4852_v1 (official-stockfish/books) → ~/pawnstar_nnue/openings.epd (2.6M pos).
Candidate binary = wrapper `pawnstar-go-cand` that execs pawnstar-go with PAWNSTAR_MODERN_HISTORY=1
(same codegen as baseline, one env flag). Log: ~/pawnstar_nnue/sprt_modern.log. cutechess -sprt syntax
mirrors sprt.sh's fastchess args.

The original BK-24 fixed-depth node ablation (all WORSE than legacy ++, which is why the probe was a
false negative):

    legacy ++ baseline .................. 6,060,317   (1.000)
    modern bonus+gravity, NO malus ...... 6,414,135   (+5.8%)   <- gravity alone already hurts
    modern +malus +malus-only LMR div256  6,348,987   (+4.8%)   <- best modern, still red
    modern +malus ....................... 6,723,270   (+10.9%)
    modern +malus +symmetric history-LMR  6,888,408   (+13.7%)  <- "deepen good" inflates

Same at 64 MB TT (+9.0%), so not TT-size. Diagnostics (historyMalusApplied / historyLmrAdjustDeeper /
historyLmrAdjustShallower) cracked why: (1) gravity's ±16384 cap COMPRESSES the discrimination legacy's
unbounded counts give among frequently-good moves → worse ordering even with no malus; (2) malus adds
tail-churn; (3) history-guided LMR can't rescue it — "reduce good less" inflates nodes, and "reduce bad
more" has almost nothing to act on because legacy history only ever REWARDS, so the LMR'd tail quiets
sit near-zero, not strongly-negative (lmrShallower fired only 4406× at div=256, 0× at div=16384). All
ratios ≥ 1.048 ≫ the 1.02 red-flag line — which is exactly why the probe was a FALSE NEGATIVE here (the
extra nodes buy better moves, invisible to a fixed-depth node count). ADOPT the +malus, NO-history-LMR
config (the SPRT'd one). The no-malus and malus-only-LMR variants are node-cheaper and UNTESTED — worth
a follow-up SPRT to see if they gain even more, but not required for adoption.

**What was built (uncommitted, flag-gated OFF by default so bench stays golden 6,888,983):**
`engine/historymodern.go` (bonus `min(depth²,400)`, gravity, malus, ablation env knobs
PAWNSTAR_MODERN_HISTORY / _HIST_NO_MALUS / _HIST_LMR / _HIST_LMR_MALUS_ONLY / _HIST_LMR_DIV),
`historyTable.counts` widened uint32→int32, `recordGood(ply,depth,prevMove,m,isCutoff,quietsTried)`,
history-guided LMR block in searchalphabeta.go, 3 prototype dbgx counters, temp driver
`engine/bkprobe_test.go` (BK-24 fixed-depth node+time probe, PROBE_HASH_MB knob).

**ADOPTION (TODO — mirrors the butterfly-history workflow):** (1) make modern history the real path —
remove the `modernHistory` env gate + the ablation knobs (_HIST_NO_MALUS/_HIST_LMR/_HIST_LMR_MALUS_ONLY/
_HIST_LMR_DIV) and the history-guided-LMR block (it was OFF in the SPRT'd candidate); keep `recordGood`
depth-weighted bonus + gravity + malus. (2) Golden signature **6,888,983 → 6,601,454** (bump
benchGoldenNodes + all CLAUDE.md/README mentions + [[project-cross-engine-bit-parity]]). (3) Regenerate
BK accepted sets (GEN_BK, -v + tee). (4) Decide on the 3 prototype dbgx counters (keep or drop; if kept,
mirror in C++). (5) MIRROR in C++ Pawnstar (same recordGood change) to preserve bit-parity; re-verify
both engines at the new signature with PAWNSTAR_BENCH_DEBUG line-for-line. (6) `make check`, commit both
(Go push ok; C++ commit, push when told). Remove bkprobe_test.go + pawnstar-go-cand wrapper, or keep the
probe driver. Optionally SPRT the node-cheaper no-malus variant first — it MIGHT gain more. Full names
per [[feedback-full-names-not-abbreviations]].
