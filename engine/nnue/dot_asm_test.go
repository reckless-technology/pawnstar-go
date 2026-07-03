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
	net := Embedded()
	if net == nil {
		t.Fatal("no embedded net")
	}
	rng := rand.New(rand.NewSource(12345))
	for trial := 0; trial < 500; trial++ {
		var stm, ntm [hiddenSize]int16
		for i := range stm {
			stm[i] = int16(rng.Intn(900) - 300) // spans <0, [0,QA], and >QA
			ntm[i] = int16(rng.Intn(900) - 300)
		}
		want := outputDotInt8(&stm, &ntm, net.outputWeightsI8)
		got := outputDotInt8Asm(&stm, &ntm, net.outputWeightsI8Nat)
		if got != want {
			t.Fatalf("trial %d: asm=%d want=%d", trial, got, want)
		}
	}
}
