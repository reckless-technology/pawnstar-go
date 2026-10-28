package engine

// The NNUE shape gates: the accumulator's incremental update across every boundary a king move can
// cross, at whatever shape this build was compiled for.
//
// Horizontal king mirroring adds a boundary that did not exist in v12's shape and that is SILENT when
// handled wrongly. On the mirrored map a king stepping d1 -> e1 keeps bucket 3 — files 3 and 4 both
// mirror to file 3 — while every feature row that perspective reads moves, because the perspective
// has gained the file flip. Code that decided whether to refresh by comparing bucket indices, which
// is all v12 ever needed, would diff an accumulator whose whole basis had just changed: no panic, no
// bounds error, just a wrong evaluation on about one king move in eight. So nnue.NeedsRefresh
// compares the whole perspective — bank, square flip and colour flip together — and these lines
// cross each boundary on purpose.
//
// What is compared is the ACCUMULATORS, lane by lane, not the evaluations they produce: two different
// accumulators can evaluate to the same number, so comparing evals would let a real divergence
// through. Each line is also walked back as well as forward, and each reports how many plies actually
// forced a refresh — a case that was meant to cross a boundary but stopped crossing one would
// otherwise pass while testing nothing.

import (
	"fmt"
	"testing"

	"pawnstar-go/engine/nnue"
)

// isShippedNetShape reports whether this build is the shape the shipped v12 net was trained for.
// A build at another shape rightly refuses that net, so a gate that is about the shipped net
// specifically — the trainer-reference evaluations, the Bratko-Kopec move set, the golden bench
// signature — has nothing to run against and says so rather than failing.
const isShippedNetShape = nnue.FormatVersion == 1 && nnue.OutputBuckets == 1

// skipWithoutTheShippedNet skips a test whose expected values belong to the shipped v12 net.
func skipWithoutTheShippedNet(t *testing.T) {
	t.Helper()
	if !isShippedNetShape {
		t.Skipf("this build is format_version %d with %d output head(s), so it refuses the shipped v12 net; "+
			"this gate's expected values are that net's. The shape's own gates are TestNNUEBoundaryCrossings "+
			"and the fixed-depth synthetic-net node counts.", nnue.FormatVersion, nnue.OutputBuckets)
	}
}

// shapeTestNet returns a net at THIS build's shape: the embedded v12 net where that is this shape,
// and otherwise the synthetic net, which is built from a fixed seed through fixed integer arithmetic
// and is byte-identical in the C++, Rust and Go ports. Weight quality is irrelevant to what the
// boundary gates check — that an incrementally-updated accumulator equals a from-scratch rebuild
// holds for any weights at all — and without it these gates would be skipped exactly at the shape
// whose new code they exist to check.
func shapeTestNet(t *testing.T) *nnue.Network {
	t.Helper()
	if isShippedNetShape {
		if net := nnue.Embedded(); net != nil {
			return net
		}
		t.Fatal("the embedded net is unusable at the shape it was trained for")
	}
	image, err := nnue.SyntheticNetImage()
	if err != nil {
		t.Fatalf("stamp the synthetic net: %v", err)
	}
	net, err := nnue.Load(image)
	if err != nil {
		t.Fatalf("this build refused its own synthetic net: %v", err)
	}
	return net
}

// boundaryLine is one hand-chosen line of play, named by the boundary it is there to cross.
type boundaryLine struct {
	what  string
	fen   string
	moves []string
}

// boundaryLines are the crossings a king move can make, one line each. The mirror lines only cross
// anything on a mirrored build; on v12's shape they still exercise king moves between file-pairs,
// so they are run at both shapes rather than compiled away.
var boundaryLines = []boundaryLine{
	{
		// The mirror boundary itself, and the case that is silent when the refresh rule is wrong:
		// d1 -> e1 keeps the bank (files 3 and 4 both mirror to file 3) and changes the square flip.
		// Both kings cross it, one per ply, with a full board of pawns to diff alongside.
		what:  "the mirror boundary, d1-e1 and d8-e8, bank unchanged",
		fen:   "3k4/pppppppp/8/8/8/8/PPPPPPPP/3K4 w - - 0 1",
		moves: []string{"d1e1", "d8e8", "e1d1", "e8d8", "d1e1", "d8e8"},
	},
	{
		// The board-half boundary: own rank 4 to rank 5, which changes the bank and leaves the
		// mirroring alone. Both perspectives put that boundary between the fourth and fifth ranks, so
		// the white king crosses it going up and the black king going down. The counterpart to the
		// line above, and the crossing v12's shape already had.
		what:  "the board-half boundary, own rank 4 to rank 5, mirroring unchanged",
		fen:   "8/8/8/7k/3K4/8/8/8 w - - 0 1",
		moves: []string{"d4d5", "h5h4", "d5d4", "h4h5", "d4d5", "h5h4"},
	},
	{
		// Both at once: d5 -> e4 changes the bank AND the mirroring on one ply, so a rule that handled
		// each separately but not together would fail here. The black king crosses the mirror alone on
		// its plies, which is the pairing that shows the two are independent.
		what:  "both boundaries on one ply, d5-e4",
		fen:   "3k4/8/8/3K4/8/8/8/8 w - - 0 1",
		moves: []string{"d5e4", "d8e7", "e4d5", "e7d8", "d5e4", "d8e7"},
	},
	{
		// Castling moves the king two files and a rook at once, so the refresh and the diff have to
		// agree about a four-square delta rather than a two-square one. Kingside crosses the mirror
		// from the far side (e1 -> g1 keeps the flip but changes the bank on the mirrored map),
		// queenside crosses it the other way.
		what:  "castling both ways",
		fen:   "r3k2r/pppppppp/8/8/8/8/PPPPPPPP/R3K2R w KQkq - 0 1",
		moves: []string{"e1g1", "e8c8", "g1h1", "c8b8", "h1g1", "b8c8"},
	},
	{
		// A promotion and an en passant capture, with king moves that cross the mirror on the plies
		// between: a square changing piece type, and a captured pawn standing nowhere near the square
		// the capturing pawn moved to. The promotion is to a knight so that it gives no check and the
		// line can carry on.
		what:  "a promotion, an en passant capture and two mirror crossings",
		fen:   "8/P3k2p/8/6P1/8/8/1p6/4K3 w - - 0 1",
		moves: []string{"a7a8q", "b2b1n", "e1d1", "h7h5", "g5h6", "e7d7"},
	},
	{
		// A king move that is also a capture and crosses the mirror on the same ply: one perspective
		// refreshes from scratch while the other has to diff a piece off the board. Getting the order
		// of those two wrong is invisible to a comparison of evaluations and visible lane by lane.
		what:  "a capture by the king, crossing the mirror on the same ply",
		fen:   "4k3/8/8/8/8/8/8/3Kn3 w - - 0 1",
		moves: []string{"d1e1", "e8d8", "e1d1", "d8e8", "d1e1", "e8d8"},
	},
}

// TestNNUEBoundaryCrossings plays each boundary line forward and back, comparing the incrementally
// updated accumulator with a from-scratch rebuild at every ply, and reports how many plies forced a
// refresh of each perspective.
//
// It is the gate the mirrored shape's refresh rule is proved by, and it was proved by BREAKING it:
// narrowing nnue.NeedsRefresh's comparison to the bank alone fails the first line on ply 1, naming
// the white perspective's unchanged bank and changed flip, while the board-half line still passes —
// which is the right discrimination. A test that passes against a broken implementation is not
// evidence.
func TestNNUEBoundaryCrossings(t *testing.T) {
	net := shapeTestNet(t)
	totalRefreshes := 0
	for _, line := range boundaryLines {
		refreshes, err := walkBoundaryLine(net, line)
		if err != "" {
			t.Errorf("%s (%s): %s", line.what, line.fen, err)
			continue
		}
		totalRefreshes += refreshes
		t.Logf("%-56s %d of %d plies forced a refresh", line.what, refreshes, len(line.moves))
		if nnue.KingMirroringEnabled && refreshes == 0 {
			t.Errorf("%s: no ply forced a refresh, so this line crosses nothing and checks nothing",
				line.what)
		}
	}
	if nnue.KingMirroringEnabled && totalRefreshes == 0 {
		t.Error("not one ply of the whole suite forced a refresh: the lines no longer cross any boundary")
	}
}

// walkBoundaryLine plays moves from fen, comparing the search's maintained accumulator with a full
// refresh at every ply, then walks the line back and compares at every ply on the way. It returns how
// many plies forced a refresh of either perspective, and a description of the first divergence.
func walkBoundaryLine(net *nnue.Network, line boundaryLine) (int, string) {
	game := NewGame()
	game.network = net // installed directly: at a new shape there is no trained net to load from a file
	game.newGameFromFen(line.fen)
	searchState := newSearchStateFromGame(game)

	refreshes, played := 0, make([]move, 0, len(line.moves))
	for ply, name := range line.moves {
		from := *searchState.currentPosition()
		chosen := findLegalMove(searchState.currentPosition(), name)
		if chosen == moveNone() {
			return refreshes, fmt.Sprintf("ply %d: %s is not legal here, so this line tests nothing", ply+1, name)
		}
		searchState.playMove(chosen)
		played = append(played, chosen)
		to := *searchState.currentPosition()

		whiteRefreshed, blackRefreshed := nnue.NeedsRefresh(from.nnueBoard(), to.nnueBoard())
		if whiteRefreshed || blackRefreshed {
			refreshes++
		}
		if diff := accumulatorDivergence(net, searchState); diff != "" {
			return refreshes, fmt.Sprintf("ply %d after %s: %s\n\t\twhite(%s) -> white(%s), refresh %v"+
				"\n\t\tblack(%s) -> black(%s), refresh %v",
				ply+1, name, diff,
				nnue.DescribePerspective(from.nnueBoard(), false), nnue.DescribePerspective(to.nnueBoard(), false), whiteRefreshed,
				nnue.DescribePerspective(from.nnueBoard(), true), nnue.DescribePerspective(to.nnueBoard(), true), blackRefreshed)
		}
	}
	for ply := len(played); ply > 0; ply-- {
		searchState.undoMove()
		if diff := accumulatorDivergence(net, searchState); diff != "" {
			return refreshes, fmt.Sprintf("after undoing ply %d (%s): %s", ply, played[ply-1].String(), diff)
		}
	}
	return refreshes, ""
}

// accumulatorDivergence compares the maintained (caught-up) accumulator for the current node with a
// full refresh of the current position, lane by lane, and describes the first difference it finds.
func accumulatorDivergence(net *nnue.Network, searchState *searchState) string {
	fresh := &nnue.Accumulator{}
	net.Refresh(fresh, searchState.currentPosition().nnueBoard())
	maintained := searchState.currentAccumulator()
	if *fresh == *maintained {
		return ""
	}
	whiteLanes, blackLanes := nnue.DiffLanes(fresh, maintained)
	return fmt.Sprintf("the maintained accumulator differs from a full refresh in %d white and %d black lanes",
		whiteLanes, blackLanes)
}

// findLegalMove returns the legal move written name in pos, or moveNone.
func findLegalMove(pos *position, name string) move {
	for _, candidate := range pos.generateLegalMoves() {
		if candidate.String() == name {
			return candidate
		}
	}
	return moveNone()
}

// TestNNUEOutputHeadsAreAllVisited walks the piece count down from a full board by preferring a
// capture wherever one is legal, so the material output heads are visited by real sequences of
// positions rather than by hand-built boards — and checks at every ply that the maintained
// accumulator still equals a full refresh while the head changes underneath it. The lines are
// GENERATED from fixed seeds rather than written out: a hand-written line that walked the count down
// through all eight heads had an illegal move in it, and a generated one walks the count down on its
// own. At one output head there is nothing to walk through and every position selects head 0, which
// this still asserts.
func TestNNUEOutputHeadsAreAllVisited(t *testing.T) {
	net := shapeTestNet(t)
	visited := make(map[int]bool, nnue.OutputBuckets)
	divisor := (32 + nnue.OutputBuckets - 1) / nnue.OutputBuckets
	lowestCount := 32
	for seed := uint64(1); seed <= 24; seed++ {
		game := NewGame()
		game.network = net // installed directly: at a new shape there is no trained net to load from a file
		game.newGameFromFen(startFen)
		searchState := newSearchStateFromGame(game)
		choices := &syntheticChoices{state: seed * 0x9E3779B97F4A7C15}
		for ply := 0; ply < 300; ply++ {
			pos := searchState.currentPosition()
			pieceCount := pos.occupied().popCount()
			visited[(pieceCount-2)/divisor] = true
			lowestCount = min(lowestCount, pieceCount)
			if diff := accumulatorDivergence(net, searchState); diff != "" {
				t.Fatalf("seed %d ply %d at %d pieces: %s", seed, ply, pieceCount, diff)
			}
			moves := pos.generateLegalMoves()
			if len(moves) == 0 {
				break
			}
			searchState.playMove(chooseCaptureOrRandom(pos, moves, choices))
		}
		if len(visited) == nnue.OutputBuckets {
			break
		}
	}
	if len(visited) != nnue.OutputBuckets {
		t.Errorf("visited %d of %d output heads (%v), the walks bottoming out at %d pieces: they no longer "+
			"reach every head", len(visited), nnue.OutputBuckets, visited, lowestCount)
	}
	t.Logf("visited all %d output head(s), the walks reaching %d pieces", nnue.OutputBuckets, lowestCount)
}

// chooseCaptureOrRandom returns the first capture among moves, or a move chosen by choices when there
// is none — so the piece count walks down of its own accord without the walk ever stalling in a
// shuffle of quiet moves.
func chooseCaptureOrRandom(pos *position, moves []move, choices *syntheticChoices) move {
	for _, candidate := range moves {
		if pos.squares[candidate.to()] != noPiece {
			return candidate
		}
	}
	return moves[choices.next()%uint64(len(moves))]
}

// syntheticChoices is the xorshift64* generator the generated lines are drawn from: written out here
// so a line is fixed by this source alone and a failure is reproducible rather than something that
// happened once.
type syntheticChoices struct {
	state uint64
}

// next advances the state and returns the next value.
func (c *syntheticChoices) next() uint64 {
	c.state ^= c.state >> 12
	c.state ^= c.state << 25
	c.state ^= c.state >> 27
	return c.state * 0x2545F4914F6CDD1D
}
