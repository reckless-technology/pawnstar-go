//go:build !(amd64 && goexperiment.simd)

package engine

// int8OutputDot is the search's int8 output dot — the scalar/archsimd kernel on non-AVX2-SIMD builds.
func (n *nnueNetwork) int8OutputDot(stm, ntm *[nnueHiddenSize]int16) int64 {
	return outputDotInt8(stm, ntm, n.outputWeightsI8)
}
