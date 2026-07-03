---
name: reference-go-nnue-kernel-layout
description: pawnstar-go NNUE package (engine/nnue) file/build-tag/dispatch layout + the 3 output-weight representations
metadata: 
  node_type: memory
  type: reference
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

Map of the NNUE eval code, now its **own package `pawnstar-go/engine/nnue`** (moved 2026-07-02; bit-identical, bench still 7,007,401). The package has NO engine imports: the engine passes positions as `nnue.Board` (raw `[7]uint64` pieces + `[2]uint64` colors + `[64]uint8` piece codes), built by `position.nnueBoard()` in `engine/nnue_bridge.go` — the only file where both vocabularies meet (also holds `Game.EnableNNUE`/`LoadNNUEFile` + `clampEvalToBand`). Exported API: `Network` (`LoadFile`/`Load`/`Embedded`), `Accumulator`, `Refresh`, `Update`, `EvaluateExact`, `EvaluateInt8` (stm = colour index int), `DiffLanes`. Needed to slot in a new-arch SIMD kernel — see [[project-go-nnue-arm64-neon]]:

**Shared (`engine/nnue/nnue.go`, all builds):** loader (`PSN1` format), the structs, `EvaluateExact` (int16 SCReLU reference — the correctness oracle, used by `TestNNUEReference`), `EvaluateInt8` (search hot path), and `deriveInt8OutputWeights`. `EvaluateInt8` calls **`dot := n.int8OutputDot(stm, ntm)`** — a build-tagged method.

**Three output-weight representations (all built in `deriveInt8OutputWeights`):**
- `outputWeights []int16` — raw net weights (drive `evaluateExact`).
- `outputWeightsI8 []int16` — requantised to int8 range but stored int16, **regrouped** into VPACKSSDW lane order (`regroupWeightsForDot`) for the archsimd int16 dot.
- `outputWeightsI8Nat []int8` — same int8 values in **natural order**; consumed by the hand-written asm dot (VPMADDUBSW/UDOT-style), which permutes activations to natural order instead of regrouping weights.

**Kernel files by build tag:**
- `engine/nnue/kernels_amd64.go` (`amd64 && goexperiment.simd`): archsimd `addColumn`/`subColumn`, `outputDot` (int16 ref), and `outputDotInt8` (int16 VPMADDWD kernel — now kept only as the bit-identical **reference** for the asm isolation test), `regroupWeightsForDot`, `nnueInt8Round`.
- `engine/nnue/dot_amd64.go` + `dot_amd64.s` (`amd64 && goexperiment.simd`): the production hand-written **AVX2** `outputDotInt8Asm(stm, ntm *[nnueHiddenSize]int16, w8 []int8) int64` and the `int8OutputDot` dispatch method → `outputDotInt8Asm(stm, ntm, n.outputWeightsI8Nat)`.
- (non-amd64-simd scalar + arm64 NEON files: see the arm64 section below.)

**arm64 NEON (landed 2026-07-02, see [[project-go-nnue-arm64-neon]]):** arm64 gets NEON for the accumulator kernels and the int8 dot while keeping scalar references (paths below now relative to `engine/nnue/`):
- `kernels_scalar.go` (tag `!(amd64 && goexperiment.simd)`, **arm64 included**): the shared scalar helpers `nnueSCReLU`, `outputDot` (drives evaluateExact on all non-simd builds), `nnueSCReLUu8`, `outputDotInt8` (the NEON dot's bit-identical reference), `regroupWeightsForDot` (identity). Moved here out of the old generic file.
- `kernels_generic.go` (tag `!(amd64 && goexperiment.simd) && !arm64`): now only the scalar `addColumn`/`subColumn`.
- `kernels_arm64.{go,s}` (tag `arm64`): NEON `addColumn`/`subColumn` (`VADD`/`VSUB` `.H8`, 32 int16/iter — named mnemonics, no WORD).
- `dot_generic.go` narrowed to `... && !arm64`; `dot_arm64.{go,s}` (tag `arm64`): `outputDotInt8Asm(stm, ntm, w8 []int16) int64` + `int8OutputDot` → `outputDotInt8Asm(stm, ntm, n.outputWeightsI8)` (arm64 consumes the **int16 natural-order** weights, since `regroupWeightsForDot` is identity — NOT `outputWeightsI8Nat`, which is amd64's VPMADDUBSW int8 form).
- `dot_asm_arm64_test.go` (tag `arm64`, in-package): `TestOutputDotInt8Asm`, NEON == scalar `outputDotInt8`.

Go's arm64 assembler has **no vector integer multiply / dot mnemonic**, so `SQXTUN`/`UMULL`/`SMLAL`/`SMLAL2` are `WORD $0x...` (clang-derived, `go tool objdump` round-trip verified). `GOEXPERIMENT=simd` only matters for the amd64 archsimd path (the Makefile exports it); hand asm needs no experiment. The int8 dot is `Σ screlu_u8(a)·w8` in int32, returned int64; `nnueInt8Shift=9`, `nnueQA=255`, round `1<<8=256`; **VUSHR must be UNSIGNED** (c²+256 can set bit 15).
