package engine

import (
	"strings"
	"testing"
)

// playUCI plays the legal move matching UCI string s on the search state, failing the test if it is
// not legal in the current position.
func playUCI(t *testing.T, ss *searchState, s string) {
	t.Helper()
	m := bookFindMove(ss.currentPosition(), s)
	if m.isNone() {
		t.Fatalf("move %q is not legal in %s", s, ss.currentPosition().toFenString())
	}
	ss.playMove(m)
}

// TestDrawByRepetition pins the three-fold detection over the combined game + search history: the
// knight shuffle Nf3/Nf6/Ng1/Ng8 twice re-reaches the start position for the third time.
func TestDrawByRepetition(t *testing.T) {
	game := NewGame()
	ss := newSearchStateFromGame(game)

	shuffle := []string{"g1f3", "g8f6", "f3g1", "f6g8"}
	for round, n := 0, 0; round < 2; round++ {
		for _, mv := range shuffle {
			n++
			if ss.isDrawByRepetition() {
				t.Fatalf("repetition reported early, after %d plies", n)
			}
			playUCI(t, ss, mv)
		}
	}
	// Start position has now occurred 3 times (plies 0, 4, 8) with the same side to move.
	if !ss.isDrawByRepetition() {
		t.Fatal("three-fold repetition not detected")
	}
}

// TestDrawByRepetitionIrreversibleBoundary pins the clock-0 early stop: a pawn push between the
// repeats resets the fifty-move clock, and the backward scan must never match across it.
func TestDrawByRepetitionIrreversibleBoundary(t *testing.T) {
	game := NewGame()
	ss := newSearchStateFromGame(game)

	// One shuffle cycle, an irreversible pawn move pair, then another shuffle cycle: the knight-cycle
	// positions before and after the pawn moves have different hashes (pawn structure changed), and
	// the scan stops at the clock-0 entries anyway.
	for _, mv := range []string{"g1f3", "g8f6", "f3g1", "f6g8", "e2e4", "e7e5", "g1f3", "g8f6", "f3g1", "f6g8"} {
		if ss.isDrawByRepetition() {
			t.Fatalf("false repetition before %s (position %s)", mv, ss.currentPosition().toFenString())
		}
		playUCI(t, ss, mv)
	}
	// The post-e4/e5 position has occurred only twice (plies 6-equivalent and 10): not a draw yet.
	if ss.isDrawByRepetition() {
		t.Fatal("repetition reported across an irreversible-move boundary after only two occurrences")
	}
}

// TestDrawByFiftyMoves pins the halfmove-clock threshold: a draw at 100 plies, not at 99.
func TestDrawByFiftyMoves(t *testing.T) {
	game := NewGame()
	game.newGameFromFen("8/8/8/8/8/4k3/8/4K3 w - - 99 120")
	ss := newSearchStateFromGame(game)
	if ss.isDrawByFiftyMoves() {
		t.Fatal("fifty-move draw reported at clock 99")
	}
	playUCI(t, ss, "e1d1") // a quiet king move: clock 99 -> 100
	if !ss.isDrawByFiftyMoves() {
		t.Fatal("fifty-move draw not reported at clock 100")
	}
}

// TestDrawByFiftyMovesAcrossCastling pins the fifty-move draw through castling: castling is neither a pawn move
// nor a capture (FIDE Article 9.3), so it takes the clock from 98 to 99 and the reply's castling to 100, a draw.
// Then the search: a queen up at clock 99 with no pawn move or capture to play, castling reaches clock 100
// and must score as the draw, where at clock 0 the same move scores the extra queen.
func TestDrawByFiftyMovesAcrossCastling(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	game.newGameFromFen("r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 98 60")
	ss := newSearchStateFromGame(game)
	playUCI(t, ss, "e1g1")
	if ss.isDrawByFiftyMoves() {
		t.Fatal("fifty-move draw reported at clock 99")
	}
	if got := ss.currentPosition().halfMove; got != 99 {
		t.Fatalf("white's castling left the clock at %d, want 99", got)
	}
	playUCI(t, ss, "e8c8") // O-O-O: the white rook on f1 covers f8
	if !ss.isDrawByFiftyMoves() {
		t.Fatalf("fifty-move draw not reported after castling to clock %d", ss.currentPosition().halfMove)
	}

	castlingScore := func(clock string) int {
		game := NewGame()
		game.newGameFromFen("4k3/8/8/8/8/8/8/3QK2R w K - " + clock + " 80")
		game.timeControl.clockType = clockFixedDepth
		ss := newSearchStateFromGame(game)
		m := bookFindMove(ss.currentPosition(), "e1g1")
		if m.isNone() {
			t.Fatal("e1g1 is not legal")
		}
		var pv variation
		return searchSingleMove(ss, 4, 0, initialAlpha, initialBeta, m, &pv, 0)
	}
	if got := castlingScore("99"); got != drawScore {
		t.Errorf("castling at clock 99 scores %d, want the fifty-move draw (%d)", got, drawScore)
	}
	if got := castlingScore("0"); got < 500 {
		t.Errorf("castling at clock 0 scores %d, want the extra queen (over 500)", got)
	}
}

// TestMateTakesPrecedenceOverFiftyMoves pins FIDE 5.1.1 over the fifty-move draw: a checkmate on the move that
// completes the fifty stands. At clock 99 Ra8 is the hundredth ply and mates, so the search scores it mate in one
// and plays it, where every other move draws. The reverse at the hundredth ply: a check that leaves a legal move
// (with the f7 pawn gone, Ra8+ leaves the king f7) and a quiet move (Ra2) both still draw.
func TestMateTakesPrecedenceOverFiftyMoves(t *testing.T) {
	skipWithoutTheShippedNet(t)
	const mating = "6k1/5ppp/8/8/8/8/8/R5K1 w - - 99 80"
	game := NewGame()
	game.newGameFromFen(mating)
	game.openingBook.clear()
	game.timeControl.clockType = clockFixedDepth
	game.timeControl.depth = 4
	var best move
	out := captureStdout(t, func() { best = searchRootNode(game) })
	if best.String() != "a1a8" || !strings.Contains(out, "score mate 1 ") {
		t.Errorf("clock 99: bestmove %s, want a1a8 scored mate 1; output:\n%s", best.String(), out)
	}

	moveScore := func(fen, uci string) int {
		game := NewGame()
		game.newGameFromFen(fen)
		game.timeControl.clockType = clockFixedDepth
		ss := newSearchStateFromGame(game)
		m := bookFindMove(ss.currentPosition(), uci)
		if m.isNone() {
			t.Fatalf("%s is not legal in %s", uci, fen)
		}
		var pv variation
		return searchSingleMove(ss, 4, 0, initialAlpha, initialBeta, m, &pv, 0)
	}
	if got, want := moveScore(mating, "a1a8"), -(checkmatedScore + 1); got != want {
		t.Errorf("the mate on the 100th ply scores %d, want mate in one (%d)", got, want)
	}
	if got := moveScore("6k1/6pp/8/8/8/8/8/R5K1 w - - 99 80", "a1a8"); got != drawScore {
		t.Errorf("a check on the 100th ply that leaves a legal move scores %d, want the draw (%d)", got, drawScore)
	}
	if got := moveScore(mating, "a1a2"); got != drawScore {
		t.Errorf("a quiet move on the 100th ply scores %d, want the draw (%d)", got, drawScore)
	}
}
