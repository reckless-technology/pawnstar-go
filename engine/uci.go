package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// HandleCommand processes a single UCI command line.
// Returns true if the engine should quit.
func (g *Game) HandleCommand(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	fields := strings.Fields(line)
	switch fields[0] {
	case "uci":
		fmt.Println("id name Pawnstar-Go")
		fmt.Println("id author Jonny Reckless")
		fmt.Println("uciok")
	case "isready":
		fmt.Println("readyok")
	case "ucinewgame":
		g.stopThinking()
		g.newGameFromFen(startFen)
	case "position":
		g.uciPosition(fields[1:])
	case "go":
		g.uciGo(fields[1:])
	case "stop":
		g.stopThinking()
	case "quit":
		g.stopThinking()
		return true
	}
	return false
}

// uciPosition handles the "position" command, setting the position from a FEN
// string or the standard starting position, then applying any supplied move list.
func (g *Game) uciPosition(args []string) {
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
	g.stopThinking()
	g.newGameFromFen(fen)
	if movesIdx >= 0 {
		for _, mv := range args[movesIdx:] {
			g.playMoveString(mv)
		}
	}
}

// uciGo handles the "go" command, parsing the clock parameters and starting the search.
func (g *Game) uciGo(args []string) {
	tc := &g.timeControl
	color := g.currentPosition().colorToMove()
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
		}
	}
	g.startThinking()
}
