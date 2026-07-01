package engine

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// startFen is the FEN string for the standard chess starting position.
const startFen = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

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

// Game is the top-level engine state.  It owns the transposition table, history
// table, opening book, chess clock, and a stack of positions for copy-make search.
// Create one with NewGame and drive it via HandleCommand.
type Game struct {
	transpositionTable transpositionTable // lockless search-result cache, shared by all Lazy SMP threads
	timeControl        timeControl        // clock configuration for the current search
	evalTable          evalHashTable      // lockless evaluation cache, shared by all Lazy SMP threads
	openingBook        openingBook        // pre-loaded book moves indexed by Zobrist hash
	nnue               *nnueNetwork       // NNUE evaluator (the only evaluator); installed by NewGame, swappable via EvalFile
	threadCount        int                // Lazy SMP search threads; 1 = single-threaded (default; tests rely on it)
	moveOverhead       time.Duration      // reserved from every hard deadline for GUI/network lag (UCI Move Overhead)
	useOwnBook         bool               // consult the opening book (UCI OwnBook); false forces a real search
	nodeCount          int                // total nodes searched
	wg                 sync.WaitGroup     // waited on by stopThinking to join the search goroutine
	posStack           []position         // game history + current search path
	isCancelPending    atomic.Bool        // set true to signal all search goroutines to stop
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

// noDeadlineNanos is the hardDeadlineNanos sentinel meaning "no hard stop": time.Now().UnixNano() (year
// ~2025, ~1.7e18) never reaches it, so the periodic time check never fires. Mirrors the C++ kNoDeadline.
const noDeadlineNanos int64 = 1<<63 - 1

// NewGame creates an engine instance ready to accept UCI commands. NNUE is the only evaluator, so the
// embedded net is installed at construction; a different net can still be loaded later via EvalFile.
func NewGame() *Game {
	game := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        newOpeningBook(),
		evalTable:          newEvalHashTable(evalHashTableSize),
		nnue:               loadEmbeddedNet(),
		threadCount:        1, // single-threaded by default; main() / UCI `Threads` raise it for play
		moveOverhead:       30 * time.Millisecond,
		useOwnBook:         true,
	}
	game.hardDeadlineNanos.Store(noDeadlineNanos) // un-armed clock = no hard stop (safe default for tests / bare search)
	game.newGameFromFen(startFen)
	return game
}

// HandleCommand processes a single UCI command line.
// Returns true if the engine should quit.
// This is the public interface to the game engine.
func (game *Game) HandleCommand(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	fields := strings.Fields(line)
	switch fields[0] {
	case "uci":
		fmt.Println("id name Pawnstar-Go")
		fmt.Println("id author Jonny Reckless")
		fmt.Println("option name OwnBook type check default true")
		fmt.Println("option name EvalFile type string default <embedded>")
		fmt.Printf("option name Threads type spin default %d min 1 max %d\n", game.threadCount, maxSearchThreads)
		fmt.Printf("option name Hash type spin default %d min 1 max 4096\n", hashTableMB)
		fmt.Println("option name Clear Hash type button")
		fmt.Printf("option name Move Overhead type spin default %d min 0 max 5000\n", game.moveOverhead.Milliseconds())
		fmt.Println("option name Ponder type check default false")
		fmt.Println("uciok")
	case "isready":
		fmt.Println("readyok")
	case "setoption":
		game.handleUciSetOption(fields[1:])
	case "ucinewgame":
		game.stopThinking()
		game.newGameFromFen(startFen)
	case "position":
		game.handleUciPosition(fields[1:])
	case "go":
		game.handleUciGo(fields[1:])
	case "ponderhit":
		// The predicted move was played: convert the running ponder search into a timed search
		// (budget starts from now). No-op if no ponder search is running (the next `go` re-arms).
		game.onPonderhit()
	case "bench":
		game.stopThinking()
		game.runBench()
	case "stop":
		game.stopThinking()
	case "quit":
		game.stopThinking()
		return true
	}
	return false
}

// SetThreads sets the Lazy SMP search-thread count (clamped to [1, maxSearchThreads]). Called by
// main() to apply the PAWNSTAR_THREADS / hardware default before the UCI loop starts.
func (game *Game) SetThreads(n int) {
	game.threadCount = min(max(n, 1), maxSearchThreads)
}

// handleUciSetOption processes "setoption name <Name> [value <Value>]". Supported options:
//   - EvalFile (string): a path to a PSN1 net to load; "<embedded>", "default" or empty reload the
//     embedded net. Names and values may contain spaces, so both are parsed span-wise.
func (game *Game) handleUciSetOption(fields []string) {
	name, value := parseSetOption(fields)
	switch strings.ToLower(name) {
	case "evalfile":
		if value == "" || value == "<embedded>" || strings.EqualFold(value, "default") {
			if err := game.EnableNNUE(); err != nil {
				fmt.Printf("info string %v\n", err)
			}
		} else if err := game.LoadNNUEFile(value); err != nil {
			fmt.Printf("info string EvalFile %q: %v\n", value, err)
		} else {
			fmt.Printf("info string NNUE net loaded from %s\n", value)
		}
	case "threads":
		if n, err := strconv.Atoi(value); err == nil {
			game.threadCount = min(max(n, 1), maxSearchThreads)
			fmt.Printf("info string Threads set to %d\n", game.threadCount)
		}
	case "hash":
		if n, err := strconv.Atoi(value); err == nil {
			game.transpositionTable = newTranspositionTable(min(max(n, 1), 4096))
			fmt.Printf("info string Hash set to %d MB\n", min(max(n, 1), 4096))
		}
	case "clear hash":
		game.transpositionTable.clear()
		game.evalTable.clear()
	case "move overhead":
		if n, err := strconv.Atoi(value); err == nil {
			game.moveOverhead = time.Duration(min(max(n, 0), 5000)) * time.Millisecond
		}
	case "ownbook":
		game.useOwnBook = !strings.EqualFold(value, "false")
	}
}

// parseSetOption splits a "name <Name...> [value <Value...>]" field list into the option name and its
// value. Everything between "name" and "value" is the (possibly multi-word) name; everything after
// "value" is the (possibly multi-word) value. Missing "value" yields an empty value.
func parseSetOption(fields []string) (name, value string) {
	i := 0
	for i < len(fields) && fields[i] != "name" {
		i++
	}
	if i < len(fields) {
		i++ // skip "name"
	}
	start := i
	for i < len(fields) && fields[i] != "value" {
		i++
	}
	name = strings.Join(fields[start:i], " ")
	if i < len(fields) { // fields[i] == "value"
		value = strings.Join(fields[i+1:], " ")
	}
	return name, value
}

// newGameFromFen resets the engine state to the position described by fen.
func (game *Game) newGameFromFen(fen string) {
	game.posStack = make([]position, 0, 512)
	game.posStack = append(game.posStack, positionFromFen(fen))
	game.nodeCount = 0
	game.isCancelPending.Store(false)
	game.timeControl = newTimeControl()
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
	for _, move := range moves {
		if move.String() == s {
			game.playMove(move)
			return move
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
	game.isCancelPending.Store(false)
	game.armClock()
	game.wg.Add(1)
	go func() {
		game.searchEntry()
		game.wg.Done()
	}()
}

// stopThinking signals the search goroutine to halt and blocks until it exits.
func (game *Game) stopThinking() {
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
	for game.isPondering.Load() && !game.isCancelPending.Load() {
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

// handleUciPosition handles the "position" command, setting the position from a FEN
// string or the standard starting position, then applying any supplied move list.
func (game *Game) handleUciPosition(args []string) {
	fen := startFen
	movesIdx := -1
	if len(args) > 0 {
		switch args[0] {
		case "startpos":
			for i, a := range args {
				if a == "moves" {
					movesIdx = i + 1
					break
				}
			}
		case "fen":
			i := 1
			for i < len(args) && args[i] != "moves" {
				i++
			}
			fen = strings.Join(args[1:i], " ")
			if i < len(args) {
				movesIdx = i + 1
			}
		}
	}
	game.stopThinking()
	game.newGameFromFen(fen)
	if movesIdx >= 0 {
		for _, mv := range args[movesIdx:] {
			game.playMoveString(mv)
		}
	}
}

// handleUciGo handles the "go" command, parsing the clock parameters and starting the search.
func (game *Game) handleUciGo(args []string) {
	tc := &game.timeControl
	tc.maxNodes = 0 // reset per-search limits (a bare `go` clears a previous nodes/searchmoves/movestogo/increment)
	tc.searchMoves = nil
	tc.numMovesRemaining = 0 // no movestogo unless this `go` provides one (else computeBudget uses the horizon)
	tc.increment = 0
	game.isPondering.Store(false) // set true below if this is a `go ponder`
	color := game.currentPosition().colorToMove
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "wtime":
			if color == white {
				tc.clockType = clockStandard
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.timeRemaining = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "btime":
			if color == black {
				tc.clockType = clockStandard
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.timeRemaining = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "winc":
			if color == white {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.increment = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "binc":
			if color == black {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.increment = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "movestogo":
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.numMovesRemaining = v
			}
			i++
		case "depth":
			tc.clockType = clockFixedDepth
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.depth = v
			}
			i++
		case "movetime":
			tc.clockType = clockFixedTime
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.timeRemaining = time.Duration(v) * time.Millisecond
			}
			i++
		case "infinite":
			tc.clockType = clockInfinite
		case "ponder":
			// Search on the opponent's time until `ponderhit` (convert to a timed search) or `stop`.
			// armClock (via startThinking) arms no hard deadline while this is set.
			game.isPondering.Store(true)
		case "nodes":
			tc.clockType = clockInfinite // node-limited; the periodic check ends it at maxNodes
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.maxNodes = v
			}
			i++
		case "searchmoves":
			// The rest of the args are UCI moves to restrict the root search to.
			tc.searchMoves = append(tc.searchMoves, args[i+1:]...)
			i = len(args)
		}
	}
	game.startThinking()
}
