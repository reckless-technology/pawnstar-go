package engine

import (
	"fmt"
	"time"
)

// ClockType controls how the search is timed.
type ClockType int

const (
	ClockStandard   ClockType = iota // Time-per-game with remaining time.
	ClockFixedDepth                  // Search to a fixed depth.
	ClockFixedTime                   // Search for a fixed number of ms.
	ClockInfinite                    // Search until stop.
)

// TimeControl holds UCI time management parameters.
type TimeControl struct {
	ClockType         ClockType
	MsRemaining       int
	NumMovesRemaining int
	Depth             int
	HardStopMs        int64 // wall-clock ms to hard-stop (0 = no limit)
	startTime         time.Time
}

func NewTimeControl() TimeControl {
	return TimeControl{
		ClockType:   ClockStandard,
		MsRemaining: 5 * 60 * 1000,
		Depth:       10,
		startTime:   time.Now(),
	}
}

func (tc *TimeControl) Reset() {
	tc.startTime = time.Now()
	tc.HardStopMs = 0
}

func (tc *TimeControl) ElapsedMs() int64 {
	return time.Since(tc.startTime).Milliseconds()
}

// Game holds the complete game state.
type Game struct {
	TransTable   *TranspositionTable
	QuiesTable   *TranspositionTable
	History      HistoryTable
	TimeCtrl     TimeControl
	Book         OpeningBook
	NodeCount    int
	CancelSearch bool

	pos      Position
	stopChan chan struct{}
	doneChan chan struct{}
}

func NewGame() *Game {
	g := &Game{
		TransTable: NewTranspositionTable(HashTableMB),
		QuiesTable: NewTranspositionTable(QHashTableMB),
		TimeCtrl:   NewTimeControl(),
		Book:       LoadOpeningBook(),
	}
	g.newGame("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	return g
}

func (g *Game) newGame(fen string) {
	g.pos = FromFen(fen)
	g.NodeCount = 0
	g.CancelSearch = false
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

// ScoreAndSortMoves assigns MVV/LVA + history scores and sorts descending.
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

// StartThinking begins the search on a background goroutine.
func (g *Game) StartThinking() {
	g.StopThinking()
	g.doneChan = make(chan struct{})
	g.CancelSearch = false
	g.TimeCtrl.Reset()
	go func() {
		defer close(g.doneChan)
		g.searchEntry()
	}()
}

// StopThinking signals the search to stop and waits for it to finish.
func (g *Game) StopThinking() {
	if g.doneChan != nil {
		g.CancelSearch = true
		<-g.doneChan
		g.doneChan = nil
		g.stopChan = nil
	}
}

// WaitForSearch waits for any running search to complete without cancelling it.
func (g *Game) WaitForSearch() {
	if g.doneChan != nil {
		<-g.doneChan
		g.doneChan = nil
		g.stopChan = nil
	}
}

func (g *Game) searchEntry() {
	move := SearchRootNode(g)
	if move != MoveNone() {
		g.PlayMove(move)
		fmt.Println("bestmove " + move.String())
	}
}
