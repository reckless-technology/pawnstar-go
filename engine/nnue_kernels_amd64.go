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

// nnueInt8Round is the SCReLU rounding addend for the int8 activation (1 << (nnueInt8Shift-1)).
const nnueInt8Round = 1 << (nnueInt8Shift - 1)

// outputDotInt8 is the int8 output dot: sum(screlu_u8(a) * w8) over both perspectives, with w8 the
// int8-range weights (stored int16). Per 16 lanes: clamp to [0,QA], widen to int32, square, add the
// rounding term and shift right by nnueInt8Shift to the uint8-range activation ([0,127]), multiply by
// the sign-extended int8 weight (product fits int32), then widen to int64 and accumulate. Bit-identical
// to the scalar fallback in nnue_kernels_generic.go.
func outputDotInt8(stm, ntm *[nnueHiddenSize]int16, w8 []int16) int64 {
	zero := archsimd.BroadcastInt16x16(0)
	qa := archsimd.BroadcastInt16x16(nnueQA)
	round := archsimd.BroadcastInt32x8(nnueInt8Round)
	// The u8*w8 products (|.| <= 127*127 = 16129) sum to at most ~33M over the 2048 terms, well within
	// int32 — so accumulate in int32 and skip the per-block widening to int64 that the exact (int16) dot
	// needs. The final sum is identical to the int64 accumulation (and to the scalar kernel).
	acc := archsimd.BroadcastInt32x8(0)

	sides := [2]*[nnueHiddenSize]int16{stm, ntm}
	for s := 0; s < 2; s++ {
		a := sides[s]
		w := w8[s*nnueHiddenSize:]
		for i := 0; i < nnueHiddenSize; i += 16 {
			x := archsimd.LoadInt16x16Slice(a[i:])
			wv := archsimd.LoadInt16x16Slice(w[i:])
			c := x.Max(zero).Min(qa) // clamp to [0, QA]

			clo := c.GetLo().ExtendToInt32()
			chi := c.GetHi().ExtendToInt32()
			// activation u8 = (c*c + round) >> shift, in [0, 127]
			ulo := clo.Mul(clo).Add(round).ShiftAllRight(nnueInt8Shift)
			uhi := chi.Mul(chi).Add(round).ShiftAllRight(nnueInt8Shift)

			wlo := wv.GetLo().ExtendToInt32()
			whi := wv.GetHi().ExtendToInt32()

			acc = acc.Add(ulo.Mul(wlo)).Add(uhi.Mul(whi)) // u8 * w8, accumulated in int32
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
