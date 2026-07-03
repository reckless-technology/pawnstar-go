package engine

// Chess-clock / time-management module: how a search's soft and hard time budgets are computed,
// armed, and (for pondering) re-armed at ponderhit. The live deadline lives in two atomics on Game
// because `ponderhit` (UCI goroutine) mutates it mid-search while the search goroutines read it
// every 2048 nodes. Mirrors the C++ ChessClock plus the game.h budget allocation.

import "time"

// clockType controls how the engine manages its time budget during search.
type clockType uint8

const (
	clockStandard   clockType = iota // wtime/btime/movestogo: allocate a fraction of remaining time.
	clockFixedDepth                  // Search to a fixed depth regardless of elapsed time.
	clockFixedTime                   // Search for a fixed wall-clock duration (movetime).
	clockInfinite                    // Search indefinitely until a stop command is received.
)

// timeControl holds the clock configuration passed to the search goroutine.
type timeControl struct {
	clockType         clockType     // how time is managed (standard, fixed depth, fixed time, infinite)
	timeRemaining     time.Duration // time left on the clock for the side to move
	increment         time.Duration // per-move increment for the side to move (UCI winc/binc); see computeBudget
	numMovesRemaining int           // moves until next time control (0 = none given → horizon formula)
	depth             int           // target depth for clockFixedDepth mode
	maxNodes          int           // node limit (`go nodes`); 0 = none. Checked per thread.
	searchMoves       []string      // if non-empty (`go searchmoves`), restrict the root to these UCI moves
}

// newTimeControl returns a default timeControl (5-minute standard game, no moves-to-go → the armDeadline
// horizon formula). Mirrors the C++ ChessClock defaults (moves_to_go_ = 0).
func newTimeControl() timeControl {
	return timeControl{
		clockType:     clockStandard,
		timeRemaining: 5 * time.Minute,
		depth:         10,
	}
}

// computeBudget returns this move's soft budget (allocated, used by iterativeDeepen for between-iteration
// early exits) and hard budget (maxSearch, reserving moveOverhead; 0 = no hard deadline). Standard clocks
// slice the remaining time by moves-to-go and cap the hard stop at 2× that (never into the last second);
// fixed-time (movetime) is a straight hard budget; fixed-depth and infinite clocks have no deadline.
// fullMoveCount is the current full-move number, used by the moves-to-go horizon when no `movestogo` was
// given. Pure (no side effects) — armClock arms the live atomic clock from these. Mirrors the C++ game.h
// allocation feeding ChessClock::StartSearch.
func (tc *timeControl) computeBudget(moveOverhead time.Duration, fullMoveCount int) (allocated, maxSearch time.Duration) {
	switch tc.clockType {
	case clockStandard:
		movesToGo := tc.numMovesRemaining
		if movesToGo <= 0 {
			// No `movestogo`: assume a decreasing horizon (~40 moves early, floored at 5) so a sudden-death
			// or increment game spends progressively more time per move. Mirrors the C++ allocation. The
			// per-move increment (winc/binc) is parsed but deliberately NOT folded in here — the C++ engine
			// SPRT-tested `+ increment/2` and found it neutral-to-slightly-negative at 8+0.08 and 10+0.1.
			movesToGo = max(40-fullMoveCount, 5)
		}
		allocated = tc.timeRemaining / time.Duration(movesToGo)
		hardStop := max(100*time.Millisecond, min(allocated*2, tc.timeRemaining-time.Second))
		maxSearch = max(time.Millisecond, hardStop-moveOverhead)
	case clockFixedTime:
		maxSearch = max(time.Millisecond, tc.timeRemaining-moveOverhead)
	}
	return allocated, maxSearch
}

// armClock computes this move's budgets and arms the live clock. Called on the UCI goroutine (via
// startThinking) before the search goroutine launches, so the budgets and the search-start origin are set
// with a happens-before to the search; only ponderhit mutates the atomics afterwards. A ponder search arms
// no hard deadline (onPonderhit arms it from the ponderhit moment). Mirrors ChessClock::Start{,Ponder}Search.
func (game *Game) armClock() {
	now := time.Now()
	game.allocatedTime, game.maxSearchTime = game.timeControl.computeBudget(game.moveOverhead, int(game.currentPosition().fullMove))
	game.searchStartNanos.Store(now.UnixNano())
	if game.isPondering.Load() {
		game.hardDeadlineNanos.Store(noDeadlineNanos) // no hard stop until ponderhit
	} else {
		game.armHardDeadline(now, game.maxSearchTime)
	}
}

// armHardDeadline stores the hard deadline as now+maxSearch, or the no-deadline sentinel when maxSearch is
// non-positive (fixed-depth / infinite / ponder). Mirrors ChessClock::ArmHardDeadline.
func (game *Game) armHardDeadline(now time.Time, maxSearch time.Duration) {
	if maxSearch > 0 {
		game.hardDeadlineNanos.Store(now.Add(maxSearch).UnixNano())
	} else {
		game.hardDeadlineNanos.Store(noDeadlineNanos)
	}
}

// elapsedSinceSearchStart returns the time since the soft-budget origin (which onPonderhit resets to the
// ponderhit moment). Read by the main search thread for info/nps and soft-time management. Mirrors
// ChessClock::ElapsedSinceSearchStart.
func (game *Game) elapsedSinceSearchStart() time.Duration {
	return time.Duration(time.Now().UnixNano() - game.searchStartNanos.Load())
}

// onPonderhit converts a running ponder search into a normal timed one: reset the soft-budget origin and
// arm the hard deadline from this moment (budget-from-ponderhit), then clear the pondering flag last so the
// search never observes an armed deadline while still flagged pondering. Mirrors ChessClock::OnPonderhit +
// the handle_ponderhit ordering. Called on the UCI goroutine while the search runs; the atomics make it safe.
func (game *Game) onPonderhit() {
	now := time.Now()
	game.searchStartNanos.Store(now.UnixNano())
	game.armHardDeadline(now, game.maxSearchTime)
	game.isPondering.Store(false)
}

// noDeadlineNanos is the hardDeadlineNanos sentinel meaning "no hard stop": time.Now().UnixNano() (year
// ~2025, ~1.7e18) never reaches it, so the periodic time check never fires. Mirrors the C++ kNoDeadline.
const noDeadlineNanos int64 = 1<<63 - 1
