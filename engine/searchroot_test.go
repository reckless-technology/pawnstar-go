package engine

import (
	"slices"
	"strings"
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

// runOrderingPass runs the plain depth-3 ordering pass (orderRootMovesWith with no hard limit, so a node limit
// only cuts the pass and does not bring in the depth-1 pass) on a game prepared by prepareOrderingPassGame and returns the move
// chosen, the ordered list, and the list in generation order.
func runOrderingPass(game *Game, maxNodes int, ttSeed move, isCancelledBeforehand bool) (best move, ordered,
	generated []move) {
	prepareOrderingPassGame(game, maxNodes, ttSeed, isCancelledBeforehand)
	generated = game.currentPosition().generateLegalMoves()
	ordered = slices.Clone(generated)
	best = orderRootMovesWith(newSearchStateFromGame(game), ordered, false)
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
// generation order. This is the pass as it runs with no hard limit; under one, searchRootNode runs the
// depth-1 pass first (the tests below).
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

// TestOrderingPassCancelPrefersADeepTTMove covers a pass cancelled part-way with a TT entry for the root
// deeper than the pass, as an earlier search of the game leaves one: its move is played, even one the pass
// never reached, ahead of the best completed move. An entry at the pass's own depth, or one whose move is
// not in the root list, is passed over for the best completed move. The node limit is the first, doubling
// from 2048, at which a move completes before the cancel. Mirrors the C++ test_principal_variation case.
func TestOrderingPassCancelPrefersADeepTTMove(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	notInList := moveSinglePush(squareFromString("a2"), squareFromString("a3"))
	for limit := 2048; limit <= 1<<24; limit *= 2 {
		expected, ordered, generated := runOrderingPass(game, limit, moveNone(), false)
		if !game.isCancelPending.Load() {
			t.Fatal("no node limit cancels the ordering pass after a move has completed")
		}
		completed := countCompleted(ordered)
		if completed == 0 {
			continue
		}
		// A move the pass did not complete, so that playing it can only come from the table.
		unreached := generated[len(generated)-1]
		if completed >= len(generated)-1 || unreached.equal(expected) {
			t.Fatalf("limit %d: the last generated move %s is reached by the cancelled pass", limit, unreached)
		}
		cases := []struct {
			name  string
			seed  move
			depth int
			want  move
		}{
			{"a TT move deeper than the pass", unreached, startDepth + 1, unreached},
			{"a TT move only as deep as the pass", unreached, startDepth, expected},
			{"a deep TT move not in the root list", notInList, startDepth + 1, expected},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				prepareOrderingPassGame(game, limit, moveNone(), false)
				game.transpositionTable.record(game.currentPosition().hash, c.seed, c.depth, nodePV)
				best := orderRootMovesWith(newSearchStateFromGame(game), slices.Clone(generated), false)
				if !best.equal(c.want) {
					t.Errorf("limit %d, cancelled after %d of %d moves, depth-%d TT move %s: the ordering pass "+
						"chose %s, want %s", limit, completed, len(generated), c.depth, c.seed, best, c.want)
				}
			})
		}
		t.Logf("limit %d: cancelled after %d of %d moves; best completed %s, deep TT move %s", limit, completed,
			len(generated), expected, unreached)
		return
	}
	t.Fatal("no node limit up to 2^24 cancels the ordering pass after a move has completed")
}

// orderingBudgetOutput runs one `go` on orderingPassCancelFen through the UCI dispatcher, from a cold table with
// one thread and no book, and returns what it printed.
func orderingBudgetOutput(t *testing.T, goCommand string) string {
	t.Helper()
	game := NewGame()
	out, _ := runUCI(t, game, "setoption name Threads value 1", "setoption name OwnBook value false",
		"position fen "+orderingPassCancelFen, goCommand)
	return out
}

// TestHardLimitPrintsTheDepthOneInfoLine: the position on which 2.3.0 answered a hard limit with no info line at
// all (its depth-3 pass outlasts the budget), under each of the limits that reproduced it. A depth-1 pass costs
// 1,982 nodes here, so a line for it comes before a legal bestmove. The clock case is the 8+0.08 game's `go`.
func TestHardLimitPrintsTheDepthOneInfoLine(t *testing.T) {
	skipWithoutTheShippedNet(t)
	legal := map[string]bool{}
	rootPosition := positionFromFen(orderingPassCancelFen)
	for _, m := range rootPosition.generateLegalMoves() {
		legal[m.String()] = true
	}
	for _, goCommand := range []string{"go nodes 4096", "go nodes 65536", "go wtime 664 btime 1110 winc 80 binc 80"} {
		t.Run(goCommand, func(t *testing.T) {
			out := orderingBudgetOutput(t, goCommand)
			infoIndex := strings.Index(out, "info depth 1 ")
			bestIndex := strings.Index(out, "bestmove ")
			if infoIndex < 0 || bestIndex < 0 || infoIndex > bestIndex {
				t.Fatalf("want an `info depth 1` line before the bestmove, got:\n%s", out)
			}
			if best := bestmoveOf(out); !legal[best] {
				t.Errorf("bestmove %q is not a legal move; output:\n%s", best, out)
			}
		})
	}
}

// TestNoHardLimitPrintsNoOrderingInfoLine: a fixed-depth search has no hard limit, so the depth-3 ordering pass
// runs alone and prints nothing, as before; its first line is iterative deepening's depth 4.
func TestNoHardLimitPrintsNoOrderingInfoLine(t *testing.T) {
	skipWithoutTheShippedNet(t)
	out := orderingBudgetOutput(t, "go depth 3")
	if strings.Contains(out, "info depth") {
		t.Errorf("go depth 3 printed an info line:\n%s", out)
	}
	if bestmoveOf(out) == "" {
		t.Errorf("go depth 3 printed no bestmove:\n%s", out)
	}
}

// TestLimitedOrderingDecidesWhenTheDepthThreePassIsCut replays the hard-limit pass by hand: a depth-1 pass
// with the PV extension off over the generated moves, a stable sort, and (for a node limit that lands inside
// the depth-3 pass) the first move of that ordering as the expected answer, the table being empty. Node limits
// below the depth-1 pass's 1,982 nodes cut it, and the 2.3.0 handling plays the best of the moves it
// completed, or the first generated move when none did. Each limit is run through orderRootMoves, with the
// limit in force, and through searchRootNode.
func TestLimitedOrderingDecidesWhenTheDepthThreePassIsCut(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	prepareOrderingPassGame(game, 0, moveNone(), false)
	generated := game.currentPosition().generateLegalMoves()
	replayed := slices.Clone(generated)
	replay := newSearchStateFromGame(game)
	var replayPV variation
	replay.pvExtensionSuppressed = true
	for i, m := range replayed {
		replayed[i] = m.withScore(searchSingleMove(replay, limitedOrderingDepth, 0, initialAlpha, initialBeta, m,
			&replayPV, i))
	}
	replay.pvExtensionSuppressed = false
	depthOneNodes := replay.nodeCount
	sortMoves(replayed)
	want := replayed[0]

	for _, limit := range []int{depthOneNodes + 1, 2048, 4096, 16384, 65536, 131072} {
		for _, run := range []string{"orderRootMoves", "searchRootNode"} {
			prepareOrderingPassGame(game, limit, moveNone(), false)
			var best move
			var ordered []move
			if run == "orderRootMoves" {
				ordered = slices.Clone(generated)
				best = orderRootMoves(newSearchStateFromGame(game), ordered)
			} else {
				best = searchRootNode(game)
			}
			if !game.isCancelPending.Load() {
				t.Fatalf("limit %d: the search was not cut", limit)
			}
			if !best.equal(want) {
				t.Errorf("limit %d, %s: chose %s, want the depth-1 ordering's first move %s", limit, run, best, want)
			}
			if run == "orderRootMoves" {
				// A cut depth-3 pass is set aside whole: the list is the depth-1 ordering with depth-1 scores.
				for i, m := range ordered {
					if !m.equal(replayed[i]) || m.score() != replayed[i].score() {
						t.Fatalf("limit %d: ordered[%d] = %s (%d), want the depth-1 ordering's %s (%d)", limit, i, m,
							m.score(), replayed[i], replayed[i].score())
					}
				}
			}
		}
	}

	// A cut inside the depth-1 pass (here a `stop` already pending; a node limit is polled only every 2048
	// nodes, after the pass's 1,982) leaves the 2.3.0 handling to apply to that pass.
	prepareOrderingPassGame(game, 1, moveNone(), true)
	ordered := slices.Clone(generated)
	best := orderRootMoves(newSearchStateFromGame(game), ordered)
	if completed := countCompleted(ordered); completed != 0 || !best.equal(generated[0]) {
		t.Errorf("a limit with cancel pending completed %d moves and chose %s, want none completed and %s", completed, best,
			generated[0])
	}
}

// TestPvExtensionSuppressionIsScopedToTheDepthOnePass: the flag is off in a fresh state and after a search that
// used it, so that it cannot reach the depth-3 pass, iterative deepening or the next search on the reused state.
func TestPvExtensionSuppressionIsScopedToTheDepthOnePass(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	prepareOrderingPassGame(game, 4096, moveNone(), false)
	state := newSearchStateFromGame(game)
	if state.pvExtensionSuppressed {
		t.Fatal("a fresh search state has the PV extension suppressed")
	}
	orderRootMoves(state, game.currentPosition().generateLegalMoves())
	if state.pvExtensionSuppressed {
		t.Error("the flag is still set after the ordering pass")
	}
	state.pvExtensionSuppressed = true
	state.resetForSearch()
	if state.pvExtensionSuppressed {
		t.Error("resetForSearch left the flag set")
	}
}
