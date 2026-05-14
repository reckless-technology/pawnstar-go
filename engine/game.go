package engine

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// ClockType controls how the search is timed.
type ClockType uint8

const (
	ClockStandard   ClockType = iota // Time-per-game with remaining time.
	ClockFixedDepth                  // Search to a fixed depth.
	ClockFixedTime                   // Search for a fixed number of ms.
	ClockInfinite                    // Search until stop.
)

// TimeControl holds UCI time management parameters.
type TimeControl struct {
	ClockType         ClockType
	TimeRemaining     time.Duration
	NumMovesRemaining int
	Depth             int
	stopTime          time.Time
}

func NewTimeControl() TimeControl {
	return TimeControl{
		ClockType:         ClockStandard,
		TimeRemaining:     5 * time.Minute,
		Depth:             10,
		NumMovesRemaining: 40,
	}
}

// Game holds the complete game state.
type Game struct {
	TranspositionTable TranspositionTable
	History            HistoryTable
	TimeCtrl           TimeControl
	Book               OpeningBook
	NodeCount          int
	IsCancelPending    bool
	wg                 sync.WaitGroup
	pos                Position
}

func NewGame() *Game {
	g := &Game{
		TranspositionTable: NewTranspositionTable(HashTableMB),
		TimeCtrl:           NewTimeControl(),
		Book:               LoadOpeningBook(),
	}
	g.newGame("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	return g
}

func (g *Game) newGame(fen string) {
	g.pos = PositionFromFen(fen)
	g.NodeCount = 0
	g.IsCancelPending = false
	g.TimeCtrl = NewTimeControl()
}

func (g *Game) NewGameFen(fen string) {
	g.StopThinking()
	g.newGame(fen)
}

func (g *Game) NewGameStart() {
	g.NewGameFen("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
}

func (g *Game) CurrentPosition() *Position {
	return &g.pos
}

func (g *Game) PlayMove(m Move) {
	g.pos.MakeMove(m)
}

func (g *Game) PlayMoveString(s string) Move {
	moves := g.pos.GenerateLegalMoves()
	for i := 0; i < len(moves); i++ {
		m := moves[i]
		if m.String() == s {
			g.PlayMove(m)
			return m
		}
	}
	return MoveNone()
}

func (g *Game) MakeNullMove() {
	g.pos.MakeNullMove()
}

func (g *Game) UndoMove() {
	if len(g.pos.stateStack) > 0 {
		g.pos.UnmakeMove()
	}
}

func (g *Game) IsDrawByFiftyMoves() bool {
	return g.pos.HalfMove() >= 100
}

func (g *Game) IsDrawByRepetition() bool {
	return g.pos.IsDrawByRepetition()
}

func (g *Game) ScoreAndSortMoves(moves []Move, ply int) {
	pos := g.CurrentPosition()
	pieceVals := [7]int{0, 100, 300, 300, 500, 900, 10000}
	for i := 0; i < len(moves); i++ {
		m := moves[i]
		score := pieceVals[pos.PieceAt(m.To())]*10000 -
			pieceVals[pos.PieceAt(m.From())]*1000 +
			int(g.History.GetCount(ply, m))
		moves[i] = m.WithScore(score)
	}
	SortMovesUnstable(moves)
}

func (g *Game) StartThinking() {
	g.StopThinking()
	g.IsCancelPending = false
	g.wg.Add(1)
	go func() {
		g.searchEntry()
		g.wg.Done()
	}()
}

func (g *Game) StopThinking() {
	g.IsCancelPending = true
	g.wg.Wait()
	g.IsCancelPending = false
}

func (g *Game) searchEntry() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered from panic: %v\n", r)

			// Print the full stack trace to standard output
			fmt.Println("Full Stack Trace:")
			fmt.Println(string(debug.Stack()))
		}
	}()
	move := SearchRootNode(g)
	if move != MoveNone() {
		g.PlayMove(move)
		fmt.Println("bestmove " + move.String())
	}
}
