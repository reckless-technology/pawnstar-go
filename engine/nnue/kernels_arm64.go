//go:build arm64

package nnue

// arm64 NEON accumulator column kernels. addColumn/subColumn are hand-written 128-bit NEON in
// nnue_kernels_arm64.s (VADD/VSUB over int16 lanes), 32 int16 per iteration. Go's arm64 assembler
// exposes VADD/VSUB directly, so — unlike the int8 output dot — no instructions need hand-encoding.
// The shared scalar helpers (nnueSCReLU, outputDot, outputDotInt8, regroupWeightsForDot) come from
// nnue_kernels_scalar.go; the NEON int8 dot lives in nnue_dot_arm64.{go,s}. Bit-identical to scalar.

//go:noescape
func addColumn(side *[nnueHiddenSize]int16, col []int16)

//go:noescape
func subColumn(side *[nnueHiddenSize]int16, col []int16)
