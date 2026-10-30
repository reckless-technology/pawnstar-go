package engine

// The main recursive alpha-beta search (PVS + the pruning/reduction stack ported from the C++
// Pawnstar). The root driver lives in searchroot.go, the capture resolver in searchquiescent.go, the
// move-ordering heuristics in moveorder.go, and the per-thread search state in search.go.

import "time"

// searchAlphaBeta is the main recursive alpha-beta function. The pruning/reduction stack is ported
// from the C++ Pawnstar (constants carry over because the NNUE eval is the same centipawn scale).
//
// prevMove is the move played at the parent (moveNone at the root, after a null move, and in qsearch);
// it keys the countermove and continuation-history move-ordering heuristics.
//
// Techniques applied (in order):
//   - Periodic time check every 65 536 nodes; draw detection (material, 50-move, repetition), with a
//     checkmate on the hundredth ply scored as mate, not the fifty-move draw (FIDE 5.1.1).
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
//     −3 from moveIdx > 12. Re-search at full depth if the reduced search beats alpha.
//   - On a beta cutoff or alpha raise, recordGood credits the move to history / continuation history
//     (and, for a quiet cutoff, killers + countermove). TT write with Cut/All/PV node type on exit.
func searchAlphaBeta(ss *searchState, depth, ply, alpha, beta int, parentPV *variation, prevMove move) int {
	ss.nodeCount++
	ss.dbg.alphaBetaCalls++
	ss.seldepth = max(ss.seldepth, ply)
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

	pos := ss.currentPosition()
	inCheck := pos.isInCheck() // constant for the whole node (copy-make: moves apply to copies)
	if cat := pos.materialDrawCategory(); cat != 0 {
		ss.recordMaterialDraw(cat)
		return drawScore
	}
	if ss.isDrawByFiftyMoves() {
		// Checkmate takes precedence over the fifty-move draw (FIDE 5.1.1; 9.6.2 says so of the 75-move
		// rule): a mate delivered on the move that completes the fifty stands. So in check the clock draws
		// only when the side to move has a legal move; with none it is mated, scored here at this ply as the
		// move loop's own checkmate is. Out of check there can be no mate, and the clock draws whatever the
		// moves. Mirrors the C++ Search.
		if inCheck {
			var evasions moveList
			pos.generateLegalMovesInto(&evasions)
			if evasions.count == 0 {
				ss.dbg.checkmates++
				return checkmatedScore + ply
			}
		}
		return drawScore
	}
	if ss.isDrawByRepetition() {
		return drawScore
	}

	// Check extension: searching a node that is in check one ply deeper resolves the forcing line
	// before evaluating, and gates off the static-eval pruning below (which is disabled in check).
	if inCheck {
		ss.dbg.checkExtensions++
		depth++
	}

	if ply >= maxPly {
		ss.dbg.maxPlyReached++
		return evaluatePositionNonDraw(ss) // material draw was excluded at node entry
	}

	// Transposition table lookup.
	transpos, isTransposition := ss.game.transpositionTable.find(pos.hash)
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
	if depth <= 0 && !inCheck {
		return searchQuiescent(ss, depth, ply, alpha, beta)
	}

	// Static-eval-based pruning at non-PV nodes that are not in check. The static eval is computed
	// once here and reused by reverse-futility pruning and null-move pruning.
	isPV := beta-alpha > 1
	if !isPV && !inCheck {
		evalScore := evaluatePositionNonDraw(ss) // material draw was excluded at node entry

		// Reverse futility pruning (static null move): at shallow depth, if we are far enough above
		// beta that even conceding rfpMargin per ply still leaves us at/above beta, assume a quiet
		// move holds the advantage and cut. Disabled in the mate zone so it cannot mask a forced mate.
		//
		// The mate bound is written as the C++ `-kCheckmatedScore - kMaxPly` and the Rust
		// `-CHECKMATED_SCORE - MAX_PLY` are, i.e. 31836, rather than as winThreshold (31000). Those two
		// tests are equivalent today, but only CONTINGENTLY, and the invariant they rest on is worth
		// stating because nothing in the test suite guards it:
		//
		//   The only scores outside the ±nnueMaxEval (30000) eval clamp are mate scores, and every mate
		//   score is `checkmatedScore + ply` (the two sites above: the fifty-move-precedence checkmate
		//   and the move loop's). `ply` cannot exceed maxPly, because a node at ply >= maxPly returns a
		//   static eval before the move loop. So |mate| lies in [-checkmatedScore - maxPly,
		//   -checkmatedScore] = [31836, 31900]. A node's beta is either -alpha of its parent (both the
		//   full window and the null-move window negate and swap) or alpha+1 of its grandparent (PVS
		//   hands the child (-alpha-1, -alpha), so the grandchild's beta is -(-alpha-1)). The smallest
		//   |beta| reachable above winThreshold is therefore 31836 exactly - and this test is strict, so
		//   it excludes that too. The open interval (winThreshold, 31836) over which the two spellings
		//   disagree holds no value beta can take.
		//
		// Measured before the spelling was changed, with the Go RFP site instrumented to classify beta at
		// every site it reached: 0 sites in that interval across ~146 million sites over twelve runs -
		// bench, seven mate positions (including KBN-v-K, a position where the side to move is the one
		// being mated, and the promotion race that drives the search to maxPly) and three deep ordinary
		// searches. Not a vacuous probe: the true mate zone took 2,889,367 sites in the promotion race
		// alone, 2,630,029 of which would have pruned had the guard allowed it, and the closest |beta|
		// ever came to the bound was 31,846. Bench held at 6,601,454 - the v12 net's signature, which
		// release 2.0.0 later moved to 5,617,906 - on both the SIMD and the scalar path, and node counts
		// stayed identical to the C++ and Rust ports.
		//
		// So: RAISE maxPly, or change the mate-score encoding, and winThreshold silently stops meaning
		// the same thing as this bound - a divergence from the other two ports that would appear only in
		// positions a bench never reaches, and so one the bit-exactness gate could not see. Keeping the
		// three ports spelled alike is what makes that invariant not load-bearing.
		if depth <= rfpMaxDepth && abs(beta) < -checkmatedScore-maxPly && evalScore-rfpMargin*depth >= beta {
			ss.dbg.reverseFutilityPrunes++
			return evalScore
		}

		// Null-move pruning: skip our turn and search at depth−4.  Only when we are already failing
		// high statically (evalScore ≥ beta), have enough material to avoid zugzwang, and the previous
		// move was not itself a null move.
		if evalScore >= beta && !pos.isNullMove() &&
			pos.colors[pos.colorToMove].popCount() > 3 {
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

	// quietsTried collects the quiet moves searched at this node (TT move included) so a later beta
	// cutoff can penalise the ones that failed (the history malus in recordGood). Backed by a stack
	// array to avoid a per-node heap allocation; capped at 64 (deeper is rare and the tail matters least).
	var quietsBuffer [64]move
	quietsTried := quietsBuffer[:0]

	if isTransposition && !transpos.move.isNone() {
		ss.dbg.tableMove++
		pv.clear()
		score := searchSingleMove(ss, depth, ply, alpha, beta, transpos.move, &pv, 0)
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.dbg.tableMoveBetaCutoffs++
			ss.game.transpositionTable.record(pos.hash, transpos.move.withScore(score), depth, nodeCut)
			ss.recordGood(ply, depth, prevMove, transpos.move, true, quietsTried)
			return score
		}
		if transpos.move.isQuiet() {
			quietsTried = append(quietsTried, transpos.move)
		}
		bestScore = score
		bestMove = transpos.move
		if score > alpha {
			ss.dbg.tableMoveRaisedAlpha++
			alpha = score
			hasRaisedAlpha = true
			copyVariation(parentPV, pv, transpos.move) // capture the PV now, while pv holds THIS move's line
			ss.recordGood(ply, depth, prevMove, transpos.move, false, quietsTried)
		}
	}

	var ml moveList
	pos.generateLegalMovesInto(&ml)
	moves := ml.slice()
	if len(moves) == 0 {
		if inCheck {
			ss.dbg.checkmates++
			return checkmatedScore + ply
		}
		ss.dbg.stalemates++
		return drawScore
	}

	ss.scoreAndSortMoves(moves, ply, prevMove)

	for i, m := range moves {
		if isTransposition && m.equal(transpos.move) {
			continue
		}

		// nullWindow is re-evaluated per move against the CURRENT alpha (not precomputed): at a PV node,
		// once an early move raises alpha to beta−1 the remaining moves are effectively a null-window
		// search, and LMP/LMR must engage for them. The C++ engine checks `beta == alpha + 1` inline in
		// the loop for exactly this reason; matching it is required for bit-identical search.
		nullWindow := beta == alpha+1

		// Late move pruning (move-count pruning): at shallow depth in a null-window node not in check,
		// once we are deep into the (SEE/history-sorted) move list, skip the remaining quiet moves —
		// they almost never raise alpha. Only quiet moves are eligible — captures and promotions are not quiet, so they never prune here.
		if nullWindow && !inCheck && depth <= lmpMaxDepth && m.isQuiet() && i >= lmpBase+depth*depth {
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
				if i > 12 {
					ss.dbg.lateMoveReduction3++
					childDepth--
				}
			}
		}

		// Record this quiet move as tried before searching it, so a cutoff by a later sibling penalises
		// it (the eventual cutoff move is excluded in recordGood).
		if m.isQuiet() && len(quietsTried) < cap(quietsTried) {
			quietsTried = append(quietsTried, m)
		}

		pv.clear()
		score := searchSingleMove(ss, childDepth, ply, alpha, beta, m, &pv, i)
		if score > alpha && childDepth < depth {
			ss.dbg.lateMoveReductionFails++
			score = searchSingleMove(ss, depth, ply, alpha, beta, m, &pv, i)
		}
		if ss.isCancelled() {
			return searchCancelledScore
		}
		if score >= beta {
			ss.game.transpositionTable.record(pos.hash, m.withScore(score), depth, nodeCut)
			ss.recordGood(ply, depth, prevMove, m, true, quietsTried)
			ss.dbg.betaCutoffs++
			return score
		}
		if score > bestScore {
			ss.dbg.bestScoreChanged++
			bestScore = score
			bestMove = m
			if score > alpha {
				ss.dbg.pvChanged++
				alpha = score
				hasRaisedAlpha = true
				ss.recordGood(ply, depth, prevMove, m, false, quietsTried)
				copyVariation(parentPV, pv, m)
			}
		}
	}

	if hasRaisedAlpha {
		ss.dbg.pvNodes++
		ss.game.transpositionTable.record(pos.hash, bestMove.withScore(alpha), depth, nodePV)
		ss.recordGood(ply, depth, prevMove, bestMove, false, quietsTried)
		// parentPV was already set, with the correct line, at the alpha-raise above (in the TT-move block
		// or the move loop) — re-copying here with the now-stale pv would truncate the PV. Matches C++.
	} else {
		ss.dbg.allNodes++
		ss.game.transpositionTable.record(pos.hash, bestMove.withScore(bestScore), depth, nodeAll)
	}
	return bestScore
}
