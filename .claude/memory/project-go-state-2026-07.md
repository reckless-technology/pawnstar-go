---
name: project-go-state-2026-07
description: Snapshot (2026-07-02) of what landed in pawnstar-go/pawnstar; arm64 NEON now DONE (Go ~parity with C++)
metadata: 
  node_type: memory
  type: project
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

**UPDATE 2026-07-02 (arm64 NEON session, on the Apple Silicon Mac):** the arm64 NEON work is **DONE** — `80067ac` NEON addColumn/subColumn (refresh 7.6×, update 7.2×), `bad4791` NEON int8 output dot, `e35035d` 4-accumulator ILP unroll (evaluateInt8 1242→125 ns, 9.9×). **Result: arm64 Go bench ~190k→~1.14M nps (6.0×), now ~0.98× C++ (parity)** — details in [[project-go-nnue-arm64-neon]] and [[reference-pawnstar-perf-measurement]].

**UPDATE 2026-07-02 (later, same Mac) — HEAD now `c50e962`.** The same session then landed, all bit-identical (bench 7,007,401 + all 664 PAWNSTAR_BENCH_DEBUG diagnostic lines identical at every step, verified against C++ at depth 12): NNUE extracted to its own package `engine/nnue` with a raw-values `nnue.Board` boundary (`a894327`, see [[reference-go-nnue-kernel-layout]]); a whole-project simplify pass (`13c2590`: game.go split into clock.go/uci.go, evalcache.go + moveorder.go carve-outs, 7 dead symbols removed, **golden bench test** `benchGoldenNodes` added — the signature is now test-enforced — plus `make race`/book/lazysmp in `make check`); search split by layer (`63ea1da`..`171aeb4`: search.go/searchroot.go/searchalphabeta.go/searchquiescent.go); then the four reported-improvement phases: unit tests for TT/move-packing/FEN/draws/rule50/material/formatScore (`5939e89`), hot-path trio incl. history→uint32 (`287698b`), **book memoized** (NewGame book cost 10ms→12.5ns; `clear()` drops the shared-map reference, never mutates it) + main-thread searchState reuse via `resetForSearch` (`b4cd179`), and gopls-driven renames: `pos` receivers, unshadowed c/pt/pc/m locals, nnue-internal prefix drop (`c50e962`). CI green throughout; C++ repo also fixed (clang-format gate, `fca64ca`) and its CI is green.

**pawnstar-go (Go) `main` HEAD = `9f04267`** (build number 144). This session's arc, in order:
- C++ **fifty-move-clock fix** (in the C++ repo, `pawnstar` commit `452145e`, pushed) — closed the last cross-engine bench divergence; both engines now bench **7,007,401** (see [[project-cross-engine-bit-parity]]).
- **Allocation-free move generation** (`moveList`, `bb38f5a`): stack `[256]move`, 0 allocs, fixed the GOMAXPROCS-collapse in `make check` perft.
- **hashStackEntry packed** into one uint64 (`5f3a4d8`).
- **NNUE int8 dot**: VPMADDWD square (`3ae33f8`), then the **hand-written AVX2 kernel** (`9ade95e`) → C++ parity, +19% search nps, **SPRT ~−62→−32 Elo vs C++** (see [[reference-pawnstar-perf-measurement]]).
- **Version scheme** (`c25c7a1`): `0.1.<git-commit-count>` in the UCI id, binary `pawnstar-go_0_1_<build>` + symlink (mirrors C++).
- **GitHub Actions** (`a257e4b`, `9f04267`): `.github/workflows/ci.yml` (make-check gate, gofmt/vet, per-OS artifacts — all green on Linux/Windows amd64 + macOS arm64) and `release.yml` (manual promote-CI-run-to-release with binaries + docs bundle + source archive), mirroring the C++ workflows.

**NEXT STEP the user is switching for:** hand-write **arm64/NEON** NNUE kernels — macOS currently runs the scalar path and is the remaining slow spot. Full plan, C++-comparison, integration, and gates in [[project-go-nnue-arm64-neon]]; kernel layout in [[reference-go-nnue-kernel-layout]]. On the Mac you can finally **run/benchmark arm64 natively** (I was on amd64 Linux and could only cross-compile-check).

Working style reminders: direct push to `main` is fine for pawnstar-go ([[feedback-direct-push-main-pawnstar-go]]); C++ repo confirm per task; don't hand-wave divergences ([[feedback-root-cause-not-benign]]).
