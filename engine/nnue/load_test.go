package nnue

// The net loader's refusals, the diagnostic lane diff, the output clamp and the int8 requantisation's
// degenerate cases. The engine package's suite exercises the loader and the evaluators on the embedded net;
// these cover the inputs it never sees: a short, unstamped, foreign or truncated file.

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadRejectsBadNets: every malformed image is refused with an error that says why, and a good image
// with trailing bytes (a trainer's padding) still loads.
func TestLoadRejectsBadNets(t *testing.T) {
	foreign := append([]byte(nil), embeddedNet[:headerBytes]...)
	binary.LittleEndian.PutUint16(foreign[10:12], hiddenSize/2) // a net of another width
	unstamped := append([]byte(nil), embeddedNet[:headerBytes]...)
	copy(unstamped, "NOPE")
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "too small"},
		{"short header", embeddedNet[:headerBytes-1], "too small"},
		{"unstamped", unstamped, "not a stamped Pawnstar net"},
		{"another architecture", foreign, "incompatible net"},
		{"truncated payload", embeddedNet[:headerBytes+payloadBytes-1], "truncated"},
	}
	for _, tc := range cases {
		net, err := Load(tc.data)
		if err == nil || net != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Load = (%v, %v), want an error containing %q", tc.name, net != nil, err, tc.want)
		}
	}
	padded := append(append([]byte(nil), embeddedNet...), 0xAA, 0xBB, 0xCC)
	if net, err := Load(padded); err != nil || net == nil {
		t.Errorf("a net with trailing padding: %v", err)
	}
}

// TestLoadFile: a missing file is an error naming it; the shipped net loads from its file the same as the
// embedded copy.
func TestLoadFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.bin")
	if _, err := LoadFile(missing); err == nil || !strings.Contains(err.Error(), "cannot read net") {
		t.Errorf("LoadFile(missing) error = %v", err)
	}
	net, err := LoadFile("pawnstar-v12.bin")
	if err != nil {
		t.Fatalf("LoadFile(pawnstar-v12.bin): %v", err)
	}
	if want := Embedded(); net.outputBias != want.outputBias || net.outputWScale != want.outputWScale {
		t.Error("the net loaded from its file differs from the embedded one")
	}
}

// TestDiffLanes counts the differing lanes of each perspective independently.
func TestDiffLanes(t *testing.T) {
	var a, b Accumulator
	if w, bl := DiffLanes(&a, &b); w != 0 || bl != 0 {
		t.Errorf("identical accumulators differ in (%d, %d) lanes", w, bl)
	}
	b.white[0], b.white[hiddenSize-1], b.black[7] = 1, -1, 300
	if w, bl := DiffLanes(&a, &b); w != 2 || bl != 1 {
		t.Errorf("DiffLanes = (%d, %d), want (2, 1)", w, bl)
	}
}

// TestDequantClamps: the network's raw output is clamped to +/-maxEval centipawns.
func TestDequantClamps(t *testing.T) {
	const huge = int64(1) << 50
	if got := dequant(huge, 0); got != maxEval {
		t.Errorf("dequant(+huge) = %d, want %d", got, maxEval)
	}
	if got := dequant(-huge, 0); got != -maxEval {
		t.Errorf("dequant(-huge) = %d, want %d", got, -maxEval)
	}
	if got := dequant(0, 0); got != 0 {
		t.Errorf("dequant(0) = %d", got)
	}
}

// TestDeriveInt8OutputWeightsDegenerate: all-zero weights still get a scale of one (never a division by
// zero), and a length that is not a multiple of sixteen keeps its tail in the regrouped layout.
func TestDeriveInt8OutputWeightsDegenerate(t *testing.T) {
	n := &Network{outputWeights: make([]int16, 17)}
	n.deriveInt8OutputWeights()
	if n.outputWScale != 1 {
		t.Errorf("all-zero weights: scale %d, want 1", n.outputWScale)
	}
	n.outputWeights[16] = 381 // scale (381 + 126) / 127 = 3, so 127 after requantising
	n.deriveInt8OutputWeights()
	if n.outputWScale != 3 || n.outputWeightsI8[16] != 127 || n.outputWeightsI8Nat[16] != 127 {
		t.Errorf("scale %d, tail %d / %d; want 3, 127 / 127", n.outputWScale, n.outputWeightsI8[16], n.outputWeightsI8Nat[16])
	}
}
