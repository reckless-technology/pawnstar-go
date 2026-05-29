package engine

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// hashStackEntry is a compact position record used exclusively for repetition detection.
// It captures only the two fields that isDrawByRepetition needs from a position:
// the Zobrist hash (to detect equal positions) and the half-move clock (to stop
// scanning at an irreversible move boundary).  At 16 bytes it is 10.5× smaller
// than a full position (168 bytes), which is the key to cheap worker forks.
type hashStackEntry struct {
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
	posStack  []position       // copy-make stack: starts with 1 entry (fork position), grows during search
	hashStack []hashStackEntry // compact history for repetition detection: game history + search path
	killers   [maxPly][2]move  // killer moves: quiet moves that caused beta cutoffs, indexed by ply
	nodeCount int              // Node count for this goroutine.
	game      *Game            // shared: time control, cancel flag, parallel semaphore, TT, history, eval table
	wasCutoff *atomic.Bool     // per-batch cutoff signal; nil for the main search
}

// newSearchState is the private constructor shared by newSearchStateFromGame and
// newWorkerState.  hs is the caller's pre-built hashStack (ownership transfers).
// startPos is the single seed entry for posStack.
func newSearchState(hs []hashStackEntry, startPos *position, game *Game, cutoff *atomic.Bool) *searchState {
	ps := make([]position, 1, maxPly)
	ps[0] = *startPos
	return &searchState{
		posStack:  ps,
		hashStack: hs,
		game:      game,
		wasCutoff: cutoff,
	}
}

// newSearchStateFromGame creates a searchState for the main (serial) search.
// posStack is seeded with only the current position.  hashStack is built from
// the game's full position history so that draw-by-repetition detection can
// look back through the complete game.
func newSearchStateFromGame(game *Game) *searchState {
	n := len(game.posStack)
	hs := make([]hashStackEntry, n, n+maxPly)
	for i, pos := range game.posStack {
		hs[i] = hashStackEntry{pos.hash, pos.halfMove}
	}
	return newSearchState(hs, &game.posStack[n-1], game, nil)
}

// newWorkerState creates a searchState for a parallel worker.
// posStack is seeded with the parent's current position (1 entry, 168 bytes).
// hashStack is copied from the parent (compact: 16 bytes × n entries).
// All other state is shared via pointer.
func newWorkerState(parent *searchState, cutoff *atomic.Bool) *searchState {
	n := len(parent.hashStack)
	hs := make([]hashStackEntry, n, n+maxPly+4)
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
	n := len(ss.posStack)
	ss.posStack = append(ss.posStack, ss.posStack[n-1])
	ss.posStack[n].makeMove(m)
	pos := &ss.posStack[n]
	ss.hashStack = append(ss.hashStack, hashStackEntry{pos.hash, pos.halfMove})
}

// makeNullMove appends a copy of the current position to posStack, applies a
// null move to it, and records the resulting (hash, halfMove) in hashStack.
func (ss *searchState) makeNullMove() {
	n := len(ss.posStack)
	ss.posStack = append(ss.posStack, ss.posStack[n-1])
	ss.posStack[n].makeNullMove()
	pos := &ss.posStack[n]
	ss.hashStack = append(ss.hashStack, hashStackEntry{pos.hash, pos.halfMove})
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
	return (ss.wasCutoff != nil && ss.wasCutoff.Load()) || ss.game.isCancelPending.Load()
}

// canGoParallel returns true when this state is eligible to spawn parallel
// workers.  Only the main search (cutoff == nil) is allowed; workers never
// spawn sub-workers.
func (ss *searchState) canGoParallel(depth int) bool {
	return ss.wasCutoff == nil && depth >= 4 && len(ss.game.parallelSlots) > 0
}

// scoreAndSortMoves assigns sort scores to moves and sorts them descending.
func (ss *searchState) scoreAndSortMoves(moves []move, ply int) {
	scoreAndSortMovesInner(ss.currentPosition(), &ss.game.historyTable, ss.killers[ply], moves, ply)
}

// updateKillers records m as a killer at ply if it is a quiet move and not
// already the first killer.  The second killer is displaced by the first.
func (ss *searchState) updateKillers(ply int, m move) {
	if m.kind() != nonCapture {
		return
	}
	if !ss.killers[ply][0].equal(m) {
		ss.killers[ply][1] = ss.killers[ply][0]
		ss.killers[ply][0] = m
	}
}

// runWorker is the body of a parallel search goroutine.  It blocks until a
// slot is free, searches m with a null window, signals cutoff on fail-high,
// and sends the scored move to resultCh.
func runWorker(ss *searchState, move move, depth, ply, alpha, beta int, resultCh chan<- move, wg *sync.WaitGroup) {
	if beta != alpha+1 {
		panic("runWorker called on non null window")
	}
	<-ss.game.parallelSlots
	defer func() {
		ss.game.parallelSlots <- struct{}{}
		wg.Done()
	}()
	if ss.isCancelled() {
		resultCh <- move.withScore(searchCancelledScore)
		return
	}
	var pv variation
	score := searchSingleMove(ss, depth, ply, alpha, beta, move, &pv, 0)
	if ss.isCancelled() {
		resultCh <- move.withScore(searchCancelledScore)
		return
	}
	if score >= beta {
		// Signal to other workers to stop since we found a beta cutoff (null window).
		ss.wasCutoff.Store(true)
		dbgx.parallelCutoffs++
	}
	resultCh <- move.withScore(score)
}

// searchMovesParallel dispatches moves to parallel workers and returns the best move found (with score packed in).
func searchMovesParallel(ss *searchState, moves []move, depth, ply, alpha, beta int) move {
	if beta != alpha+1 {
		panic("searchMovesParallel called on non null window")
	}

	dbgx.parallelSearches++

	var cutoff atomic.Bool
	var wg sync.WaitGroup
	resultChan := make(chan move, len(moves))

	for _, move := range moves {
		if cutoff.Load() || ss.isCancelled() {
			break
		}
		dbgx.parallelMovesSearched++
		wg.Add(1)
		go runWorker(newWorkerState(ss, &cutoff), move, depth, ply, alpha, beta, resultChan, &wg)
	}

	wg.Wait()
	close(resultChan)

	bestScore := initialAlpha
	bestMove := moveNone()
	for r := range resultChan {
		if r.score() > bestScore {
			bestScore = r.score()
			bestMove = r
			if bestScore >= beta {
				return bestMove
			}
		}
	}
	return bestMove
}

// variation is a line of play (principal variation).
type variation struct {
	moves []move
}

// copyVariation sets dst to best followed by all moves in src.
func copyVariation(dst *variation, src variation, best move) {
	dst.moves = dst.moves[:0]
	dst.moves = append(dst.moves, best)
	dst.moves = append(dst.moves, src.moves...)
}

// searchSingleMove plays m, calls searchAlphaBeta recursively, undoes the move,
// and returns the score.  When the position is not a PV node and the move is not
// the first tried, a null-window PVS search is attempted first; if it fails high,
// a full-window re-search is performed.
func searchSingleMove(ss *searchState, depth, ply, alpha, beta int, m move, pv *variation, moveIdx int) int {
	wasInCheck := ss.currentPosition().isInCheck()
	childDepth := depth - 1
	ss.playMove(m)
	isChecking := ss.currentPosition().isInCheck()
	var score int
	if beta > alpha+1 && moveIdx > 0 && !wasInCheck && !isChecking {
		// Try principal variation search.
		dbgx.pvsAttempts++
		score = -searchAlphaBeta(ss, childDepth, ply+1, -alpha-1, -alpha, pv)
		if score > alpha {
			// PVS failed high — re-search with full window.
			dbgx.pvsFails++
			score = -searchAlphaBeta(ss, childDepth, ply+1, -beta, -alpha, pv)
		}
	} else {
		score = -searchAlphaBeta(ss, childDepth, ply+1, -beta, -alpha, pv)
	}
	ss.undoMove()
	return score
}

// searchAlphaBeta is the main recursive alpha-beta function.
//
// Techniques applied (in order):
//   - Periodic time check every 65 536 nodes.
//   - Draw detection: material, 50-move rule, repetition.
//   - Transposition table lookup: Cut/All nodes prune; PV nodes extend depth by 1.
//   - Quiescence search at depth ≤ 0 (when not in check).
//   - Null-move pruning with R = 4 (skipped in check, zugzwang-risk positions, non-null windows).
//   - TT move tried before generating all legal moves.
//   - Late move reduction: −1 ply from the 2nd move onward (moveIdx > 0, quiet, non-check, non-pawn/king, depth > 2);
//     −2 ply additionally from moveIdx 7 (depth > 3, zero history count).
//   - LMR re-search at full depth if the reduced search exceeds alpha.
//   - After elder brother search at null-window nodes (depth ≥ 4): remaining moves
//     searched in parallel via Young Brothers Wait.
//   - TT write with Cut/All/PV node type on exit.
func searchAlphaBeta(ss *searchState, depth, ply, alpha, beta int, parentPV *variation) int {
	ss.nodeCount++
	if (ss.nodeCount&0xFFFF) == 0 && (ss.game.timeControl.clockType == clockStandard || ss.game.timeControl.clockType == clockFixedTime) &&
		time.Now().After(ss.game.timeControl.stopTime) {
		ss.game.isCancelPending.Store(true)
		return searchCancelledScore
	}

	position := ss.currentPosition()
	if position.isDrawByMaterial() || ss.isDrawByFiftyMoves() || ss.isDrawByRepetition() {
		return drawScore
	}

	if ply >= maxPly {
		return evaluatePosition(ss, alpha, beta)
	}

	// Transposition table lookup.
	transpos, isTransposition := ss.game.transpositionTable.find(position.hash)
	if isTransposition && int(transpos.depth) >= depth {
		switch transpos.nodeType {
		case nodeCut:
			if transpos.move.score() >= beta {
				dbgx.tableHitBeta++
				return transpos.move.score()
			}
		case nodeAll:
			if transpos.move.score() <= alpha {
				dbgx.tableHitAlpha++
				return transpos.move.score()
			}
		case nodePV:
			// PV hit: search deeper so the PV is fully verified.
			dbgx.tableHitPv++
			depth++
		}
	}

	// Enter quiescence at depth <= 0 (unless in check, which quiescence handles
	// by re-entering alpha-beta at the same depth).
	if depth <= 0 && !position.isInCheck() {
		return searchQuiescent(ss, depth, ply, alpha, beta)
	}

	// Null-move pruning: skip our turn and search at depth−4.  If the result still
	// beats beta, the position is likely good enough to prune.  Skipped when:
	// already in a null-move position, in check, not a null window, or in an
	// endgame scenario.
	pstScore := position.scores[white] - position.scores[black]
	if position.colorToMove == black {
		pstScore = -pstScore
	}
	if beta == alpha+1 && pstScore > (beta+100) && !position.isNullMove() && !position.isInCheck() &&
		position.piecesOfColor(position.colorToMove).popCount() > 3 {
		ss.makeNullMove()
		dbgx.nullMoveAttempts++
		var dummy variation
		score := -searchAlphaBeta(ss, depth-4, ply+1, -beta, -alpha, &dummy)
		ss.undoMove()
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			return beta
		}
		dbgx.nullMoveFails++

	}

	// Try the transposition table move first. If we can get a beta cutoff from the table move
	// this will save us the effort of generating moves.
	var pv variation
	bestMove := moveNone()
	bestScore := initialAlpha
	hasRaisedAlpha := false

	if isTransposition && !transpos.move.isNone() {
		dbgx.tableMove++
		score := searchSingleMove(ss, depth, ply, alpha, beta, transpos.move, &pv, 0)
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			dbgx.tableMoveBetaCutoff++
			ss.game.transpositionTable.record(position.hash, transpos.move.withScore(score), int16(depth), nodeCut)
			ss.game.historyTable.recordGoodMove(ply, transpos.move)
			ss.updateKillers(ply, transpos.move)
			return score
		}
		bestScore = score
		bestMove = transpos.move
		if score > alpha {
			dbgx.tableMoveRaiseAlpha++
			alpha = score
			hasRaisedAlpha = true
			ss.game.historyTable.recordGoodMove(ply, transpos.move)
		}
	}

	moves := position.generateLegalMoves()
	if len(moves) == 0 {
		if position.isInCheck() {
			dbgx.checkmates++
			return checkmatedScore + ply
		}
		dbgx.stalemates++
		return drawScore
	}

	ss.scoreAndSortMoves(moves, ply)

	for i, move := range moves {
		if isTransposition && move.equal(transpos.move) {
			continue
		}

		childDepth := depth
		if depth > 2 && i > 0 && beta == alpha+1 && !position.isInCheck() && move.kind() == nonCapture {
			dbgx.lmrAttempts1++
			childDepth--
			if depth > 3 && i > 6 && ss.game.historyTable.getCount(ply, move) == 0 {
				dbgx.lmrAttempts2++
				childDepth--
			}
		}

		score := searchSingleMove(ss, childDepth, ply, alpha, beta, move, &pv, i)
		if score > alpha && childDepth < depth {
			dbgx.lmrFails++
			score = searchSingleMove(ss, depth, ply, alpha, beta, move, &pv, i)
		}
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.game.transpositionTable.record(position.hash, move.withScore(score), int16(depth), nodeCut)
			ss.game.historyTable.recordGoodMove(ply, move)
			ss.updateKillers(ply, move)
			if move.kind() == nonCapture {
				dbgx.killerBetaCutoffs++
			}
			dbgx.nodesCut++
			return score
		}
		if score > bestScore {
			bestScore = score
			bestMove = move
			if score > alpha {
				dbgx.nodesPvChanged++
				alpha = score
				hasRaisedAlpha = true
				ss.game.historyTable.recordGoodMove(ply, move)
				copyVariation(parentPV, pv, move)
			}
		}
		if i > 1 {
			// We have searched moves up to and including the ith move without achieving a cutoff.
			// Search the remaining moves in parallel on the assumption this is probably an all node
			// and no move will raise alpha.
			if !ss.isCancelled() && beta == alpha+1 && ss.canGoParallel(depth) {
				parallelBest := searchMovesParallel(ss, moves[i+1:], depth, ply, alpha, beta)
				if ss.isCancelled() {
					return searchCancelledScore
				}
				parallelScore := parallelBest.score()
				if parallelScore >= beta {
					ss.game.transpositionTable.record(position.hash, parallelBest, int16(depth), nodeCut)
					ss.updateKillers(ply, parallelBest)
					return parallelScore
				}
				if parallelScore > bestScore {
					bestScore = parallelScore
					bestMove = parallelBest
				}
				break
			}
		}

	}

	if hasRaisedAlpha {
		dbgx.nodesPv++
		ss.game.transpositionTable.record(position.hash, bestMove.withScore(alpha), int16(depth), nodePV)
		ss.game.historyTable.recordGoodMove(ply, bestMove)
		copyVariation(parentPV, pv, bestMove)
	} else {
		dbgx.nodesAll++
		ss.game.transpositionTable.record(position.hash, bestMove.withScore(bestScore), int16(depth), nodeAll)
	}
	return bestScore
}

// searchQuiescent extends the search by generating captures and promotions until
// the position is quiet.  An in-check position re-enters searchAlphaBeta at the
// same depth (no depth decrement) so that all evasions are considered.
func searchQuiescent(ss *searchState, depth, ply, alpha, beta int) int {
	dbgx.quiescentCalls++
	if ply >= maxPly {
		return evaluatePosition(ss, alpha, beta)
	}
	pos := ss.currentPosition()
	if pos.isInCheck() {
		dbgx.quiescentChecks++
		var dummy variation
		return searchAlphaBeta(ss, depth, ply, alpha, beta, &dummy)
	}
	// Stand-pat: if the static eval already beats beta we can prune.
	score := evaluatePosition(ss, alpha, beta)
	if score >= beta {
		dbgx.quiescentEvalCutoffs++
		return score
	}
	if score > alpha {
		alpha = score
		dbgx.quiescentEvalRaisesAlpha++
	}
	bestScore := score
	moves := pos.generateLegalCaptures()
	ss.scoreAndSortMoves(moves, ply)
	for _, m := range moves {
		ss.playMove(m)
		score = -searchQuiescent(ss, depth-1, ply+1, -beta, -alpha)
		ss.undoMove()
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.game.historyTable.recordGoodMove(ply, m)
			dbgx.quiescentBetaCutoffs++
			return score
		}
		if score > bestScore {
			bestScore = score
			if score > alpha {
				alpha = score
				ss.game.historyTable.recordGoodMove(ply, m)
			}
		}
	}
	dbgx.quiescentAllNodes++
	return bestScore
}

// searchRootNode performs iterative-deepening search and returns the best move.
//
// If the opening book contains a move for the current position, it is returned
// immediately.  Otherwise, an initial pass at startDepth (3) scores and orders
// the legal moves, then deepening continues until the time limit, a forced
// mate/loss, or an early-exit heuristic triggers.
//
// Early-exit conditions (Standard clock only):
//   - Best move consistent AND score stable across all completed depths: exit if elapsed > ¼ of budget.
//   - Either condition alone: exit if elapsed > ½ of budget.
//   - Hard stop if elapsed > budget.
func searchRootNode(game *Game) move {
	// Check opening book.
	bookMove := game.openingBook.getMove(game.currentPosition().hash)
	if bookMove != moveNone() {
		return bookMove
	}

	moves := game.currentPosition().generateLegalMoves()
	if len(moves) == 0 {
		return moveNone()
	}
	if len(moves) == 1 {
		return moves[0]
	}

	// Set up time management.
	allocatedTime := time.Duration(0)
	tc := &game.timeControl
	switch tc.clockType {

	case clockStandard:
		if tc.numMovesRemaining > 0 {
			allocatedTime = tc.timeRemaining / time.Duration(tc.numMovesRemaining)
		} else {
			allocatedTime = tc.timeRemaining
		}
		hardStopDuration := max(100*time.Millisecond, min(allocatedTime*2, tc.timeRemaining-time.Second))
		tc.stopTime = time.Now().Add(hardStopDuration)

	case clockFixedTime:
		tc.stopTime = time.Now().Add(tc.timeRemaining)
	}

	startTime := time.Now()
	game.isCancelPending.Store(false)
	game.historyTable.reset()
	game.transpositionTable.age()

	ss := newSearchStateFromGame(game)

	var principalVariation variation
	var bestMoves []move

	// Initial shallow pass to get move ordering before deepening.
	var initPV variation
	for i := range moves {
		m := moves[i]
		score := searchSingleMove(ss, startDepth, 0, initialAlpha, initialBeta, m, &initPV, i)
		moves[i] = m.withScore(score)
	}
	sortMovesStable(moves)
	bestMove := moves[0]
	prevScore := bestMove.score() // seed for aspiration windows

	for depth := startDepth + 1; depth < maxPly; depth++ {
		if tc.clockType == clockFixedDepth && depth > tc.depth {
			break
		}
		sortMovesStable(moves)
		bestMoves = append(bestMoves, bestMove)
		ss.nodeCount = 0

		aspirAlpha := max(initialAlpha, prevScore-aspirDelta)
		aspirBeta := min(initialBeta, prevScore+aspirDelta)
		alpha := initialAlpha

		for attempt := 0; attempt < 2; attempt++ {
			var pv variation
			alpha = aspirAlpha
			failHigh := false

			for i, move := range moves {
				score := searchSingleMove(ss, depth, 0, alpha, aspirBeta, move, &pv, i)
				moves[i] = move.withScore(score)
				if ss.isCancelled() {
					return bestMove
				}
				if score > alpha {
					alpha = score
					bestMove = move
					copyVariation(&principalVariation, pv, bestMove)
					pvStr := pvString(principalVariation)
					fmt.Printf("info depth %2d score cp %4d time %5d nodes %7d pv %s\n",
						depth, alpha, time.Since(startTime).Milliseconds(), ss.nodeCount, pvStr)
					if score >= aspirBeta {
						failHigh = true
						break // window failed high — remaining moves must not be searched with inverted window
					}
				}
			}

			if (!failHigh && alpha > aspirAlpha) || attempt == 1 {
				prevScore = alpha
				break
			}
			if ss.isCancelled() {
				return bestMove
			}
			dbgx.aspirationFails++
			aspirAlpha = initialAlpha
			aspirBeta = initialBeta
		}

		if alpha > winThreshold || alpha < loseThreshold {
			break
		}

		if tc.clockType == clockStandard {
			elapsedTime := time.Since(startTime)
			isBestMoveConsistent := allSame(bestMoves, bestMove)
			isScoreStable := allScoreStable(bestMoves, bestMove)
			if isBestMoveConsistent && isScoreStable && elapsedTime*4 > allocatedTime {
				fmt.Println("info string Best move consistent and score stable - search terminated.")
				break
			}
			if (isBestMoveConsistent || isScoreStable) && elapsedTime*2 > allocatedTime {
				if isBestMoveConsistent {
					fmt.Println("info string Best move consistent - search terminated.")
				} else {
					fmt.Println("info string Score stable - search terminated.")
				}
				break
			}
			if elapsedTime > allocatedTime {
				break
			}
		}
	}
	return bestMove
}

// pvString formats a principal variation as a space-separated UCI move list.
func pvString(pv variation) string {
	var b strings.Builder
	for i, move := range pv.moves {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(move.String())
	}
	return b.String()
}

// allSame returns true if every move in moves has the same identity as best.
func allSame(moves []move, best move) bool {
	for _, move := range moves {
		if !move.equal(best) {
			return false
		}
	}
	return true
}

// allScoreStable returns true if every move in moves has a score within
// scoreInstabilityThresh centipawns of best's score.
func allScoreStable(moves []move, best move) bool {
	for _, move := range moves {
		if absInt(move.score()-best.score()) > scoreInstabilityThresh {
			return false
		}
	}
	return true
}

// absInt returns the absolute value of x.
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
