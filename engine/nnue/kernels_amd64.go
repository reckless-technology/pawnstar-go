//go:build amd64 && goexperiment.simd

package nnue

import "simd/archsimd"

// SIMD NNUE kernels using 256-bit AVX2-width archsimd vectors. Bit-identical to the scalar fallback
// in kernels_generic.go: the int16 column add/sub wrap identically, and the output dot uses the
// same clamp^2*weight products (which fit int32) accumulated in int64. Requires GOEXPERIMENT=simd.
//
// hiddenSize (1024) is a multiple of 16, so every kernel loops in whole 16-lane int16 steps.

// addColumn adds a feature column into a perspective accumulator (16 int16 lanes per step).
func addColumn(side *[hiddenSize]int16, col []int16) {
	for i := 0; i < hiddenSize; i += 16 {
		a := archsimd.LoadInt16x16Slice(side[i:])
		c := archsimd.LoadInt16x16Slice(col[i:])
		a.Add(c).StoreSlice(side[i:])
	}
}

// subColumn subtracts a feature column from a perspective accumulator (inverse of addColumn).
func subColumn(side *[hiddenSize]int16, col []int16) {
	for i := 0; i < hiddenSize; i += 16 {
		a := archsimd.LoadInt16x16Slice(side[i:])
		c := archsimd.LoadInt16x16Slice(col[i:])
		a.Sub(c).StoreSlice(side[i:])
	}
}

// outputDot is the raw int64 SCReLU output dot over both perspectives: sum(clamp(a,0,QA)^2 * weight),
// stm weights first then ntm. Per 16 lanes: clamp to [0,QA], widen to int32, square and multiply by
// the (sign-extended) weight — each product fits int32 — then widen to int64 and accumulate.
func outputDot(stm, ntm *[hiddenSize]int16, weights []int16) int64 {
	zero := archsimd.BroadcastInt16x16(0)
	qa := archsimd.BroadcastInt16x16(qa)
	acc := archsimd.BroadcastInt64x4(0)

	sides := [2]*[hiddenSize]int16{stm, ntm}
	for s := 0; s < 2; s++ {
		a := sides[s]
		w := weights[s*hiddenSize:]
		for i := 0; i < hiddenSize; i += 16 {
			x := archsimd.LoadInt16x16Slice(a[i:])
			wv := archsimd.LoadInt16x16Slice(w[i:])
			c := x.Max(zero).Min(qa) // clamp to [0, QA]

			clo := c.GetLo().ExtendToInt32()
			chi := c.GetHi().ExtendToInt32()
			wlo := wv.GetLo().ExtendToInt32()
			whi := wv.GetHi().ExtendToInt32()

			plo := clo.Mul(clo).Mul(wlo) // clamp^2 * weight (fits int32)
			phi := chi.Mul(chi).Mul(whi)

			acc = acc.Add(plo.GetLo().ExtendToInt64())
			acc = acc.Add(plo.GetHi().ExtendToInt64())
			acc = acc.Add(phi.GetLo().ExtendToInt64())
			acc = acc.Add(phi.GetHi().ExtendToInt64())
		}
	}

	var lanes [4]int64
	acc.StoreSlice(lanes[:])
	return lanes[0] + lanes[1] + lanes[2] + lanes[3]
}

// int8Round is the SCReLU rounding addend for the int8 activation (1 << (int8Shift-1)).
const int8Round = 1 << (int8Shift - 1)

// outputDotInt8 is the int8 output dot: sum(screlu_u8(a) * w8) over both perspectives, with w8 the
// int8-range weights (stored int16). Per 16 lanes: clamp to [0,QA], widen to int32, square, add the
// rounding term and shift right by int8Shift to the uint8-range activation ([0,127]), multiply by
// the sign-extended int8 weight (product fits int32), then widen to int64 and accumulate. Bit-identical
// to the scalar fallback in kernels_generic.go.
func outputDotInt8(stm, ntm *[hiddenSize]int16, w8 []int16) int64 {
	zero := archsimd.BroadcastInt16x16(0)
	qa := archsimd.BroadcastInt16x16(qa)
	round := archsimd.BroadcastInt32x8(int8Round)
	// The u8*w8 products (|.| <= 127*127 = 16129) sum to ~33M over the 2048 terms, well within int32, so
	// the whole dot accumulates in int32 (no int64 widening). Each block requantises to the uint8-range
	// activation, packs it to int16, and uses VPMADDWD (DotProductPairs) against the pre-regrouped weights.
	acc := archsimd.BroadcastInt32x8(0)

	sides := [2]*[hiddenSize]int16{stm, ntm}
	for s := 0; s < 2; s++ {
		a := sides[s]
		w := w8[s*hiddenSize:]
		for i := 0; i < hiddenSize; i += 16 {
			x := archsimd.LoadInt16x16Slice(a[i:])
			c := x.Max(zero).Min(qa) // clamp to [0, QA]

			clo := c.GetLo().ExtendToInt32()
			chi := c.GetHi().ExtendToInt32()
			// activation u8 = (c*c + round) >> shift, in [0, 127] — never saturates the int16 pack.
			// Square via VPMADDWD (DotProductPairs) rather than VPMULLD: c ∈ [0, QA] so each sign-extended
			// int32 lane is the int16 pair [v, 0], and madd(x, x) sums v·v + 0·0 = v² per lane. VPMADDWD is
			// ~2× the throughput of VPMULLD on this µarch, and the square is the kernel's hot op. Bit-identical.
			cloI := clo.AsInt16x16()
			chiI := chi.AsInt16x16()
			ulo := cloI.DotProductPairs(cloI).Add(round).ShiftAllRight(int8Shift)
			uhi := chiI.DotProductPairs(chiI).Add(round).ShiftAllRight(int8Shift)
			u := ulo.SaturateToInt16ConcatGrouped(uhi) // pack to int16 (VPACKSSDW group order)

			// The weights are pre-regrouped (regroupWeightsForDot) into that same group order, so
			// DotProductPairs (VPMADDWD) multiplies each activation by its own weight and sums pairs into
			// int32 — an exact partial dot. No saturation anywhere: |u*w| <= 127*127 and a pair sum < 2^15,
			// and the int32 total over all 2048 terms is ~33M. Bit-identical to the scalar Σ u8*w8.
			wv := archsimd.LoadInt16x16Slice(w[i:])
			acc = acc.Add(u.DotProductPairs(wv))
		}
	}

	var lanes [8]int32
	acc.StoreSlice(lanes[:])
	var sum int64
	for _, v := range lanes {
		sum += int64(v)
	}
	return sum
}

// regroupWeightsForDot reorders the int8 output weights, in 16-element blocks, into the lane order that
// SaturateToInt16ConcatGrouped (VPACKSSDW) produces for the activations in outputDotInt8. Within a block
// the two int32x8 halves pack per 128-bit group as [0,1,2,3, 8,9,10,11, 4,5,6,7, 12,13,14,15], so the
// weights are permuted to match — then DotProductPairs pairs each activation with its own weight with no
// per-eval permute. hiddenSize (and the 2*hiddenSize weight array) is a multiple of 16.
func regroupWeightsForDot(w []int16) []int16 {
	group := [16]int{0, 1, 2, 3, 8, 9, 10, 11, 4, 5, 6, 7, 12, 13, 14, 15}
	out := make([]int16, len(w))
	for base := 0; base+16 <= len(w); base += 16 {
		for p := 0; p < 16; p++ {
			out[base+p] = w[base+group[p]]
		}
	}
	for i := len(w) &^ 15; i < len(w); i++ { // defensive tail (none for a 16-multiple length)
		out[i] = w[i]
	}
	return out
}
