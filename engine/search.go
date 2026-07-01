package engine

import (
	"fmt"
	"strings"
	"sync"
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

// searchState holds all mutable state for one Lazy SMP search thread. Each thread owns an independent
// searchState searching the whole tree; the threads share only the lockless transposition table and
// eval cache (via game). Everything on searchState — posStack, hashStack, killers, history,
// continuation history, countermoves, the accumulator stack, node count, and diagnostics — is
// per-thread, so no thread ever writes state another reads (which keeps the concurrent search
// data-race free without locks).
//
// posStack is the copy-make stack for the search path.  It starts with exactly one entry (the current
// position) and grows by one entry for each move played.
//
// hashStack is a compact (hash, halfMove) record for every position from the start of the game through
// the current search node.  It is the only structure consulted by isDrawByRepetition.
type searchState struct {
	posStack  []position       // copy-make stack: one entry per move played; seeded with the current position
	hashStack []hashStackEntry // full history from game start through current node, for repetition detection
	killers   [maxPly][2]move  // per-ply quiet moves that recently caused beta cutoffs
	history   historyTable     // per-thread history heuristic (per-thread to avoid Lazy-SMP contention)
	nodeCount int              // nodes searched since the last root iteration reset
	seldepth  int              // deepest ply reached this search (for UCI `info seldepth`)
	game      *Game            // shared: time control, cancel flag, lockless TT + eval cache
	dbg       *debugX          // diagnostic counters: the global dbgx for the main thread, a throwaway per helper

	// Move-ordering follow-up heuristics keyed by the previous move (persist across a whole search).
	// countermoves[prev.pieceTo()] is the best quiet refutation observed to the previous move.
	// continuationHistory[prev.pieceTo()*contKeys + move.pieceTo()] scores playing move after prev.
	countermoves        [contKeys]move
	continuationHistory []int16

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
		posStack:            ps,
		hashStack:           hs,
		game:                game,
		continuationHistory: make([]int16, contKeys*contKeys),
		dbg:                 &dbgx, // main thread / single-threaded default; helper threads get their own
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
				ss.dbg.drawsByRepetition++
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

// scoreAndSortMoves assigns each move a sort score and sorts best-first, mirroring the C++
// ScoreAndSortMoves banded scheme: winning captures/promotions (by SEE) on top, then the two killers,
// then the countermove to prevMove, then quiet moves by history + continuation history (clamped just
// below the countermove); losing captures score by their negative SEE and sink below the quiet moves.
// prevMove is the move played at the parent (moveNone at the root, after a null move, and in qsearch).
func (ss *searchState) scoreAndSortMoves(moves []move, ply int, prevMove move) {
	pos := ss.currentPosition()
	killer0 := ss.killers[ply][0]
	killer1 := ss.killers[ply][1]
	for i, m := range moves {
		var sort int
		switch {
		case !m.isQuiet(): // capture or promotion: order by static exchange evaluation
			see, _ := evaluateStaticExchange(pos, m)
			if see >= 0 {
				sort = winningCaptureBase + see
			} else {
				sort = see // losing captures sort below quiet moves
			}
		case killer0.equal(m):
			sort = killerScore0
		case killer1.equal(m):
			sort = killerScore1
		case ss.isCountermove(prevMove, m):
			sort = countermoveScore
		default: // quiet move: main history + continuation history, clamped below the countermove
			h := int(ss.history.getCount(ply, m)) + ss.contHistScore(prevMove, m)
			if h > maxQuietScore {
				h = maxQuietScore
			}
			sort = h
		}
		moves[i] = m.withScore(sort)
	}
	sortMovesUnstable(moves)
}

// updateKillers records a quiet move m that caused a beta cutoff as the primary killer at ply,
// shifting the existing primary into the secondary slot; duplicates are not re-recorded.
func (ss *searchState) updateKillers(ply int, m move) {
	if !ss.killers[ply][0].equal(m) {
		ss.killers[ply][1] = ss.killers[ply][0]
		ss.killers[ply][0] = m
	}
}

// contHistScore returns the continuation-history score for playing m after prevMove.
func (ss *searchState) contHistScore(prevMove, m move) int {
	return int(ss.continuationHistory[prevMove.pieceTo()*contKeys+m.pieceTo()])
}

// isCountermove reports whether m is the recorded countermove (best quiet refutation) to prevMove.
func (ss *searchState) isCountermove(prevMove, m move) bool {
	return ss.countermoves[prevMove.pieceTo()].equal(m)
}

// recordContinuationHistory rewards a quiet m that was good (raised alpha or cut) after prevMove.
// Captures are ignored (they are ordered by SEE). The count saturates like the history table.
func (ss *searchState) recordContinuationHistory(prevMove, m move) {
	if !m.isQuiet() {
		return
	}
	idx := prevMove.pieceTo()*contKeys + m.pieceTo()
	if ss.continuationHistory[idx] < 16384 {
		ss.continuationHistory[idx]++
	}
}

// recordCountermove records a quiet m that caused a beta cutoff as the countermove to prevMove.
func (ss *searchState) recordCountermove(prevMove, m move) {
	if m.isQuiet() {
		ss.countermoves[prevMove.pieceTo()] = m
	}
}

// recordGood credits move m (which raised alpha, or caused a beta cutoff when isCutoff) into the
// move-ordering heuristics: the main history and continuation history for any move; additionally, on a
// beta cutoff, a quiet m becomes a killer at ply and the countermove to prevMove.
func (ss *searchState) recordGood(ply int, prevMove, m move, isCutoff bool) {
	ss.history.recordGoodMove(ply, m)
	ss.recordContinuationHistory(prevMove, m)
	if isCutoff && m.isQuiet() {
		ss.updateKillers(ply, m)
		ss.recordCountermove(prevMove, m)
		ss.dbg.killerBetaCutoffs++
	}
}

// variation is a line of play (principal variation).
type variation struct {
	moves []move
}

// clear empties the variation. Called before each searchSingleMove so a child that does not raise
// alpha (a cutoff or all-node) leaves no stale tail behind — otherwise the parent's reconstructed PV
// would splice on a move from a previously searched sibling, producing an "illegal PV move".
func (v *variation) clear() { v.moves = v.moves[:0] }

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
		ss.dbg.pvsAttempts++
		score = -searchAlphaBeta(ss, childDepth, ply+1, -alpha-1, -alpha, pv, m)
		if score > alpha {
			// PVS failed high — re-search with full window.
			ss.dbg.pvsFails++
			score = -searchAlphaBeta(ss, childDepth, ply+1, -beta, -alpha, pv, m)
		}
	} else {
		score = -searchAlphaBeta(ss, childDepth, ply+1, -beta, -alpha, pv, m)
	}
	ss.undoMove()
	return score
}

// searchAlphaBeta is the main recursive alpha-beta function. The pruning/reduction stack is ported
// from the C++ Pawnstar (constants carry over because the NNUE eval is the same centipawn scale).
//
// prevMove is the move played at the parent (moveNone at the root, after a null move, and in qsearch);
// it keys the countermove and continuation-history move-ordering heuristics.
//
// Techniques applied (in order):
//   - Periodic time check every 65 536 nodes; draw detection (material, 50-move, repetition).
//   - Check extension: a node in check is searched one ply deeper.
//   - Transposition table lookup: Cut/All nodes prune; PV nodes extend depth by 1.
//   - Internal iterative reduction: −1 ply at depth ≥ noTTReduceMinDepth when there is no TT move.
//   - Quiescence search at depth ≤ 0 (when not in check).
//   - At non-PV, non-check nodes the static eval is computed once and drives:
//       · Reverse futility pruning: return eval when depth ≤ rfpMaxDepth and eval − rfpMargin·depth ≥ beta.
//       · Null-move pruning (R = 4): only when eval ≥ beta and there is enough material (no zugzwang).
//   - TT move tried before generating all legal moves; remaining moves ordered by scoreAndSortMoves
//     (winning captures, killers, countermove, then history + continuation history).
//   - Late move pruning: skip quiet moves at depth ≤ lmpMaxDepth once moveIdx ≥ lmpBase + depth².
//   - Late move reduction (null-window, non-check): −1 from moveIdx > 3; −2 from moveIdx > 6 (depth > 3);
//     −3 additionally when history count is zero. Re-search at full depth if the reduced search beats alpha.
//   - On a beta cutoff or alpha raise, recordGood credits the move to history / continuation history
//     (and, for a quiet cutoff, killers + countermove). TT write with Cut/All/PV node type on exit.
func searchAlphaBeta(ss *searchState, depth, ply, alpha, beta int, parentPV *variation, prevMove move) int {
	ss.nodeCount++
	if ply > ss.seldepth {
		ss.seldepth = ply
	}
	// Poll the hard deadline (and the `go nodes` limit) every 2048 nodes: between polls the search
	// cannot stop, so a coarse interval (the former 64k) let a node-heavy batch overrun the deadline and
	// forfeit on time. 2048 keeps the overrun sub-millisecond while the clock read stays negligible.
	if (ss.nodeCount & 0x7FF) == 0 {
		tc := &ss.game.timeControl
		timedOut := (tc.clockType == clockStandard || tc.clockType == clockFixedTime) && time.Now().After(tc.stopTime)
		if timedOut || (tc.maxNodes > 0 && ss.nodeCount >= tc.maxNodes) {
			ss.game.isCancelPending.Store(true)
			return searchCancelledScore
		}
	}

	position := ss.currentPosition()
	if position.isDrawByMaterial() || ss.isDrawByFiftyMoves() || ss.isDrawByRepetition() {
		return drawScore
	}

	// Check extension: searching a node that is in check one ply deeper resolves the forcing line
	// before evaluating, and gates off the static-eval pruning below (which is disabled in check).
	if position.isInCheck() {
		ss.dbg.checkExtensions++
		depth++
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
				ss.dbg.tableHitBeta++
				return transpos.move.score()
			}
		case nodeAll:
			if transpos.move.score() <= alpha {
				ss.dbg.tableHitAlpha++
				return transpos.move.score()
			}
		case nodePV:
			// PV hit: search deeper so the PV is fully verified.
			ss.dbg.tableHitPv++
			depth++
		}
	}

	// Internal iterative reduction: with no usable transposition-table move our move ordering is weak,
	// so rather than spend the full depth with a poor first move, reduce by one ply. The shallower
	// search still fills the TT with a best move, recouping the cost via better ordering on revisit.
	if depth >= noTTReduceMinDepth && (!isTransposition || transpos.move.isNone()) {
		ss.dbg.iirReductions++
		depth--
	}

	// Enter quiescence at depth <= 0 (unless in check, which quiescence handles
	// by re-entering alpha-beta at the same depth).
	if depth <= 0 && !position.isInCheck() {
		return searchQuiescent(ss, depth, ply, alpha, beta)
	}

	// Static-eval-based pruning at non-PV nodes that are not in check. The static eval is computed
	// once here and reused by reverse-futility pruning and null-move pruning.
	isPV := beta-alpha > 1
	if !isPV && !position.isInCheck() {
		evalScore := evaluatePosition(ss, alpha, beta)

		// Reverse futility pruning (static null move): at shallow depth, if we are far enough above
		// beta that even conceding rfpMargin per ply still leaves us at/above beta, assume a quiet
		// move holds the advantage and cut. Disabled in the mate zone so it cannot mask a forced mate.
		if depth <= rfpMaxDepth && absInt(beta) < winThreshold && evalScore-rfpMargin*depth >= beta {
			ss.dbg.rfpPrunes++
			return evalScore
		}

		// Null-move pruning: skip our turn and search at depth−4.  Only when we are already failing
		// high statically (evalScore ≥ beta), have enough material to avoid zugzwang, and the previous
		// move was not itself a null move.
		if evalScore >= beta && !position.isNullMove() &&
			position.colors[position.colorToMove].popCount() > 3 {
			ss.makeNullMove()
			ss.dbg.nullMoveAttempts++
			var dummy variation
			score := -searchAlphaBeta(ss, depth-4, ply+1, -beta, -alpha, &dummy, moveNone())
			ss.undoMove()
			if ss.isCancelled() {
				return searchCancelledScore
			}
			if score >= beta {
				return beta
			}
			ss.dbg.nullMoveFails++
		}
	}

	// Try the transposition table move first. If we can get a beta cutoff from the table move
	// this will save us the effort of generating moves.
	var pv variation
	bestMove := moveNone()
	bestScore := initialAlpha
	hasRaisedAlpha := false

	if isTransposition && !transpos.move.isNone() {
		ss.dbg.tableMove++
		pv.clear()
		score := searchSingleMove(ss, depth, ply, alpha, beta, transpos.move, &pv, 0)
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.dbg.tableMoveBetaCutoff++
			ss.game.transpositionTable.record(position.hash, transpos.move.withScore(score), int16(depth), nodeCut)
			ss.recordGood(ply, prevMove, transpos.move, true)
			return score
		}
		bestScore = score
		bestMove = transpos.move
		if score > alpha {
			ss.dbg.tableMoveRaiseAlpha++
			alpha = score
			hasRaisedAlpha = true
			ss.recordGood(ply, prevMove, transpos.move, false)
		}
	}

	moves := position.generateLegalMoves()
	if len(moves) == 0 {
		if position.isInCheck() {
			ss.dbg.checkmates++
			return checkmatedScore + ply
		}
		ss.dbg.stalemates++
		return drawScore
	}

	ss.scoreAndSortMoves(moves, ply, prevMove)

	inCheck := position.isInCheck()
	nullWindow := beta == alpha+1

	for i, move := range moves {
		if isTransposition && move.equal(transpos.move) {
			continue
		}

		// Late move pruning (move-count pruning): at shallow depth in a null-window node not in check,
		// once we are deep into the (SEE/history-sorted) move list, skip the remaining quiet moves —
		// they almost never raise alpha. Captures and promotions are never pruned.
		if nullWindow && !inCheck && depth <= lmpMaxDepth && move.isQuiet() && i >= lmpBase+depth*depth {
			ss.dbg.lmpPrunes++
			continue
		}

		// Late move reduction: reduce late moves at null-window nodes not in check (any move type;
		// the SEE/history sort has already pushed the unpromising moves here).
		childDepth := depth
		if nullWindow && i > 3 && !inCheck && depth > 2 {
			ss.dbg.lmrAttempts1++
			childDepth--
			if depth > 3 && i > 6 {
				ss.dbg.lmrAttempts2++
				childDepth--
				if ss.history.getCount(ply, move) == 0 {
					ss.dbg.lmrAttempts3++
					childDepth--
				}
			}
		}

		pv.clear()
		score := searchSingleMove(ss, childDepth, ply, alpha, beta, move, &pv, i)
		if score > alpha && childDepth < depth {
			ss.dbg.lmrFails++
			score = searchSingleMove(ss, depth, ply, alpha, beta, move, &pv, i)
		}
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.game.transpositionTable.record(position.hash, move.withScore(score), int16(depth), nodeCut)
			ss.recordGood(ply, prevMove, move, true)
			ss.dbg.nodesCut++
			return score
		}
		if score > bestScore {
			bestScore = score
			bestMove = move
			if score > alpha {
				ss.dbg.nodesPvChanged++
				alpha = score
				hasRaisedAlpha = true
				ss.recordGood(ply, prevMove, move, false)
				copyVariation(parentPV, pv, move)
			}
		}
	}

	if hasRaisedAlpha {
		ss.dbg.nodesPv++
		ss.game.transpositionTable.record(position.hash, bestMove.withScore(alpha), int16(depth), nodePV)
		ss.recordGood(ply, prevMove, bestMove, false)
		copyVariation(parentPV, pv, bestMove)
	} else {
		ss.dbg.nodesAll++
		ss.game.transpositionTable.record(position.hash, bestMove.withScore(bestScore), int16(depth), nodeAll)
	}
	return bestScore
}

// searchQuiescent extends the search by generating captures and promotions until
// the position is quiet.  An in-check position re-enters searchAlphaBeta at the
// same depth (no depth decrement) so that all evasions are considered.
func searchQuiescent(ss *searchState, depth, ply, alpha, beta int) int {
	ss.dbg.quiescentCalls++
	if ply > ss.seldepth {
		ss.seldepth = ply
	}
	if ply >= maxPly {
		return evaluatePosition(ss, alpha, beta)
	}
	pos := ss.currentPosition()
	if pos.isInCheck() {
		ss.dbg.quiescentChecks++
		var dummy variation
		return searchAlphaBeta(ss, depth, ply, alpha, beta, &dummy, moveNone())
	}
	// Stand-pat: if the static eval already beats beta we can prune.
	score := evaluatePosition(ss, alpha, beta)
	if score >= beta {
		ss.dbg.quiescentEvalCutoffs++
		return score
	}
	if score > alpha {
		alpha = score
		ss.dbg.quiescentEvalRaisesAlpha++
	}
	bestScore := score
	moves := pos.generateLegalCaptures()
	ss.scoreAndSortMoves(moves, ply, moveNone())
	for _, m := range moves {
		// SEE pruning: captures are sorted by SEE, and their sort score is negative exactly when the
		// capture loses material. Once we reach the first losing capture every remaining move also
		// loses, so stop searching them.
		if m.score() < 0 {
			ss.dbg.qsearchSeePrunes++
			return bestScore
		}
		ss.playMove(m)
		score = -searchQuiescent(ss, depth-1, ply+1, -beta, -alpha)
		ss.undoMove()
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.history.recordGoodMove(ply, m)
			ss.dbg.quiescentBetaCutoffs++
			return score
		}
		if score > bestScore {
			bestScore = score
			if score > alpha {
				alpha = score
				ss.history.recordGoodMove(ply, m)
			}
		}
	}
	ss.dbg.quiescentAllNodes++
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
	searchMoves := game.timeControl.searchMoves
	// Opening book — skipped when OwnBook is off, or when `go searchmoves` restricts the root.
	if game.useOwnBook && len(searchMoves) == 0 {
		if bookMove := game.openingBook.getMove(game.currentPosition().hash); bookMove != moveNone() {
			return bookMove
		}
	}

	moves := game.currentPosition().generateLegalMoves()
	// `go searchmoves`: restrict the root move list to the requested moves (ignored if none are legal
	// here, so a malformed list cannot wedge the search).
	if len(searchMoves) > 0 {
		var filtered []move
		for _, m := range moves {
			for _, s := range searchMoves {
				if m.String() == s {
					filtered = append(filtered, m)
					break
				}
			}
		}
		if len(filtered) > 0 {
			moves = filtered
		}
	}
	if len(moves) == 0 {
		return moveNone()
	}
	if len(moves) == 1 && len(searchMoves) == 0 {
		return moves[0]
	}

	// Set up time management: arm the hard deadline and get the soft budget for between-iteration stops.
	tc := &game.timeControl
	startTime := time.Now()
	allocatedTime := tc.armDeadline(startTime, game.moveOverhead)
	game.isCancelPending.Store(false)
	game.transpositionTable.age()

	ss := newSearchStateFromGame(game)

	// Initial shallow pass to get move ordering before deepening.
	var initPV variation
	for i := range moves {
		m := moves[i]
		score := searchSingleMove(ss, startDepth, 0, initialAlpha, initialBeta, m, &initPV, i)
		moves[i] = m.withScore(score)
	}
	sortMovesStable(moves)
	bestMove := moves[0]

	// Lazy SMP: each thread runs an independent iterative-deepening search of the same root, sharing
	// only the lockless transposition table and eval cache (history/killers/continuation/countermoves
	// are per-thread on each searchState). The main thread (id 0) is authoritative — it prints info,
	// applies time management, and returns the move — while helper threads deepen the shared TT to
	// accelerate it, desynchronised by the per-thread iteration-skip schedule. Each helper gets its own
	// searchState, a private throwaway diagnostics struct, and its own copy of the move list.
	nThreads := game.threadCount
	if nThreads < 1 {
		nThreads = 1
	} else if nThreads > maxSearchThreads {
		nThreads = maxSearchThreads
	}
	var helpers sync.WaitGroup
	for i := 1; i < nThreads; i++ {
		helpers.Add(1)
		// The move-list clone and seed move are passed as arguments so they are evaluated here, in the
		// launching goroutine, before the main thread's iterativeDeepen begins mutating moves/bestMove.
		go func(threadID int, helperMoves []move, seed move) {
			defer helpers.Done()
			helper := newSearchStateFromGame(game)
			helper.dbg = &debugX{} // helper diagnostics are discarded; keep them off the global dbgx
			iterativeDeepen(helper, helperMoves, seed, startTime, allocatedTime, threadID)
		}(i, append([]move(nil), moves...), bestMove)
	}

	bestMove = iterativeDeepen(ss, moves, bestMove, startTime, allocatedTime, 0)
	game.nodeCount = ss.nodeCount // main-thread node total (used by `bench`)

	game.isCancelPending.Store(true) // main thread is done; signal helpers to stop
	helpers.Wait()
	return bestMove
}

// lazySmpSkipSize / lazySmpSkipPhase drive the Stockfish-style per-helper iteration-skip schedule:
// helper thread t (1-based via (t-1) mod len) skips iteration depth d when ((d + phase) / size) is odd,
// so helpers desynchronise from the main thread and each other and prefill the shared TT with diverse
// entries instead of recomputing the same tree in lockstep. The main thread (id 0) never skips.
var (
	lazySmpSkipSize  = [20]int{1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4}
	lazySmpSkipPhase = [20]int{0, 1, 0, 1, 2, 3, 0, 1, 2, 3, 4, 5, 0, 1, 2, 3, 4, 5, 6, 7}
)

// iterativeDeepen runs one thread's iterative-deepening search over its own move list and returns its
// best move. threadID 0 is the authoritative main thread (prints info, does soft time management);
// helper threads (id ≥ 1) follow the iteration-skip schedule and stay silent. All threads share the
// lockless TT and eval cache via ss.game; the hard deadline (checked inside searchAlphaBeta) and the
// game cancel flag stop every thread together.
func iterativeDeepen(ss *searchState, moves []move, bestMove move, startTime time.Time, allocatedTime time.Duration, threadID int) move {
	isMain := threadID == 0
	tc := &ss.game.timeControl
	var principalVariation variation
	var bestMoves []move
	prevScore := bestMove.score() // seed for aspiration windows

	for depth := startDepth + 1; depth < maxPly; depth++ {
		if tc.clockType == clockFixedDepth && depth > tc.depth {
			break
		}
		if !isMain { // helper iteration-skip schedule
			s := (threadID - 1) % len(lazySmpSkipSize)
			if ((depth+lazySmpSkipPhase[s])/lazySmpSkipSize[s])%2 != 0 {
				continue
			}
		}
		sortMovesStable(moves)
		bestMoves = append(bestMoves, bestMove)

		alphaAspiration := max(initialAlpha, prevScore-aspirationDelta)
		betaAspiration := min(initialBeta, prevScore+aspirationDelta)
		alpha := alphaAspiration

		for attempt := 0; attempt < 2; attempt++ {
			var pv variation
			alpha = alphaAspiration
			hasFailedHigh := false
			hasRaisedAlpha := false

			for i, move := range moves {
				// Report the move currently being searched at the root, but only once a search is slow
				// enough that a GUI benefits — avoids flooding info lines on the fast shallow iterations.
				if isMain && time.Since(startTime) > 3*time.Second {
					fmt.Printf("info depth %d currmove %s currmovenumber %d\n", depth, move.String(), i+1)
				}
				pv.clear()
				score := searchSingleMove(ss, depth, 0, alpha, betaAspiration, move, &pv, i)
				moves[i] = move.withScore(score)
				if ss.isCancelled() {
					return bestMove
				}
				if score > alpha {
					hasRaisedAlpha = true
					alpha = score
					bestMove = move
					if isMain {
						copyVariation(&principalVariation, pv, bestMove)
						elapsedMs := time.Since(startTime).Milliseconds()
						nps := int64(ss.nodeCount) * 1000 / max(1, elapsedMs)
						fmt.Printf("info depth %d seldepth %d score %s time %d nodes %d nps %d hashfull %d pv %s\n",
							depth, ss.seldepth, formatScore(alpha), elapsedMs, ss.nodeCount, nps,
							ss.game.transpositionTable.hashfull(), pvString(principalVariation))
					}
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
			ss.dbg.aspirationFails++
			alphaAspiration = initialAlpha
			betaAspiration = initialBeta
		}
		prevScore = alpha // update seed for next depth's aspiration window

		if alpha > winThreshold || alpha < loseThreshold {
			break
		}

		if isMain && tc.clockType == clockStandard {
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

// formatScore renders a search score as a UCI `score` field: `mate N` inside the mate band (positive
// = we deliver mate in N moves, negative = we are mated in N), otherwise `cp N`. Mate scores are
// ±(mateValue − plies_to_mate), so plies_to_mate = mateValue − |score| and the move count rounds up.
func formatScore(score int) string {
	const mateValue = -checkmatedScore
	switch {
	case score >= winThreshold:
		return fmt.Sprintf("mate %d", (mateValue-score+1)/2)
	case score <= loseThreshold:
		return fmt.Sprintf("mate %d", -((mateValue + score + 1) / 2))
	default:
		return fmt.Sprintf("cp %d", score)
	}
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
