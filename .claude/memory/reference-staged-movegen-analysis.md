---
name: reference-staged-movegen-analysis
description: "Staged move generation analysed and REJECTED for pawnstar (legal-movegen cost structure); the one sound design if ever revisited + how to bound the prize first"
metadata:
  type: reference
---

Analysis (2026-07-03, with the user) of staged move generation (MovePicker-style: captures first, quiets only if no cutoff) for pawnstar-go/pawnstar. **Verdict: don't do it.** The allocation-free `moveList` (+38% perft, core-count independence) already banked the cheap movegen win; this is the follow-on idea that does NOT survive scrutiny.

**Why naive staging loses with a LEGAL move generator:** the generator's cost is front-loaded — king forbidden-square set (full enemy attack sweep with X-ray through the king), `computePins`, check-restriction masks — and the per-piece loops after that are cheap masked iterations. Staging with the existing entry points (`generateLegalCapturesInto` then quiets later) pays that analysis **twice** at every node reaching stage 2, eating the savings.

**Why switching to pseudo-legal + lazy legality doesn't rescue it** (the user's point): generation gets cheaper per move but you pay legality per *tried* move, and the cheap static legality test needs exactly the pin/check machinery you were avoiding (or make/unmake + in-check probe, worse). The cost moves from generation time to try time. In this project it's also a paradigm swap needing identical execution in BOTH engines + full perft re-validation — worst effort/payoff on the board.

**The only sound design if ever revisited:** a two-phase split **sharing one analysis pass** — factor forbidden-squares/pins/check-masks into a per-node struct computed once, then `capturesInto(&ml, &analysis)` and, only if no cutoff, `quietsInto(&ml, &analysis)`. Keeps the legal generator, no per-move legality tax, setup paid once. The masked piece loops are already cleanly separable. Still a dual-engine refactor of the most perft-sensitive code.

**Why the prize is small anyway:** the TT-move-tried-before-generation path already skips generation at the biggest class of cut-nodes; what remains is nodes where the TT move missed but an early capture/killer cuts. Move-gen ≈ 10–15% of search time (eval dominates), skippable fraction ≤ ~half → **~5% nps ceiling ≈ +3–4 Elo** (rule of thumb: 2× speed ≈ +70 Elo), vs 10–50 Elo for a successful search feature.

**Step zero if revisited:** bound the prize empirically before designing — cpuprofile of `bench` for `generateMovesInto`'s true share, plus the dbgx cutoff counters (tableMoveBetaCutoffs vs betaCutoffs, cutoff move-index distribution). Never profile while an SPRT owns the machine.
