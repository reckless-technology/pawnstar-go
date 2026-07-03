//go:build !(amd64 && goexperiment.simd) && !arm64

package nnue

// Scalar accumulator column kernels: the portable fallback for targets without a hand-written
// vectorised addColumn/subColumn. arm64 supplies NEON versions in nnue_kernels_arm64.{go,s}; the
// amd64+simd build supplies archsimd versions in nnue_kernels_amd64.go. The shared scalar helpers
// (nnueSCReLU, outputDot, outputDotInt8, ...) live in nnue_kernels_scalar.go. All are bit-identical.

// addColumn adds a feature column into a perspective accumulator (wrapping int16).
func addColumn(side *[nnueHiddenSize]int16, col []int16) {
	for i := 0; i < nnueHiddenSize; i++ {
		side[i] += col[i]
	}
}

// subColumn subtracts a feature column from a perspective accumulator (inverse of addColumn).
func subColumn(side *[nnueHiddenSize]int16, col []int16) {
	for i := 0; i < nnueHiddenSize; i++ {
		side[i] -= col[i]
	}
}
