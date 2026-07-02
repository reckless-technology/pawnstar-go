---
name: project-go-nnue-arm64-neon
description: "NEXT STEP — hand-write arm64/NEON NNUE kernels for macOS (Go doesn't auto-vectorize; archsimd is amd64-only)"
metadata: 
  node_type: memory
  type: project
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

**Active task (user switched to an Apple Silicon Mac to do this):** speed up NNUE evaluation on macOS/arm64.

**Why it's slow now:** on arm64 the Go engine uses the **scalar** NNUE kernels (`engine/nnue_kernels_generic.go`, build tag `!(amd64 && goexperiment.simd)`). The archsimd kernels and the hand-written AVX2 kernel are **amd64-only**, and **Go's compiler does essentially no auto-vectorization**, so those scalar loops stay scalar (one int16 add / one multiply per lane).

**How C++ pawnstar does it (the answer that motivated this):** C++ has **no NEON code** — just `#if defined(__AVX2__) … #else // scalar fallback` with plain contiguous-array loops (`acc[i] = acc[i] + column[i]`, `Σ screlu_u8·w8`). On arm64 it compiles the scalar branch with **`-O3 -mcpu=apple-m1`** (generic aarch64 uses `-march=armv8.2-a+dotprod`, set in the C++ CMakeLists), and **clang auto-vectorizes** it into NEON: `add.8h` for the accumulators and **`UDOT`/`SDOT`** (ARMv8.2 dot-product, the NEON analog of VPMADDUBSW) for the int8 dot. Go can't replicate this via a flag — it needs explicit SIMD.

**Plan for Go — hand-written NEON assembly**, mirroring the amd64 asm:
- Port `engine/nnue_dot_amd64.s` → `engine/nnue_dot_arm64.s` (`outputDotInt8Asm`). Algorithm per 32 (or 16) lanes: clamp int16 to [0,255] (`VMAX`/`VMIN`), SCReLU square, `+256`, `>>9` to uint8 in [0,127], then **`UDOT`** (uint8 activations × int8 weights → int32) accumulate. Consumes `n.outputWeightsI8Nat` (`[]int8`, natural order — already derived in `deriveInt8OutputWeights`).
- Also NEON-ify `addColumn`/`subColumn` (int16 add/sub, `VADD`/`VSUB` over 8×int16 = 128-bit NEON).
- **SCReLU formula (bit-identical):** `u8 = (clamp(x,0,255)² + 256) >> 9`, in [0,127]; dot = Σ u8·w8 (w8 ∈ [-127,127]) in int32, no saturation (guaranteed by shift=9). Returns int64.

**Integration (build-tag dispatch, mirror the amd64 layout):**
- Kernel files today: `nnue_dot_amd64.go`+`.s` (`amd64 && goexperiment.simd`) define `outputDotInt8Asm` + method `nnueNetwork.int8OutputDot`; `nnue_dot_generic.go` (`!(amd64 && goexperiment.simd)`) defines the scalar `int8OutputDot`. `evaluateInt8` calls `n.int8OutputDot(stm, ntm)`.
- For arm64: add `nnue_dot_arm64.go` (build tag `arm64`, no goexperiment needed — it's hand asm) declaring `outputDotInt8Asm` + the `int8OutputDot` dispatch, and `nnue_dot_arm64.s`. **Narrow `nnue_dot_generic.go`'s tag to also exclude arm64** so arm64 uses the NEON dispatch. Same for the accumulator kernels vs `nnue_kernels_generic.go`.

**Gates (all must stay green on arm64):** add an isolation test like `TestOutputDotInt8Asm` but comparing the NEON `outputDotInt8Asm` to the **scalar** `outputDotInt8` (which IS compiled on arm64) over random accumulators; plus `TestNNUEInt8` (scalar==SIMD), `TestNNUEReference`, `TestNNUEIncremental`, and `bench` still **7,007,401** (see [[project-cross-engine-bit-parity]]).

**Big advantage on the Mac:** you can **run and benchmark arm64 natively** (I could only cross-compile-check on amd64 Linux via `GOOS=darwin GOARCH=arm64 GOEXPERIMENT=simd go build`). Develop iteratively: isolation test for correctness, then micro-bench the kernel vs scalar.

**Gotcha to verify first:** Go's arm64 assembler NEON coverage is thinner than amd64's — `VADD`/`SMULL` exist, but confirm **`VUDOT`/`VSDOT`/`UDOT`** are supported (grep `$(go env GOROOT)/src/cmd/internal/obj/arm64/anames.go`); if not, hand-encode via `WORD $0x...`. See [[reference-go-nnue-kernel-layout]] and [[reference-pawnstar-perf-measurement]].
