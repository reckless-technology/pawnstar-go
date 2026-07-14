package engine

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"pawnstar-go/engine/nnue"
)

// startFen is the FEN string for the standard chess starting position.
const startFen = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

// Game is the top-level engine state.  It owns the transposition table, history
// table, opening book, chess clock, and a stack of positions for copy-make search.
// Create one with NewGame and drive it via HandleCommand.
type Game struct {
	transpositionTable transpositionTable // lockless search-result cache, shared by all Lazy SMP threads
	timeControl        timeControl        // clock configuration for the current search
	evalTable          evalHashTable      // lockless evaluation cache, shared by all Lazy SMP threads
	openingBook        openingBook        // pre-loaded book moves indexed by Zobrist hash
	nnue               *nnue.Network      // NNUE evaluator (the only evaluator); installed by NewGame, swappable via EvalFile
	threadCount        int                // Lazy SMP search threads; 1 = single-threaded (default; tests rely on it)
	moveOverhead       time.Duration      // reserved from every hard deadline for GUI/network lag (UCI Move Overhead)
	useOwnBook         bool               // consult the opening book (UCI OwnBook); false forces a real search
	nodeCount          int                // total nodes searched
	wg                 sync.WaitGroup     // waited on by stopThinking to join the search goroutine
	posStack           []position         // game history + current search path
	isCancelPending    atomic.Bool        // set true to signal all search goroutines to stop
	// isStopRequested means specifically "the GUI asked us to stop" (`stop`/`quit`, or a hard-deadline
	// expiry) — as distinct from isCancelPending, which ALSO means "the main thread is done, helpers wind
	// up" and is therefore set by the search itself at the end of every root search. Conflating the two is
	// what broke pondering: the ponder-wait in searchEntry cannot key off isCancelPending, because a search
	// that finished on its own leaves it set. Only the UCI goroutine ever writes this flag; the search
	// goroutines never do, so it cannot be clobbered by a search that ends concurrently with a `stop`.
	isStopRequested atomic.Bool
	mainSearchState *searchState // main thread's search state, reused across searches (resetForSearch per go)
	// Pondering + the live clock. isPondering suppresses the hard/soft time stops while a `go ponder`
	// search runs. searchStartNanos (soft-budget origin) and hardDeadlineNanos are atomics because
	// `ponderhit` (UCI goroutine) mutates them while the search goroutines read them every node; the
	// budgets are set once by armClock before the search starts, so they are plain. Mirrors the C++
	// Game.is_pondering_ / ChessClock.
	isPondering       atomic.Bool   // true while a `go ponder` search runs (until ponderhit / stop)
	ponderMove        move          // predicted reply (PV[1]); emitted as `bestmove <m> ponder <ponderMove>` (search goroutine only)
	searchStartNanos  atomic.Int64  // unix-nano soft-budget origin; reset by onPonderhit
	hardDeadlineNanos atomic.Int64  // unix-nano hard deadline, or noDeadlineNanos; set by armClock / onPonderhit
	allocatedTime     time.Duration // soft budget for this move; set by armClock before the search
	maxSearchTime     time.Duration // hard budget; re-applied from the ponderhit moment by onPonderhit
}

// NewGame creates an engine instance ready to accept UCI commands. NNUE is the only evaluator, so the
// embedded net is installed at construction; a different net can still be loaded later via EvalFile.
func NewGame() *Game {
	game := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        newOpeningBook(),
		evalTable:          newEvalHashTable(evalHashTableSize),
		nnue:               nnue.Embedded(),
		threadCount:        1, // single-threaded by default; main() / UCI `Threads` raise it for play
		moveOverhead:       30 * time.Millisecond,
		useOwnBook:         true,
	}
	game.hardDeadlineNanos.Store(noDeadlineNanos) // un-armed clock = no hard stop (safe default for tests / bare search)
	game.newGameFromFen(startFen)
	return game
}

// SetThreads sets the Lazy SMP search-thread count (clamped to [1, maxSearchThreads]). Called by
// main() to apply the PAWNSTAR_THREADS / hardware default before the UCI loop starts.
func (game *Game) SetThreads(n int) {
	game.threadCount = min(max(n, 1), maxSearchThreads)
}

// newGameFromFen resets the engine state to the position described by fen.
func (game *Game) newGameFromFen(fen string) {
	game.posStack = make([]position, 0, 512)
	game.posStack = append(game.posStack, positionFromFen(fen))
	game.nodeCount = 0
	game.isCancelPending.Store(false)
	game.timeControl = newTimeControl()
	// Seed the clock origin. A real search re-arms it in armClock (via startThinking), but callers that
	// invoke searchRootNode directly — bench, and the fixed-depth tests — never go through armClock, and a
	// zero origin means "the Unix epoch": elapsedSinceSearchStart then reports decades, which prints garbage
	// `time`/`nps` and (on a clockStandard search) would trip the soft-budget early-exit on the first
	// iteration. Sane default here; harmless when armClock overwrites it.
	game.searchStartNanos.Store(time.Now().UnixNano())
}

// currentPosition returns a pointer to the current (last) position on the stack.
func (game *Game) currentPosition() *position {
	return &game.posStack[len(game.posStack)-1]
}

// playMove appends a copy of the current position to the stack and applies m to it.
func (game *Game) playMove(m move) {
	game.posStack = append(game.posStack, game.posStack[len(game.posStack)-1])
	pos := &game.posStack[len(game.posStack)-1]
	pos.makeMove(m)
}

// playMoveString finds the legal move matching UCI string s, plays it, and returns it.
// Returns moveNone if s does not match any legal move.
func (game *Game) playMoveString(s string) move {
	moves := game.currentPosition().generateLegalMoves()
	for _, m := range moves {
		if m.String() == s {
			game.playMove(m)
			return m
		}
	}
	return moveNone()
}

// startThinking launches a search goroutine for the current position and time
// control.  Any previously running search is stopped first.  The clock is armed here — on the calling
// (UCI) goroutine, before the search goroutine starts — so the budgets are set with a happens-before to
// the search and only ponderhit mutates the live clock afterwards. isPondering must already be set by the
// caller (handleUciGo) so armClock arms no hard deadline for a ponder search.
func (game *Game) startThinking() {
	game.stopThinking()
	game.isStopRequested.Store(false) // fresh search: the previous stop (if any) is resolved
	game.isCancelPending.Store(false)
	game.armClock()
	game.wg.Add(1)
	go func() {
		game.searchEntry()
		game.wg.Done()
	}()
}

// stopThinking signals the search goroutine to halt and blocks until it exits. It sets isStopRequested as
// well as isCancelPending: the latter aborts the search, the former is what releases a ponder-wait (and,
// unlike isCancelPending, is never written by a search goroutine, so a search finishing concurrently
// cannot clobber it).
func (game *Game) stopThinking() {
	game.isStopRequested.Store(true)
	game.isCancelPending.Store(true)
	game.wg.Wait()
	game.isCancelPending.Store(false)
}

// searchEntry is the body of the search goroutine.  It runs the search, plays the best move on the
// internal position, and prints the UCI bestmove response (with `ponder <reply>` when a predicted reply is
// known). UCI forbids sending bestmove while pondering: if a ponder search finishes on its own (max depth
// or a forced line) before the GUI resolves it, wait until ponderhit clears isPondering or stop sets the
// cancel flag. For a normal search isPondering is false, so the wait is a no-op. Mirrors SearchThreadEntry.
func (game *Game) searchEntry() {
	m := searchRootNode(game)
	// Wait for the GUI to resolve the ponder. This keys off isStopRequested, NOT isCancelPending: the root
	// search sets isCancelPending at the end of EVERY search (to wind up the Lazy SMP helpers), so gating on
	// it made this loop dead code and the engine answered while still pondering. ponderhit clears
	// isPondering; stop/quit sets isStopRequested. Both are written only by the UCI goroutine.
	for game.isPondering.Load() && !game.isStopRequested.Load() {
		time.Sleep(time.Millisecond)
	}
	if m.isNone() {
		fmt.Println("bestmove 0000")
		return
	}
	game.playMove(m)
	if !game.ponderMove.isNone() {
		fmt.Println("bestmove " + m.String() + " ponder " + game.ponderMove.String())
	} else {
		fmt.Println("bestmove " + m.String())
	}
}
