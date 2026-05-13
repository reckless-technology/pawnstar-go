package engine

import (
	"fmt"
	"strings"
)

// Variation is a line of play (principal variation).
type Variation []Move

func copyVariation(dst *Variation, src Variation, best Move) {
	*dst = (*dst)[:0]
	*dst = append(*dst, best)
	*dst = append(*dst, src...)
}

// searchSingleMove plays move, searches recursively, undoes, and returns the score.
func searchSingleMove(g *Game, depth, ply, alphav, betav int, m Move, pv *Variation, moveIdx int) int {
	wasInCheck := g.CurrentPosition().IsInCheck()

	childDepth := depth - 1
	mt := m.Type()
	if mt == PromotionKnight || mt == PromotionBishop || mt == PromotionRook || mt == PromotionQueen || mt == EpCapture {
		childDepth++
	}

	g.PlayMove(m)
	isChecking := g.CurrentPosition().IsInCheck()

	var score int
	if betav > alphav+1 && moveIdx > 0 && !wasInCheck && !isChecking {
		// Principal variation search.
		score = -search(g, childDepth, ply+1, -alphav-1, -alphav, pv)
		if score > alphav {
			score = -search(g, childDepth, ply+1, -betav, -alphav, pv)
		}
	} else {
		score = -search(g, childDepth, ply+1, -betav, -alphav, pv)
	}
	g.UndoMove()
	return score
}

// search is the main alpha-beta recursive function.
func search(g *Game, depth, ply, alphav, betav int, parentPV *Variation) int {
	g.NodeCount++
	if (g.NodeCount&0xFFFF) == 0 && g.TimeCtrl.HardStopMs != 0 &&
		g.TimeCtrl.ElapsedMs() >= g.TimeCtrl.HardStopMs {
		g.CancelSearch = true
		return SearchCancelledScore
	}

	pos := g.CurrentPosition()
	if pos.IsDrawByMaterial() || g.IsDrawByFiftyMoves() || g.IsDrawByRepetition() {
		return DrawScore
	}

	if pos.IsInCheck() {
		depth++
	}
	if ply >= MaxPly {
		return evaluatePosition(g, alphav, betav)
	}

	// Transposition table lookup.
	t := g.TransTable.Find(pos.Hash())
	if t != nil && int(t.depth) >= depth {
		switch t.nodeType {
		case NodeCut:
			if t.score >= betav {
				return t.score
			}
		case NodeAll:
			if t.score < alphav {
				return t.score
			}
		case NodePV:
			depth++
		}
	}

	// Enter quiescence at depth <= 0.
	if depth <= 0 && !pos.IsInCheck() {
		return searchQuiescent(g, depth, ply, alphav, betav)
	}

	// Null move pruning.
	if !pos.IsNullMove() && !pos.IsInCheck() && betav == alphav+1 &&
		pos.ColorBB(pos.ColorToMove()).PopCount() > 3 {
		evalScore := evaluatePosition(g, alphav, betav)
		if evalScore >= betav {
			var dummy Variation
			g.MakeNullMove()
			score := -search(g, depth-3, ply+1, -betav, -alphav, &dummy)
			g.UndoMove()
			if g.CancelSearch {
				return SearchCancelledScore
			}
			if score >= betav {
				return betav
			}
		}
	}

	// Try the transposition table move first.
	var pv Variation
	bestMove := MoveNone()
	bestScore := Alpha
	hasRaisedAlpha := false

	if t != nil && t.move != MoveNone() {
		score := searchSingleMove(g, depth, ply, alphav, betav, t.move, &pv, 0)
		if g.CancelSearch {
			return SearchCancelledScore
		}
		if score >= betav {
			g.TransTable.Record(Transposition{pos.Hash(), t.move, score, int16(depth), NodeCut, false})
			g.History.RecordGoodMove(ply, t.move)
			return score
		}
		bestScore = score
		if score > alphav {
			alphav = score
			hasRaisedAlpha = true
			bestMove = t.move
			g.History.RecordGoodMove(ply, t.move)
		}
	}

	moves := pos.GenerateLegalMoves()
	if len(moves) == 0 {
		if pos.IsInCheck() {
			return CheckmatedScore + ply
		}
		return DrawScore
	}

	g.ScoreAndSortMoves(moves, ply)

	for i := 0; i < len(moves); i++ {
		m := moves[i]
		if t != nil && m == t.move {
			continue
		}
		moveIdx := i

		// Late move reduction.
		lmrDepth := depth
		if moveIdx > 3 && !pos.IsInCheck() && depth > 2 && betav == alphav+1 &&
			pos.PieceAt(m.To()) == NoPiece && pos.PieceAt(m.From()) != Pawn {
			lmrDepth--
			if depth > 3 && moveIdx > 6 {
				lmrDepth--
			}
		}

		score := searchSingleMove(g, lmrDepth, ply, alphav, betav, m, &pv, moveIdx)
		if score > alphav && lmrDepth < depth {
			score = searchSingleMove(g, depth, ply, alphav, betav, m, &pv, moveIdx)
		}
		if g.CancelSearch {
			return SearchCancelledScore
		}
		if score >= betav {
			g.TransTable.Record(Transposition{pos.Hash(), m, score, int16(depth), NodeCut, false})
			g.History.RecordGoodMove(ply, m)
			return score
		}
		if score > bestScore {
			bestScore = score
			bestMove = m
			if score > alphav {
				alphav = score
				hasRaisedAlpha = true
				g.History.RecordGoodMove(ply, m)
			}
		}
	}

	if hasRaisedAlpha {
		g.TransTable.Record(Transposition{pos.Hash(), bestMove, alphav, int16(depth), NodePV, false})
		g.History.RecordGoodMove(ply, bestMove)
		copyVariation(parentPV, pv, bestMove)
	} else {
		g.TransTable.Record(Transposition{pos.Hash(), bestMove, bestScore, int16(depth), NodeAll, false})
	}
	return bestScore
}

// searchQuiescent is the quiescence search (captures only).
func searchQuiescent(g *Game, depth, ply, alphav, betav int) int {
	if ply >= MaxPly {
		return evaluatePosition(g, alphav, betav)
	}
	pos := g.CurrentPosition()
	if pos.IsInCheck() {
		var dummy Variation
		return search(g, depth, ply, alphav, betav, &dummy)
	}

	qt := g.QuiesTable.Find(pos.Hash())
	if qt != nil && qt.score >= betav {
		return qt.score
	}

	score := evaluatePosition(g, alphav, betav)
	if score >= betav {
		g.QuiesTable.Record(Transposition{pos.Hash(), MoveNone(), score, int16(depth), NodeCut, false})
		return score
	}
	if score > alphav {
		alphav = score
	}
	bestScore := score

	if qt != nil && qt.move != MoveNone() {
		g.PlayMove(qt.move)
		score = -searchQuiescent(g, depth-1, ply+1, -betav, -alphav)
		g.UndoMove()
		if g.CancelSearch {
			return SearchCancelledScore
		}
		if score >= betav {
			return score
		}
		if score > bestScore {
			bestScore = score
			if score > alphav {
				alphav = score
			}
		}
	}

	moves := pos.GenerateLegalCaptures()
	g.ScoreAndSortMoves(moves, ply)

	for i := 0; i < len(moves); i++ {
		m := moves[i]
		if qt != nil && m == qt.move {
			continue
		}
		g.PlayMove(m)
		score = -searchQuiescent(g, depth-1, ply+1, -betav, -alphav)
		g.UndoMove()
		if g.CancelSearch {
			return SearchCancelledScore
		}
		if score >= betav {
			g.History.RecordGoodMove(ply, m)
			g.QuiesTable.Record(Transposition{pos.Hash(), m, score, int16(depth), NodeCut, false})
			return score
		}
		if score > bestScore {
			bestScore = score
			if score > alphav {
				alphav = score
				g.History.RecordGoodMove(ply, m)
				g.QuiesTable.Record(Transposition{pos.Hash(), m, score, int16(depth), NodeCut, false})
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
	tc := &g.TimeCtrl
	var msAllocated int64
	switch tc.ClockType {
	case ClockStandard:
		var numMovesToGo int
		if tc.NumMovesRemaining != 0 {
			numMovesToGo = tc.NumMovesRemaining
		} else {
			mc := g.CurrentPosition().FullMove()
			numMovesToGo = max(40-mc, 5)
		}
		msAllocated = int64(tc.MsRemaining) / int64(numMovesToGo)
		msTimeout := max(int64(100), min(msAllocated*2, int64(tc.MsRemaining)-1000))
		tc.HardStopMs = tc.ElapsedMs() + msTimeout
	case ClockFixedTime:
		msAllocated = int64(tc.MsRemaining)
		tc.HardStopMs = tc.ElapsedMs() + msAllocated
	default:
		tc.HardStopMs = 0
	}

	startMs := tc.ElapsedMs()
	g.CancelSearch = false
	g.History.Reset()
	g.TransTable.Age()

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
		if tc.ClockType == ClockFixedDepth && depth > tc.Depth {
			break
		}
		SortMovesStable(moves)
		var childPV Variation
		alphav := Alpha
		g.NodeCount = 0
		bestMoves = append(bestMoves, bestMove)

		for i := 0; i < len(moves); i++ {
			m := moves[i]
			score := searchSingleMove(g, depth, 0, alphav, Beta, m, &childPV, i)
			moves[i] = m.WithScore(score)
			if g.CancelSearch {
				return bestMove
			}
			if score > alphav {
				alphav = score
				bestMove = m
				copyVariation(&principalVariation, childPV, bestMove)
				pvStr := pvString(principalVariation)
				fmt.Printf("info depth %d score cp %d time %d nodes %d pv %s\n",
					depth, alphav, tc.ElapsedMs()-startMs, g.NodeCount, pvStr)
			}
		}

		stopMs := tc.ElapsedMs()
		if alphav > WinThreshold || alphav < LoseThreshold {
			break
		}

		if tc.ClockType == ClockStandard {
			elapsedMs := stopMs - startMs
			isBestMoveConsistent := allSame(bestMoves, bestMove)
			isScoreStable := allScoreStable(bestMoves, bestMove)
			if isBestMoveConsistent && isScoreStable && elapsedMs*4 > msAllocated {
				fmt.Println("info string Best move consistent AND score stable - search terminated.")
				break
			}
			if (isBestMoveConsistent || isScoreStable) && elapsedMs*2 > msAllocated {
				if isBestMoveConsistent {
					fmt.Println("info string Best move consistent - search terminated.")
				} else {
					fmt.Println("info string Score stable - search terminated.")
				}
				break
			}
			if elapsedMs > msAllocated {
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
		if m != best {
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

