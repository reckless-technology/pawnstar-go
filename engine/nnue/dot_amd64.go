//go:build amd64 && goexperiment.simd

package nnue

// outputDotInt8Asm is the hand-written AVX2 int8 output dot (dot_amd64.s), a port of the C++
// OutputDotInt8Avx2: per 32 lanes it SCReLU-requantises the accumulator to uint8, then VPMADDUBSW against
// the natural-order int8 weights and widens+accumulates in int32. w8 must have 2*hiddenSize entries.
// Bit-identical to the scalar/archsimd outputDotInt8 (Σ screlu_u8(a)·w8); returns the int32 dot as int64.
func outputDotInt8Asm(stm, ntm *[hiddenSize]int16, w8 []int8) int64

// int8OutputDot is the search's int8 output dot — the hand-written AVX2 kernel on this build. (The archsimd
// int16 outputDotInt8 is retained as the bit-identical reference for TestOutputDotInt8Asm.) bucket selects
// the output head, whose 2*hiddenSize weights are a contiguous run, so the kernel is head-agnostic.
func (n *Network) int8OutputDot(stm, ntm *[hiddenSize]int16, bucket int) int64 {
	start := bucket * 2 * hiddenSize
	return outputDotInt8Asm(stm, ntm, n.outputWeightsI8Nat[start:start+2*hiddenSize])
}
