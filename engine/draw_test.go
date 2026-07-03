package engine

import "testing"

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
