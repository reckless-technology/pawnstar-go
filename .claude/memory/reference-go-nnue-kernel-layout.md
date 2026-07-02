---
name: reference-go-nnue-kernel-layout
description: pawnstar-go NNUE kernel file/build-tag/dispatch layout + the 3 output-weight representations
metadata: 
  node_type: memory
  type: reference
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

Map of the NNUE eval code in `engine/` (needed to slot in a new-arch SIMD kernel, e.g. arm64 NEON — see [[project-go-nnue-arm64-neon]]):

**Shared (`nnue.go`, all builds):** loader (`PSN1` format), the structs, `evaluateExact` (int16 SCReLU reference — the correctness oracle, used by `TestNNUEReference`), `evaluateInt8` (search hot path), and `deriveInt8OutputWeights`. `evaluateInt8` clamps to the eval band then calls **`dot := n.int8OutputDot(stm, ntm)`** — a build-tagged method.

**Three output-weight representations (all built in `deriveInt8OutputWeights`):**
- `outputWeights []int16` — raw net weights (drive `evaluateExact`).
- `outputWeightsI8 []int16` — requantised to int8 range but stored int16, **regrouped** into VPACKSSDW lane order (`regroupWeightsForDot`) for the archsimd int16 dot.
- `outputWeightsI8Nat []int8` — same int8 values in **natural order**; consumed by the hand-written asm dot (VPMADDUBSW/UDOT-style), which permutes activations to natural order instead of regrouping weights.

**Kernel files by build tag:**
- `nnue_kernels_amd64.go` (`amd64 && goexperiment.simd`): archsimd `addColumn`/`subColumn`, `outputDot` (int16 ref), and `outputDotInt8` (int16 VPMADDWD kernel — now kept only as the bit-identical **reference** for the asm isolation test), `regroupWeightsForDot`, `nnueInt8Round`.
- `nnue_dot_amd64.go` + `nnue_dot_amd64.s` (`amd64 && goexperiment.simd`): the production hand-written **AVX2** `outputDotInt8Asm(stm, ntm *[nnueHiddenSize]int16, w8 []int8) int64` and the `int8OutputDot` dispatch method → `outputDotInt8Asm(stm, ntm, n.outputWeightsI8Nat)`.
- `nnue_kernels_generic.go` (`!(amd64 && goexperiment.simd)`): scalar `nnueSCReLU`, `nnueSCReLUu8`, `outputDot`, `outputDotInt8(…, w8 []int16)`, `addColumn`, `subColumn`, `regroupWeightsForDot` (identity).
- `nnue_dot_generic.go` (`!(amd64 && goexperiment.simd)`): scalar `int8OutputDot` → `outputDotInt8(stm, ntm, n.outputWeightsI8)`.

So **on arm64 today**, `nnue_kernels_generic.go` + `nnue_dot_generic.go` are active → fully scalar. To add NEON: new `nnue_dot_arm64.{go,s}` (tag `arm64`), and **narrow the `generic` files' tags to also exclude arm64** so arm64 picks up the NEON dispatch/kernels. `GOEXPERIMENT=simd` only matters for the amd64 archsimd path (the Makefile exports it); hand asm needs no experiment. The int8 dot is `Σ screlu_u8(a)·w8` in int32, returned int64; `nnueInt8Shift=9`, `nnueQA=255`, round `1<<8=256`.
