//go:build !(amd64 && goexperiment.simd) && !arm64

package nnue

// Scalar accumulator column kernels: the portable fallback for targets without a hand-written
// vectorised addColumn/subColumn. arm64 supplies NEON versions in kernels_arm64.{go,s}; the
// amd64+simd build supplies archsimd versions in kernels_amd64.go. The shared scalar helpers
// (screlu, outputDot, outputDotInt8, ...) live in kernels_scalar.go. All are bit-identical.

// addColumn adds a feature column into a perspective accumulator (wrapping int16).
func addColumn(side *[hiddenSize]int16, col []int16) {
	for i := 0; i < hiddenSize; i++ {
		side[i] += col[i]
	}
}

// subColumn subtracts a feature column from a perspective accumulator (inverse of addColumn).
func subColumn(side *[hiddenSize]int16, col []int16) {
	for i := 0; i < hiddenSize; i++ {
		side[i] -= col[i]
	}
}
