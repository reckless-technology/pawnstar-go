---
name: project-go-nnue-port
description: Plan to port the C++ Pawnstar NNUE evaluator (and search wins) into the Go engine
metadata: 
  node_type: memory
  type: project
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

Goal (decided 2026-06-30): incorporate the C++ improvements made to `../pawnstar` since the Go port forked (2026-05-11) into `pawnstar-go`. User chose **NNUE first**, with a **self-play SPRT harness** to gate changes, using Go's `simd/archsimd` for kernels (see [[reference-go-simd-archsimd]]), and the Go evaluator must **load the pawnstar `*.bin` net directly**.

**C++ NNUE architecture (v12, the shipped default) — the port target:**
- Features: Chess768 (768 = 2 colors × 6 types × 64 squares) per perspective, × **8 king buckets** (selected by king file-pair × board-half; `kKingBucketMap`), bucket-major → `kFeatureRows = 6144`.
- FT/accumulator: **1024 int16** per perspective; two perspectives concatenated [stm | ntm].
- Activation: **SCReLU** (clamp to [0, QA=255] then square), NOT plain clipped ReLU.
- Output: 2×1024 int16 weights (scale QB=64) → 1 scalar; dequant tail reduces one QA, adds bias, ×`kScale=400`/(QA·QB), clamps to ±kMaxEvaluation.
- Two forward passes in `src/nnue.h`: `EvaluateExact` (int16, the reference/trainer-matching path) and `Evaluate` (int8 fast path, bit-identical because `kInt8Shift=9` avoids maddubs saturation).
- Net file: 32-byte `NetHeader` (magic "PSN1", format_version=1, then input_size/king_buckets/hidden_size/qa/qb/scale) + int16 arrays (feature_weights col-major `feature*hidden+i`, feature_bias, output_weights). `pawnstar-v12.bin` payload = 12,589,152 B. Native little-endian int16 ⇒ direct read on amd64.
- Accumulator maintained incrementally across make/undo, **lazy/deferred** (brought current only when an eval reads it, on eval-cache miss) — the C++ lazy update was +20.66 Elo. Refresh only on king-bucket crossing. 8 MB lockless eval cache (zobrist→score); C++ found 8 MB optimal (32 MB was −7 Elo).

**Correctness gates (mirror C++):** Go `EvaluateExact` == C++ 250-FEN reference at **0 cp**; incremental == full-refresh bit-identical; then SPRT vs the HCE baseline. BK accepted-move sets are eval-specific — regenerate after switching to NNUE.

**After NNUE, the portable search wins to port (SPRT-gated, one at a time, margins tuned for THIS engine — C++ found the whole stack together regressed −174 Elo):** reverse futility pruning (C++ +114.8), quiescence SEE pruning (+38.6), internal iterative reduction (+31.8), late-move pruning (+18.6), countermove + 1-ply continuation history (+9). Plus robustness: 50-move eval scaling toward draw, finer time-deadline polling (C++ 64k→2k nodes) + Move Overhead, illegal-PV fix. **Skip** (C++ tested & rejected): aspiration re-tune, make/unmake, correction history, singular extensions, output buckets, NNUE hidden layer, asm kernels, null-move R=3. Go already has: magic bitboards, YBW parallel, copy-make.

**Validation infra:** fastchess is installed (used by C++ `tools/run_sprt.sh`). Build an SPRT harness for `pawnstar-go(candidate)` vs `pawnstar-go(baseline)`; the current HCE build is baseline #0. SPRT runs are long (thousands of games) — run in background.

**PROGRESS:**
- **Phase 0 DONE.** go.mod→1.26; `Makefile` wraps `GOEXPERIMENT=simd` (`make build`/`test`/`baseline`). Env cleaned: the 1.26.4 GOROOT was relocated `~/.local/bin/go`→`~/.local/go` with `~/.local/bin/go` now a symlink to the binary (it used to be the GOROOT *dir*, shadowing the binary under make); apt `golang-1.22` purged; `go` now unambiguously 1.26.4. SPRT harness `tools/sprt.sh` (binary-vs-binary, `GOMAXPROCS=1` for single-core engines, fixed-depth default, openings `~/pawnstar_nnue/openings.epd`) — smoke-tested green. Oracle staged: `engine/nnue/pawnstar-v12.bin` + `engine/testdata/nnue_reference.txt` (250 FENs).
- **Phase 1 DONE.** `engine/nnue.go` = PSN1 loader + scalar `evaluateExact` (int16 SCReLU reference). `TestNNUEReference` gate: **max |diff| = 0 cp over all 250 positions** (better than the ±2 target) — loader, Chess768 feature indexing, king-bucket orientation, SCReLU and dequant all verified. Confirmed constant mapping Go≡C++: white=0/black=1, pawn=1..king=6, rankFlip=0x38.
- **Confirmed Go port has the C++ "Illegal PV move" bug** (stale PV reconstruction; moves played are legal). Phase 5 fix = C++ d376b8a.
- **Phase 2 band reconciliation DECIDED:** clamp NNUE search eval to ±(winThreshold−1000)=±8000 (`nnueSearchClamp`, `clampEvalToBand`); `evaluateExact` keeps the faithful ±30000 clamp for the reference test.
- **Phase 2a+2b DONE.** NNUE wired into `evaluatePosition` (branch when `game.nnue != nil`, drops the HCE material early-exit). Net embedded via `go:embed` (`engine/nnue_embed.go`); enabled in **main.go** only (env `PAWNSTAR_NNUE=0` disables) so the existing test suite stays on HCE — no BK regeneration needed yet. Incremental accumulator on `searchState.acc`: refreshed at construction, maintained in playMove/makeNullMove/undoMove (`update` is reversible; null moves no-op). `TestNNUEIncremental` gate: **bit-identical to full refresh at every node + after every undo** (castling/EP/promotion/bucket-cross all correct). `make test-short` fully green.
- **Perf (scalar, single-thread):** NNUE ~30× slower than HCE (depth 9: 51s vs 1.7s). Micro-bench: `evaluateExact` dot **1134 ns**, `refresh` **13283 ns**, `update` ~1–7μs (quiet vs bucket-cross). Eager updates run on every play/undo. **Phase 3 (archsimd on the output dot + addColumn/subColumn) is the critical speed fix**; lazy accumulator (skip updates for cut-off nodes, C++ +20 Elo) is the follow-on. Real SPRT waits until fast enough.
- Baseline HCE binary = `pawnstar-go-baseline` (gitignored); SPRT: `tools/sprt.sh`. New files: engine/nnue.go, nnue_embed.go, nnue_test.go, nnue_bench_test.go, nnue/pawnstar-v12.bin, testdata/nnue_reference.txt, Makefile, tools/sprt.sh.
