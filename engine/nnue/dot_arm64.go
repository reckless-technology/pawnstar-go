//go:build arm64

package nnue

// outputDotInt8Asm is the hand-written NEON int8 output dot (dot_arm64.s), the arm64 analogue of the
// amd64 AVX2 kernel. Per 8 int16 lanes it SCReLU-requantises the accumulator to a uint8-range activation
// (SQXTUN clamp+narrow, UMULL square, +round, VUSHR >>9), then signed-widening multiply-accumulates
// (SMLAL/SMLAL2) against the int8-range weights (stored int16, natural order) into int32 accumulators.
// Bit-identical to the scalar outputDotInt8 (Σ screlu_u8(a)·w8); returns the int32 dot as int64.
//
// Go's arm64 assembler has no vector integer multiply or dot-product mnemonic, so SQXTUN/UMULL/SMLAL/SMLAL2
// are emitted as WORD-encoded instructions (verified by disassembly and gated bit-identical by
// TestOutputDotInt8Asm). w8 must have 2*hiddenSize entries.
//
//go:noescape
func outputDotInt8Asm(stm, ntm *[hiddenSize]int16, w8 []int16) int64

// int8OutputDot is the search's int8 output dot — the hand-written NEON kernel on arm64. (The scalar
// outputDotInt8 is retained as the bit-identical reference for TestOutputDotInt8Asm.)
func (n *Network) int8OutputDot(stm, ntm *[hiddenSize]int16) int64 {
	return outputDotInt8Asm(stm, ntm, n.outputWeightsI8)
}
