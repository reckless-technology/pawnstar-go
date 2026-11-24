---
name: reference-pawnstar-perf-measurement
description: "How to measure pawnstar perf reliably on this PC — thermal throttling, GOMAXPROCS, memprofile pitfalls"
metadata: 
  node_type: memory
  type: reference
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

Benchmarking on this machine (laptop i9-13900HX) is noisy. The general method (best of N, interleaved builds in one
thermal state, no nps from a profiled run, the Apple Silicon `go test -c` interleaving) is in the
`reckless-chess-engines` skill, "Best of N, interleaved builds, one thermal state". What follows is this engine's own
figures and what the skill does not carry:

- **Thermal throttling, the figure:** a perft that looked "11× slower than C++" was really ~3× once measured clean.
- **GOMAXPROCS matters for allocation-bound Go code:** default (all cores) can be *slower* than `GOMAXPROCS=1`, because Go's concurrent GC spins up background mark workers (~25% of GOMAXPROCS) that contend for memory bandwidth with a single-threaded mutator. This is why the old perft (per-node slice alloc) showed ~45 Mnps under multi-core `make check` vs ~145 at 1 core.
- **Apple Silicon, the figures:** `go test -bench` single-shots read ~1.5× slow and `-count=N` agreed with itself while wrong (2026-07: Update read 4.7μs across 4 "consistent" runs vs its true 3.2μs); interleaved precompiled binaries gave pre/post readings within 1% at both 1s and 2s benchtimes. A kernel-level cpuprofile is the tie-breaker (artifact = same profile shape, everything uniformly slower).

Perft baseline after the allocation-free `moveList` (start position, single-thread, now core-count-independent): **Go ~200 Mnps vs C++ ~540 Mnps (~2.7×)**. But perft (move-gen only) overstates the practical gap — real NNUE search is **eval-dominated**.

**Eval (the lever that actually moves strength).** The int8 output dot was the whole eval-kernel gap; the accumulator update was already at parity. Head-to-head on this PC: archsimd `outputDotInt8` ~244 ns vs C++ `OutputDotInt8Avx2` ~130 ns (1.87×). The **hand-written AVX2 kernel** (`nnue_dot_amd64.s`, VPMADDUBSW) is now **~129 ns = C++ parity**; `evaluateInt8` ~249→**132 ns**; bench nps ~1.44M→**~1.72M (+19%)**. **SPRT at 10+0.1 vs C++ improved from ~−62 Elo to ~−32** (roughly +30) after the asm kernel — the first change that meaningfully closed the gap. **On arm64 after NEON (2026-07-02, 300 games @ 10+0.1 vs the DEBUGX-off C++ release): Go +5.8 ± 23.7 Elo — full playing-strength parity.** (Earlier steps — moveList, hashStackEntry pack, the VPMADDWD square — were engineering wins but ~neutral in the SPRT.)

**macOS/arm64 is now FIXED (2026-07-02):** hand-written NEON kernels landed (`80067ac`/`bad4791`/`e35035d`). Kernel micro-bench (Apple Silicon): evaluateInt8 **1242→125 ns (9.9×)**, refresh **7.6×**, update **7.2×**. Clean apples-to-apples bench (both engines 7,007,401 nodes = identical trees, interleaved, same thermal state): arm64 Go **~190k→~1.14M nps (6.0×)** vs C++ **~1.15–1.19M nps** → Go **~0.98× C++ (within 2–3%, ~parity)**, *closer* than the amd64 gap. How (WORD-encoded SQXTUN/UMULL/SMLAL via clang, ILP unroll) in [[project-go-nnue-arm64-neon]] and [[reference-go-nnue-kernel-layout]]. **PEXT sliding attacks: DEAD lever (2026-07-03)** — the user's own C++ experiments on x86_64 found PEXT gives little-to-no benefit over magic bitboards (matches general experience: both are one op + the same L1 lookup; PEXT was microcoded/slower on Zen 1/2). So the Go↔C++ perft gap (~200 vs ~540 Mnps) is NOT a PEXT gap — it is compiler codegen (C++ inlining/no bounds checks/scheduling), largely unrecoverable from Go and irrelevant to strength since eval dominates search. See also [[reference-staged-movegen-analysis]]. See [[project-cross-engine-bit-parity]].

**Per-thread eval cache experiment (2026-07-03): NO EFFECT — hypothesis (shared-cache contention slows Lazy SMP) not supported on this hardware.** Made the eval cache per-searchState with plain non-atomic slots (removing both the cross-thread sharing and the LDAR/STLR per access) and measured **time-to-depth at Threads=8** over BK-24 (bench can't be used — it pins Threads=1; and single-threaded tests cannot see contention at all — at 1T the only delta is atomic-instruction overhead, a different claim). 10 interleaved rounds (4 @ d12, 6 @ d13): **candidate −1.8% total, per-round σ ≈ 10% → statistically zero** (contention saved ≈ sharing lost, or both small — Apple Silicon's cheap on-die coherence + the 1M-slot table diluting hot lines). Reverted to the shared atomic cache. Revisit only on high-core-count amd64 (pricier coherence) or with a hybrid (small per-thread L1-ish cache in front of the shared table). Harness: scratchpad bk24_mt_ttd{,13}.py (UCI driver, Threads=8, Clear Hash per position, interleaved).
