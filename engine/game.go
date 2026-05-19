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
// table, opening book, chess clock, and the single position that is mutated
// in-place during search.  Create one with NewGame and drive it via HandleCommand.
type Game struct {
	transpositionTable transpositionTable
	historyTable       historyTable
	timeControl        timeControl
	openingBook        openingBook
	nodeCount          int
	wg                 sync.WaitGroup
	pos                position
	isCancelPending    bool
}

// NewGame creates an engine instance ready to accept UCI commands.
func NewGame() *Game {
	g := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        loadOpeningBook(),
	}
	g.newGameFromFen(startFen)
	return g
}

// newGameFromFen resets the engine state to the position described by fen.
func (g *Game) newGameFromFen(fen string) {
	g.pos = positionFromFen(fen)
	g.nodeCount = 0
	g.isCancelPending = false
	g.timeControl = newTimeControl()
	g.pos.stateStack = g.pos.stateStack[:0]
}

func (g *Game) currentPosition() *position {
	return &g.pos
}

func (g *Game) playMove(m move) {
	g.pos.makeMove(m)
}

// playMoveString finds the legal move matching UCI string s, plays it, and returns it.
// Returns moveNone if s does not match any legal move.
func (g *Game) playMoveString(s string) move {
	moves := g.pos.generateLegalMoves()
	for _, move := range moves {
		if move.String() == s {
			g.playMove(move)
			return move
		}
	}
	return moveNone()
}

func (g *Game) makeNullMove() {
	g.pos.makeNullMove()
}

func (g *Game) undoMove() {
	if len(g.pos.stateStack) > 0 {
		g.pos.unmakeMove()
	}
}

func (g *Game) isDrawByFiftyMoves() bool {
	return g.pos.getHalfMove() >= 100
}

func (g *Game) isDrawByRepetition() bool {
	return g.pos.isDrawByRepetition()
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
