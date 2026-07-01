//go:build amd64 && goexperiment.simd

package engine

import "simd/archsimd"

// SIMD NNUE kernels using 256-bit AVX2-width archsimd vectors. Bit-identical to the scalar fallback
// in nnue_kernels_generic.go: the int16 column add/sub wrap identically, and the output dot uses the
// same clamp^2*weight products (which fit int32) accumulated in int64. Requires GOEXPERIMENT=simd.
//
// nnueHiddenSize (1024) is a multiple of 16, so every kernel loops in whole 16-lane int16 steps.

// addColumn adds a feature column into a perspective accumulator (16 int16 lanes per step).
func addColumn(side *[nnueHiddenSize]int16, col []int16) {
	for i := 0; i < nnueHiddenSize; i += 16 {
		a := archsimd.LoadInt16x16Slice(side[i:])
		c := archsimd.LoadInt16x16Slice(col[i:])
		a.Add(c).StoreSlice(side[i:])
	}
}

// subColumn subtracts a feature column from a perspective accumulator (inverse of addColumn).
func subColumn(side *[nnueHiddenSize]int16, col []int16) {
	for i := 0; i < nnueHiddenSize; i += 16 {
		a := archsimd.LoadInt16x16Slice(side[i:])
		c := archsimd.LoadInt16x16Slice(col[i:])
		a.Sub(c).StoreSlice(side[i:])
	}
}

// outputDot is the raw int64 SCReLU output dot over both perspectives: sum(clamp(a,0,QA)^2 * weight),
// stm weights first then ntm. Per 16 lanes: clamp to [0,QA], widen to int32, square and multiply by
// the (sign-extended) weight — each product fits int32 — then widen to int64 and accumulate.
func outputDot(stm, ntm *[nnueHiddenSize]int16, weights []int16) int64 {
	zero := archsimd.BroadcastInt16x16(0)
	qa := archsimd.BroadcastInt16x16(nnueQA)
	acc := archsimd.BroadcastInt64x4(0)

	sides := [2]*[nnueHiddenSize]int16{stm, ntm}
	for s := 0; s < 2; s++ {
		a := sides[s]
		w := weights[s*nnueHiddenSize:]
		for i := 0; i < nnueHiddenSize; i += 16 {
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
