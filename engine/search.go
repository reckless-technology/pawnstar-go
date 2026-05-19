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
	if (game.nodeCount&0xFFFF) == 0 &&
		(game.timeControl.clockType == clockStandard || game.timeControl.clockType == clockFixedTime) &&
		time.Now().After(game.timeControl.stopTime) {
		game.isCancelPending = true
		return searchCancelledScore
	}

	position := game.currentPosition()
	if position.isDrawByMaterial() || game.isDrawByFiftyMoves() || game.isDrawByRepetition() {
		return drawScore
	}

	if ply >= maxPly {
		return evaluatePosition(game, alpha, beta)
	}

	// Transposition table lookup.
	tt, isTransposition := game.transpositionTable.find(position.getHash())
	if isTransposition && int(tt.depth) >= depth {
		switch tt.nodeType {
		case nodeCut:
			if tt.move.score() >= beta {
				dbgx.tableHitBeta++
				return tt.move.score()
			}
		case nodeAll:
			if tt.move.score() <= alpha {
				dbgx.tableHitAlpha++
				return tt.move.score()
			}
		case nodePV:
			// PV hit: search deeper so the PV is fully verified.
			dbgx.tableHitPV++
			depth++
		}
	}

	// Enter quiescence at depth <= 0 (unless in check, which quiescence handles
	// by re-entering alpha-beta at the same depth).
	if depth <= 0 && !position.isInCheck() {
		return searchQuiescent(game, depth, ply, alpha, beta)
	}

	// Null-move pruning: skip our turn and search at depth−4.  If the result still
	// beats beta, the position is likely good enough to prune.  Skipped when:
	// already in a null-move position, in check, not a null window, or the side
	// has ≤ 3 pieces (zugzwang risk).
	if !position.isNullMove() &&
		!position.isInCheck() &&
		beta == alpha+1 &&
		position.colorBB(position.colorToMove()).popCount() > 3 {
		evalScore := evaluatePosition(game, alpha, beta)
		if evalScore >= beta {
			var dummy variation
			game.makeNullMove()
			dbgx.nullMoveAttempts++
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

	if isTransposition && tt.move.fromTo() != 0 {
		score := searchSingleMove(game, depth, ply, alpha, beta, tt.move, &pv, 0)
		if game.isCancelPending {
			return searchCancelledScore
		}
		if score >= beta {
			game.transpositionTable.record(&transposition{position.getHash(), tt.move.withScore(score), int16(depth), nodeCut, false})
			game.historyTable.recordGoodMove(ply, tt.move)
			return score
		}
		bestScore = score
		if score > alpha {
			alpha = score
			hasRaisedAlpha = true
			bestMove = tt.move
			game.historyTable.recordGoodMove(ply, tt.move)
		}
	}

	moves := position.generateLegalMoves()
	if len(moves) == 0 {
		if position.isInCheck() {
			return checkmatedScore + ply
		}
		return drawScore
	}

	game.scoreAndSortMoves(moves, ply)

	for moveIdx, m := range moves {
		if isTransposition && m.equal(tt.move) {
			continue // already searched above
		}

		// Late move reduction.
		childDepth := depth
		if moveIdx > 3 &&
			!position.isInCheck() &&
			!m.isChecking() &&
			depth > 2 &&
			beta == alpha+1 &&
			m.piece() != pawn &&
			m.captured() == noPiece {
			dbgx.lmrAttempts++
			childDepth--
			if depth > 3 && moveIdx > 6 {
				dbgx.lmr2Attempts++
				childDepth--
			}
		}

		score := searchSingleMove(game, childDepth, ply, alpha, beta, m, &pv, moveIdx)
		if score > alpha && childDepth < depth {
			// LMR re-search: the move was good despite the reduction.
			dbgx.lmrFails++
			score = searchSingleMove(game, depth, ply, alpha, beta, m, &pv, moveIdx)
		}
		if game.isCancelPending {
			return searchCancelledScore
		}
		if score >= beta {
			game.transpositionTable.record(&transposition{position.getHash(), m.withScore(score), int16(depth), nodeCut, false})
			game.historyTable.recordGoodMove(ply, m)
			dbgx.betaCutoffs++
			return score
		}
		if score > bestScore {
			bestScore = score
			bestMove = m
			if score > alpha {
				alpha = score
				hasRaisedAlpha = true
				game.historyTable.recordGoodMove(ply, m)
			}
		}
	}

	if hasRaisedAlpha {
		game.transpositionTable.record(&transposition{position.getHash(), bestMove.withScore(alpha), int16(depth), nodePV, false})
		game.historyTable.recordGoodMove(ply, bestMove)
		copyVariation(parentPV, pv, bestMove)
	} else {
		game.transpositionTable.record(&transposition{position.getHash(), bestMove.withScore(alpha), int16(depth), nodeAll, false})
	}
	return bestScore
}

// searchQuiescent extends the search by generating captures and promotions until
// the position is quiet.  An in-check position re-enters searchAlphaBeta at the
// same depth (no depth decrement) so that all evasions are considered.
func searchQuiescent(game *Game, depth, ply, alpha, beta int) int {
	if ply >= maxPly {
		return evaluatePosition(game, alpha, beta)
	}
	pos := game.currentPosition()
	if pos.isInCheck() {
		var dummy variation
		return searchAlphaBeta(game, depth, ply, alpha, beta, &dummy)
	}
	// Stand-pat: if the static eval already beats beta we can prune.
	score := evaluatePosition(game, alpha, beta)
	if score >= beta {
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
func searchRootNode(g *Game) move {
	// Check opening book.
	bookMove := g.openingBook.getMove(g.currentPosition().getHash())
	if bookMove != moveNone() {
		return bookMove
	}

	moves := g.currentPosition().generateLegalMoves()
	if len(moves) == 0 {
		return moveNone()
	}
	if len(moves) == 1 {
		return moves[0]
	}

	// Set up time management.
	allocatedTime := time.Duration(0)
	tc := &g.timeControl

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
	g.isCancelPending = false
	g.historyTable.reset()
	g.transpositionTable.age()

	var principalVariation variation
	var bestMoves []move

	// Initial shallow pass to get move ordering before deepening.
	var initPV variation
	for i := range moves {
		m := moves[i]
		score := searchSingleMove(g, startDepth, 0, initialAlpha, initialBeta, m, &initPV, i)
		moves[i] = m.withScore(score)
	}
	sortMovesStable(moves)
	bestMove := moves[0]

	for depth := startDepth + 1; depth < maxPly; depth++ {
		if tc.clockType == clockFixedDepth && depth > tc.depth {
			break
		}
		sortMovesStable(moves)
		var childPV variation
		alpha := initialAlpha
		g.nodeCount = 0
		bestMoves = append(bestMoves, bestMove)

		for i := range moves {
			m := moves[i]
			score := searchSingleMove(g, depth, 0, alpha, initialBeta, m, &childPV, i)
			moves[i] = m.withScore(score)
			if g.isCancelPending {
				return bestMove
			}
			if score > alpha {
				alpha = score
				bestMove = m
				copyVariation(&principalVariation, childPV, bestMove)
				pvStr := pvString(principalVariation)
				fmt.Printf("info depth %2d score cp %4d time %5d nodes %7d pv %s\n",
					depth, alpha, time.Since(startTime).Milliseconds(), g.nodeCount, pvStr)
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
