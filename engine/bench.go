package engine

import (
	"fmt"
	"time"
)

// benchFens is a small, fixed set of diverse positions searched by the `bench` command. Kept stable so
// the reported node count is a reproducible signal a change can be measured against.
var benchFens = []string{
	"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	"r1bqkbnr/pppp1ppp/2n5/4p3/4P3/5N2/PPPP1PPP/RNBQKB1R w KQkq - 2 3",
	"2rq1rk1/pp1bppbp/2np1np1/8/2BNP3/2N1BP2/PPPQ2PP/2KR3R w - - 0 1",
	"r2q1rk1/1b1nbppp/pp1ppn2/8/2PNP3/1PN1BP2/P2QB1PP/R4RK1 w - - 0 1",
	"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 1",
	"4rrk1/pp1n1pp1/2p2q1p/3p4/3P1P2/2P1PN2/PP1Q2PP/R4RK1 w - - 0 1",
	"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1",
	"3r1k2/4npp1/1ppr3p/p6P/P2PPPP1/1NR5/5K2/2R5 w - - 0 1",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 b - - 0 1",
	"6k1/5ppp/8/8/8/8/5PPP/3R2K1 w - - 0 1",
}

// benchDepth is the fixed search depth used by `bench`.
const benchDepth = 11

// runBench searches every benchFens position to a fixed depth, single-threaded and with the book off, so
// the same build reports the same node count on every run (guarding the uninitialised-read class of bug
// that makes counts wobble). It prints "<nodes> nodes <nps> nps".
func (game *Game) runBench() {
	savedThreads, savedBook := game.threadCount, game.useOwnBook
	game.threadCount = 1
	game.useOwnBook = false
	defer func() { game.threadCount, game.useOwnBook = savedThreads, savedBook }()

	game.transpositionTable.clear() // fresh start so the count is independent of prior session state
	game.evalTable.clear()

	var total int64
	start := time.Now()
	for _, fen := range benchFens {
		game.newGameFromFen(fen)
		game.timeControl.clockType = clockFixedDepth
		game.timeControl.depth = benchDepth
		game.isCancelPending.Store(false)
		searchRootNode(game)
		total += int64(game.nodeCount)
	}
	ms := time.Since(start).Milliseconds()
	fmt.Printf("%d nodes %d nps\n", total, total*1000/max(int64(1), ms))
}
