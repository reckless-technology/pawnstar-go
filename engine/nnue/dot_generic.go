//go:build !(amd64 && goexperiment.simd) && !arm64

package nnue

// int8OutputDot is the search's int8 output dot — the scalar/archsimd kernel on non-AVX2-SIMD builds.
func (n *Network) int8OutputDot(stm, ntm *[hiddenSize]int16) int64 {
	return outputDotInt8(stm, ntm, n.outputWeightsI8)
}
