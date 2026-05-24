package engine

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const startFen = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

// timeControl holds the clock configuration passed to the search goroutine.
type timeControl struct {
	clockType         clockType
	timeRemaining     time.Duration // time left on the clock for the side to move
	numMovesRemaining int           // moves until next time control (0 = sudden death)
	depth             int           // target depth for clockFixedDepth mode
	stopTime          time.Time     // hard deadline; search sets isCancelPending when exceeded
}

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
	transpositionTable transpositionTable
	historyTable       historyTable
	timeControl        timeControl
	evalTable          evalHashTable
	openingBook        openingBook
	nodeCount          int
	wg                 sync.WaitGroup
	parallelSlots      chan struct{} // semaphore limiting concurrent search workers
	posStack           []position
	isCancelPending    atomic.Bool
}

// NewGame creates an engine instance ready to accept UCI commands.
func NewGame() *Game {
	n := runtime.NumCPU() - 1
	if n < 1 {
		n = 1
	}
	slots := make(chan struct{}, n)
	for range n {
		slots <- struct{}{}
	}
	game := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        loadOpeningBook(),
		evalTable:          newEvalHashTable(evalHashTableSize),
		parallelSlots:      slots,
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
		fmt.Println("uciok")
	case "isready":
		fmt.Println("readyok")
	case "ucinewgame":
		game.stopThinking()
		game.newGameFromFen(startFen)
	case "position":
		game.uciPosition(fields[1:])
	case "go":
		game.uciGo(fields[1:])
	case "stop":
		game.stopThinking()
	case "quit":
		game.stopThinking()
		return true
	}
	return false
}

// newGameFromFen resets the engine state to the position described by fen.
func (game *Game) newGameFromFen(fen string) {
	game.posStack = make([]position, 0, 512)
	game.posStack = append(game.posStack, positionFromFen(fen))
	game.nodeCount = 0
	game.isCancelPending.Store(false)
	game.timeControl = newTimeControl()
}

func (game *Game) currentPosition() *position {
	return &game.posStack[len(game.posStack)-1]
}

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

func (game *Game) makeNullMove() {
	game.posStack = append(game.posStack, game.posStack[len(game.posStack)-1])
	pos := &game.posStack[len(game.posStack)-1]
	pos.makeNullMove()
}

func (game *Game) undoMove() {
	if len(game.posStack) > 1 {
		game.posStack = game.posStack[:len(game.posStack)-1]
	}
}

func (game *Game) isDrawByFiftyMoves() bool {
	return game.currentPosition().halfMove >= 100
}

// isDrawByRepetition returns true if the current position has occurred twice
// before in the game history (three occurrences total).  Steps back by two plies
// at a time (same side to move) and stops early at an irreversible move.
func (game *Game) isDrawByRepetition() bool {
	n := len(game.posStack)
	if n < 5 {
		return false
	}
	h := game.posStack[n-1].hash
	repetitions := 2
	for i := n - 5; i >= 0; i -= 2 {
		if game.posStack[i].hash == h {
			repetitions--
			if repetitions == 0 {
				return true
			}
		}
		if game.posStack[i].halfMove == 0 {
			return false
		}
	}
	return false
}

// pieceVals maps piece type to centipawn value, used in move scoring (MVV-LVA).
var pieceVals = [7]int{0, 100, 300, 300, 500, 900, 10000}

// scoreAndSortMovesInner assigns sort scores and sorts moves.  It is shared by
// Game and searchState so parallel workers use the same logic without duplication.
func scoreAndSortMovesInner(pos *position, history *historyTable, moves []move, ply int) {
	occupied := pos.occupied()
	color := pos.colorToMove
	enemyColor := enemyOf(color)
	enemyPieces := pos.piecesOfColor(enemyColor)
	enemyPawns := enemyPieces & pos.pawns
	var enemyAttacks bitboard
	if color == white {
		enemyAttacks = enemyPawns.shiftSouthwest() | enemyPawns.shiftSoutheast()
	} else {
		enemyAttacks = enemyPawns.shiftNorthwest() | enemyPawns.shiftNortheast()
	}
	enemyPieces &= ^enemyPawns
	for enemyPieces != 0 {
		sq := enemyPieces.popLsb()
		enemyAttacks |= pieceAttacks(pos.squares[sq], occupied, sq)
	}
	for i, m := range moves {
		captured := m.captured()
		piece := m.piece()
		promoted := m.promoted()
		var score int
		if (enemyAttacks & squareBB(m.to())) != 0 {
			score = pieceVals[captured] - pieceVals[piece] + pieceVals[promoted]
		} else {
			score = pieceVals[captured] + pieceVals[promoted]
		}
		score = score*1000 + int(history.getCount(ply, m))
		score += pstValue(color, piece, m.to()) - pstValue(color, piece, m.from())
		moves[i] = m.withScore(score)
	}
	sortMovesUnstable(moves)
}

func (game *Game) scoreAndSortMoves(moves []move, ply int) {
	scoreAndSortMovesInner(game.currentPosition(), &game.historyTable, moves, ply)
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

// uciPosition handles the "position" command, setting the position from a FEN
// string or the standard starting position, then applying any supplied move list.
func (game *Game) uciPosition(args []string) {
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

// uciGo handles the "go" command, parsing the clock parameters and starting the search.
func (game *Game) uciGo(args []string) {
	tc := &game.timeControl
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
		}
	}
	game.startThinking()
}
