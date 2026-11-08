package engine

import (
	"slices"
	"testing"
)

// orderingPassCancelFen is the position on which a hard deadline landing inside the root's ordering pass
// played the interrupted move: its cancel sentinel, truncated into the move's 23-bit score field, sorted
// it first. At `go wtime 664 btime 1110 winc 80 binc 80` 2.2.0 answered b2b1r in ~80 ms with no info line,
// where `go depth 4` prefers h8g7. Its pass searches d8c7 and h8g7 in under 2048 nodes, the interval at
// which the search polls its limits, then spends ~270,000 nodes on the four promotions.
const orderingPassCancelFen = "3k3b/3P1KP1/8/5B2/7p/7P/1p6/8 b - - 0 92"

// prepareOrderingPassGame resets game to orderingPassCancelFen with fresh tables and a node limit
// (0 = none), records ttSeed at the root unless it is moveNone, and sets the cancel flag first when
// isCancelledBeforehand (a `stop` that arrived before the pass began).
func prepareOrderingPassGame(game *Game, maxNodes int, ttSeed move, isCancelledBeforehand bool) {
	game.newGameFromFen(orderingPassCancelFen)
	game.transpositionTable.clear()
	game.evalTable.clear()
	game.useOwnBook = false
	game.timeControl.clockType = clockInfinite
	game.timeControl.maxNodes = maxNodes
	if ttSeed != moveNone() {
		game.transpositionTable.record(game.currentPosition().hash, ttSeed, 10, nodePV)
	}
	game.isCancelPending.Store(isCancelledBeforehand)
}

// runOrderingPass runs orderRootMoves on a game prepared by prepareOrderingPassGame and returns the move
// chosen, the ordered list, and the list in generation order.
func runOrderingPass(game *Game, maxNodes int, ttSeed move, isCancelledBeforehand bool) (best move, ordered,
	generated []move) {
	prepareOrderingPassGame(game, maxNodes, ttSeed, isCancelledBeforehand)
	generated = game.currentPosition().generateLegalMoves()
	ordered = slices.Clone(generated)
	best = orderRootMoves(newSearchStateFromGame(game), ordered)
	return best, ordered, generated
}

// countCompleted returns how many moves of an ordered list carry a searched score.
func countCompleted(ordered []move) int {
	completed := 0
	for completed < len(ordered) && ordered[completed].score() != unsearchedRootMoveScore {
		completed++
	}
	return completed
}

// TestOrderingPassCancelPlaysACompletedMove places node limits through the root's ordering pass on
// orderingPassCancelFen and checks that a move whose search was interrupted is never chosen: the move
// played is the best of the moves that completed, each scored exactly as an uninterrupted pass scores it.
// The completed moves are a prefix of generation order sorted ahead of the unsearched rest, which keeps
// generation order. Each limit is also run through searchRootNode, which must play the same move:
// iterative deepening, starting with cancel already pending, returns its seed unchanged.
func TestOrderingPassCancelPlaysACompletedMove(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	_, reference, generated := runOrderingPass(game, 0, moveNone(), false)
	if game.isCancelPending.Load() {
		t.Fatal("the uncancelled reference pass was cancelled")
	}
	referenceScore := map[move]int{} // keyed by the move with its score cleared
	for _, m := range reference {
		referenceScore[m.withScore(0)] = m.score()
	}

	completedCountsSeen := map[int]bool{}
	// The limit is polled every 2048 nodes; a stride of 32768 lands inside each of the last four moves.
	for limit := 1; ; limit += 32768 {
		best, ordered, _ := runOrderingPass(game, limit, moveNone(), false)
		if !game.isCancelPending.Load() {
			break // the limit is beyond the whole pass
		}
		completed := countCompleted(ordered)
		completedCountsSeen[completed] = true
		for _, m := range ordered[:completed] {
			if index := slices.IndexFunc(generated, m.equal); index < 0 || index >= completed {
				t.Fatalf("limit %d: %s (generated #%d) is scored but only %d moves completed", limit, m, index,
					completed)
			}
			if m.score() != referenceScore[m.withScore(0)] {
				t.Errorf("limit %d: %s scored %d, the uncancelled pass %d", limit, m, m.score(),
					referenceScore[m.withScore(0)])
			}
		}
		for i, m := range ordered[completed:] {
			if m.score() != unsearchedRootMoveScore || !m.equal(generated[completed+i]) {
				t.Fatalf("limit %d: the unsearched tail %v is not generation order from #%d", limit,
					ordered[completed:], completed)
			}
		}
		if completed == 0 {
			t.Fatalf("limit %d: no move completed, which this position's pass cannot reach by a node limit", limit)
		}
		want := generated[0]
		for _, m := range generated[1:completed] {
			if referenceScore[m.withScore(0)] > referenceScore[want.withScore(0)] {
				want = m
			}
		}
		if !best.equal(want) {
			t.Errorf("limit %d (%d of %d moves completed): the ordering pass chose %s, want %s", limit, completed,
				len(generated), best, want)
		}

		prepareOrderingPassGame(game, limit, moveNone(), false)
		if played := searchRootNode(game); !played.equal(want) {
			t.Errorf("limit %d: searchRootNode played %s, want %s", limit, played, want)
		}
	}
	if len(completedCountsSeen) < 2 {
		t.Errorf("the limits stopped the pass at only %v completed moves", completedCountsSeen)
	}
}

// TestOrderingPassCancelFallsBackToTheTTMove covers the pass in which no root move completed, here
// because cancel was already pending when it began: the root's TT move is played when it is in the root
// list, and the first generated move otherwise. searchRootNode must play the same move.
func TestOrderingPassCancelFallsBackToTheTTMove(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	rootPosition := positionFromFen(orderingPassCancelFen)
	generated := rootPosition.generateLegalMoves()
	last := generated[len(generated)-1]
	notInList := moveSinglePush(squareFromString("a2"), squareFromString("a3"))
	cases := []struct {
		name   string
		ttSeed move
		want   move
	}{
		{"TT move in the root list", last, last},
		{"TT move not in the root list", notInList, generated[0]},
		{"no TT entry", moveNone(), generated[0]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			best, ordered, _ := runOrderingPass(game, 0, c.ttSeed, true)
			if completed := countCompleted(ordered); completed != 0 {
				t.Fatalf("%d moves completed with cancel already pending", completed)
			}
			if !best.equal(c.want) {
				t.Errorf("the ordering pass chose %s, want %s", best, c.want)
			}
			prepareOrderingPassGame(game, 0, c.ttSeed, true)
			if played := searchRootNode(game); !played.equal(c.want) {
				t.Errorf("searchRootNode played %s, want %s", played, c.want)
			}
		})
	}
}
