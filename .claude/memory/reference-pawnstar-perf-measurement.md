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

Perft baseline after the allocation-free `moveList` (start position, single-thread, now core-count-independent): **Go ~200 Mnps vs C++ ~540 Mnps (~2.7×)**. In real NNUE search the gap is only ~1.66× (eval-dominated), so perft overstates the practical difference. Next perf lever if pursued: BMI2 **PEXT** sliding attacks in Go (C++ uses it; Go has no stdlib `pext` intrinsic, so it needs hand-written assembly). See [[project-cross-engine-bit-parity]].
