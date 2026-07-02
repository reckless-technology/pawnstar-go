//go:build !(amd64 && goexperiment.simd)

package engine

// Shared scalar NNUE helper kernels: the portable fallback used on every target that isn't the
// amd64+goexperiment.simd archsimd build. This file holds the kernels that stay scalar on all such
// targets (including arm64, which only accelerates addColumn/subColumn and the int8 output dot);
// addColumn/subColumn live in the arch-split files (nnue_kernels_generic.go / nnue_kernels_arm64.{go,s}).
// All are bit-identical to the SIMD kernels in nnue_kernels_amd64.go.

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

// nnueSCReLUu8 is the int8-path activation: clamp to [0,QA], square, round, and shift right by
// nnueInt8Shift, yielding a uint8-range value in [0,127]. Mirrors the C++ ScReluU8 scalar path.
func nnueSCReLUu8(x int16) int64 {
	c := int(x)
	if c < 0 {
		c = 0
	} else if c > nnueQA {
		c = nnueQA
	}
	return int64((c*c + (1 << (nnueInt8Shift - 1))) >> nnueInt8Shift)
}

// outputDotInt8 is the int8 output dot: sum(screlu_u8(a) * w8) over both perspectives, where w8 are the
// int8-range weights (stored int16). The activations lie in [0,127] and the weights in [-127,127], so
// every product fits well within int32 and the exact integer sum matches the SIMD kernel bit-for-bit.
func outputDotInt8(stm, ntm *[nnueHiddenSize]int16, w8 []int16) int64 {
	var output int64
	for i := 0; i < nnueHiddenSize; i++ {
		output += nnueSCReLUu8(stm[i]) * int64(w8[i])
		output += nnueSCReLUu8(ntm[i]) * int64(w8[nnueHiddenSize+i])
	}
	return output
}

// regroupWeightsForDot is the identity for the scalar kernels — outputDotInt8 above indexes the weights in
// natural order. (The SIMD kernel reorders them once at load to match its packed-activation lane layout.)
func regroupWeightsForDot(w []int16) []int16 { return w }
