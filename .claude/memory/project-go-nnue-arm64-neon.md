---
name: project-go-nnue-arm64-neon
description: "DONE (2026-07-02) — hand-written arm64/NEON NNUE kernels landed; arm64 Go now ~parity with C++ on nps"
metadata: 
  node_type: memory
  type: project
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

**COMPLETED 2026-07-02** (Apple Silicon Mac). Hand-written arm64/NEON NNUE kernels shipped, taking arm64 off the fully-scalar path. Commits on pawnstar-go `main`: `80067ac` (accumulator add/sub), `bad4791` (int8 dot), `e35035d` (dot ILP unroll). All bit-identical (bench still **7,007,401**).

**What landed:**
- **`addColumn`/`subColumn`** ([engine/nnue_kernels_arm64.{go,s}]) — 128-bit NEON `VADD`/`VSUB` over `.H8`, 32 int16/iter. Go's arm64 assembler exposes these directly, **no hand-encoding needed**. Micro-bench: refresh 22152→2901 ns (**7.6×**), update 23181→3204 ns (**7.2×**).
- **int8 output dot** ([engine/nnue_dot_arm64.{go,s}], `outputDotInt8Asm`) — per 8 lanes: `SQXTUN` (clamp int16→uint8 [0,QA] + narrow), `UMULL` (uint8² → uint16), `VADD` round 256, **`VUSHR` (UNSIGNED)** >>9 → activation [0,127], `SMLAL`/`SMLAL2` (signed-widen MAC) into int32. Unrolled 4 blocks/iter with **4 independent int32 accumulator pairs (V16–V23)** to break the loop-carried SMLAL-latency chain. Micro-bench: evaluateInt8 1242→**125 ns (9.9×)**.

**The gotcha resolved:** Go's arm64 assembler has NO vector integer multiply / dot-product mnemonic (no `VMUL`/`UMLAL`/`UDOT`/`SDOT`, no signed min/max). So `SQXTUN`/`UMULL`/`SMLAL`/`SMLAL2` are emitted as **`WORD $0x...` encodings**, obtained from **clang** (`clang -c neon.s` → dump the 32-bit words) and verified two ways: `go tool objdump` decodes each WORD back to the intended instruction (it *disassembles* `VSQXTUN`/`VUMULL`/`VSMLAL` even though it can't *assemble* them), and `TestOutputDotInt8Asm` gates NEON == scalar bit-for-bit over 500 random trials (incl. clamp edges). The clean scalar helpers (nnueSCReLU, outputDot, outputDotInt8, regroupWeightsForDot) moved to `nnue_kernels_scalar.go` (tag `!(amd64&&simd)`, arm64 included); only addColumn/subColumn + the dot are arch-split. See [[reference-go-nnue-kernel-layout]].

**Result (clean apples-to-apples, both engines bench 7,007,401 = identical trees):** arm64 Go bench **~190k → ~1.14M nps (6.0×)**; vs C++ (`/Users/jonny/work/pawnstar` rebuilt at the parity HEAD) **~1.15–1.19M nps** → Go is now **~0.98× C++ (within 2–3%), i.e. ~parity** on Apple Silicon — up from ~6.2× slower when fully scalar, and notably *closer* than the amd64 gap (~1.66×). See [[reference-pawnstar-perf-measurement]] and [[project-cross-engine-bit-parity]].

**Possible next levers (all optional now that arm64 is at parity):** amd64 BMI2 PEXT sliding attacks (helps perft/movegen, not eval; needs Go asm); giving the dot's per-block temps distinct registers for marginally more ILP; an SPRT at a real TC to confirm the strength effect of the arm64 speedup.
