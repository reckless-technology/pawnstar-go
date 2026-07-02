//go:build arm64

#include "textflag.h"

// Hand-written NEON int8 output dot — the arm64 analogue of the AVX2 OutputDotInt8Avx2. Per 8-lane block:
// SQXTUN clamps the int16 accumulator to unsigned [0,QA] and narrows to uint8 (negatives -> 0, >QA -> QA);
// UMULL squares it to uint16 (<= QA*QA = 65025, exact); add the round (256) and VUSHR >>9 gives the uint8
// activation in [0,127]; then SMLAL/SMLAL2 signed-widen-multiply the activation by the int8-range weights
// (stored int16) and accumulate into two int32 vectors. Bit-identical to the scalar Σ screlu_u8(a)·w8:
// with nnueInt8Shift=9 the activation is <=127 and each lane sum stays well within int32.
//
// Go's arm64 assembler lacks vector integer multiply / dot-product mnemonics, so those four ops are
// WORD-encoded (clang-verified; see TestOutputDotInt8Asm for the bit-identical gate). VUSHR is the
// UNSIGNED shift: c*c+256 can set bit 15 (up to 65281), and only a logical >>9 recovers the [0,127] value.
//
// Registers per block: V0=accumulator int16 (8 lanes), V1=uint8 clamp, V2=square then activation,
// V3=int16 weights, V4/V5=int32 accumulators (lo/hi lanes), V6=round (256 per int16 lane). The four
// WORD-encoded ops are: sqxtun v1.8b,v0.8h; umull v2.8h,v1.8b,v1.8b; smlal v4.4s,v2.4h,v3.4h;
// smlal2 v5.4s,v2.8h,v3.8h.
#define SCRELU_BLOCK       \
	VLD1.P 16(R0), [V0.B16]   \
	WORD $0x2e212801          \
	WORD $0x2e21c022          \
	VADD  V6.H8, V2.H8, V2.H8 \
	VUSHR $9, V2.H8, V2.H8    \
	VLD1.P 16(R1), [V3.B16]   \
	WORD $0x0e638044          \
	WORD $0x4e638045

// func outputDotInt8Asm(stm, ntm *[nnueHiddenSize]int16, w8 []int16) int64
TEXT ·outputDotInt8Asm(SB), NOSPLIT, $0-48
	MOVD stm+0(FP), R0
	MOVD w8_base+16(FP), R1
	VMOVI $0, V4.B16 // zero the int32 accumulators
	VMOVI $0, V5.B16
	MOVD  $roundH<>(SB), R3
	VLD1  (R3), [V6.B16] // round constant: 256 in every int16 lane

	MOVD $128, R2 // nnueHiddenSize/8 blocks for the stm perspective
loopStm:
	SCRELU_BLOCK
	SUB  $1, R2, R2
	CBNZ R2, loopStm

	MOVD ntm+8(FP), R0 // ntm perspective; weights R1 continue into the second half
	MOVD $128, R2
loopNtm:
	SCRELU_BLOCK
	SUB  $1, R2, R2
	CBNZ R2, loopNtm

	VADD  V5.S4, V4.S4, V4.S4 // combine lo+hi int32 accumulators
	VADDV V4.S4, V0           // horizontal sum of the 4 int32 lanes -> S0
	VMOV  V0.S[0], R0         // move the 32-bit sum to R0 (upper 32 zeroed)
	SXTW  R0, R0              // sign-extend to int64
	MOVD  R0, ret+40(FP)
	RET

DATA roundH<>+0(SB)/8, $0x0100010001000100
DATA roundH<>+8(SB)/8, $0x0100010001000100
GLOBL roundH<>(SB), RODATA|NOPTR, $16
