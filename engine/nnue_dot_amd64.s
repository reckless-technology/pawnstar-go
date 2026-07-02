//go:build amd64 && goexperiment.simd

#include "textflag.h"

// Hand-written AVX2 int8 output dot — a port of the C++ OutputDotInt8Avx2. Per 32-lane block: clamp the
// accumulator to [0,QA], SCReLU-square (VPMADDWD self-multiply of the zero-extended [v,0] pairs), >>9 to a
// uint8 activation, pack to uint8x32 in natural order (two VPACKUSDW + VPACKUSWB + VPERMD), then VPMADDUBSW
// against the natural-order int8 weights and widen (VPMADDWD by 1) into an int32 accumulator. Bit-identical
// to the scalar Σ screlu_u8(a)·w8: with kInt8Shift=9 the activation is <=127 and each maddubs pair-sum
// (< 2*127*127) stays in int16, so no saturation anywhere.
//
// One block: 32 int16 accumulator lanes at (SI), 32 int8 weights at (DX), accumulated into Y0.
// Constants: Y1=zero, Y2=qa(255,i16), Y3=round(256,i32), Y4=ones(1,i16), Y5=perm[0,4,1,5,2,6,3,7].
#define SCRELU_BLOCK \
	VMOVDQU (SI), Y6         \
	VMOVDQU 32(SI), Y7       \
	VPMAXSW Y1, Y6, Y6       \
	VPMINSW Y2, Y6, Y6       \
	VPMAXSW Y1, Y7, Y7       \
	VPMINSW Y2, Y7, Y7       \
	VPMOVZXWD X6, Y8         \
	VPMADDWD Y8, Y8, Y8      \
	VPADDD Y3, Y8, Y8        \
	VPSRLD $9, Y8, Y8        \
	VEXTRACTI128 $1, Y6, X9  \
	VPMOVZXWD X9, Y9         \
	VPMADDWD Y9, Y9, Y9      \
	VPADDD Y3, Y9, Y9        \
	VPSRLD $9, Y9, Y9        \
	VPMOVZXWD X7, Y10        \
	VPMADDWD Y10, Y10, Y10   \
	VPADDD Y3, Y10, Y10      \
	VPSRLD $9, Y10, Y10      \
	VEXTRACTI128 $1, Y7, X11 \
	VPMOVZXWD X11, Y11       \
	VPMADDWD Y11, Y11, Y11   \
	VPADDD Y3, Y11, Y11      \
	VPSRLD $9, Y11, Y11      \
	VPACKUSDW Y9, Y8, Y8     \
	VPACKUSDW Y11, Y10, Y10  \
	VPACKUSWB Y10, Y8, Y8    \
	VPERMD Y8, Y5, Y8        \
	VMOVDQU (DX), Y12        \
	VPMADDUBSW Y12, Y8, Y8   \
	VPMADDWD Y4, Y8, Y8      \
	VPADDD Y8, Y0, Y0

// func outputDotInt8Asm(stm, ntm *[nnueHiddenSize]int16, w8 []int8) int64
TEXT ·outputDotInt8Asm(SB), NOSPLIT, $0-48
	MOVQ stm+0(FP), SI
	MOVQ w8_base+16(FP), DX
	VPXOR Y0, Y0, Y0
	VPXOR Y1, Y1, Y1
	VMOVDQU qaConst<>(SB), Y2
	VMOVDQU roundConst<>(SB), Y3
	VMOVDQU onesConst<>(SB), Y4
	VMOVDQU permConst<>(SB), Y5

	MOVQ $32, CX // nnueHiddenSize/32 blocks for the stm perspective
loopStm:
	SCRELU_BLOCK
	ADDQ $64, SI
	ADDQ $32, DX
	DECQ CX
	JNZ loopStm

	MOVQ ntm+8(FP), SI
	MOVQ $32, CX // and the ntm perspective (weights DX continue into the second half)
loopNtm:
	SCRELU_BLOCK
	ADDQ $64, SI
	ADDQ $32, DX
	DECQ CX
	JNZ loopNtm

	// horizontal sum of the 8 int32 lanes in Y0 -> AX (sign-extended to 64 bits)
	VEXTRACTI128 $1, Y0, X1
	VPADDD X1, X0, X0
	VPSHUFD $0xEE, X0, X1
	VPADDD X1, X0, X0
	VPSHUFD $0x55, X0, X1
	VPADDD X1, X0, X0
	VMOVD X0, AX
	MOVLQSX AX, AX
	MOVQ AX, ret+40(FP)
	VZEROUPPER
	RET

DATA qaConst<>+0(SB)/8, $0x00ff00ff00ff00ff
DATA qaConst<>+8(SB)/8, $0x00ff00ff00ff00ff
DATA qaConst<>+16(SB)/8, $0x00ff00ff00ff00ff
DATA qaConst<>+24(SB)/8, $0x00ff00ff00ff00ff
GLOBL qaConst<>(SB), RODATA|NOPTR, $32

DATA roundConst<>+0(SB)/8, $0x0000010000000100
DATA roundConst<>+8(SB)/8, $0x0000010000000100
DATA roundConst<>+16(SB)/8, $0x0000010000000100
DATA roundConst<>+24(SB)/8, $0x0000010000000100
GLOBL roundConst<>(SB), RODATA|NOPTR, $32

DATA onesConst<>+0(SB)/8, $0x0001000100010001
DATA onesConst<>+8(SB)/8, $0x0001000100010001
DATA onesConst<>+16(SB)/8, $0x0001000100010001
DATA onesConst<>+24(SB)/8, $0x0001000100010001
GLOBL onesConst<>(SB), RODATA|NOPTR, $32

DATA permConst<>+0(SB)/8, $0x0000000400000000
DATA permConst<>+8(SB)/8, $0x0000000500000001
DATA permConst<>+16(SB)/8, $0x0000000600000002
DATA permConst<>+24(SB)/8, $0x0000000700000003
GLOBL permConst<>(SB), RODATA|NOPTR, $32
