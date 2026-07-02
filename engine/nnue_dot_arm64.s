//go:build arm64

#include "textflag.h"

// Hand-written NEON int8 output dot — the arm64 analogue of the AVX2 OutputDotInt8Avx2. Per 8-lane block:
// SQXTUN clamps the int16 accumulator to unsigned [0,QA] and narrows to uint8 (negatives -> 0, >QA -> QA);
// UMULL squares it to uint16 (<= QA*QA = 65025, exact); add the round (256) and VUSHR >>9 give the uint8
// activation in [0,127]; then SMLAL/SMLAL2 signed-widen-multiply the activation by the int8-range weights
// (stored int16) and accumulate into int32. Bit-identical to the scalar Σ screlu_u8(a)·w8: with
// nnueInt8Shift=9 the activation is <=127 and every lane sum stays well within int32.
//
// The loop is unrolled 4 blocks (32 lanes) per iteration, each block accumulating into its OWN int32
// vector pair (V16/V17, V18/V19, V20/V21, V22/V23). The SMLAL accumulate is a read-modify-write of an
// architectural register, so a single accumulator would serialise the loop on SMLAL's latency; four
// independent accumulators let consecutive blocks overlap and the out-of-order core hides it. The pairs
// are summed after the loops. nnueHiddenSize (1024) is a multiple of 32, so 32 iterations per perspective.
//
// Go's arm64 assembler lacks vector integer multiply / dot-product mnemonics, so SQXTUN/UMULL/SMLAL/SMLAL2
// are WORD-encoded (clang-verified; go tool objdump decodes them back; TestOutputDotInt8Asm gates them
// bit-identical to the scalar dot). VUSHR is the UNSIGNED shift: c*c+256 can set bit 15 (up to 65281), and
// only a logical >>9 recovers the [0,127] value.
//
// Per block: V0=accumulator int16 (8 lanes), V1=uint8 clamp, V2=square then activation, V3=int16 weights,
// V6=round (256 per int16 lane). REQUANT is the shared clamp/square/shift; each block then adds its own
// SMLAL pair. Ops: sqxtun v1.8b,v0.8h; umull v2.8h,v1.8b,v1.8b; smlal v{16,18,20,22}.4s,v2.4h,v3.4h;
// smlal2 v{17,19,21,23}.4s,v2.8h,v3.8h.
#define REQUANT            \
	VLD1.P 16(R0), [V0.B16]   \
	WORD $0x2e212801          \
	WORD $0x2e21c022          \
	VADD  V6.H8, V2.H8, V2.H8 \
	VUSHR $9, V2.H8, V2.H8    \
	VLD1.P 16(R1), [V3.B16]

#define DOT32          \
	REQUANT           \
	WORD $0x0e638050  \
	WORD $0x4e638051  \
	REQUANT           \
	WORD $0x0e638052  \
	WORD $0x4e638053  \
	REQUANT           \
	WORD $0x0e638054  \
	WORD $0x4e638055  \
	REQUANT           \
	WORD $0x0e638056  \
	WORD $0x4e638057

// func outputDotInt8Asm(stm, ntm *[nnueHiddenSize]int16, w8 []int16) int64
TEXT ·outputDotInt8Asm(SB), NOSPLIT, $0-48
	MOVD  stm+0(FP), R0
	MOVD  w8_base+16(FP), R1
	VMOVI $0, V16.B16 // zero the four int32 accumulator pairs
	VMOVI $0, V17.B16
	VMOVI $0, V18.B16
	VMOVI $0, V19.B16
	VMOVI $0, V20.B16
	VMOVI $0, V21.B16
	VMOVI $0, V22.B16
	VMOVI $0, V23.B16
	MOVD  $roundH<>(SB), R3
	VLD1  (R3), [V6.B16] // round constant: 256 in every int16 lane

	MOVD $32, R2 // nnueHiddenSize/32 iterations for the stm perspective
loopStm:
	DOT32
	SUB  $1, R2, R2
	CBNZ R2, loopStm

	MOVD ntm+8(FP), R0 // ntm perspective; weights R1 continue into the second half
	MOVD $32, R2
loopNtm:
	DOT32
	SUB  $1, R2, R2
	CBNZ R2, loopNtm

	VADD  V17.S4, V16.S4, V16.S4 // reduce the four accumulator pairs
	VADD  V19.S4, V18.S4, V18.S4
	VADD  V21.S4, V20.S4, V20.S4
	VADD  V23.S4, V22.S4, V22.S4
	VADD  V18.S4, V16.S4, V16.S4
	VADD  V22.S4, V20.S4, V20.S4
	VADD  V20.S4, V16.S4, V16.S4
	VADDV V16.S4, V0  // horizontal sum of the 4 int32 lanes -> S0
	VMOV  V0.S[0], R0 // move the 32-bit sum to R0 (upper 32 zeroed)
	SXTW  R0, R0      // sign-extend to int64
	MOVD  R0, ret+40(FP)
	RET

DATA roundH<>+0(SB)/8, $0x0100010001000100
DATA roundH<>+8(SB)/8, $0x0100010001000100
GLOBL roundH<>(SB), RODATA|NOPTR, $16
