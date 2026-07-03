//go:build arm64

package nnue

import (
	"math/rand"
	"testing"
)

// TestOutputDotInt8Asm checks the hand-written NEON kernel is bit-identical to the scalar int8 dot
// (itself gated bit-identical to the int16 reference by TestNNUEInt8) over random accumulators —
// including values below 0 and above QA so the SQXTUN clamp is exercised on both ends.
func TestOutputDotInt8Asm(t *testing.T) {
	net := Embedded()
	if net == nil {
		t.Fatal("no embedded net")
	}
	rng := rand.New(rand.NewSource(12345))
	for trial := 0; trial < 500; trial++ {
		var stm, ntm [nnueHiddenSize]int16
		for i := range stm {
			stm[i] = int16(rng.Intn(900) - 300) // spans <0, [0,QA], and >QA
			ntm[i] = int16(rng.Intn(900) - 300)
		}
		want := outputDotInt8(&stm, &ntm, net.outputWeightsI8)
		got := outputDotInt8Asm(&stm, &ntm, net.outputWeightsI8)
		if got != want {
			t.Fatalf("trial %d: asm=%d want=%d", trial, got, want)
		}
	}
}
