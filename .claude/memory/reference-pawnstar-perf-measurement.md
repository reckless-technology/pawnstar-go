---
name: reference-pawnstar-perf-measurement
description: "How to measure pawnstar perf reliably on this PC — thermal throttling, GOMAXPROCS, memprofile pitfalls"
metadata: 
  node_type: memory
  type: reference
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

Benchmarking on this machine (laptop i9-13900HX) is noisy — take best-of-N and interleave the two engines in the same thermal state:

- **Thermal throttling:** sustained/back-to-back heavy runs drop clocks and read up to ~2× slow; short, cool runs are truer. (A perft that looked "11× slower than C++" was really ~3× once measured clean.)
- **`-memprofile` massively inflates** allocation-heavy workloads (it samples every allocation) — never trust nps from a run that also has `-memprofile`.
- **GOMAXPROCS matters for allocation-bound Go code:** default (all cores) can be *slower* than `GOMAXPROCS=1`, because Go's concurrent GC spins up background mark workers (~25% of GOMAXPROCS) that contend for memory bandwidth with a single-threaded mutator. This is why the old perft (per-node slice alloc) showed ~45 Mnps under multi-core `make check` vs ~145 at 1 core.

Perft baseline after the allocation-free `moveList` (start position, single-thread, now core-count-independent): **Go ~200 Mnps vs C++ ~540 Mnps (~2.7×)**. But perft (move-gen only) overstates the practical gap — real NNUE search is **eval-dominated**.

**Eval (the lever that actually moves strength).** The int8 output dot was the whole eval-kernel gap; the accumulator update was already at parity. Head-to-head on this PC: archsimd `outputDotInt8` ~244 ns vs C++ `OutputDotInt8Avx2` ~130 ns (1.87×). The **hand-written AVX2 kernel** (`nnue_dot_amd64.s`, VPMADDUBSW) is now **~129 ns = C++ parity**; `evaluateInt8` ~249→**132 ns**; bench nps ~1.44M→**~1.72M (+19%)**. **SPRT at 10+0.1 vs C++ improved from ~−62 Elo to ~−32** (roughly +30) after the asm kernel — the first change that meaningfully closed the gap. (Earlier steps — moveList, hashStackEntry pack, the VPMADDWD square — were engineering wins but ~neutral in the SPRT.)

**macOS/arm64 is the remaining slow spot:** it runs the fully scalar kernels (no auto-vectorization in Go; archsimd + the AVX2 asm are amd64-only). Fixing it needs hand-written NEON — see [[project-go-nnue-arm64-neon]] and [[reference-go-nnue-kernel-layout]]. Lower-priority alt lever on amd64: BMI2 PEXT sliding attacks (helps perft, not eval; needs Go asm). See [[project-cross-engine-bit-parity]].
