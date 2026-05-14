package engine

import (
	"fmt"
	"strings"
	"time"
)

// variation is a line of play (principal variation).
type variation []move

func copyVariation(dst *variation, src variation, best move) {
	*dst = (*dst)[:0]
	*dst = append(*dst, best)
	*dst = append(*dst, src...)
}

// searchSingleMove plays m, searches recursively, undoes, and returns the score.
func searchSingleMove(game *Game, depth, ply, alpha, beta int, m move, pv *variation, moveIdx int) int {
	wasInCheck := game.currentPosition().isInCheck()
	childDepth := depth - 1
	game.playMove(m)
	isChecking := game.currentPosition().isInCheck()
	var score int
	if beta > alpha+1 && moveIdx > 0 && !wasInCheck && !isChecking {
		// Try principal variation search.
		score = -searchAlphaBeta(game, childDepth, ply+1, -alpha-1, -alpha, pv)
		if score > alpha {
			// PVS failed.
			score = -searchAlphaBeta(game, childDepth, ply+1, -beta, -alpha, pv)
		}
	} else {
		score = -searchAlphaBeta(game, childDepth, ply+1, -beta, -alpha, pv)
	}
	game.undoMove()
	return score
}

// searchAlphaBeta is the main alpha-beta recursive function.
func searchAlphaBeta(game *Game, depth, ply, alpha, beta int, parentPV *variation) int {
	game.nodeCount++
	if (game.nodeCount&0xFFFF) == 0 &&
		(game.timeControl.clockType == clockStandard || game.timeControl.clockType == clockFixedTime) &&
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
	tt, isTransposition := game.transpositionTable.find(pos.getHash())
	if isTransposition && int(tt.depth) >= depth {
		switch tt.nodeType {
		case nodeCut:
			if tt.move.score() >= beta {
				tt.move.score()
			}
		case nodeAll:
			if tt.move.score() < alpha {
				tt.move.score()
			}
		case nodePV:
			depth++
		}
	}

	// Enter quiescence at depth <= 0.
	if depth <= 0 && !pos.isInCheck() {
		return searchQuiescent(game, depth, ply, alpha, beta)
	}

	// Null move pruning.
	if !pos.isNullMove() &&
		!pos.isInCheck() &&
		beta == alpha+1 &&
		pos.colorBB(pos.colorToMove()).popCount() > 3 {
		evalScore := evaluatePosition(game, alpha, beta)
		if evalScore >= beta {
			var dummy variation
			game.makeNullMove()
			score := -searchAlphaBeta(game, depth-3, ply+1, -beta, -alpha, &dummy)
			game.undoMove()
			if game.isCancelPending {
				return searchCancelledScore
			}
			if score >= beta {
				return beta
			}
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
			game.transpositionTable.record(&transposition{pos.getHash(), tt.move.withScore(score), int16(depth), nodeCut, false})
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

	moves := pos.generateLegalMoves()
	if len(moves) == 0 {
		if pos.isInCheck() {
			return checkmatedScore + ply
		}
		return drawScore
	}

	game.scoreAndSortMoves(moves, ply)

	for moveIdx, m := range moves {
		if isTransposition && m.equal(tt.move) {
			continue
		}

		// Late move reduction.
		childDepth := depth
		if moveIdx > 3 &&
			!pos.isInCheck() &&
			!m.isChecking() &&
			depth > 2 &&
			beta == alpha+1 &&
			m.piece() != pawn &&
			m.captured() == noPiece {
			childDepth--
			if depth > 3 && moveIdx > 6 {
				childDepth--
			}
		}

		score := searchSingleMove(game, childDepth, ply, alpha, beta, m, &pv, moveIdx)
		if score > alpha && childDepth < depth {
			score = searchSingleMove(game, depth, ply, alpha, beta, m, &pv, moveIdx)
		}
		if game.isCancelPending {
			return searchCancelledScore
		}
		if score >= beta {
			game.transpositionTable.record(&transposition{pos.getHash(), m.withScore(score), int16(depth), nodeCut, false})
			game.historyTable.recordGoodMove(ply, m)
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
		game.transpositionTable.record(&transposition{pos.getHash(), bestMove.withScore(alpha), int16(depth), nodePV, false})
		game.historyTable.recordGoodMove(ply, bestMove)
		copyVariation(parentPV, pv, bestMove)
	} else {
		game.transpositionTable.record(&transposition{pos.getHash(), bestMove.withScore(alpha), int16(depth), nodeAll, false})
	}
	return bestScore
}

// Quiescent search - captures and pawn promotions only.
func searchQuiescent(game *Game, depth, ply, alpha, beta int) int {
	if ply >= maxPly {
		return evaluatePosition(game, alpha, beta)
	}
	pos := game.currentPosition()
	if pos.isInCheck() {
		var dummy variation
		// Can't handle checks in quiescent search.
		return searchAlphaBeta(game, depth, ply, alpha, beta, &dummy)
	}
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

	// Initial shallow pass to get move ordering.
	var initPV variation
	for i := 0; i < len(moves); i++ {
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

		for i := 0; i < len(moves); i++ {
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
				fmt.Printf("info depth %d score cp %d time %d nodes %d pv %s\n",
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
	for i, m := range pv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.String())
	}
	return b.String()
}

func allSame(moves []move, best move) bool {
	for _, m := range moves {
		if !m.equal(best) {
			return false
		}
	}
	return true
}

func allScoreStable(moves []move, best move) bool {
	for _, m := range moves {
		if absInt(m.score()-best.score()) > scoreInstabilityThresh {
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
