//go:build !(amd64 && goexperiment.simd) && !arm64

package nnue

// int8OutputDot is the search's int8 output dot — the scalar/archsimd kernel on non-AVX2-SIMD builds.
// bucket selects the output head, whose 2*hiddenSize weights are a contiguous run, so the kernel is
// head-agnostic.
func (n *Network) int8OutputDot(stm, ntm *[hiddenSize]int16, bucket int) int64 {
	start := bucket * 2 * hiddenSize
	return outputDotInt8(stm, ntm, n.outputWeightsI8[start:start+2*hiddenSize])
}
