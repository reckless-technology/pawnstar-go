package engine

// UCI protocol layer: command parsing and dispatch (HandleCommand and its handle*/parse* helpers)
// plus the UCI output formatting the search's info lines use (formatScore, pvString). Engine state
// lives in game.go; the clock in clock.go; the search itself in search.go.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
		fmt.Printf("id name Pawnstar-Go %s\n", versionString())
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
		depth := benchDepth // `bench` uses benchDepth; `bench <depth>` overrides (matches C++ handle_bench)
		if len(fields) > 1 {
			if d, err := strconv.Atoi(fields[1]); err == nil && d > 0 {
				depth = d
			}
		}
		game.runBench(depth)
	case "stop":
		game.stopThinking()
	case "quit":
		game.stopThinking()
		return true
	}
	return false
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
			game.SetThreads(n) // clamps to [1, maxSearchThreads]
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

// goArgValue returns the integer value following the keyword at args[i], and whether one was present and
// parseable. The bounds check is load-bearing, not defensive decoration: every `go` keyword that takes a
// value reads args[i+1], so a truncated line (a bare `go wtime`, or a GUI that fumbles a clock update)
// would panic on an out-of-range index — and a UCI engine that crashes mid-game forfeits it. A missing or
// unparseable value is ignored together with its keyword (handleUciGo changes nothing for it), the rule the
// C++ and Rust engines share: `go nodes abc` is a plain `go`, not an unbounded node-limited search.
func goArgValue(args []string, i int) (int, bool) {
	if i+1 >= len(args) {
		return 0, false
	}
	v, err := strconv.Atoi(args[i+1])
	return v, err == nil
}

// handleUciGo handles the "go" command, parsing the clock parameters and starting the search.
func (game *Game) handleUciGo(args []string) {
	// Stop any running search before touching the clock it reads: a `go` that arrives while one runs would
	// otherwise rewrite timeControl under it (the race detector caught `go infinite` + `go depth 1`). The Rust
	// engine stops first for the same reason; startThinking's own stop is then a no-op.
	game.stopThinking()
	tc := &game.timeControl
	tc.maxNodes = 0 // reset per-search limits (a bare `go` clears a previous nodes/searchmoves/movestogo/increment)
	tc.searchMoves = nil
	tc.numMovesRemaining = 0 // no movestogo unless this `go` provides one (else computeBudget uses the horizon)
	tc.increment = 0
	game.isPondering.Store(false) // set true below if this is a `go ponder`
	c := game.currentPosition().colorToMove
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "wtime":
			if v, ok := goArgValue(args, i); ok && c == white {
				tc.clockType = clockStandard
				tc.timeRemaining = time.Duration(v) * time.Millisecond
			}
			i++
		case "btime":
			if v, ok := goArgValue(args, i); ok && c == black {
				tc.clockType = clockStandard
				tc.timeRemaining = time.Duration(v) * time.Millisecond
			}
			i++
		case "winc":
			if c == white {
				if v, ok := goArgValue(args, i); ok {
					tc.increment = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "binc":
			if c == black {
				if v, ok := goArgValue(args, i); ok {
					tc.increment = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "movestogo":
			if v, ok := goArgValue(args, i); ok {
				tc.numMovesRemaining = v
			}
			i++
		case "depth":
			if v, ok := goArgValue(args, i); ok {
				tc.clockType = clockFixedDepth
				tc.depth = v
			}
			i++
		case "movetime":
			if v, ok := goArgValue(args, i); ok {
				tc.clockType = clockFixedTime
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
			if v, ok := goArgValue(args, i); ok {
				tc.clockType = clockInfinite // node-limited; the periodic check ends it at maxNodes
				tc.maxNodes = v
			}
			i++
		case "searchmoves":
			// The rest of the args are UCI moves to restrict the root search to. args[i+1:] is safe for any
			// i < len(args) (an empty tail yields an empty slice), so no bounds check is needed here.
			tc.searchMoves = append(tc.searchMoves, args[i+1:]...)
			i = len(args)
		}
	}
	game.startThinking()
}

// formatScore renders a search score as a UCI `score` field: `mate N` inside the mate band (positive
// = we deliver mate in N moves, negative = we are mated in N), otherwise `cp N`. Mate scores are
// ±(mateValue − plies_to_mate), so plies_to_mate = mateValue − |score| and the move count rounds up.
func formatScore(score int) string {
	const mateValue = -checkmatedScore
	switch {
	case score >= winThreshold:
		return fmt.Sprintf("mate %d", (mateValue-score+1)/2)
	case score <= loseThreshold:
		return fmt.Sprintf("mate %d", -((mateValue + score + 1) / 2))
	default:
		return fmt.Sprintf("cp %d", score)
	}
}

// pvString formats a principal variation as a space-separated UCI move list.
func pvString(pv variation) string {
	var b strings.Builder
	for i, m := range pv.moves {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.String())
	}
	return b.String()
}
