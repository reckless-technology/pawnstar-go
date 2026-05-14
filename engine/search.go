package engine

import (
	"fmt"
	"strings"
	"time"
)

// Variation is a line of play (principal variation).
type Variation []Move

func copyVariation(dst *Variation, src Variation, best Move) {
	*dst = (*dst)[:0]
	*dst = append(*dst, best)
	*dst = append(*dst, src...)
}

// searchSingleMove plays move, searches recursively, undoes, and returns the score.
func searchSingleMove(game *Game, depth, ply, alpha, beta int, move Move, pv *Variation, moveIdx int) int {
	wasInCheck := game.CurrentPosition().IsInCheck()
	childDepth := depth - 1
	// moveType := move.Type()
	// if moveType == PromotionKnight ||
	// 	moveType == PromotionBishop ||
	// 	moveType == PromotionRook ||
	// 	moveType == PromotionQueen ||
	// 	moveType == EpCapture {
	// 	childDepth++
	// }
	game.PlayMove(move)
	isChecking := game.CurrentPosition().IsInCheck()
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
	game.UndoMove()
	return score
}

// searchAlphaBeta is the main alpha-beta recursive function.
func searchAlphaBeta(game *Game, depth, ply, alpha, beta int, parentPV *Variation) int {
	game.NodeCount++
	if (game.NodeCount&0xFFFF) == 0 &&
		(game.TimeCtrl.ClockType == ClockStandard || game.TimeCtrl.ClockType == ClockFixedTime) &&
		time.Now().After(game.TimeCtrl.stopTime) {
		game.IsCancelPending = true
		return SearchCancelledScore
	}

	position := game.CurrentPosition()
	if position.IsDrawByMaterial() || game.IsDrawByFiftyMoves() || game.IsDrawByRepetition() {
		return DrawScore
	}

	if ply >= MaxPly {
		return evaluatePosition(game, alpha, beta)
	}

	// Transposition table lookup.
	transposition, isTransposition := game.TranspositionTable.Find(position.Hash())
	if isTransposition && int(transposition.depth) >= depth {
		switch transposition.nodeType {
		case NodeCut:
			if transposition.move.Score() >= beta {
				transposition.move.Score()
			}
		case NodeAll:
			if transposition.move.Score() < alpha {
				transposition.move.Score()
			}
		case NodePV:
			depth++
		}
	}

	// Enter quiescence at depth <= 0.
	if depth <= 0 && !position.IsInCheck() {
		return searchQuiescent(game, depth, ply, alpha, beta)
	}

	// Null move pruning.
	if !position.IsNullMove() &&
		!position.IsInCheck() &&
		beta == alpha+1 &&
		position.ColorBB(position.ColorToMove()).PopCount() > 3 {
		evalScore := evaluatePosition(game, alpha, beta)
		if evalScore >= beta {
			var dummy Variation
			game.MakeNullMove()
			score := -searchAlphaBeta(game, depth-3, ply+1, -beta, -alpha, &dummy)
			game.UndoMove()
			if game.IsCancelPending {
				return SearchCancelledScore
			}
			if score >= beta {
				return beta
			}
		}
	}

	// Try the transposition table move first.
	var pv Variation
	bestMove := MoveNone()
	bestScore := Alpha
	hasRaisedAlpha := false

	if isTransposition && transposition.move.FromTo() != 0 {
		score := searchSingleMove(game, depth, ply, alpha, beta, transposition.move, &pv, 0)
		if game.IsCancelPending {
			return SearchCancelledScore
		}
		if score >= beta {
			game.TranspositionTable.Record(&Transposition{position.Hash(), transposition.move.WithScore(score), int16(depth), NodeCut, false})
			game.History.RecordGoodMove(ply, transposition.move)
			return score
		}
		bestScore = score
		if score > alpha {
			alpha = score
			hasRaisedAlpha = true
			bestMove = transposition.move
			game.History.RecordGoodMove(ply, transposition.move)
		}
	}

	moves := position.GenerateLegalMoves()
	if len(moves) == 0 {
		if position.IsInCheck() {
			return CheckmatedScore + ply
		}
		return DrawScore
	}

	game.ScoreAndSortMoves(moves, ply)

	for moveIdx, move := range moves {
		if isTransposition && move.Equal(transposition.move) {
			continue
		}

		// Late move reduction.
		childDepth := depth
		if moveIdx > 3 &&
			!position.IsInCheck() &&
			!move.IsChecking() &&
			depth > 2 &&
			beta == alpha+1 &&
			move.Piece() != Pawn &&
			move.Captured() == NoPiece {
			childDepth--
			if depth > 3 && moveIdx > 6 {
				childDepth--
			}
		}

		score := searchSingleMove(game, childDepth, ply, alpha, beta, move, &pv, moveIdx)
		if score > alpha && childDepth < depth {
			score = searchSingleMove(game, depth, ply, alpha, beta, move, &pv, moveIdx)
		}
		if game.IsCancelPending {
			return SearchCancelledScore
		}
		if score >= beta {
			game.TranspositionTable.Record(&Transposition{position.Hash(), move.WithScore(score), int16(depth), NodeCut, false})
			game.History.RecordGoodMove(ply, move)
			return score
		}
		if score > bestScore {
			bestScore = score
			bestMove = move
			if score > alpha {
				alpha = score
				hasRaisedAlpha = true
				game.History.RecordGoodMove(ply, move)
			}
		}
	}

	if hasRaisedAlpha {
		game.TranspositionTable.Record(&Transposition{position.Hash(), bestMove.WithScore(alpha), int16(depth), NodePV, false})
		game.History.RecordGoodMove(ply, bestMove)
		copyVariation(parentPV, pv, bestMove)
	} else {
		game.TranspositionTable.Record(&Transposition{position.Hash(), bestMove.WithScore(alpha), int16(depth), NodeAll, false})
	}
	return bestScore
}

// Quiescent search - captures and pawn promotions only.
func searchQuiescent(game *Game, depth, ply, alpha, beta int) int {
	if ply >= MaxPly {
		return evaluatePosition(game, alpha, beta)
	}
	position := game.CurrentPosition()
	if position.IsInCheck() {
		var dummy Variation
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
	moves := position.GenerateLegalCaptures()
	game.ScoreAndSortMoves(moves, ply)
	for _, move := range moves {
		game.PlayMove(move)
		score = -searchQuiescent(game, depth-1, ply+1, -beta, -alpha)
		game.UndoMove()
		if game.IsCancelPending {
			return SearchCancelledScore
		}
		if score >= beta {
			game.History.RecordGoodMove(ply, move)
			return score
		}
		if score > bestScore {
			bestScore = score
			if score > alpha {
				alpha = score
				game.History.RecordGoodMove(ply, move)
			}
		}
	}
	return bestScore
}

// SearchRootNode performs iterative-deepening search and returns the best move.
func SearchRootNode(g *Game) Move {
	// Check opening book.
	bookMove := g.Book.GetMove(g.CurrentPosition().Hash())
	if bookMove != MoveNone() {
		return bookMove
	}

	moves := g.CurrentPosition().GenerateLegalMoves()
	if len(moves) == 0 {
		return MoveNone()
	}
	if len(moves) == 1 {
		return moves[0]
	}

	// Set up time management.
	allocatedTime := time.Duration(0)
	timeControl := &g.TimeCtrl

	switch timeControl.ClockType {
	case ClockStandard:
		if timeControl.NumMovesRemaining > 0 {
			allocatedTime = timeControl.TimeRemaining / time.Duration(timeControl.NumMovesRemaining)
		} else {
			allocatedTime = timeControl.TimeRemaining
		}
		hardStopDuration := max(100*time.Millisecond, min(allocatedTime*2, timeControl.TimeRemaining-time.Second))
		timeControl.stopTime = time.Now().Add(hardStopDuration)

	case ClockFixedTime:
		timeControl.stopTime = time.Now().Add(timeControl.TimeRemaining)
	}
	startTime := time.Now()
	g.IsCancelPending = false
	g.History.Reset()
	g.TranspositionTable.Age()

	var principalVariation Variation
	var bestMoves []Move

	// Initial shallow pass to get move ordering.
	var initPV Variation
	for i := 0; i < len(moves); i++ {
		m := moves[i]
		score := searchSingleMove(g, StartDepth, 0, Alpha, Beta, m, &initPV, i)
		moves[i] = m.WithScore(score)
	}
	SortMovesStable(moves)
	bestMove := moves[0]

	for depth := StartDepth + 1; depth < MaxPly; depth++ {
		if timeControl.ClockType == ClockFixedDepth && depth > timeControl.Depth {
			break
		}
		SortMovesStable(moves)
		var childPV Variation
		alpha := Alpha
		g.NodeCount = 0
		bestMoves = append(bestMoves, bestMove)

		for i := 0; i < len(moves); i++ {
			m := moves[i]
			score := searchSingleMove(g, depth, 0, alpha, Beta, m, &childPV, i)
			moves[i] = m.WithScore(score)
			if g.IsCancelPending {
				return bestMove
			}
			if score > alpha {
				alpha = score
				bestMove = m
				copyVariation(&principalVariation, childPV, bestMove)
				pvStr := pvString(principalVariation)
				fmt.Printf("info depth %d score cp %d time %d nodes %d pv %s\n",
					depth, alpha, time.Since(startTime).Milliseconds(), g.NodeCount, pvStr)
			}
		}
		if alpha > WinThreshold || alpha < LoseThreshold {
			break
		}

		if timeControl.ClockType == ClockStandard {
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

func pvString(pv Variation) string {
	var b strings.Builder
	for i, m := range pv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.String())
	}
	return b.String()
}

func allSame(moves []Move, best Move) bool {
	for _, m := range moves {
		if !m.Equal(best) {
			return false
		}
	}
	return true
}

func allScoreStable(moves []Move, best Move) bool {
	for _, m := range moves {
		if abs(m.Score()-best.Score()) > ScoreInstabilityThresh {
			return false
		}
	}
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
