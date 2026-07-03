package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestSearch(t *testing.T) {
	maxDepth := 10
	if testing.Short() {
		maxDepth = 9
	}
	for _, s := range perftEPD {
		fen := s[:strings.IndexRune(s, ';')]
		t.Logf("\n\r%s\n", fen)
		game := NewGame()
		game.newGameFromFen(fen)
		game.openingBook.clear()
		game.timeControl.clockType = clockFixedDepth
		game.timeControl.depth = maxDepth
		// Bound each position: the promotion/ep search extensions (like the C++ engine) are unguarded, so a
		// promotion-dense endgame (e.g. 8/PPPk4/8/8/8/8/4Kppp/8 w) drives the search to maxPly and would hang
		// this fixed-depth smoke test. Normal positions finish the target depth well under this cap; the cap
		// only bites on the pathological ones (real play bounds them with the clock; C++ has no such test).
		game.timeControl.maxNodes = 3_000_000
		dbgxClear()
		game.startThinking()
		game.wg.Wait()
		dbgxPrint()
	}
}

type bkCase struct {
	fen      string
	accepted []string // acceptable best moves; the search runs the parallel NNUE evaluator, which picks
	// among near-equal moves nondeterministically, so this is the union observed over many runs.
}

// bkCases are the 24 Bratko-Kopec positions with accepted best moves from the NNUE evaluator. The
// search is single-threaded and therefore deterministic (see TestGenerateBratkoKopecNNUE). Where two
// moves are listed they are the depth-7 (short) and depth-8 (full) choices. Regenerate with GEN_BK=1
// after swapping the embedded net.
var bkCases = [24]bkCase{
	{"1k1r4/pp1b1R02/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - -", []string{"d6d1"}},
	{"3r1k2/4npp1/1ppr3p/p6P/P2PPPP1/1NR5/5K2/2R5 w - -", []string{"d4d5"}},
	{"2q1rr1k/3bbnnp/p2p1pp1/2pPp3/PpP1P1P1/1P2BNNP/2BQ1PRK/7R b - -", []string{"a6a5"}},
	{"rnbqkb1r/p3pppp/1p6/2ppP3/3N4/2P5/PPP1QPPP/R1B1KB1R w KQkq -", []string{"e5e6"}},
	{"r1b2rk1/2q1b1pp/p2ppn2/1p6/3QP3/1BN1B3/PPP3PP/R4RK1 w - -", []string{"c3d5"}},
	{"2r3k1/pppR1pp1/4p3/4P1P1/5P2/1P4K1/P1P5/8 w - -", []string{"g3g4", "c2c4"}},
	{"1nk1r1r1/pp2n1pp/4p3/q2pPp1N/b1pP1P2/B1P2R2/2P1B1PP/R2Q2K1 w - -", []string{"f3g3"}},
	{"4b3/p3kp2/6p1/3pP2p/2pP1P2/4K1P1/P3N2P/8 w - -", []string{"e2c3"}},
	{"2kr1bnr/pbpq4/2n1pp2/3p3p/3P1P1B/2N2N1Q/PPP3PP/2KR1B1R w - -", []string{"d1e1"}},
	{"3rr1k1/pp3pp1/1qn2np1/8/3p4/PP1R1P2/2P1NQPP/R1B3K1 b - -", []string{"b6c5"}},
	{"2r1nrk1/p2q1ppp/bp1p4/n1pPp3/P1P1P3/2PBB1N1/4QPPP/R4RK1 w - -", []string{"a1a3"}},
	{"r3r1k1/ppqb1ppp/8/4p1NQ/8/2P5/PP3PPP/R3R1K1 b - -", []string{"d7f5"}},
	{"r2q1rk1/4bppp/p2p4/2pP4/3pP3/3Q4/PP1B1PPP/R3R1K1 w - -", []string{"b2b4"}},
	{"rnb2r1k/pp2p2p/2pp2p1/q2P1p2/8/1Pb2NP1/PB2PPBP/R2Q1RK1 w - -", []string{"d1e1"}},
	{"2r3k1/1p2q1pp/2b1pr2/p1pp4/6Q1/1P1PP1R1/P1PN2PP/5RK1 w - -", []string{"g4g7"}},
	{"r1bqkb1r/4npp1/p1p4p/1p1pP1B1/8/1B6/PPPN1PPP/R2Q1RK1 w kq -", []string{"d2e4"}},
	{"r2q1rk1/1ppnbppp/p2p1nb1/3Pp3/2P1P1P1/2N2N1P/PPB1QP2/R1B2RK1 b - -", []string{"h7h6", "h7h5"}},
	{"r1bq1rk1/pp2ppbp/2np2p1/2n5/P3PP2/N1P2N2/1PB3PP/R1B1QRK1 b - -", []string{"f7f5"}},
	{"3rr3/2pq2pk/p2p1pnp/8/2QBPP2/1P6/P5PP/4RRK1 b - -", []string{"d6d5"}},
	{"r4k2/pb2bp1r/1p1qp2p/3pNp2/3P1P2/2N3P1/PPP1Q2P/2KRR3 w - -", []string{"g3g4"}},
	{"3rn2k/ppb2rpp/2ppqp2/5N2/2P1P3/1P5Q/PB3PPP/3RR1K1 w - -", []string{"f5h6"}},
	{"2r2rk1/1bqnbpp1/1p1ppn1p/pP6/N1P1P3/P2B1N1P/1B2QPP1/R2R2K1 b - -", []string{"f6h5"}},
	{"r1bqk2r/pp2bppp/2p5/3pP3/P2Q1P2/2N1B3/1PP3PP/R4RK1 b kq -", []string{"c8f5"}},
	{"r2qnrnk/p2b2b1/1p1p2pp/2pPpp2/1PP1P3/PRNBB3/3QNPPP/5RK1 w - -", []string{"f2f4"}},
}

func TestBratkoKopec(t *testing.T) {
	failures := 0
	for i, tc := range bkCases {
		game := NewGame()
		if err := game.EnableNNUE(); err != nil { // BK gates the shipped (NNUE) evaluator
			t.Fatalf("enable NNUE: %v", err)
		}
		game.newGameFromFen(tc.fen)
		game.openingBook.clear()
		game.timeControl.clockType = clockFixedDepth
		depth := 8
		if testing.Short() {
			depth = 7
		}
		game.timeControl.depth = depth
		dbgxClear()
		m := searchRootNode(game)
		dbgxPrint()
		got := m.String()

		accepted := tc.accepted
		pass := false
		for _, a := range accepted {
			if a != "" && got == a {
				pass = true
				break
			}
		}

		status := "PASS"
		if !pass {
			status = "FAIL"
			failures++
		}
		var valid []string
		for _, m := range accepted {
			if m != "" {
				valid = append(valid, m)
			}
		}
		fmt.Printf("[%s] pos%02d got=%s accepted=[%s] %s\n", status, i+1, got, strings.Join(valid, " "), tc.fen)
	}
	fmt.Printf("\n%d/24 passed\n", 24-failures)
	if failures > 0 {
		t.Errorf("%d/24 positions not in accepted set", failures)
	}
}
