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
	numMovesRemaining int           // moves until next time control (0 = sudden death)
	depth             int           // target depth for clockFixedDepth mode
	stopTime          time.Time     // hard deadline; search sets isCancelPending when exceeded
	maxNodes          int           // node limit (`go nodes`); 0 = none. Checked per thread.
	searchMoves       []string      // if non-empty (`go searchmoves`), restrict the root to these UCI moves
}

// newTimeControl returns a default timeControl (5-minute standard game, 40 moves).
func newTimeControl() timeControl {
	return timeControl{
		clockType:         clockStandard,
		timeRemaining:     5 * time.Minute,
		depth:             10,
		numMovesRemaining: 40,
	}
}

// Game is the top-level engine state.  It owns the transposition table, history
// table, opening book, chess clock, and a stack of positions for copy-make search.
// Create one with NewGame and drive it via HandleCommand.
type Game struct {
	transpositionTable transpositionTable // lockless search-result cache, shared by all Lazy SMP threads
	timeControl        timeControl        // clock configuration for the current search
	evalTable          evalHashTable      // lockless evaluation cache, shared by all Lazy SMP threads
	openingBook        openingBook        // pre-loaded book moves indexed by Zobrist hash
	nnue               *nnueNetwork       // NNUE evaluator; nil selects the handcrafted evaluation
	threadCount        int                // Lazy SMP search threads; 1 = single-threaded (default; tests rely on it)
	moveOverhead       time.Duration      // reserved from every hard deadline for GUI/network lag (UCI Move Overhead)
	nodeCount          int                // total nodes searched
	wg                 sync.WaitGroup     // waited on by stopThinking to join the search goroutine
	posStack           []position         // game history + current search path
	isCancelPending    atomic.Bool        // set true to signal all search goroutines to stop
}

// NewGame creates an engine instance ready to accept UCI commands.
func NewGame() *Game {
	game := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        newOpeningBookFromFile(),
		evalTable:          newEvalHashTable(evalHashTableSize),
		threadCount:        1, // single-threaded by default; main() / UCI `Threads` raise it for play
		moveOverhead:       30 * time.Millisecond,
	}
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
		fmt.Println("option name UseNNUE type check default true")
		fmt.Println("option name EvalFile type string default <embedded>")
		fmt.Printf("option name Threads type spin default %d min 1 max %d\n", game.threadCount, maxSearchThreads)
		fmt.Printf("option name Hash type spin default %d min 1 max 4096\n", hashTableMB)
		fmt.Println("option name Clear Hash type button")
		fmt.Printf("option name Move Overhead type spin default %d min 0 max 5000\n", game.moveOverhead.Milliseconds())
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
//   - UseNNUE (check): true installs the embedded NNUE evaluator, false selects the handcrafted eval.
//   - EvalFile (string): a path to a PSN1 net to load; "<embedded>", "default" or empty reload the
//     embedded net. Names and values may contain spaces, so both are parsed span-wise.
func (game *Game) handleUciSetOption(fields []string) {
	name, value := parseSetOption(fields)
	switch strings.ToLower(name) {
	case "usennue":
		if strings.EqualFold(value, "false") {
			game.nnue = nil
			game.evalTable.clear() // switching evaluators invalidates cached scores
			fmt.Println("info string NNUE disabled; using handcrafted eval")
		} else if err := game.EnableNNUE(); err != nil {
			fmt.Printf("info string %v\n", err)
		}
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
// control.  Any previously running search is stopped first.
func (game *Game) startThinking() {
	game.stopThinking()
	game.isCancelPending.Store(false)
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

// searchEntry is the body of the search goroutine.  It runs the search, plays the
// best move on the internal position, and prints the UCI bestmove response.
func (game *Game) searchEntry() {
	m := searchRootNode(game)
	if m != moveNone() {
		game.playMove(m)
		fmt.Println("bestmove " + m.String())
	} else {
		fmt.Println("bestmove 0000")
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
	tc.maxNodes = 0        // reset per-search limits (a bare `go` clears a previous nodes/searchmoves)
	tc.searchMoves = nil
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
