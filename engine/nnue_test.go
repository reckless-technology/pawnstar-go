package engine

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestNNUEReference cross-checks the scalar int16 EvaluateExact forward pass against the trainer's
// reference evaluations (engine/testdata/nnue_reference.txt, one "<fen> | <expected_cp>" per line,
// produced by bullet). Agreement within a couple of centipawns confirms the feature indexing,
// king-bucket orientation, SCReLU activation, dequantisation and net file format all match. This is
// the correctness oracle for the whole NNUE port — the SIMD kernels must reproduce it bit-for-bit.
func TestNNUEReference(t *testing.T) {
	const tolerance = 2 // integer-quantisation rounding vs bullet; mirrors the C++ test_nnue bound

	net, err := loadNNUENetworkFile("nnue/pawnstar-v12.bin")
	if err != nil {
		t.Fatalf("load net: %v", err)
	}

	f, err := os.Open("testdata/nnue_reference.txt")
	if err != nil {
		t.Fatalf("open reference: %v", err)
	}
	defer f.Close()

	checked, maxDiff := 0, 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		bar := strings.LastIndex(line, "|")
		if bar < 0 {
			continue
		}
		fen := strings.TrimSpace(line[:bar])
		expected, err := strconv.Atoi(strings.TrimSpace(line[bar+1:]))
		if err != nil {
			t.Fatalf("bad expected value in line %q: %v", line, err)
		}

		pos := positionFromFen(fen)
		got := net.evaluateExactPosition(&pos)
		diff := got - expected
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
		checked++
		if diff > tolerance {
			t.Errorf("fen %q: got %d cp, expected %d cp (diff %d)", fen, got, expected, diff)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan reference: %v", err)
	}
	if checked != 250 {
		t.Fatalf("checked %d positions, expected 250", checked)
	}
	t.Logf("NNUE reference: %d positions, max |diff| = %d cp (tolerance %d)", checked, maxDiff, tolerance)
}

// TestNNUEIncremental verifies the incrementally-maintained search accumulator (updated across
// playMove/undoMove) stays bit-identical to a full refresh at every node of a legal-move walk, and
// is fully restored after each undo. This is the gate for the incremental accumulator: if it holds,
// the fast incremental path evaluates exactly as the reference full-refresh path.
func TestNNUEIncremental(t *testing.T) {
	game := NewGame()
	if err := game.EnableNNUE(); err != nil {
		t.Fatalf("enable NNUE: %v", err)
	}
	// Positions exercising castling, captures, en passant and promotions (the tricky update cases).
	fens := []string{
		startFen,
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", // Kiwipete: castling + captures
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",                            // en passant, king near bucket edges
		"n1n5/PPPk4/8/8/8/8/4Kppp/5N1N b - - 0 1",                             // promotions (incl. under-promotion)
	}
	depth := 3
	if testing.Short() {
		depth = 2
	}
	for _, fen := range fens {
		game.newGameFromFen(fen)
		ss := newSearchStateFromGame(game)
		if msg := findAccMismatch(game.nnue, ss, depth); msg != "" {
			t.Errorf("fen %q: %s", fen, msg)
		}
	}
}

// findAccMismatch walks all legal move sequences to the given depth, comparing the maintained
// accumulator with a full refresh at every node and after every undo. Returns "" if all match, else
// a description (move path + lane-diff counts) of the first mismatch found.
func findAccMismatch(net *nnueNetwork, ss *searchState, depth int) string {
	var fresh nnueAccumulator
	net.refresh(&fresh, ss.currentPosition())
	if fresh != ss.acc {
		dw, db := accDiffCount(&fresh, &ss.acc)
		return fmt.Sprintf("depth %d: accumulator differs (%d white / %d black lanes)", depth, dw, db)
	}
	if depth == 0 {
		return ""
	}
	for _, m := range ss.currentPosition().generateLegalMoves() {
		ss.playMove(m)
		if msg := findAccMismatch(net, ss, depth-1); msg != "" {
			return m.String() + " > " + msg
		}
		ss.undoMove()
		net.refresh(&fresh, ss.currentPosition())
		if fresh != ss.acc {
			return "accumulator not restored after undo " + m.String()
		}
	}
	return ""
}

// accDiffCount returns how many white-perspective and black-perspective lanes differ.
func accDiffCount(a, b *nnueAccumulator) (int, int) {
	dw, db := 0, 0
	for i := 0; i < nnueHiddenSize; i++ {
		if a.white[i] != b.white[i] {
			dw++
		}
		if a.black[i] != b.black[i] {
			db++
		}
	}
	return dw, db
}
