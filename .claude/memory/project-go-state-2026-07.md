---
name: project-go-state-2026-07
description: Snapshot (2026-07-02) of what landed in pawnstar-go/pawnstar and where the next step (arm64 NEON) picks up
metadata: 
  node_type: memory
  type: project
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

Handoff snapshot as of 2026-07-02 (user switching machines to an Apple Silicon Mac for the arm64 work). Everything below is **committed and pushed**.

**pawnstar-go (Go) `main` HEAD = `9f04267`** (build number 144). This session's arc, in order:
- C++ **fifty-move-clock fix** (in the C++ repo, `pawnstar` commit `452145e`, pushed) — closed the last cross-engine bench divergence; both engines now bench **7,007,401** (see [[project-cross-engine-bit-parity]]).
- **Allocation-free move generation** (`moveList`, `bb38f5a`): stack `[256]move`, 0 allocs, fixed the GOMAXPROCS-collapse in `make check` perft.
- **hashStackEntry packed** into one uint64 (`5f3a4d8`).
- **NNUE int8 dot**: VPMADDWD square (`3ae33f8`), then the **hand-written AVX2 kernel** (`9ade95e`) → C++ parity, +19% search nps, **SPRT ~−62→−32 Elo vs C++** (see [[reference-pawnstar-perf-measurement]]).
- **Version scheme** (`c25c7a1`): `0.1.<git-commit-count>` in the UCI id, binary `pawnstar-go_0_1_<build>` + symlink (mirrors C++).
- **GitHub Actions** (`a257e4b`, `9f04267`): `.github/workflows/ci.yml` (make-check gate, gofmt/vet, per-OS artifacts — all green on Linux/Windows amd64 + macOS arm64) and `release.yml` (manual promote-CI-run-to-release with binaries + docs bundle + source archive), mirroring the C++ workflows.

**NEXT STEP the user is switching for:** hand-write **arm64/NEON** NNUE kernels — macOS currently runs the scalar path and is the remaining slow spot. Full plan, C++-comparison, integration, and gates in [[project-go-nnue-arm64-neon]]; kernel layout in [[reference-go-nnue-kernel-layout]]. On the Mac you can finally **run/benchmark arm64 natively** (I was on amd64 Linux and could only cross-compile-check).

Working style reminders: direct push to `main` is fine for pawnstar-go ([[feedback-direct-push-main-pawnstar-go]]); C++ repo confirm per task; don't hand-wave divergences ([[feedback-root-cause-not-benign]]).
