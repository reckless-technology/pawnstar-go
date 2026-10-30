package engine

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"pawnstar-go/engine/nnue"
)

// TestNnueBoardLayout guards the zero-copy reinterpret in nnueBoard(): position's leading fields must
// stay laid out identically to nnue.Board, or a *position reinterpreted as *nnue.Board would read
// garbage and corrupt every eval. It asserts the field offsets and struct size, then checks the
// reinterpret against the independent explicit builder over the start position, Kiwipete, and every
// bench position — plus every one-ply successor of each, so captures, en passant, castling and
// promotion all appear in a `to` position. A reorder of position's first three fields fails here.
func TestNnueBoardLayout(t *testing.T) {
	var pos position
	var brd nnue.Board
	if got, want := unsafe.Offsetof(pos.pieces), unsafe.Offsetof(brd.Pieces); got != want {
		t.Fatalf("pieces offset %d != nnue.Board.Pieces offset %d", got, want)
	}
	if got, want := unsafe.Offsetof(pos.colors), unsafe.Offsetof(brd.Colors); got != want {
		t.Fatalf("colors offset %d != nnue.Board.Colors offset %d", got, want)
	}
	if got, want := unsafe.Offsetof(pos.squares), unsafe.Offsetof(brd.Squares); got != want {
		t.Fatalf("squares offset %d != nnue.Board.Squares offset %d", got, want)
	}
	if unsafe.Sizeof(pos) < unsafe.Sizeof(brd) {
		t.Fatalf("position (%d bytes) smaller than nnue.Board (%d bytes) — reinterpret would over-read",
			unsafe.Sizeof(pos), unsafe.Sizeof(brd))
	}

	check := func(p *position, label string) {
		t.Helper()
		if got, want := *p.nnueBoard(), p.nnueBoardExplicit(); got != want {
			t.Fatalf("nnueBoard reinterpret != explicit build (%s)", label)
		}
	}
	fens := append([]string{startFen, midgameFen}, benchFens[:]...)
	for _, fen := range fens {
		p := positionFromFen(fen)
		check(&p, fen)
		for _, m := range p.generateLegalMoves() {
			c := p
			c.makeMove(m)
			check(&c, fen+" after "+m.String())
		}
	}
}

// TestNNUEReference cross-checks the scalar int16 EvaluateExact forward pass against an independent
// oracle's evaluations (engine/testdata/nnue_reference.txt, one "<fen> | <expected_cp>" per line).
// Agreement within a couple of centipawns confirms the feature indexing, king-bucket orientation and
// mirroring, the material output head, the SCReLU activation, dequantisation and the net file format
// all match. This is the correctness oracle for the whole NNUE port — the SIMD kernels must reproduce
// it bit-for-bit.
//
// Provenance of the expected values. For the v12 net they were bullet's own, and the tolerance of 2
// below is the float-vs-integer rounding that allowed for (the C++ suite's bound). Release 2.0.0
// shipped pawnstar-v20.bin, for which there is no trainer-side file, so they were recomputed for the
// same 250 positions by a reader and forward pass written in Python from the reckless-nnue
// specification rather than from this package - an independent implementation, not a recording of
// this one. That reader was validated first by reproducing all 250 of bullet's v12 values to 0 cp,
// which is the check the specification requires of any new implementation of the metric; it then
// agrees with this package on the new net to 0 cp as well. The tolerance stays at 2 rather than
// being tightened to 0: it is the C++ bound, and a trainer-produced file would carry the rounding
// again.
func TestNNUEReference(t *testing.T) {
	skipWithoutTheShippedNet(t)
	const tolerance = 2 // integer-quantisation rounding vs bullet; mirrors the C++ test_nnue bound

	net, err := nnue.LoadFile("nnue/pawnstar-v20.bin")
	if err != nil {
		t.Fatalf("load net: %v", err)
	}

	f, err := os.Open("testdata/nnue_reference.txt")
	if err != nil {
		t.Fatalf("open reference: %v", err)
	}
	defer func() { _ = f.Close() }()

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
		got := evaluateExactPosition(net, &pos)
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

// TestNNUEInt8 checks the search's int8 output dot (evaluateInt8) stays within a bounded deviation of the
// exact int16 evaluator (evaluateExact) over the 250 reference positions — mirroring the C++ test's int8
// bound (kInt8Bound = 40). Because evaluateInt8 is deterministic, running this under both the SIMD and the
// scalar build also confirms the two int8 kernels agree (identical deviations => identical int8 values),
// the way TestNNUEReference guards the int16 kernels.
func TestNNUEInt8(t *testing.T) {
	skipWithoutTheShippedNet(t)
	const int8Bound = 40 // matches C++ kInt8Bound; the measured max is far smaller

	net, err := nnue.LoadFile("nnue/pawnstar-v20.bin")
	if err != nil {
		t.Fatalf("load net: %v", err)
	}
	f, err := os.Open("testdata/nnue_reference.txt")
	if err != nil {
		t.Fatalf("open reference: %v", err)
	}
	defer func() { _ = f.Close() }()

	checked, maxDev := 0, 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		bar := strings.LastIndex(line, "|")
		if bar < 0 {
			continue
		}
		fen := strings.TrimSpace(line[:bar])
		pos := positionFromFen(fen)
		b := pos.nnueBoard()
		var acc nnue.Accumulator
		net.Refresh(&acc, b)
		i8 := net.EvaluateInt8(&acc, b, int(pos.colorToMove))
		ref := net.EvaluateExact(&acc, b, int(pos.colorToMove))
		dev := i8 - ref
		if dev < 0 {
			dev = -dev
		}
		if dev > maxDev {
			maxDev = dev
		}
		checked++
		if dev > int8Bound {
			t.Errorf("fen %q: int8 %d cp deviates %d from int16 ref %d (> %d)", fen, i8, dev, ref, int8Bound)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan reference: %v", err)
	}
	if checked != 250 {
		t.Fatalf("checked %d positions, expected 250", checked)
	}
	t.Logf("NNUE int8: %d positions, max |int8-int16| = %d cp (bound %d)", checked, maxDev, int8Bound)
}

// TestNNUEIncremental verifies the lazy search accumulator stays bit-identical to a full refresh at
// every node of a legal-move walk, and after every undo. It exercises two paths: findAccMismatch
// forces currentAccumulator at every node (single-ply catch-ups over all move types), and
// findDeepCatchUpMismatch descends without evaluating and catches up in one multi-ply sweep — the
// deferred path a real search takes when interior nodes never evaluate. If both hold, the fast lazy
// path evaluates exactly as the reference full-refresh path.
func TestNNUEIncremental(t *testing.T) {
	game := NewGame()
	// At this build's shape, whatever it is: the embedded net where that is this shape, and otherwise
	// the synthetic one. The accumulator property under test holds for any weights at all, and this is
	// the broad walk behind the hand-chosen crossings in TestNNUEBoundaryCrossings — skipping it at a
	// new shape would skip it exactly where the new indexing code is.
	game.network = shapeTestNet(t)
	// Positions exercising castling, captures, en passant and promotions (the tricky update cases).
	fens := []string{
		startFen,
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", // Kiwipete: castling + captures
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",                            // en passant, king near bucket edges
		"n1n5/PPPk4/8/8/8/8/4Kppp/5N1N b - - 0 1",                              // promotions (incl. under-promotion)
	}
	depth := 3
	if testing.Short() {
		depth = 2
	}
	for _, fen := range fens {
		game.newGameFromFen(fen)
		ss := newSearchStateFromGame(game)
		if msg := findAccMismatch(game.network, ss, depth); msg != "" {
			t.Errorf("fen %q (per-node): %s", fen, msg)
		}
		ss = newSearchStateFromGame(game)
		if msg := findDeepCatchUpMismatch(game.network, ss, depth); msg != "" {
			t.Errorf("fen %q (deep catch-up): %s", fen, msg)
		}
	}
}

// evaluateExactPosition refreshes a scratch accumulator for pos and returns the exact int16
// reference evaluation (test helper; the search maintains its accumulator incrementally).
func evaluateExactPosition(net *nnue.Network, pos *position) int {
	b := pos.nnueBoard()
	var acc nnue.Accumulator
	net.Refresh(&acc, b)
	return net.EvaluateExact(&acc, b, int(pos.colorToMove))
}

// accsEqual reports whether the maintained (caught-up) accumulator for the current node matches a
// full refresh of the current position.
func accsEqual(net *nnue.Network, ss *searchState) (bool, *nnue.Accumulator, *nnue.Accumulator) {
	fresh := &nnue.Accumulator{}
	b := ss.currentPosition().nnueBoard()
	net.Refresh(fresh, b)
	got := ss.currentAccumulator()
	return *fresh == *got, fresh, got
}

// findAccMismatch walks all legal move sequences to the given depth, forcing a catch-up and comparing
// against a full refresh at every node and after every undo. Returns "" if all match, else a
// description (move path + lane-diff counts) of the first mismatch found.
func findAccMismatch(net *nnue.Network, ss *searchState, depth int) string {
	if ok, fresh, got := accsEqual(net, ss); !ok {
		dw, db := nnue.DiffLanes(fresh, got)
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
		if ok, _, _ := accsEqual(net, ss); !ok {
			return "accumulator not restored after undo " + m.String()
		}
	}
	return ""
}

// findDeepCatchUpMismatch descends the first legal move at each node all the way to the leaf WITHOUT
// ever evaluating, then forces a single multi-ply catch-up at the leaf and compares to a full refresh.
// This exercises the deferred path where several plies are brought current in one sweep.
func findDeepCatchUpMismatch(net *nnue.Network, ss *searchState, depth int) string {
	played := 0
	for d := depth; d > 0; d-- {
		moves := ss.currentPosition().generateLegalMoves()
		if len(moves) == 0 {
			break
		}
		ss.playMove(moves[0]) // no currentAccumulator call here — leaves intermediate depths invalid
		played++
	}
	fresh := &nnue.Accumulator{}
	b := ss.currentPosition().nnueBoard()
	net.Refresh(fresh, b)
	if got := ss.currentAccumulator(); *fresh != *got { // single sweep catches up all `played` plies
		dw, db := nnue.DiffLanes(fresh, got)
		return fmt.Sprintf("deep catch-up over %d plies differs (%d white / %d black lanes)", played, dw, db)
	}
	return ""
}
