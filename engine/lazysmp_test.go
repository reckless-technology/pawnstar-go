package engine

import "testing"

// TestLazySMPSmoke runs genuine multi-threaded (Lazy SMP) searches and checks each returns a legal
// move without crashing or deadlocking. Run under -race it also proves the concurrent search is
// data-race free: the transposition table and eval cache are lockless (atomic), and every other piece
// of search state (history, killers, continuation history, countermoves, accumulator, diagnostics) is
// per-thread. The single-threaded tests elsewhere cover determinism; this one covers concurrency.
func TestLazySMPSmoke(t *testing.T) {
	fens := []string{
		startFen,
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", // Kiwipete
		"r1bqkbnr/pppp1ppp/2n5/4p3/4P3/5N2/PPPP1PPP/RNBQKB1R w KQkq - 2 3",     // open game
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",                            // endgame
	}
	depth := 8
	if testing.Short() {
		depth = 6
	}
	for _, threads := range []int{2, 4} {
		for _, fen := range fens {
			game := NewGame()
			if err := game.EnableNNUE(); err != nil {
				t.Fatalf("enable NNUE: %v", err)
			}
			game.SetThreads(threads)
			game.newGameFromFen(fen)
			game.openingBook.clearOpeningBook()
			game.timeControl.clockType = clockFixedDepth
			game.timeControl.depth = depth

			m := searchRootNode(game)

			legal := false
			for _, lm := range game.currentPosition().generateLegalMoves() {
				if lm.equal(m) {
					legal = true
					break
				}
			}
			if !legal {
				t.Errorf("threads=%d fen=%q: searchRootNode returned illegal move %s", threads, fen, m)
			}
		}
	}
}
