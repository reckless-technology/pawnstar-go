package engine

import (
	"testing"
	"time"
)

// TestTimeControlDefaults ports the C++ chess-clock Reset check: newTimeControl restores the standard
// clock, 5-minute remaining time, 40 moves-to-go, and depth 10.
func TestTimeControlDefaults(t *testing.T) {
	tc := newTimeControl()
	if tc.clockType != clockStandard {
		t.Errorf("clockType = %v, want clockStandard", tc.clockType)
	}
	if tc.timeRemaining != 5*time.Minute {
		t.Errorf("timeRemaining = %v, want 5m", tc.timeRemaining)
	}
	if tc.numMovesRemaining != 40 {
		t.Errorf("numMovesRemaining = %d, want 40", tc.numMovesRemaining)
	}
	if tc.depth != 10 {
		t.Errorf("depth = %d, want 10", tc.depth)
	}
}

// TestArmDeadline ports the C++ chess-clock deadline logic (the non-ponder parts): armDeadline sets a
// hard stop that fires only after the budget elapses, reserves the move overhead, applies the 1ms floor,
// and leaves fixed-depth / infinite clocks with no deadline. Deterministic — no real sleeps.
func TestArmDeadline(t *testing.T) {
	now := time.Now()

	// Standard clock: soft budget = remaining / moves-to-go; hard stop = min(2×soft, remaining−1s).
	{
		tc := timeControl{clockType: clockStandard, timeRemaining: 40 * time.Second, numMovesRemaining: 40}
		allocated := tc.armDeadline(now, 0)
		if allocated != time.Second { // 40s / 40
			t.Errorf("allocated = %v, want 1s", allocated)
		}
		if got := tc.stopTime.Sub(now); got != 2*time.Second { // min(2×1s, 39s) = 2s
			t.Errorf("standard deadline = now+%v, want now+2s", got)
		}
		if !now.Before(tc.stopTime) {
			t.Error("deadline must be in the future right after arming (not reached immediately)")
		}
	}

	// The move overhead is reserved from the hard deadline.
	{
		tc := timeControl{clockType: clockStandard, timeRemaining: 40 * time.Second, numMovesRemaining: 40}
		tc.armDeadline(now, 200*time.Millisecond)
		if got := tc.stopTime.Sub(now); got != 2*time.Second-200*time.Millisecond {
			t.Errorf("deadline with overhead = now+%v, want now+1.8s", got)
		}
	}

	// Fixed-time (movetime) is a straight hard budget less the overhead, no soft budget.
	{
		tc := timeControl{clockType: clockFixedTime, timeRemaining: 500 * time.Millisecond}
		if a := tc.armDeadline(now, 50*time.Millisecond); a != 0 {
			t.Errorf("fixed-time allocated = %v, want 0", a)
		}
		if got := tc.stopTime.Sub(now); got != 450*time.Millisecond {
			t.Errorf("fixed-time deadline = now+%v, want now+450ms", got)
		}
	}

	// The 1ms floor: a movetime shorter than the overhead still leaves at least 1ms.
	{
		tc := timeControl{clockType: clockFixedTime, timeRemaining: 10 * time.Millisecond}
		tc.armDeadline(now, 100*time.Millisecond)
		if got := tc.stopTime.Sub(now); got != time.Millisecond {
			t.Errorf("floored deadline = now+%v, want now+1ms", got)
		}
	}

	// Fixed-depth and infinite clocks arm no deadline (the periodic time check ignores them).
	for _, ct := range []clockType{clockFixedDepth, clockInfinite} {
		tc := timeControl{clockType: ct}
		if a := tc.armDeadline(now, 30*time.Millisecond); a != 0 {
			t.Errorf("clock %v allocated = %v, want 0", ct, a)
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
