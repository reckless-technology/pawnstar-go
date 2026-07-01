package engine

import (
	"fmt"
	"strings"
	"time"
)

// hashStackEntry is a compact position record used exclusively by isDrawByRepetition.
// Storing only the Zobrist hash and the half-move clock (16 bytes total) instead of a
// full position (168 bytes) keeps the repetition scan cache-friendly and the per-search
// history cheap to build.
type hashStackEntry struct {
	hash          zobrist // Zobrist hash identifies the position
	halfMoveClock uint8   // clock value at this position; a zero entry marks an irreversible move boundary
}

// searchState holds all mutable state for the search.  The search runs single-threaded on the
// goroutine launched by startThinking; there is exactly one searchState per search.  (Lazy SMP, where
// each thread would own an independent searchState searching the whole tree, is planned but not yet
// reintroduced.)
//
// posStack is the copy-make stack for the search path.  It starts with exactly one entry (the current
// position) and grows by one entry for each move played.
//
// hashStack is a compact (hash, halfMove) record for every position from the start of the game through
// the current search node.  It is the only structure consulted by isDrawByRepetition.
//
// tt, history, evalTable, and game (for time control and the cancel flag) are shared via pointer.
type searchState struct {
	posStack  []position       // copy-make stack: one entry per move played; seeded with the current position
	hashStack []hashStackEntry // full history from game start through current node, for repetition detection
	killers   [maxPly][2]move  // per-ply quiet moves that recently caused beta cutoffs
	nodeCount int              // nodes searched since the last root iteration reset
	game      *Game            // shared: time control, cancel flag, TT, history, eval table

	// NNUE lazy accumulator stack, indexed by posStack depth: accStack[i] is the accumulator for
	// posStack[i], valid iff accValid[i]. Maintained deferred — playMove/makeNullMove only mark the
	// new depth invalid; the accumulator is brought current on demand by currentAccumulator (nearest
	// valid ancestor + forward updates) only when an eval actually reads it (on eval-cache miss), so
	// cut-off nodes never pay for it. undoMove is free: ancestors stay valid, and the popped depth is
	// re-invalidated by the next playMove into that slot. Both slices are nil when nnue is disabled.
	accStack []nnueAccumulator
	accValid []bool
}

// newSearchState is the private constructor used by newSearchStateFromGame.  hs is the caller's
// pre-built hashStack (ownership transfers).  startPos is the single seed entry for posStack.
func newSearchState(hs []hashStackEntry, startPos *position, game *Game) *searchState {
	ps := make([]position, 1, maxPly)
	ps[0] = *startPos
	ss := &searchState{
		posStack:  ps,
		hashStack: hs,
		game:      game,
	}
	// Seed the current position's accumulator (depth 0); deeper accumulators are computed on demand.
	// accStack grows lazily to the search depth so construction stays cheap.
	if game.nnue != nil {
		ss.accStack = make([]nnueAccumulator, 1, 8)
		ss.accValid = make([]bool, 1, maxPly+8)
		game.nnue.refresh(&ss.accStack[0], &ss.posStack[0])
		ss.accValid[0] = true
	}
	return ss
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
	return newSearchState(hs, &game.posStack[n-1], game)
}

// currentPosition returns a pointer to the current (deepest) position on the stack.
func (ss *searchState) currentPosition() *position {
	return &ss.posStack[len(ss.posStack)-1]
}

// ensureAccDepth grows the lazy accumulator stack so depth d is indexable. Growth appends zeroed
// (invalid) entries and happens only when the search first reaches a new maximum depth.
func (ss *searchState) ensureAccDepth(d int) {
	for len(ss.accStack) <= d {
		ss.accStack = append(ss.accStack, nnueAccumulator{})
		ss.accValid = append(ss.accValid, false)
	}
}

// invalidateAcc marks depth d's accumulator stale (its position just changed). O(1) — no computation.
func (ss *searchState) invalidateAcc(d int) {
	ss.ensureAccDepth(d)
	ss.accValid[d] = false
}

// currentAccumulator brings the current (top) position's accumulator up to date and returns it.
// It walks back to the deepest valid ancestor, then applies incremental updates forward one ply at a
// time (copy the parent accumulator, apply the piece delta). Called only on an eval-cache miss, so a
// node that never evaluates never triggers any accumulator work. accValid[0] is seeded true at
// construction, so the backward walk always terminates.
func (ss *searchState) currentAccumulator() *nnueAccumulator {
	d := len(ss.posStack) - 1
	ss.ensureAccDepth(d)
	k := d
	for k > 0 && !ss.accValid[k] {
		k--
	}
	for j := k + 1; j <= d; j++ {
		ss.accStack[j] = ss.accStack[j-1]
		ss.game.nnue.update(&ss.accStack[j], &ss.posStack[j-1], &ss.posStack[j])
		ss.accValid[j] = true
	}
	return &ss.accStack[d]
}

// playMove appends a copy of the current position to posStack, applies m to it,
// and records the resulting (hash, halfMove) in hashStack.
func (ss *searchState) playMove(m move) {
	n := len(ss.posStack)
	ss.posStack = append(ss.posStack, ss.posStack[n-1])
	ss.posStack[n].makeMove(m)
	pos := &ss.posStack[n]
	ss.hashStack = append(ss.hashStack, hashStackEntry{pos.hash, pos.halfMove})
	if ss.game.nnue != nil {
		ss.invalidateAcc(n) // depth n's accumulator is now stale; computed on demand by currentAccumulator
	}
}

// makeNullMove appends a copy of the current position to posStack, applies a
// null move to it, and records the resulting (hash, halfMove) in hashStack.
func (ss *searchState) makeNullMove() {
	n := len(ss.posStack)
	ss.posStack = append(ss.posStack, ss.posStack[n-1])
	ss.posStack[n].makeNullMove()
	pos := &ss.posStack[n]
	ss.hashStack = append(ss.hashStack, hashStackEntry{pos.hash, pos.halfMove})
	// A null move leaves piece placement unchanged, so the deferred update is a no-op copy; still mark
	// the new depth invalid so a deeper eval recomputes through it correctly.
	if ss.game.nnue != nil {
		ss.invalidateAcc(n)
	}
}

// undoMove removes the last position from posStack and the last entry from
// hashStack, reverting to the state before the most recent playMove or makeNullMove.
// The accumulator needs no work: ancestor accumulators stay valid, and the popped depth's slot is
// re-invalidated by the next playMove into it.
func (ss *searchState) undoMove() {
	n := len(ss.posStack)
	ss.posStack = ss.posStack[:n-1]
	ss.hashStack = ss.hashStack[:len(ss.hashStack)-1]
}

// isDrawByFiftyMoves reports whether the 50-move rule draw condition is met.
func (ss *searchState) isDrawByFiftyMoves() bool {
	return ss.currentPosition().halfMove >= 100
}

// isDrawByRepetition reports whether the current position has appeared twice
// before in the combined game + search history (three occurrences total).
// The scan steps backwards in two-ply increments so only positions with the
// same side to move are compared.  It stops early at an irreversible move
// (halfMoveClock == 0) because repetitions cannot cross a pawn move or capture.
// A minimum of five entries is required: positions 0, 2, and 4 would be the
// earliest possible three-fold repetition.
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

// isCancelled returns true when the search should stop because the engine received a time-expiry or
// stop command.
func (ss *searchState) isCancelled() bool {
	return ss.game.isCancelPending.Load()
}

// scoreAndSortMoves scores and sorts moves at ply using SEE for captures,
// killer bonuses for quiet moves that previously caused cutoffs, and the
// history heuristic for all quiet moves.
func (ss *searchState) scoreAndSortMoves(moves []move, ply int) {
	scoreAndSortMovesInner(ss.currentPosition(), &ss.game.historyTable, ss.killers[ply], moves, ply)
}

// updateKillers records m as the primary killer at ply, shifting the existing
// primary into the secondary slot.  Only nonCapture moves are stored; captures
// are already ordered by SEE and do not benefit from the killer heuristic.
// A move already occupying the primary slot is not duplicated.
func (ss *searchState) updateKillers(ply int, m move) {
	if m.kind() != nonCapture {
		return
	}
	if !ss.killers[ply][0].equal(m) {
		ss.killers[ply][1] = ss.killers[ply][0]
		ss.killers[ply][0] = m
	}
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
	if isTransposition && transpos.depth() >= depth {
		switch transpos.nodeType() {
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
		position.colors[position.colorToMove].popCount() > 3 {
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

		alphaAspiration := max(initialAlpha, prevScore-aspirationDelta)
		betaAspiration := min(initialBeta, prevScore+aspirationDelta)
		alpha := alphaAspiration

		for attempt := 0; attempt < 2; attempt++ {
			var pv variation
			alpha = alphaAspiration
			hasFailedHigh := false
			hasRaisedAlpha := false

			for i, move := range moves {
				score := searchSingleMove(ss, depth, 0, alpha, betaAspiration, move, &pv, i)
				moves[i] = move.withScore(score)
				if ss.isCancelled() {
					return bestMove
				}
				if score > alpha {
					hasRaisedAlpha = true
					alpha = score
					bestMove = move
					copyVariation(&principalVariation, pv, bestMove)
					pvStr := pvString(principalVariation)
					fmt.Printf("info depth %2d score cp %4d time %5d nodes %7d pv %s\n",
						depth, alpha, time.Since(startTime).Milliseconds(), ss.nodeCount, pvStr)
					if score >= betaAspiration {
						hasFailedHigh = true
						break // window failed high — remaining moves must not be searched with inverted window
					}
				}
			}

			if ss.isCancelled() {
				return bestMove
			}

			if hasRaisedAlpha && !hasFailedHigh {
				break
			}
			dbgx.aspirationFails++
			alphaAspiration = initialAlpha
			betaAspiration = initialBeta
		}
		prevScore = alpha // update seed for next depth's aspiration window

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
