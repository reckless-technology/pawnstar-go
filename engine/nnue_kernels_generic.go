//go:build !(amd64 && goexperiment.simd)

package engine

// Scalar NNUE kernels: the portable fallback used on non-amd64 targets, or when built without
// GOEXPERIMENT=simd (which gates simd/archsimd). Bit-identical to the SIMD kernels in
// nnue_kernels_amd64.go.

// nnueSCReLU is the squared clipped ReLU activation: clamp to [0,QA] then square.
func nnueSCReLU(x int16) int32 {
	y := int32(x)
	switch {
	case y < 0:
		y = 0
	case y > nnueQA:
		y = nnueQA
	}
	return y * y
}

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

// outputDot is the raw int64 SCReLU output dot over both perspectives: sum(clamp(a,0,QA)^2 * weight),
// stm weights (weights[:nnueHiddenSize]) first, then ntm (weights[nnueHiddenSize:]).
func outputDot(stm, ntm *[nnueHiddenSize]int16, weights []int16) int64 {
	var output int64
	for i := 0; i < nnueHiddenSize; i++ {
		output += int64(nnueSCReLU(stm[i])) * int64(weights[i])
		output += int64(nnueSCReLU(ntm[i])) * int64(weights[nnueHiddenSize+i])
	}
	return output
}
