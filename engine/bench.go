package engine

import (
	"fmt"
	"os"
	"time"
)

// benchFens is the fixed position set searched by the `bench` command — a verbatim copy of the C++
// engine's kBenchFens (16 Bratko-Kopec positions). Kept identical to C++ so the reported node total is a
// cross-engine determinism signature: with fixed-depth search parity, the two engines print the same count.
var benchFens = []string{
	"1k1r4/pp1b1R2/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - - 0 1",
	"3r1k2/4npp1/1ppr3p/p6P/P2PPPP1/1NR5/5K2/2R5 w - - 0 1",
	"2q1rr1k/3bbnnp/p2p1pp1/2pPp3/PpP1P1P1/1P2BNNP/2BQ1PRK/7R b - - 0 1",
	"rnbqkb1r/p3pppp/1p6/2ppP3/3N4/2P5/PPP1QPPP/R1B1KB1R w KQkq - 0 1",
	"r1b2rk1/2q1b1pp/p2ppn2/1p6/3QP3/1BN1B3/PPP3PP/R4RK1 w - - 0 1",
	"2r3k1/pppR1pp1/4p3/4P1P1/5P2/1P4K1/P1P5/8 w - - 0 1",
	"1nk1r1r1/pp2n1pp/4p3/q2pPp1N/b1pP1P2/B1P2R2/2P1B1PP/R2Q2K1 w - - 0 1",
	"4b3/p3kp2/6p1/3pP2p/2pP1P2/4K1P1/P3N2P/8 w - - 0 1",
	"2kr1bnr/pbpq4/2n1pp2/3p3p/3P1P1B/2N2N1Q/PPP3PP/2KR1B1R w - - 0 1",
	"3rr1k1/pp3pp1/1qn2np1/8/3p4/PP1R1P2/2P1NQPP/R1B3K1 b - - 0 1",
	"2r1nrk1/p2q1ppp/bp1p4/n1pPp3/P1P1P3/2PBB1N1/4QPPP/R4RK1 w - - 0 1",
	"r3r1k1/ppqb1ppp/8/4p1NQ/8/2P5/PP3PPP/R3R1K1 b - - 0 1",
	"r2q1rk1/4bppp/p2p4/2pP4/3pP3/3Q4/PP1B1PPP/R3R1K1 w - - 0 1",
	"rnb2r1k/pp2p2p/2pp2p1/q2P1p2/8/1Pb2NP1/PB2PPBP/R2Q1RK1 w - - 0 1",
	"2r3k1/1p2q1pp/2b1pr2/p1pp4/6Q1/1P1PP1R1/P1PN2PP/5RK1 w - - 0 1",
	"r1bqkb1r/4npp1/p1p4p/1p1pP1B1/8/1B6/PPPN1PPP/R2Q1RK1 w kq - 0 1",
}

// benchDepth is the default `bench` search depth (matching the C++ kBenchDepth); overridable by `bench <depth>`.
const benchDepth = 13

// benchHashMB pins the transposition-table size for `bench` so the node total is a stable signature
// independent of the `Hash` option — and directly comparable to the C++ engine, whose default hash is
// also 64 MB (TT size changes node counts at fixed depth, so an unpinned bench would wobble with Hash).
const benchHashMB = 64

// runBench searches every benchFens position to a fixed depth, single-threaded and with the book off, so
// the same build reports the same node count on every run (guarding the uninitialised-read class of bug
// that makes counts wobble). It pins a fresh benchHashMB TT (restored afterwards, so the caller's Hash is
// untouched) and mirrors the C++ handle_bench otherwise: the hash starts empty but persists across the
// positions within the run. It prints "<nodes> nodes <nps> nps".
func (game *Game) runBench(depth int) {
	savedThreads, savedBook, savedTT := game.threadCount, game.useOwnBook, game.transpositionTable
	game.threadCount = 1
	game.useOwnBook = false
	// Swap in a fresh, fixed-size TT so the count is reproducible regardless of the Hash setting; restore
	// the caller's table (size + contents) on return. bench is single-threaded, so no thread races the swap.
	game.transpositionTable = newTranspositionTable(benchHashMB)
	game.evalTable.clear()
	defer func() {
		game.threadCount, game.useOwnBook, game.transpositionTable = savedThreads, savedBook, savedTT
	}()

	// PAWNSTAR_BENCH_DEBUG dumps each position's bestmove and (single-threaded) diagnostic counters after
	// its search, for cross-engine comparison against the C++ bench. Off by default, so normal bench output
	// (the "<nodes> nodes <nps> nps" line) is unchanged.
	debug := os.Getenv("PAWNSTAR_BENCH_DEBUG") != ""
	var total int64
	start := time.Now()
	for i, fen := range benchFens {
		game.newGameFromFen(fen)
		game.timeControl.clockType = clockFixedDepth
		game.timeControl.depth = depth
		game.isCancelPending.Store(false)
		if debug {
			dbgxClear()
		}
		m := searchRootNode(game)
		total += int64(game.nodeCount)
		if debug {
			fmt.Printf("### pos %02d bm=%s nodes=%d\n", i+1, m.String(), game.nodeCount)
			dbgxPrint()
		}
	}
	ms := time.Since(start).Milliseconds()
	fmt.Printf("%d nodes %d nps\n", total, total*1000/max(int64(1), ms))
}
