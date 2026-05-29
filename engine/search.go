package engine

import (
	"fmt"
	"strings"
	"time"
)

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
	if beta == alpha+1 && pstScore > (beta+100) && !position.isNullMove() && !position.isInCheck() && !isEndgame(position) {
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
		if depth > 2 && i > 0 && beta == alpha+1 && !position.isInCheck() && !move.isChecking() &&
			move.piece() != pawn && move.piece() != king && move.captured() == noPiece {
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

	for depth := startDepth + 1; depth < maxPly; depth++ {
		if tc.clockType == clockFixedDepth && depth > tc.depth {
			break
		}
		sortMovesStable(moves)
		var pv variation
		alpha := initialAlpha
		ss.nodeCount = 0
		bestMoves = append(bestMoves, bestMove)

		for i, move := range moves {
			score := searchSingleMove(ss, depth, 0, alpha, initialBeta, move, &pv, i)
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
			}
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
