package engine

import (
	"fmt"
	"sync"
	"time"
)

const startFen = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

type timeControl struct {
	clockType         clockType
	timeRemaining     time.Duration
	numMovesRemaining int
	depth             int
	stopTime          time.Time
}

func newTimeControl() timeControl {
	return timeControl{
		clockType:         clockStandard,
		timeRemaining:     5 * time.Minute,
		depth:             10,
		numMovesRemaining: 40,
	}
}

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

func NewGame() *Game {
	g := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        loadOpeningBook(),
	}
	g.newGameFromFen(startFen)
	return g
}

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

func (g *Game) scoreAndSortMoves(moves []move, ply int) {
	pos := g.currentPosition()
	pieceVals := [7]int{0, 100, 300, 300, 500, 900, 10000}
	for i := 0; i < len(moves); i++ {
		m := moves[i]
		s := pieceVals[pos.pieceAt(m.to())]*10000 -
			pieceVals[pos.pieceAt(m.from())]*1000 +
			int(g.historyTable.getCount(ply, m))
		moves[i] = m.withScore(s)
	}
	sortMovesUnstable(moves)
}

func (g *Game) startThinking() {
	g.stopThinking()
	g.isCancelPending = false
	g.wg.Add(1)
	go func() {
		g.searchEntry()
		g.wg.Done()
	}()
}

func (g *Game) stopThinking() {
	g.isCancelPending = true
	g.wg.Wait()
	g.isCancelPending = false
}

func (g *Game) searchEntry() {
	m := searchRootNode(g)
	if m != moveNone() {
		g.playMove(m)
		fmt.Println("bestmove " + m.String())
	}
}
