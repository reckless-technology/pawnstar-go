---
name: project-cross-engine-bit-parity
description: "Go and C++ pawnstar are kept bit-for-bit identical at fixed depth; bench=7,007,401 is the signature"
metadata: 
  node_type: memory
  type: project
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

pawnstar-go (Go, `/home/jonny/work/pawnstar-go`) is a faithful port of pawnstar (C++, `/home/jonny/work/pawnstar`, GitHub `jonny-reckless/pawnstar`). They are maintained **bit-for-bit identical at fixed depth**: the 16-position `bench` command reports the same node total — **7,007,401** — and the same per-position bestmove + node count in both engines. This is the standing faithfulness gate for any search / move-gen / eval / TT change.

**Why:** Any divergence signals a real bug in one engine. Example (fixed 2026-07): C++ had no `kPawnSinglePush` move type, so a single pawn push was encoded as `kNonCapture` and *incremented* the reversible-move (50-move) clock instead of resetting it; since the Zobrist hash excludes the clock, `ScaleByRule50` then scaled a deep static eval differently, flipping one quiescence stand-pat cutoff at int8 resolution (C++ commit 452145e; Go was already correct).

**Verified through depth 12 on arm64 (2026-07-02):** `bench 12` totals 4,231,499 in both engines with `PAWNSTAR_BENCH_DEBUG=1` output **identical line-for-line** (per-position bestmove + nodes + every non-zero DEBUGX diagnostic counter, 664 normalized lines); and all 24 BK positions via UCI at `go depth 12` (Clear Hash per position, Threads 1, Hash 64) matched on depth-reached/bestmove/score/nodes — 6,133,129 total nodes both, including the pos-1 mate-in-3 early stop at depth 4. Relative speed on Apple Silicon at those identical trees: Go ~0.95× C++ (round range 0.89–0.98).

**How to apply:** After any change touching search, move generation, eval, or the TT, run `bench` in **both** engines and confirm 7,007,401 (and equal per-position counts via `PAWNSTAR_BENCH_DEBUG=1`). Changes often need mirroring across both repos. Go: direct push to main is fine ([[feedback-direct-push-main-pawnstar-go]]); C++: confirm per task, and commit ≠ push (the user says "push" separately). See [[feedback-root-cause-not-benign]] and [[reference-pawnstar-perf-measurement]].
