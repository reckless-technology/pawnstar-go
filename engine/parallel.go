package engine

import (
	"sync"
	"sync/atomic"
)

// searchState holds all mutable state for one search goroutine.  Both the main
// (serial) search and each parallel worker own a searchState.
//
// posStack and nodeCount are private to each goroutine.  tt, history, evalTable,
// and game (for time control and the cancel flag) are shared via pointer with
// zero copying.
type searchState struct {
	posStack  []position // position history including game history + search path
	nodeCount int
	tt        *transpositionTable
	history   *historyTable
	evalTable *evalHashTable
	game      *Game        // shared: time control, cancel flag, parallel semaphore
	cutoff    *atomic.Bool // per-batch cutoff signal; nil for the main search
}

// newSearchStateFromGame creates a searchState for the main (serial) search.
// posStack is pre-populated from the game's current history so that draw-by-
// repetition detection can look back through the full game.
func newSearchStateFromGame(game *Game) *searchState {
	ps := make([]position, len(game.posStack), len(game.posStack)+maxPly+4)
	copy(ps, game.posStack)
	return &searchState{
		posStack:  ps,
		tt:        &game.transpositionTable,
		history:   &game.historyTable,
		evalTable: &game.evalTable,
		game:      game,
	}
}

// newWorkerState creates a searchState for a parallel worker.
// Only posStack is copied (the current search path, typically a few dozen
// positions).  All other state is shared via pointer.
func newWorkerState(parent *searchState, cutoff *atomic.Bool) *searchState {
	n := len(parent.posStack)
	ps := make([]position, n, n+maxPly+4)
	copy(ps, parent.posStack)
	return &searchState{
		posStack:  ps,
		tt:        parent.tt,
		history:   parent.history,
		evalTable: parent.evalTable,
		game:      parent.game,
		cutoff:    cutoff,
	}
}

func (ss *searchState) currentPosition() *position {
	return &ss.posStack[len(ss.posStack)-1]
}

func (ss *searchState) playMove(m move) {
	ss.posStack = append(ss.posStack, ss.posStack[len(ss.posStack)-1])
	ss.posStack[len(ss.posStack)-1].makeMove(m)
}

func (ss *searchState) makeNullMove() {
	ss.posStack = append(ss.posStack, ss.posStack[len(ss.posStack)-1])
	ss.posStack[len(ss.posStack)-1].makeNullMove()
}

func (ss *searchState) undoMove() {
	ss.posStack = ss.posStack[:len(ss.posStack)-1]
}

func (ss *searchState) isDrawByFiftyMoves() bool {
	return ss.currentPosition().halfMove >= 100
}

func (ss *searchState) isDrawByRepetition() bool {
	n := len(ss.posStack)
	if n < 5 {
		return false
	}
	h := ss.posStack[n-1].hash
	repetitions := 2
	for i := n - 5; i >= 0; i -= 2 {
		if ss.posStack[i].hash == h {
			repetitions--
			if repetitions == 0 {
				return true
			}
		}
		if ss.posStack[i].halfMove == 0 {
			return false
		}
	}
	return false
}

// isCancelled returns true when the search should stop: either the engine
// received a time-expiry or stop command, or a sibling worker found a beta
// cutoff in the current parallel batch.
func (ss *searchState) isCancelled() bool {
	return ss.game.isCancelPending.Load() || (ss.cutoff != nil && ss.cutoff.Load())
}

// canGoParallel returns true when this state is eligible to spawn parallel
// workers.  Only the main search (cutoff == nil) is allowed; workers never
// spawn sub-workers.
func (ss *searchState) canGoParallel(depth int) bool {
	return ss.cutoff == nil && depth >= 4 && len(ss.game.parallelSlots) > 0
}

func (ss *searchState) scoreAndSortMoves(moves []move, ply int) {
	scoreAndSortMovesInner(ss.currentPosition(), ss.history, moves, ply)
}

// searchMovesParallel searches the remaining moves in parallel using Young
// Brothers Wait.  skipTTMove and elderMove are moves already searched by the
// caller and are skipped here.  alpha and beta come from the calling node
// (beta == alpha+1 is guaranteed — we only parallelize null-window nodes).
//
// LMR is applied to each move based on its position in the sorted slice before
// handing off to a worker.  Workers never go parallel themselves.
//
// Returns the best score and best move found.
func searchMovesParallel(ss *searchState, moves []move, skipTTMove move, elderMove move, depth, ply, alpha, beta int) (int, move) {
	dbgx.parallelBatches++

	var cutoff atomic.Bool
	var wg sync.WaitGroup

	pos := ss.currentPosition()
	inCheck := pos.isInCheck()

	type result struct {
		score int
		m     move
	}
	resultCh := make(chan result, len(moves))

	launchCount := 0
	for idx, m := range moves {
		if (!skipTTMove.isNone() && m.equal(skipTTMove)) || m.equal(elderMove) {
			continue
		}
		if cutoff.Load() || ss.game.isCancelPending.Load() {
			break
		}

		// Apply LMR to get the child depth for this worker.
		childDepth := depth
		if depth > 2 && idx > 0 && !inCheck && !m.isChecking() &&
			m.piece() != pawn && m.piece() != king && m.captured() == noPiece {
			childDepth--
			if depth > 3 && idx > 6 && ss.history.getCount(ply, m) == 0 {
				childDepth--
			}
		}

		w := newWorkerState(ss, &cutoff)
		mCopy := m
		cd := childDepth
		wg.Add(1)
		launchCount++
		go func() {
			// Block until a parallel slot is free, then search.
			<-ss.game.parallelSlots
			defer func() {
				ss.game.parallelSlots <- struct{}{}
				wg.Done()
			}()
			if cutoff.Load() || ss.game.isCancelPending.Load() {
				resultCh <- result{initialAlpha, mCopy}
				return
			}
			var pv variation
			score := searchSingleMove(w, cd, ply, alpha, beta, mCopy, &pv, 0)
			if score > alpha {
				cutoff.Store(true)
				dbgx.parallelCutoffs++
			}
			resultCh <- result{score, mCopy}
		}()
	}
	dbgx.parallelMovesSaved += uint(launchCount)

	wg.Wait()
	close(resultCh)

	bestScore := initialAlpha
	bestMove := moveNone()
	for r := range resultCh {
		if r.score > bestScore {
			bestScore = r.score
			bestMove = r.m
		}
	}
	return bestScore, bestMove
}
