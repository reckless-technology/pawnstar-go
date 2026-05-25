package engine

import (
	"sync"
	"sync/atomic"
)

// repetitionHashEntry is a compact position record used exclusively for repetition detection.
// It captures only the two fields that isDrawByRepetition needs from a position:
// the Zobrist hash (to detect equal positions) and the half-move clock (to stop
// scanning at an irreversible move boundary).  At 16 bytes it is 10.5× smaller
// than a full position (168 bytes), which is the key to cheap worker forks.
type repetitionHashEntry struct {
	hash          zobrist // Zobrist hash of the position
	halfMoveClock uint8   // half-move clock at this position (for 50-move / repetition cutoff)
}

// searchState holds all mutable state for one search goroutine.  Both the main
// (serial) search and each parallel worker own a searchState.
//
// posStack is the copy-make stack for the goroutine's own search path.  It
// starts with exactly one entry (the position at the fork point) and grows by
// one entry for each move played.  Workers never undo past their starting
// position, so the game history is not needed here.
//
// hashStack is a compact (hash, halfMove) record for every position from the
// start of the game through the current search node.  It is the only structure
// consulted by isDrawByRepetition.  Copying hashStack when forking a worker
// costs roughly 16 bytes × n rather than 168 bytes × n for a full posStack copy.
//
// tt, history, evalTable, and game (for time control and the cancel flag) are
// shared via pointer with zero copying.
type searchState struct {
	posStack  []position            // copy-make stack: starts with 1 entry (fork position), grows during search
	hashStack []repetitionHashEntry // compact history for repetition detection: game history + search path
	nodeCount int
	tt        *transpositionTable
	history   *historyTable
	evalTable *evalHashTable
	game      *Game        // shared: time control, cancel flag, parallel semaphore
	cutoff    *atomic.Bool // per-batch cutoff signal; nil for the main search
}

// newSearchState is the private constructor shared by newSearchStateFromGame and
// newWorkerState.  hs is the caller's pre-built hashStack (ownership transfers).
// startPos is the single seed entry for posStack.
func newSearchState(hs []repetitionHashEntry, startPos *position, game *Game, cutoff *atomic.Bool) *searchState {
	ps := make([]position, 0, maxPly+4)
	ps = append(ps, *startPos)
	return &searchState{
		posStack:  ps,
		hashStack: hs,
		tt:        &game.transpositionTable,
		history:   &game.historyTable,
		evalTable: &game.evalTable,
		game:      game,
		cutoff:    cutoff,
	}
}

// newSearchStateFromGame creates a searchState for the main (serial) search.
// posStack is seeded with only the current position.  hashStack is built from
// the game's full position history so that draw-by-repetition detection can
// look back through the complete game.
func newSearchStateFromGame(game *Game) *searchState {
	n := len(game.posStack)
	hs := make([]repetitionHashEntry, n, n+maxPly+4)
	for i, pos := range game.posStack {
		hs[i] = repetitionHashEntry{pos.hash, pos.halfMove}
	}
	return newSearchState(hs, &game.posStack[n-1], game, nil)
}

// newWorkerState creates a searchState for a parallel worker.
// posStack is seeded with the parent's current position (1 entry, 168 bytes).
// hashStack is copied from the parent (compact: 16 bytes × n entries).
// All other state is shared via pointer.
func newWorkerState(parent *searchState, cutoff *atomic.Bool) *searchState {
	n := len(parent.hashStack)
	hs := make([]repetitionHashEntry, n, n+maxPly+4)
	copy(hs, parent.hashStack)
	return newSearchState(hs, &parent.posStack[len(parent.posStack)-1], parent.game, cutoff)
}

// currentPosition returns a pointer to the current (deepest) position on the stack.
func (ss *searchState) currentPosition() *position {
	return &ss.posStack[len(ss.posStack)-1]
}

// playMove appends a copy of the current position to posStack, applies m to it,
// and records the resulting (hash, halfMove) in hashStack.
func (ss *searchState) playMove(m move) {
	ss.posStack = append(ss.posStack, ss.posStack[len(ss.posStack)-1])
	ss.posStack[len(ss.posStack)-1].makeMove(m)
	pos := &ss.posStack[len(ss.posStack)-1]
	ss.hashStack = append(ss.hashStack, repetitionHashEntry{pos.hash, pos.halfMove})
}

// makeNullMove appends a copy of the current position to posStack, applies a
// null move to it, and records the resulting (hash, halfMove) in hashStack.
func (ss *searchState) makeNullMove() {
	ss.posStack = append(ss.posStack, ss.posStack[len(ss.posStack)-1])
	ss.posStack[len(ss.posStack)-1].makeNullMove()
	pos := &ss.posStack[len(ss.posStack)-1]
	ss.hashStack = append(ss.hashStack, repetitionHashEntry{pos.hash, pos.halfMove})
}

// undoMove removes the last position from posStack and the last entry from
// hashStack, reverting to the state before the most recent playMove or makeNullMove.
func (ss *searchState) undoMove() {
	ss.posStack = ss.posStack[:len(ss.posStack)-1]
	ss.hashStack = ss.hashStack[:len(ss.hashStack)-1]
}

// isDrawByFiftyMoves reports whether the 50-move rule draw condition is met.
func (ss *searchState) isDrawByFiftyMoves() bool {
	return ss.currentPosition().halfMove >= 100
}

// isDrawByRepetition returns true if the current position has occurred twice
// before in the combined game + search history (three occurrences total).
// It scans hashStack backwards in two-ply steps (same side to move) and stops
// early at an irreversible move (halfMove == 0).
func (ss *searchState) isDrawByRepetition() bool {
	n := len(ss.hashStack)
	if n < 5 {
		return false
	}
	h := ss.hashStack[n-1].hash
	repetitions := 2
	for i := n - 5; i >= 0; i -= 2 {
		if ss.hashStack[i].hash == h {
			repetitions--
			if repetitions == 0 {
				dbgx.drawsByRepetition++
				return true
			}
		}
		if ss.hashStack[i].halfMoveClock == 0 {
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

// scoreAndSortMoves assigns sort scores to moves and sorts them descending.
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
	if beta != alpha+1 {
		panic("parallel search on non null window")
	}

	dbgx.parallelSearches++

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
	dbgx.parallelLaunchCount += uint(launchCount)

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
