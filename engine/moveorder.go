package engine

// Move-ordering heuristics for the search: the banded sort scores (TT move first — handled in
// searchAlphaBeta — then winning captures, killers, the countermove, history-scored quiets, losing
// captures, mirroring the C++ ScoreAndSortMoves) and the killer / countermove / continuation-history
// bookkeeping that feeds them. The history table itself lives in history.go; all the tables are
// per-thread on searchState.

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
			see := evaluateStaticExchange(pos, m)
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
			h := int(ss.history.count(pos.colorToMove, m)) + ss.contHistScore(prevMove, m)
			if h > maxQuietScore {
				h = maxQuietScore
			}
			sort = h
		}
		moves[i] = m.withScore(sort)
	}
	sortMoves(moves)
}

// updateKillers records a quiet move m that caused a beta cutoff as the primary killer at ply,
// shifting the existing primary into the secondary slot; duplicates are not re-recorded.
func (ss *searchState) updateKillers(ply int, m move) {
	if !ss.killers[ply][0].equal(m) {
		ss.dbg.killerBetaCutoffs++ // count a newly recorded killer (matches C++ RecordKiller placement)
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

// applyContinuationHistoryBonus credits (bonus > 0) or debits (bonus < 0) the 1-ply continuation-history
// entry for playing m after prevMove, with the same gravity as the butterfly table. Non-quiet moves are
// skipped (captures order by SEE).
func (ss *searchState) applyContinuationHistoryBonus(prevMove, m move, bonus int32) {
	if !m.isQuiet() {
		return
	}
	entry := int32(ss.continuationHistory[prevMove.pieceTo()*contKeys+m.pieceTo()])
	applyGravity(&entry, bonus)
	ss.continuationHistory[prevMove.pieceTo()*contKeys+m.pieceTo()] = int16(entry)
}

// recordCountermove records a quiet m that caused a beta cutoff as the countermove to prevMove.
func (ss *searchState) recordCountermove(prevMove, m move) {
	if m.isQuiet() {
		ss.countermoves[prevMove.pieceTo()] = m
	}
}

// recordGood credits move m (which raised alpha, or caused a beta cutoff when isCutoff) into the
// move-ordering heuristics with a depth-weighted gravity bonus: the butterfly history and continuation
// history for m. On a beta cutoff it additionally (a) debits every other quiet move that was searched at
// this node but failed (the malus, over quietsTried) and (b) makes a quiet m the killer at ply and the
// countermove to prevMove.
func (ss *searchState) recordGood(ply, depth int, prevMove, m move, isCutoff bool, quietsTried []move) {
	c := ss.currentPosition().colorToMove
	bonus := historyBonus(depth)
	ss.history.applyBonus(c, m, bonus)
	ss.applyContinuationHistoryBonus(prevMove, m, bonus)
	if isCutoff {
		for _, quiet := range quietsTried {
			if !quiet.equal(m) {
				ss.history.applyBonus(c, quiet, -bonus)
				ss.applyContinuationHistoryBonus(prevMove, quiet, -bonus)
			}
		}
		if m.isQuiet() {
			ss.updateKillers(ply, m)
			ss.recordCountermove(prevMove, m)
		}
	}
}
