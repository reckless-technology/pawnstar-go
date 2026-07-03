//go:build arm64

#include "textflag.h"

// Hand-written NEON accumulator column kernels — the arm64 analogue of the archsimd addColumn/subColumn.
// Each processes 32 int16 lanes per iteration (four 128-bit registers, 64 bytes) using VADD/VSUB with the
// .H8 (8×int16) arrangement, which wrap identically to the scalar `side[i] += col[i]`. nnueHiddenSize
// (1024) is a multiple of 32, so the loop runs a whole 32 iterations with no tail.

// func addColumn(side *[nnueHiddenSize]int16, col []int16)
TEXT ·addColumn(SB), NOSPLIT, $0-32
	MOVD side+0(FP), R0
	MOVD col_base+8(FP), R1
	MOVD $32, R2 // nnueHiddenSize/32 iterations
addLoop:
	VLD1   (R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VLD1.P 64(R1), [V4.B16, V5.B16, V6.B16, V7.B16]
	VADD   V4.H8, V0.H8, V0.H8
	VADD   V5.H8, V1.H8, V1.H8
	VADD   V6.H8, V2.H8, V2.H8
	VADD   V7.H8, V3.H8, V3.H8
	VST1.P [V0.B16, V1.B16, V2.B16, V3.B16], 64(R0)
	SUB    $1, R2, R2
	CBNZ   R2, addLoop
	RET

// func subColumn(side *[nnueHiddenSize]int16, col []int16)
TEXT ·subColumn(SB), NOSPLIT, $0-32
	MOVD side+0(FP), R0
	MOVD col_base+8(FP), R1
	MOVD $32, R2 // nnueHiddenSize/32 iterations
subLoop:
	VLD1   (R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VLD1.P 64(R1), [V4.B16, V5.B16, V6.B16, V7.B16]
	VSUB   V4.H8, V0.H8, V0.H8
	VSUB   V5.H8, V1.H8, V1.H8
	VSUB   V6.H8, V2.H8, V2.H8
	VSUB   V7.H8, V3.H8, V3.H8
	VST1.P [V0.B16, V1.B16, V2.B16, V3.B16], 64(R0)
	SUB    $1, R2, R2
	CBNZ   R2, subLoop
	RET
