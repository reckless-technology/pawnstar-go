//go:build !(amd64 && goexperiment.simd)

package nnue

// Shared scalar NNUE helper kernels: the portable fallback used on every target that isn't the
// amd64+goexperiment.simd archsimd build. This file holds the kernels that stay scalar on all such
// targets (including arm64, which only accelerates addColumn/subColumn and the int8 output dot);
// addColumn/subColumn live in the arch-split files (kernels_generic.go / kernels_arm64.{go,s}).
// All are bit-identical to the SIMD kernels in kernels_amd64.go.

// screlu is the squared clipped ReLU activation: clamp to [0,QA] then square.
func screlu(x int16) int32 {
	y := int32(x)
	switch {
	case y < 0:
		y = 0
	case y > qa:
		y = qa
	}
	return y * y
}

// outputDot is the raw int64 SCReLU output dot over both perspectives: sum(clamp(a,0,QA)^2 * weight),
// stm weights (weights[:hiddenSize]) first, then ntm (weights[hiddenSize:]).
func outputDot(stm, ntm *[hiddenSize]int16, weights []int16) int64 {
	var output int64
	for i := 0; i < hiddenSize; i++ {
		output += int64(screlu(stm[i])) * int64(weights[i])
		output += int64(screlu(ntm[i])) * int64(weights[hiddenSize+i])
	}
	return output
}

// screluU8 is the int8-path activation: clamp to [0,QA], square, round, and shift right by
// int8Shift, yielding a uint8-range value in [0,127]. Mirrors the C++ ScReluU8 scalar path.
func screluU8(x int16) int64 {
	c := int(x)
	if c < 0 {
		c = 0
	} else if c > qa {
		c = qa
	}
	return int64((c*c + (1 << (int8Shift - 1))) >> int8Shift)
}

// outputDotInt8 is the int8 output dot: sum(screlu_u8(a) * w8) over both perspectives, where w8 are the
// int8-range weights (stored int16). The activations lie in [0,127] and the weights in [-127,127], so
// every product fits well within int32 and the exact integer sum matches the SIMD kernel bit-for-bit.
func outputDotInt8(stm, ntm *[hiddenSize]int16, w8 []int16) int64 {
	var output int64
	for i := 0; i < hiddenSize; i++ {
		output += screluU8(stm[i]) * int64(w8[i])
		output += screluU8(ntm[i]) * int64(w8[hiddenSize+i])
	}
	return output
}

// regroupWeightsForDot is the identity for the scalar kernels — outputDotInt8 above indexes the weights in
// natural order. (The SIMD kernel reorders them once at load to match its packed-activation lane layout.)
func regroupWeightsForDot(w []int16) []int16 { return w }
