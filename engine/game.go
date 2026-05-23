package engine

import (
	"fmt"
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
	openingBook        openingBook
	nodeCount          int
	wg                 sync.WaitGroup
	posStack           []position
	isCancelPending    bool
}

// NewGame creates an engine instance ready to accept UCI commands.
func NewGame() *Game {
	g := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        loadOpeningBook(),
		posStack:           make([]position, 0, 512),
	}
	g.newGameFromFen(startFen)
	return g
}

// newGameFromFen resets the engine state to the position described by fen.
func (g *Game) newGameFromFen(fen string) {
	g.posStack = g.posStack[:0]
	g.posStack = append(g.posStack, positionFromFen(fen))
	g.nodeCount = 0
	g.isCancelPending = false
	g.timeControl = newTimeControl()
}

func (g *Game) currentPosition() *position {
	return &g.posStack[len(g.posStack)-1]
}

func (g *Game) playMove(m move) {
	pos := g.posStack[len(g.posStack)-1]
	pos.makeMove(m)
	g.posStack = append(g.posStack, pos)
}

// playMoveString finds the legal move matching UCI string s, plays it, and returns it.
// Returns moveNone if s does not match any legal move.
func (g *Game) playMoveString(s string) move {
	moves := g.currentPosition().generateLegalMoves()
	for _, move := range moves {
		if move.String() == s {
			g.playMove(move)
			return move
		}
	}
	return moveNone()
}

func (g *Game) makeNullMove() {
	pos := g.posStack[len(g.posStack)-1]
	pos.makeNullMove()
	g.posStack = append(g.posStack, pos)
}

func (g *Game) undoMove() {
	if len(g.posStack) > 1 {
		g.posStack = g.posStack[:len(g.posStack)-1]
	}
}

func (g *Game) isDrawByFiftyMoves() bool {
	return g.currentPosition().getHalfMove() >= 100
}

// isDrawByRepetition returns true if the current position has occurred twice
// before in the game history (three occurrences total).  Steps back by two plies
// at a time (same side to move) and stops early at an irreversible move.
func (g *Game) isDrawByRepetition() bool {
	n := len(g.posStack)
	if n < 5 {
		return false
	}
	h := g.posStack[n-1].hash
	repetitions := 2
	for i := n - 5; i >= 0; i -= 2 {
		if g.posStack[i].hash == h {
			repetitions--
			if repetitions == 0 {
				return true
			}
		}
		if g.posStack[i].halfMove == 0 {
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
func (g *Game) scoreAndSortMoves(moves []move, ply int) {
	for i := range moves {
		move := moves[i]
		s := (pieceVals[move.captured()] * 10000) -
			(pieceVals[move.piece()] * 1000) +
			int(g.historyTable.getCount(ply, move))
		moves[i] = move.withScore(s)
	}
	sortMovesUnstable(moves)
}

// startThinking launches a search goroutine for the current position and time
// control.  Any previously running search is stopped first.
func (g *Game) startThinking() {
	g.stopThinking()
	g.isCancelPending = false
	g.wg.Add(1)
	go func() {
		g.searchEntry()
		g.wg.Done()
	}()
}

// stopThinking signals the search goroutine to halt and blocks until it exits.
func (g *Game) stopThinking() {
	g.isCancelPending = true
	g.wg.Wait()
	g.isCancelPending = false
}

// searchEntry is the body of the search goroutine.  It runs the search, plays the
// best move on the internal position, and prints the UCI bestmove response.
func (g *Game) searchEntry() {
	m := searchRootNode(g)
	if m != moveNone() {
		g.playMove(m)
		fmt.Println("bestmove " + m.String())
	} else {
		fmt.Println("bestmove 0000")
	}
}
