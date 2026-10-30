package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestGenerateBratkoKopecNNUE regenerates the accepted-move sets for TestBratkoKopec against the NNUE
// evaluator. It is skipped unless GEN_BK is set. For each position it replays the exact test setup
// (fresh NewGame + NNUE, cold TT, opening book cleared) several times at both the -short depth (7) and
// the full depth (8), then prints, per position, the distinct best moves seen ordered by frequency.
// The search is single-threaded and deterministic, so what goes into bkCases is the union across the
// two DEPTHS, not across runs; the second run of each is there only to confirm that. Rerun after
// swapping the embedded net:
//
//	GOEXPERIMENT=simd GEN_BK=1 go test ./engine/ -run TestGenerateBratkoKopecNNUE -v 2>/dev/null | grep BKGEN
func TestGenerateBratkoKopecNNUE(t *testing.T) {
	if os.Getenv("GEN_BK") == "" {
		t.Skip("set GEN_BK=1 to regenerate Bratko-Kopec accepted moves for NNUE")
	}
	const runs = 2 // serial search is deterministic; 2 runs confirms no residual nondeterminism
	depths := []int{7, 8}
	for i, tc := range bkCases {
		freq := map[string]int{}
		var order []string
		for _, depth := range depths {
			for r := 0; r < runs; r++ {
				game := NewGame()
				if err := game.EnableNNUE(); err != nil {
					t.Fatalf("enable NNUE: %v", err)
				}
				game.newGameFromFen(tc.fen)
				game.openingBook.clear()
				game.timeControl.clockType = clockFixedDepth
				game.timeControl.depth = depth
				dbgxClear()
				m := searchRootNode(game)
				got := m.String()
				if freq[got] == 0 {
					order = append(order, got)
				}
				freq[got]++
			}
		}
		sort.SliceStable(order, func(a, b int) bool { return freq[order[a]] > freq[order[b]] })
		quoted := make([]string, len(order))
		for j, mv := range order {
			quoted[j] = fmt.Sprintf("%q", mv)
		}
		fmt.Printf("BKGEN\t{%q, []string{%s}},  // pos%02d freq=%v\n",
			tc.fen, strings.Join(quoted, ", "), i+1, freq)
	}
}
