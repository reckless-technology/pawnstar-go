package engine

// The main recursive alpha-beta search (PVS + the pruning/reduction stack ported from the C++
// Pawnstar). The root driver lives in rootsearch.go, the capture resolver in quiescence.go, the
// move-ordering heuristics in moveorder.go, and the per-thread search state in search.go.

import "time"

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
//     · Reverse futility pruning: return eval when depth ≤ rfpMaxDepth and eval − rfpMargin·depth ≥ beta.
//     · Null-move pruning (R = 4): only when eval ≥ beta and there is enough material (no zugzwang).
//   - TT move tried before generating all legal moves; remaining moves ordered by scoreAndSortMoves
//     (winning captures, killers, countermove, then history + continuation history).
//   - Late move pruning: skip quiet moves at depth ≤ lmpMaxDepth once moveIdx ≥ lmpBase + depth².
//   - Late move reduction (null-window, non-check): −1 from moveIdx > 3; −2 from moveIdx > 6 (depth > 3);
//     −3 additionally when history count is zero. Re-search at full depth if the reduced search beats alpha.
//   - On a beta cutoff or alpha raise, recordGood credits the move to history / continuation history
//     (and, for a quiet cutoff, killers + countermove). TT write with Cut/All/PV node type on exit.
func searchAlphaBeta(ss *searchState, depth, ply, alpha, beta int, parentPV *variation, prevMove move) int {
	ss.nodeCount++
	ss.dbg.alphaBetaCalls++
	if ply > ss.seldepth {
		ss.seldepth = ply
	}
	// Poll the hard deadline (and the `go nodes` limit) every 2048 nodes: between polls the search
	// cannot stop, so a coarse interval (the former 64k) let a node-heavy batch overrun the deadline and
	// forfeit on time. 2048 keeps the overrun sub-millisecond while the clock read stays negligible.
	if (ss.nodeCount & 0x7FF) == 0 {
		tc := &ss.game.timeControl
		// hardDeadlineNanos is the no-deadline sentinel for fixed-depth / infinite / an in-progress ponder
		// search, so this never fires there; ponderhit swaps in a real deadline mid-search (atomic).
		timedOut := time.Now().UnixNano() >= ss.game.hardDeadlineNanos.Load()
		if timedOut || (tc.maxNodes > 0 && ss.nodeCount >= tc.maxNodes) {
			ss.game.isCancelPending.Store(true)
			return searchCancelledScore
		}
	}

	position := ss.currentPosition()
	if cat := position.materialDrawCategory(); cat != 0 {
		ss.recordMaterialDraw(cat)
		return drawScore
	}
	if ss.isDrawByFiftyMoves() || ss.isDrawByRepetition() {
		return drawScore
	}

	// Check extension: searching a node that is in check one ply deeper resolves the forcing line
	// before evaluating, and gates off the static-eval pruning below (which is disabled in check).
	if position.isInCheck() {
		ss.dbg.checkExtensions++
		depth++
	}

	if ply >= maxPly {
		ss.dbg.maxPlyReached++
		return evaluatePosition(ss)
	}

	// Transposition table lookup.
	transpos, isTransposition := ss.game.transpositionTable.find(position.hash)
	if isTransposition && transpos.depth() >= depth {
		switch transpos.nodeType() {
		case nodeCut:
			ss.dbg.tableHitCutNode++
			if transpos.move.score() >= beta {
				ss.dbg.tableHitCutNodeCutoffs++
				return transpos.move.score()
			}
		case nodeAll:
			ss.dbg.tableHitAllNode++
			if transpos.move.score() <= alpha {
				ss.dbg.tableHitAllNodeCutoffs++
				return transpos.move.score()
			}
		case nodePV:
			// PV hit: search deeper so the PV is fully verified.
			ss.dbg.tableHitPvNode++
			depth++
		}
	}

	// Internal iterative reduction: with no usable transposition-table move our move ordering is weak,
	// so rather than spend the full depth with a poor first move, reduce by one ply. The shallower
	// search still fills the TT with a best move, recouping the cost via better ordering on revisit.
	if depth >= noTTReduceMinDepth && (!isTransposition || transpos.move.isNone()) {
		ss.dbg.internalIterativeReductions++
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
		evalScore := evaluatePosition(ss)

		// Reverse futility pruning (static null move): at shallow depth, if we are far enough above
		// beta that even conceding rfpMargin per ply still leaves us at/above beta, assume a quiet
		// move holds the advantage and cut. Disabled in the mate zone so it cannot mask a forced mate.
		if depth <= rfpMaxDepth && absInt(beta) < winThreshold && evalScore-rfpMargin*depth >= beta {
			ss.dbg.reverseFutilityPrunes++
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
			ss.dbg.tableMoveBetaCutoffs++
			ss.game.transpositionTable.record(position.hash, transpos.move.withScore(score), int16(depth), nodeCut)
			ss.recordGood(ply, prevMove, transpos.move, true)
			return score
		}
		bestScore = score
		bestMove = transpos.move
		if score > alpha {
			ss.dbg.tableMoveRaisedAlpha++
			alpha = score
			hasRaisedAlpha = true
			copyVariation(parentPV, pv, transpos.move) // capture the PV now, while pv holds THIS move's line
			ss.recordGood(ply, prevMove, transpos.move, false)
		}
	}

	var ml moveList
	position.generateLegalMovesInto(&ml)
	moves := ml.slice()
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

	for i, move := range moves {
		if isTransposition && move.equal(transpos.move) {
			continue
		}

		// nullWindow is re-evaluated per move against the CURRENT alpha (not precomputed): at a PV node,
		// once an early move raises alpha to beta−1 the remaining moves are effectively a null-window
		// search, and LMP/LMR must engage for them. The C++ engine checks `beta == alpha + 1` inline in
		// the loop for exactly this reason; matching it is required for bit-identical search.
		nullWindow := beta == alpha+1

		// Late move pruning (move-count pruning): at shallow depth in a null-window node not in check,
		// once we are deep into the (SEE/history-sorted) move list, skip the remaining quiet moves —
		// they almost never raise alpha. Captures, promotions and checking moves are never pruned.
		if nullWindow && !inCheck && depth <= lmpMaxDepth && move.isQuiet() && !move.isChecking() && i >= lmpBase+depth*depth {
			ss.dbg.lateMovePrunes++
			continue
		}

		// Late move reduction: reduce late moves at null-window nodes not in check (any move type;
		// the SEE/history sort has already pushed the unpromising moves here).
		childDepth := depth
		if nullWindow && i > 3 && !inCheck && depth > 2 {
			ss.dbg.lateMoveReduction1++
			childDepth--
			if depth > 3 && i > 6 {
				ss.dbg.lateMoveReduction2++
				childDepth--
				if ss.history.count(ply, move) == 0 {
					ss.dbg.lateMoveReduction3++
					childDepth--
				}
			}
		}

		pv.clear()
		score := searchSingleMove(ss, childDepth, ply, alpha, beta, move, &pv, i)
		if score > alpha && childDepth < depth {
			ss.dbg.lateMoveReductionFails++
			score = searchSingleMove(ss, depth, ply, alpha, beta, move, &pv, i)
		}
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.game.transpositionTable.record(position.hash, move.withScore(score), int16(depth), nodeCut)
			ss.recordGood(ply, prevMove, move, true)
			ss.dbg.betaCutoffs++
			return score
		}
		if score > bestScore {
			ss.dbg.bestScoreChanged++
			bestScore = score
			bestMove = move
			if score > alpha {
				ss.dbg.pvChanged++
				alpha = score
				hasRaisedAlpha = true
				ss.recordGood(ply, prevMove, move, false)
				copyVariation(parentPV, pv, move)
			}
		}
	}

	if hasRaisedAlpha {
		ss.dbg.pvNodes++
		ss.game.transpositionTable.record(position.hash, bestMove.withScore(alpha), int16(depth), nodePV)
		ss.recordGood(ply, prevMove, bestMove, false)
		// parentPV was already set, with the correct line, at the alpha-raise above (in the TT-move block
		// or the move loop) — re-copying here with the now-stale pv would truncate the PV. Matches C++.
	} else {
		ss.dbg.allNodes++
		ss.game.transpositionTable.record(position.hash, bestMove.withScore(bestScore), int16(depth), nodeAll)
	}
	return bestScore
}
