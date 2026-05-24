package engine

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
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
	posStackData       [512]position
	posStack           []position
	isCancelPending    bool
}

// NewGame creates an engine instance ready to accept UCI commands.
func NewGame() *Game {
	game := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        loadOpeningBook(),
	}
	game.newGameFromFen(startFen)
	return game
}

// newGameFromFen resets the engine state to the position described by fen.
func (game *Game) newGameFromFen(fen string) {
	game.posStack = game.posStackData[:0]
	game.posStack = append(game.posStack, positionFromFen(fen))
	game.nodeCount = 0
	game.isCancelPending = false
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

// scoreAndSortMoves assigns a sort score to each move and sorts descending.
// Score = captured_value × 10000 − moving_piece_value × 1000 + history_count,
// giving priority to captures (MVV-LVA) and historically good quiet moves.
func (game *Game) scoreAndSortMovesX(moves []move, ply int) {
	color := game.currentPosition().colorToMove()
	for i, move := range moves {
		score := (pieceVals[move.captured()] * 10) - pieceVals[move.piece()]
		score += pstValue(color, move.piece(), move.to()) - pstValue(color, move.piece(), move.from())
		score = (score * 1000) + int(game.historyTable.getCount(ply, move))
		moves[i] = move.withScore(score)
	}
	sortMovesUnstable(moves)
}

func (game *Game) scoreAndSortMoves(moves []move, ply int) {
	// Determine all squares attacked by the enemy
	position := game.currentPosition()
	occupied := position.occupied()
	color := position.colorToMove()
	enemyColor := enemyOf(color)
	enemyPieces := position.piecesOfColor(enemyColor)
	enemyPawns := enemyPieces & position.pawns
	var enemyAttacks bitboard
	if color == white {
		enemyAttacks = enemyPawns.shiftSouthwest() | enemyPawns.shiftSoutheast()
	} else {
		enemyAttacks = enemyPawns.shiftNorthwest() | enemyPawns.shiftNortheast()
	}
	enemyPieces &= ^enemyPawns
	for enemyPieces != 0 {
		sq := enemyPieces.popLsb()
		enemyPiece := position.squares[sq]
		enemyAttacks |= pieceAttacks(enemyPiece, occupied, sq)
	}
	// Now determine provisional approximate score for move based on cpatured and promoted material and PST.
	// If the target square is attacked by the enemy, assume we lose the moving piece.
	// This is much cruder than SEE, but it's fast and reasonably accurate for rough move ordering.
	for i, move := range moves {
		captured := move.captured()
		piece := move.piece()
		promoted := move.promoted()
		var score int
		if (enemyAttacks & squareBB(move.to())) != 0 {
			// Enemy attacks destination square
			score = pieceVals[captured] - pieceVals[piece] + pieceVals[promoted]

		} else {
			score = pieceVals[captured] + pieceVals[promoted]
		}
		score += pstValue(color, piece, move.to()) - pstValue(color, piece, move.from())
		score = score*1000 + int(game.historyTable.getCount(ply, move))
		moves[i] = move.withScore(score)
	}
	sortMovesUnstable(moves)
}

// startThinking launches a search goroutine for the current position and time
// control.  Any previously running search is stopped first.
func (game *Game) startThinking() {
	game.stopThinking()
	game.isCancelPending = false
	game.wg.Add(1)
	go func() {
		game.searchEntry()
		game.wg.Done()
	}()
}

// stopThinking signals the search goroutine to halt and blocks until it exits.
func (game *Game) stopThinking() {
	game.isCancelPending = true
	game.wg.Wait()
	game.isCancelPending = false
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

// HandleCommand processes a single UCI command line.
// Returns true if the engine should quit.
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
	color := game.currentPosition().colorToMove()
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
