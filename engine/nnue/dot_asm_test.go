//go:build amd64 && goexperiment.simd

package nnue

import (
	"math/rand"
	"testing"
)

// TestOutputDotInt8Asm checks the hand-written AVX2 kernel is bit-identical to the archsimd int16 dot
// (itself gated bit-identical to scalar) over random accumulators — including values below 0 and above QA
// so the SCReLU clamp is exercised on both ends.
func TestOutputDotInt8Asm(t *testing.T) {
	net := shapeNet(t)
	rng := rand.New(rand.NewSource(12345))
	for trial := 0; trial < 500; trial++ {
		var stm, ntm [hiddenSize]int16
		for i := range stm {
			stm[i] = int16(rng.Intn(900) - 300) // spans <0, [0,QA], and >QA
			ntm[i] = int16(rng.Intn(900) - 300)
		}
		// Every output head, so the head offset the dispatch applies is gated too: a head is a
		// contiguous 2*hiddenSize run in each weight representation, and picking the wrong run would
		// show here as a kernel disagreement rather than only as a wrong evaluation in a search.
		for bucket := 0; bucket < outputBuckets; bucket++ {
			start := bucket * 2 * hiddenSize
			want := outputDotInt8(&stm, &ntm, net.outputWeightsI8[start:start+2*hiddenSize])
			got := net.int8OutputDot(&stm, &ntm, bucket)
			if got != want {
				t.Fatalf("trial %d head %d: asm=%d want=%d", trial, bucket, got, want)
			}
		}
	}
}
