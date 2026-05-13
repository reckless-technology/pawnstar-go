package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"pawnstar-go/engine"
)

func main() {
	fmt.Println("Pawnstar (Go) — UCI compatible chess engine")

	game := engine.NewGame()
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		cmd := fields[0]

		switch cmd {
		case "uci":
			fmt.Println("id name Pawnstar-Go")
			fmt.Println("id author Jonny Reckless")
			fmt.Println("uciok")

		case "isready":
			fmt.Println("readyok")

		case "ucinewgame":
			game.NewGameStart()

		case "position":
			parsePosition(game, fields[1:])

		case "go":
			handleGo(game, fields[1:])

		case "stop":
			game.StopThinking()

		case "quit":
			game.StopThinking()
			return

		case "wait":
			game.WaitForSearch()

		case "perft":
			depth := 5
			if len(fields) > 1 {
				if d, err := strconv.Atoi(fields[1]); err == nil {
					depth = d
				}
			}
			runPerft(*game.CurrentPosition(), depth)

		default:
			// Silently ignore unknown commands (UCI requirement).
		}
	}
	// stdin closed — wait for any running search before exiting.
	game.WaitForSearch()
}

func parsePosition(game *engine.Game, args []string) {
	startFen := "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"
	fen := startFen
	movesIdx := -1

	if len(args) > 0 {
		switch args[0] {
		case "startpos":
			fen = startFen
			for i, a := range args {
				if a == "moves" {
					movesIdx = i + 1
					break
				}
			}
		case "fen":
			fenParts := []string{}
			i := 1
			for i < len(args) && args[i] != "moves" {
				fenParts = append(fenParts, args[i])
				i++
			}
			fen = strings.Join(fenParts, " ")
			if i < len(args) && args[i] == "moves" {
				movesIdx = i + 1
			}
		}
	}

	game.NewGameFen(fen)
	if movesIdx >= 0 {
		for _, mv := range args[movesIdx:] {
			game.PlayMoveString(mv)
		}
	}
}

func handleGo(game *engine.Game, args []string) {
	tc := &game.TimeCtrl
	color := game.CurrentPosition().ColorToMove()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "wtime":
			if color == engine.White {
				tc.ClockType = engine.ClockStandard
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.MsRemaining = v
				}
			}
			i++
		case "btime":
			if color == engine.Black {
				tc.ClockType = engine.ClockStandard
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.MsRemaining = v
				}
			}
			i++
		case "movestogo":
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.NumMovesRemaining = v
			}
			i++
		case "depth":
			tc.ClockType = engine.ClockFixedDepth
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.Depth = v
			}
			i++
		case "movetime":
			tc.ClockType = engine.ClockFixedTime
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.MsRemaining = v
			}
			i++
		case "infinite":
			tc.ClockType = engine.ClockInfinite
		}
	}
	game.StartThinking()
}

func runPerft(pos engine.Position, maxDepth int) {
	for depth := 1; depth <= maxDepth; depth++ {
		start := time.Now()
		nodes := engine.Perft(&pos, depth)
		elapsed := time.Since(start)
		mnps := 0.0
		if elapsed > 0 {
			mnps = float64(nodes) / elapsed.Seconds() / 1e6
		}
		fmt.Printf("depth: %d  nodes: %d  time: %s  Mnps: %.0f\n",
			depth, nodes, elapsed.Round(time.Millisecond), mnps)
	}
}
