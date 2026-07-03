package engine

// Quiescence search: resolve tactical noise at the horizon by searching captures/promotions (with
// SEE pruning) until the position is quiet, standing pat on the static eval. An in-check node
// re-enters searchAlphaBeta so all evasions are considered. Called from searchAlphaBeta at depth 0.

// searchQuiescent extends the search by generating captures and promotions until
// the position is quiet.  An in-check position re-enters searchAlphaBeta at the
// same depth (no depth decrement) so that all evasions are considered.
func searchQuiescent(ss *searchState, depth, ply, alpha, beta int) int {
	ss.dbg.quiescentCalls++
	if ply > ss.seldepth {
		ss.seldepth = ply
	}
	if ply >= maxPly {
		ss.dbg.quiescentMaxPly++
		return evaluatePosition(ss)
	}
	pos := ss.currentPosition()
	if pos.isInCheck() {
		ss.dbg.quiescentChecks++
		var dummy variation
		return searchAlphaBeta(ss, depth, ply, alpha, beta, &dummy, moveNone())
	}
	// Stand-pat: if the static eval already beats beta we can prune.
	score := evaluatePosition(ss)
	if score >= beta {
		ss.dbg.quiescentEvalBetaCutoffs++
		return score
	}
	if score > alpha {
		alpha = score
		ss.dbg.quiescentEvalRaisesAlpha++
	}
	bestScore := score
	var ml moveList
	pos.generateLegalCapturesInto(&ml)
	moves := ml.slice()
	ss.scoreAndSortMoves(moves, ply, moveNone())
	for _, m := range moves {
		// SEE pruning: captures are sorted by SEE, and their sort score is negative exactly when the
		// capture loses material. Once we reach the first losing capture every remaining move also
		// loses, so stop searching them.
		if m.score() < 0 {
			ss.dbg.quiescentSeePrunes++
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
				ss.dbg.quiescentPvChanged++
				ss.history.recordGoodMove(ply, m)
			}
		}
	}
	ss.dbg.quiescentAllNodes++
	return bestScore
}
