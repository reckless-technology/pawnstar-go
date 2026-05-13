package engine

// PST tables are from white's perspective (rank 1 at index 0).
// For black, XOR the square index with RankFlip (0x38).

// clang-format off
var pawnSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	25, 25, 25, 25, 25, 25, 25, 25,
	15, 15, 15, 20, 20, 15, 15, 15,
	10, 10, 10, 15, 15, 10, 10, 10,
	0, 0, 0, 10, 10, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, -40, -40, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
}

var knightSquare = [64]int{
	-20, -10, -10, -10, -10, -10, -10, -20,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-20, -20, -10, -10, -10, -10, -20, -20,
}

var bishopSquare = [64]int{
	-10, -10, -10, -10, -10, -10, -10, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, -10, -20, -10, -10, -20, -10, -10,
}

var rookSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	5, 10, 10, 10, 10, 10, 10, 5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 5, 5, 5, 5, 0, -5,
}

var queenSquare = [64]int{
	-10, -10, -10, -5, -5, -10, -10, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-5, 0, 5, 5, 5, 5, 0, -5,
	0, 0, 5, 5, 5, 5, 0, -5,
	-10, 5, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 0, 0, 0, 0, -10,
	-10, -10, -10, -5, -5, -10, -10, -10,
}

var kingSqMidgame = [64]int{
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-20, -20, -20, -20, -20, -20, -20, -20,
	20, 40, 40, -20, 0, -20, 40, 20,
}

var kingSqEndgame = [64]int{
	0, 10, 20, 30, 30, 20, 10, 0,
	10, 20, 30, 40, 40, 30, 20, 10,
	20, 30, 40, 50, 50, 40, 30, 20,
	30, 40, 50, 60, 60, 50, 40, 30,
	30, 40, 50, 60, 60, 50, 40, 30,
	20, 30, 40, 50, 50, 40, 30, 20,
	10, 20, 30, 40, 40, 30, 20, 10,
	0, 10, 20, 30, 30, 20, 10, 0,
}

var passedPawnSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	30, 30, 30, 30, 30, 30, 30, 30,
	30, 30, 30, 30, 30, 30, 30, 30,
	20, 20, 20, 20, 20, 20, 20, 20,
	15, 15, 15, 15, 15, 15, 15, 15,
	10, 10, 10, 10, 10, 10, 10, 10,
	5, 5, 5, 5, 5, 5, 5, 5,
	0, 0, 0, 0, 0, 0, 0, 0,
}

var bishopAttackScores = [14]int{-20, -10, -5, -5, 0, 5, 10, 15, 20, 20, 20, 20, 20, 20}
var rookAttackScores = [15]int{-10, -10, -5, -5, 0, 5, 10, 15, 20, 20, 20, 20, 20, 20, 20}

var fileBitboards = [8]Bitboard{FileA, FileB, FileC, FileD, FileE, FileF, FileG, FileH}

// clang-format on

type PawnStructure struct {
	passedPawns   Bitboard
	isolatedPawns Bitboard
	backwardPawns Bitboard
	doubledPawns  Bitboard
	defendedPawns Bitboard
}

func determinePawnStructure(pos *Position, color Color) PawnStructure {
	friendly := pos.Pawns() & pos.ColorBB(color)
	enemy := pos.Pawns() ^ friendly
	var ps PawnStructure

	if color == White {
		ps.defendedPawns = friendly & (friendly.ShiftNorthwest() | friendly.ShiftNortheast())
	} else {
		ps.defendedPawns = friendly & (friendly.ShiftSouthwest() | friendly.ShiftSoutheast())
	}

	for b := friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		bit := SquareBB(sq)

		var passedMask, supportedMask, doubledMask [64]Bitboard
		if color == White {
			passedMask = PassedPawnMaskWhite
			supportedMask = SupportedPawnMaskWhite
			doubledMask = DoubledPawnMaskWhite
		} else {
			passedMask = PassedPawnMaskBlack
			supportedMask = SupportedPawnMaskBlack
			doubledMask = DoubledPawnMaskBlack
		}
		isolatedMask := IsolatedPawnMask

		if passedMask[sq]&enemy == 0 {
			ps.passedPawns |= bit
		}
		if doubledMask[sq]&friendly != 0 {
			ps.doubledPawns |= bit
		}
		if isolatedMask[sq]&friendly == 0 {
			ps.isolatedPawns |= bit
		}
		if supportedMask[sq]&friendly == 0 {
			if color == White {
				enemyAttacks := enemy.ShiftSouthwest() | enemy.ShiftSoutheast()
				fwd := Square(int(sq) + 8)
				if enemyAttacks&SquareBB(fwd) != 0 {
					ps.backwardPawns |= bit
				}
			} else {
				enemyAttacks := enemy.ShiftNorthwest() | enemy.ShiftNortheast()
				fwd := Square(int(sq) - 8)
				if enemyAttacks&SquareBB(fwd) != 0 {
					ps.backwardPawns |= bit
				}
			}
		}
	}
	ps.passedPawns &^= ps.doubledPawns
	return ps
}

func evaluateMaterial(pos *Position, color Color) int {
	friendly := pos.ColorBB(color)
	score := (pos.Pawns()&friendly).PopCount()*100 +
		(pos.Knights()&friendly).PopCount()*325 +
		(pos.Bishops()&friendly).PopCount()*325 +
		(pos.Rooks()&friendly).PopCount()*500 +
		(pos.Queens()&friendly).PopCount()*1000

	bishops := pos.Bishops() & friendly
	if bishops&WhiteSqs != 0 && bishops&BlackSqs != 0 {
		score += 50
	}
	return score
}

func evaluateMobility(pos *Position, color Color, ps [2]PawnStructure) int {
	friendly := pos.ColorBB(color)
	occ := pos.Occupied()
	enemy := EnemyOf(color)
	score := 0

	for b := pos.Bishops() & friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		attacks := BishopAttacks(occ, sq) &^ friendly
		attacks &^= ps[enemy].defendedPawns
		n := attacks.PopCount()
		if n < 14 {
			score += bishopAttackScores[n]
		} else {
			score += bishopAttackScores[13]
		}
	}
	for b := pos.Rooks() & friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		attacks := RookAttacks(occ, sq) &^ friendly
		n := attacks.PopCount()
		if n < 15 {
			score += rookAttackScores[n]
		} else {
			score += rookAttackScores[14]
		}
	}
	return score
}

func evaluatePieceSquare(pos *Position, color Color) int {
	rankFlip := 0
	if color == White {
		rankFlip = RankFlip
	}
	friendly := pos.ColorBB(color)
	score := 0
	for b := pos.Pawns() & friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += pawnSquare[int(sq)^rankFlip]
	}
	for b := pos.Knights() & friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += knightSquare[int(sq)^rankFlip]
	}
	for b := pos.Bishops() & friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += bishopSquare[int(sq)^rankFlip]
	}
	for b := pos.Rooks() & friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += rookSquare[int(sq)^rankFlip]
	}
	for b := pos.Queens() & friendly; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += queenSquare[int(sq)^rankFlip]
	}
	return score
}

func evaluatePawnStructure(ps *PawnStructure, color Color) int {
	rankFlip := 0
	if color == White {
		rankFlip = RankFlip
	}
	score := 0
	for b := ps.passedPawns; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += passedPawnSquare[int(sq)^rankFlip]
	}
	score += ps.defendedPawns.PopCount() * 5
	score -= ps.backwardPawns.PopCount() * 10
	score -= ps.doubledPawns.PopCount() * 10
	score -= ps.isolatedPawns.PopCount() * 20
	return score
}

func evaluateKing(pos *Position, color Color) int {
	rankFlip := 0
	if color == White {
		rankFlip = RankFlip
	}
	friendly := pos.ColorBB(color)
	enemyC := EnemyOf(color)
	friendlyPawns := friendly & pos.Pawns()
	enemyPawns := pos.ColorBB(enemyC) & pos.Pawns()

	isEndgame := pos.Queens() == 0 && (pos.Occupied()^pos.Pawns()).PopCount() < 8

	kingLoc := pos.KingLocation(color)
	var pieceSquareScore int
	if isEndgame {
		pieceSquareScore = kingSqEndgame[int(kingLoc)^rankFlip]
	} else {
		pieceSquareScore = kingSqMidgame[int(kingLoc)^rankFlip]
	}

	safetyScore := 0
	if !isEndgame {
		var shelter1 Bitboard
		if color == White {
			shelter1 = KingPawnShelterWhite[kingLoc]
		} else {
			shelter1 = KingPawnShelterBlack[kingLoc]
		}
		var shelter2, shelter3 Bitboard
		if color == White {
			shelter2 = shelter1.ShiftNorth()
			shelter3 = shelter2.ShiftNorth()
		} else {
			shelter2 = shelter1.ShiftSouth()
			shelter3 = shelter2.ShiftSouth()
		}
		safetyScore = 15*(friendlyPawns&shelter1).PopCount() +
			10*(friendlyPawns&shelter2).PopCount() +
			5*(friendlyPawns&shelter3).PopCount() -
			15*(enemyPawns&shelter1).PopCount() -
			10*(enemyPawns&shelter2).PopCount() -
			5*(enemyPawns&shelter3).PopCount()

		// Penalty for open files near king.
		kingFile := fileBitboards[kingLoc.File()]
		if kingFile&friendlyPawns == 0 {
			safetyScore -= 15
		}
		adjWest := kingFile.ShiftWest()
		if adjWest != 0 && adjWest&friendlyPawns == 0 {
			safetyScore -= 10
		}
		adjEast := kingFile.ShiftEast()
		if adjEast != 0 && adjEast&friendlyPawns == 0 {
			safetyScore -= 10
		}
	}

	// Bonus for friendly pieces near king.
	adj1 := KingAttacks[kingLoc]
	adj2 := KingAttacks2[kingLoc] ^ adj1
	nonPawns := friendly ^ friendlyPawns
	safetyScore += 10*(adj1&nonPawns).PopCount() + 5*(adj2&nonPawns).PopCount()

	return pieceSquareScore + safetyScore
}

// EvaluatePosition scores the position from the side-to-move's perspective.
// game is a *Game so we can check draw conditions.
func evaluatePosition(g *Game, alphav, betav int) int {
	pos := g.CurrentPosition()
	if pos.IsDrawByMaterial() || g.IsDrawByFiftyMoves() || g.IsDrawByRepetition() {
		return DrawScore
	}

	var scores [2]int
	scores[White] = evaluateMaterial(pos, White)
	scores[Black] = evaluateMaterial(pos, Black)

	color := pos.ColorToMove()
	var score int
	if color == White {
		score = scores[White] - scores[Black]
	} else {
		score = scores[Black] - scores[White]
	}
	if score > betav+200 || score < alphav-200 {
		return score
	}

	scores[White] += evaluatePieceSquare(pos, White)
	scores[Black] += evaluatePieceSquare(pos, Black)

	ps := [2]PawnStructure{
		determinePawnStructure(pos, White),
		determinePawnStructure(pos, Black),
	}
	scores[White] += evaluatePawnStructure(&ps[White], White)
	scores[Black] += evaluatePawnStructure(&ps[Black], Black)

	scores[White] += evaluateMobility(pos, White, ps)
	scores[Black] += evaluateMobility(pos, Black, ps)

	scores[White] += evaluateKing(pos, White)
	scores[Black] += evaluateKing(pos, Black)

	if color == White {
		score = scores[White] - scores[Black]
	} else {
		score = scores[Black] - scores[White]
	}
	return (score / 5) * 5
}
