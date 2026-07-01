package engine

import (
	"testing"
	"time"
)

// TestTimeControlDefaults ports the C++ chess-clock Reset check: newTimeControl restores the standard
// clock, 5-minute remaining time, no moves-to-go (0 → the armDeadline horizon formula, matching C++
// moves_to_go_ = 0), and depth 10.
func TestTimeControlDefaults(t *testing.T) {
	tc := newTimeControl()
	if tc.clockType != clockStandard {
		t.Errorf("clockType = %v, want clockStandard", tc.clockType)
	}
	if tc.timeRemaining != 5*time.Minute {
		t.Errorf("timeRemaining = %v, want 5m", tc.timeRemaining)
	}
	if tc.numMovesRemaining != 0 {
		t.Errorf("numMovesRemaining = %d, want 0", tc.numMovesRemaining)
	}
	if tc.depth != 10 {
		t.Errorf("depth = %d, want 10", tc.depth)
	}
}

// TestComputeBudget ports the C++ chess-clock allocation (the non-ponder parts): computeBudget returns the
// soft budget (allocated) and hard budget (maxSearch, overhead reserved; 0 = no deadline), applies the
// moves-to-go horizon when no movestogo is given, and the 1ms floor. Pure — no clock reads, deterministic.
func TestComputeBudget(t *testing.T) {
	// Standard clock: allocated = remaining / moves-to-go; maxSearch = min(2×allocated, remaining−1s) − overhead.
	{
		tc := timeControl{clockType: clockStandard, timeRemaining: 40 * time.Second, numMovesRemaining: 40}
		allocated, maxSearch := tc.computeBudget(0, 0)
		if allocated != time.Second { // 40s / 40
			t.Errorf("allocated = %v, want 1s", allocated)
		}
		if maxSearch != 2*time.Second { // min(2×1s, 39s) = 2s
			t.Errorf("maxSearch = %v, want 2s", maxSearch)
		}
	}

	// No movestogo: the horizon formula max(40−fullMove, 5) sets moves-to-go. Early game divides by ~40;
	// late game floors at 5, spending more per move.
	{
		tc := timeControl{clockType: clockStandard, timeRemaining: 40 * time.Second} // numMovesRemaining 0
		if a, _ := tc.computeBudget(0, 0); a != time.Second {                         // 40s / max(40−0,5)=40
			t.Errorf("horizon allocated (move 0) = %v, want 1s", a)
		}
		if a, _ := tc.computeBudget(0, 38); a != 8*time.Second { // 40s / max(40−38,5)=5
			t.Errorf("horizon allocated (move 38) = %v, want 8s", a)
		}
	}

	// The move overhead is reserved from the hard budget.
	{
		tc := timeControl{clockType: clockStandard, timeRemaining: 40 * time.Second, numMovesRemaining: 40}
		if _, maxSearch := tc.computeBudget(200*time.Millisecond, 0); maxSearch != 2*time.Second-200*time.Millisecond {
			t.Errorf("maxSearch with overhead = %v, want 1.8s", maxSearch)
		}
	}

	// Fixed-time (movetime) is a straight hard budget less the overhead, no soft budget.
	{
		tc := timeControl{clockType: clockFixedTime, timeRemaining: 500 * time.Millisecond}
		allocated, maxSearch := tc.computeBudget(50*time.Millisecond, 0)
		if allocated != 0 {
			t.Errorf("fixed-time allocated = %v, want 0", allocated)
		}
		if maxSearch != 450*time.Millisecond {
			t.Errorf("fixed-time maxSearch = %v, want 450ms", maxSearch)
		}
	}

	// The 1ms floor: a movetime shorter than the overhead still leaves at least 1ms.
	{
		tc := timeControl{clockType: clockFixedTime, timeRemaining: 10 * time.Millisecond}
		if _, maxSearch := tc.computeBudget(100*time.Millisecond, 0); maxSearch != time.Millisecond {
			t.Errorf("floored maxSearch = %v, want 1ms", maxSearch)
		}
	}

	// Fixed-depth and infinite clocks have no budget at all (no deadline).
	for _, ct := range []clockType{clockFixedDepth, clockInfinite} {
		tc := timeControl{clockType: ct}
		if a, m := tc.computeBudget(30*time.Millisecond, 0); a != 0 || m != 0 {
			t.Errorf("clock %v budget = (%v, %v), want (0, 0)", ct, a, m)
		}
	}
}

// TestPonderRace exercises the pondering state machine and its concurrency — the reason pondering was
// deferred: `ponderhit` (this goroutine) mutates the live clock while the search goroutines read it. Run
// under -race it proves that path is data-race free (searchStartNanos / hardDeadlineNanos are atomics). It
// also checks the two behaviours: a ponder search does not finish until ponderhit/stop (the bestmove is
// deferred by the ponder-wait), and ponderhit converts it into a timed search that actually terminates.
func TestPonderRace(t *testing.T) {
	if testing.Short() {
		t.Skip("ponder timing test skipped under -short")
	}
	const kiwipete = "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1" // rich, not in book

	// waitDone reports whether the search goroutine has finished within d (wg.Wait with a timeout).
	waitDone := func(g *Game, d time.Duration) bool {
		done := make(chan struct{})
		go func() { g.wg.Wait(); close(done) }()
		select {
		case <-done:
			return true
		case <-time.After(d):
			return false
		}
	}

	// ponder -> ponderhit: the search must defer completion until ponderhit, then run a bounded timed
	// search (wtime 2000 -> ~100ms hard budget) and terminate. Two threads exercise Lazy SMP under ponder.
	{
		game := NewGame()
		game.useOwnBook = false
		game.moveOverhead = 0
		game.SetThreads(2)
		game.newGameFromFen(kiwipete)
		game.HandleCommand("go wtime 2000 btime 2000 ponder")
		time.Sleep(80 * time.Millisecond)
		if waitDone(game, 60*time.Millisecond) { // must still be pondering (bestmove deferred)
			t.Error("ponder search completed before ponderhit/stop (bestmove not deferred)")
		}
		game.HandleCommand("ponderhit")
		if !waitDone(game, 3*time.Second) { // must convert to a timed search and stop
			game.stopThinking()
			t.Error("search did not terminate after ponderhit (hard deadline not armed?)")
		}
	}

	// ponder -> stop: `stop` must break out of the ponder-wait and join cleanly.
	{
		game := NewGame()
		game.useOwnBook = false
		game.SetThreads(2)
		game.newGameFromFen(kiwipete)
		game.HandleCommand("go wtime 2000 btime 2000 ponder")
		time.Sleep(50 * time.Millisecond)
		game.HandleCommand("stop") // stopThinking joins the search goroutine; returns only once it has exited
		if !waitDone(game, time.Second) {
			t.Error("search did not terminate after stop during ponder")
		}
	}
}

// TestFixedTimeDeadlineFires is the integration counterpart: a movetime search must actually run to
// roughly its budget — the deadline must not fire early, nor let the search overrun by a wide margin.
// Bounds are deliberately generous (the C++ clock test does the same) so it stays reliable under load.
func TestFixedTimeDeadlineFires(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test skipped under -short")
	}
	game := NewGame()
	if err := game.EnableNNUE(); err != nil {
		t.Fatalf("enable NNUE: %v", err)
	}
	game.newGameFromFen("r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1")
	game.openingBook.clearOpeningBook() // else a book move returns instantly, before any search
	game.timeControl.clockType = clockFixedTime
	game.timeControl.timeRemaining = 150 * time.Millisecond
	game.moveOverhead = 0

	start := time.Now()
	game.startThinking()
	game.wg.Wait()
	elapsed := time.Since(start)

	if elapsed < 80*time.Millisecond {
		t.Errorf("search stopped after %v; the deadline should not fire before the ~150ms budget", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("search ran %v; it overran the 150ms budget by far too much", elapsed)
	}
}
