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

func copyVariation(dst *variation, src variation, best move) {
	dst.moves = dst.moves[:0]
	dst.moves = append(dst.moves, best)
	dst.moves = append(dst.moves, src.moves...)
}

// searchSingleMove plays m, calls searchAlphaBeta recursively, undoes the move,
// and returns the score.  When the position is not a PV node and the move is not
// the first tried, a null-window PVS search is attempted first; if it fails high,
// a full-window re-search is performed.
func searchSingleMove(game *Game, depth, ply, alpha, beta int, m move, pv *variation, moveIdx int) int {
	wasInCheck := game.currentPosition().isInCheck()
	childDepth := depth - 1
	game.playMove(m)
	isChecking := game.currentPosition().isInCheck()
	var score int
	if beta > alpha+1 && moveIdx > 0 && !wasInCheck && !isChecking {
		// Try principal variation search.
		dbgx.pvsAttempts++
		score = -searchAlphaBeta(game, childDepth, ply+1, -alpha-1, -alpha, pv)
		if score > alpha {
			// PVS failed high — re-search with full window.
			dbgx.pvsFails++
			score = -searchAlphaBeta(game, childDepth, ply+1, -beta, -alpha, pv)
		}
	} else {
		score = -searchAlphaBeta(game, childDepth, ply+1, -beta, -alpha, pv)
	}
	game.undoMove()
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
//   - Late move reduction: −1 ply for moves 4+, −2 ply additionally for moves 7+ (quiet, non-check, depth > 2).
//   - LMR re-search at full depth if the reduced search exceeds alpha.
//   - TT write with Cut/All/PV node type on exit.
func searchAlphaBeta(game *Game, depth, ply, alpha, beta int, parentPV *variation) int {
	game.nodeCount++
	if (game.nodeCount&0xFFFF) == 0 && (game.timeControl.clockType == clockStandard || game.timeControl.clockType == clockFixedTime) &&
		time.Now().After(game.timeControl.stopTime) {
		game.isCancelPending = true
		return searchCancelledScore
	}

	pos := game.currentPosition()
	if pos.isDrawByMaterial() || game.isDrawByFiftyMoves() || game.isDrawByRepetition() {
		return drawScore
	}

	if ply >= maxPly {
		return evaluatePosition(game, alpha, beta)
	}

	// Transposition table lookup.
	trans, isTransposition := game.transpositionTable.find(pos.hash)
	if isTransposition && int(trans.depth) >= depth {
		switch trans.nodeType {
		case nodeCut:
			if trans.move.score() >= beta {
				dbgx.tableHitBeta++
				return trans.move.score()
			}
		case nodeAll:
			if trans.move.score() <= alpha {
				dbgx.tableHitAlpha++
				return trans.move.score()
			}
		case nodePV:
			// PV hit: search deeper so the PV is fully verified.
			dbgx.tableHitPv++
			depth++
		}
	}

	// Enter quiescence at depth <= 0 (unless in check, which quiescence handles
	// by re-entering alpha-beta at the same depth).
	if depth <= 0 && !pos.isInCheck() {
		return searchQuiescent(game, depth, ply, alpha, beta)
	}

	// Null-move pruning: skip our turn and search at depth−4.  If the result still
	// beats beta, the position is likely good enough to prune.  Skipped when:
	// already in a null-move position, in check, not a null window, or the side
	// has ≤ 3 pieces (zugzwang risk).
	if !pos.isNullMove() && !pos.isInCheck() && beta == alpha+1 && pos.piecesOfColor(pos.colorToMove).popCount() > 3 {
		pstScore := pos.scores[white] - pos.scores[black]
		if pos.colorToMove == black {
			pstScore = -pstScore
		}
		if pstScore >= (beta + 200) {
			game.makeNullMove()
			dbgx.nullMoveAttempts++
			var dummy variation
			score := -searchAlphaBeta(game, depth-4, ply+1, -beta, -alpha, &dummy)
			game.undoMove()
			if game.isCancelPending {
				return searchCancelledScore
			}
			if score >= beta {
				return beta
			}
			dbgx.nullMoveFails++
		}
	}

	// Try the transposition table move first.
	var pv variation
	bestMove := moveNone()
	bestScore := initialAlpha
	hasRaisedAlpha := false

	if isTransposition && trans.move.fromTo() != 0 {
		dbgx.tableMove++
		score := searchSingleMove(game, depth, ply, alpha, beta, trans.move, &pv, 0)
		if game.isCancelPending {
			return searchCancelledScore
		}
		if score >= beta {
			dbgx.tableMoveBetaCutoff++
			game.transpositionTable.record(&transposition{pos.hash, trans.move.withScore(score), int16(depth), nodeCut, false})
			game.historyTable.recordGoodMove(ply, trans.move)
			return score
		}
		bestScore = score
		bestMove = trans.move
		if score > alpha {
			dbgx.tableMoveRaiseAlpha++
			alpha = score
			hasRaisedAlpha = true
			game.historyTable.recordGoodMove(ply, trans.move)
		}
	}

	moves := pos.generateLegalMoves()
	if len(moves) == 0 {
		if pos.isInCheck() {
			dbgx.checkmates++
			return checkmatedScore + ply
		}
		dbgx.stalemates++
		return drawScore
	}

	game.scoreAndSortMoves(moves, ply)

	for moveIdx, move := range moves {
		if isTransposition && move.equal(trans.move) {
			continue // already searched above
		}

		// Late move reduction.
		childDepth := depth
		if depth > 2 && moveIdx > 0 && !pos.isInCheck() && !move.isChecking() && beta == alpha+1 &&
			move.piece() != pawn && move.piece() != king && move.captured() == noPiece {
			dbgx.lmrAttempts1++
			childDepth--
			if depth > 3 && moveIdx > 6 && game.historyTable.getCount(ply, move) == 0 {
				dbgx.lmrAttempts2++
				childDepth--
			}
		}

		score := searchSingleMove(game, childDepth, ply, alpha, beta, move, &pv, moveIdx)
		if score > alpha && childDepth < depth {
			dbgx.lmrFails++
			score = searchSingleMove(game, depth, ply, alpha, beta, move, &pv, moveIdx)
		}
		if game.isCancelPending {
			return searchCancelledScore
		}
		if score >= beta {
			game.transpositionTable.record(&transposition{pos.hash, move.withScore(score), int16(depth), nodeCut, false})
			game.historyTable.recordGoodMove(ply, move)
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
				game.historyTable.recordGoodMove(ply, move)
			}
		}
	}

	if hasRaisedAlpha {
		dbgx.nodesPv++
		game.transpositionTable.record(&transposition{pos.hash, bestMove.withScore(alpha), int16(depth), nodePV, false})
		game.historyTable.recordGoodMove(ply, bestMove)
		copyVariation(parentPV, pv, bestMove)
	} else {
		dbgx.nodesAll++
		game.transpositionTable.record(&transposition{pos.hash, bestMove.withScore(bestScore), int16(depth), nodeAll, false})
	}
	return bestScore
}

// searchQuiescent extends the search by generating captures and promotions until
// the position is quiet.  An in-check position re-enters searchAlphaBeta at the
// same depth (no depth decrement) so that all evasions are considered.
func searchQuiescent(game *Game, depth, ply, alpha, beta int) int {
	dbgx.quiescentCalls++
	if ply >= maxPly {
		return evaluatePosition(game, alpha, beta)
	}
	pos := game.currentPosition()
	if pos.isInCheck() {
		dbgx.quiescentChecks++
		var dummy variation
		return searchAlphaBeta(game, depth, ply, alpha, beta, &dummy)
	}
	// Stand-pat: if the static eval already beats beta we can prune.
	score := evaluatePosition(game, alpha, beta)
	if score >= beta {
		dbgx.quiescentEvalCutoffs++
		return score
	}
	if score > alpha {
		alpha = score
	}
	bestScore := score
	moves := pos.generateLegalCaptures()
	game.scoreAndSortMoves(moves, ply)
	for _, m := range moves {
		game.playMove(m)
		score = -searchQuiescent(game, depth-1, ply+1, -beta, -alpha)
		game.undoMove()
		if game.isCancelPending {
			return searchCancelledScore
		}
		if score >= beta {
			game.historyTable.recordGoodMove(ply, m)
			dbgx.quiescentBetaCutoffs++
			return score
		}
		if score > bestScore {
			bestScore = score
			if score > alpha {
				alpha = score
				game.historyTable.recordGoodMove(ply, m)
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
	game.isCancelPending = false
	game.historyTable.reset()
	game.transpositionTable.age()

	var principalVariation variation
	var bestMoves []move

	// Initial shallow pass to get move ordering before deepening.
	var initPV variation
	for i := range moves {
		m := moves[i]
		score := searchSingleMove(game, startDepth, 0, initialAlpha, initialBeta, m, &initPV, i)
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
		game.nodeCount = 0
		bestMoves = append(bestMoves, bestMove)

		for i, move := range moves {
			score := searchSingleMove(game, depth, 0, alpha, initialBeta, move, &pv, i)
			moves[i] = move.withScore(score)
			if game.isCancelPending {
				return bestMove
			}
			if score > alpha {
				alpha = score
				bestMove = move
				copyVariation(&principalVariation, pv, bestMove)
				pvStr := pvString(principalVariation)
				fmt.Printf("info depth %2d score cp %4d time %5d nodes %7d pv %s\n",
					depth, alpha, time.Since(startTime).Milliseconds(), game.nodeCount, pvStr)
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

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
