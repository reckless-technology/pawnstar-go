package engine

// PST tables ported from the C Pawnstar engine.
// Stored with rank 8 at low indices, rank 1 at high indices.
// Black reads the table directly; White reads via sq^rankFlip to mirror ranks.

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

// King tables are phase-dependent and not maintained incrementally.
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

// passedPawnSquare rewards passed pawns based on how advanced they are.
// Indexed from Black's perspective (rank 8 at low indices); White flips via ^rankFlip.
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

// Mobility scores indexed by number of available squares (after exclusions).
var bishopAttackScores = [14]int{-20, -10, -5, -5, 0, 5, 10, 15, 20, 20, 20, 20, 20, 20}
var rookAttackScores = [15]int{-10, -10, -5, -5, 0, 5, 10, 15, 20, 20, 20, 20, 20, 20, 20}

// fileBBs maps file index (0=a … 7=h) to the full-file bitboard.
var fileBBs = [8]bitboard{fileA, fileB, fileC, fileD, fileE, fileF, fileG, fileH}

var flips = [2]square{rankFlip, 0}

// pstValue returns material value plus piece-square bonus for piece p of color c
// on sq.  Returns 0 for the king; the king's PST is phase-dependent and applied
// separately in evaluatePosition.
func pstValue(c color, p piece, sq square) int {
	flip := flips[c]
	switch p {
	case pawn:
		return 100 + pawnSquare[sq^flip]
	case knight:
		return 300 + knightSquare[sq^flip]
	case bishop:
		return 300 + bishopSquare[sq^flip]
	case rook:
		return 500 + rookSquare[sq^flip]
	case queen:
		return 900 + queenSquare[sq^flip]
	}
	return 0
}

// isEndgame returns true when neither side has a queen, or every side that has a
// queen also has no rooks and at most one minor piece.
func isEndgame(pos *position) bool {
	for c := white; c <= black; c++ {
		friendly := pos.colorBB(c)
		if (pos.queens() & friendly).isEmpty() {
			continue
		}
		if (pos.rooks()&friendly).popCount() > 0 ||
			((pos.knights()|pos.bishops())&friendly).popCount() > 1 {
			return false
		}
	}
	return true
}

// pawnStructure holds pawn classification for one side.
type pawnStructure struct {
	passed   bitboard
	isolated bitboard
	backward bitboard
	doubled  bitboard
	defended bitboard
}

// determinePawnStructure classifies all pawns of color c into structural categories.
func determinePawnStructure(pos *position, c color) pawnStructure {
	friendlyPawns := pos.pawns() & pos.colorBB(c)
	enemyPawns := pos.pawns() ^ friendlyPawns
	var ps pawnStructure

	if c == white {
		ps.defended = friendlyPawns & (friendlyPawns.shiftNorthwest() | friendlyPawns.shiftNortheast())
	} else {
		ps.defended = friendlyPawns & (friendlyPawns.shiftSouthwest() | friendlyPawns.shiftSoutheast())
	}

	for f := friendlyPawns; f.isNotEmpty(); {
		sq := f.popLsb()
		b := squareBB(sq)
		if c == white {
			if (passedPawnMaskWhite[sq] & enemyPawns).isEmpty() {
				ps.passed |= b
			}
			if (doubledPawnMaskWhite[sq] & friendlyPawns).isNotEmpty() {
				ps.doubled |= b
			}
			if (isolatedPawnMask[sq] & friendlyPawns).isEmpty() {
				ps.isolated |= b
			}
			// Backward: unsupported pawn whose advance square is controlled by an enemy pawn.
			if (supportedPawnMaskWhite[sq] & friendlyPawns).isEmpty() {
				enemyAttacks := enemyPawns.shiftSouthwest() | enemyPawns.shiftSoutheast()
				if (enemyAttacks & squareBB(sq+8)).isNotEmpty() {
					ps.backward |= b
				}
			}
		} else {
			if (passedPawnMaskBlack[sq] & enemyPawns).isEmpty() {
				ps.passed |= b
			}
			if (doubledPawnMaskBlack[sq] & friendlyPawns).isNotEmpty() {
				ps.doubled |= b
			}
			if (isolatedPawnMask[sq] & friendlyPawns).isEmpty() {
				ps.isolated |= b
			}
			if (supportedPawnMaskBlack[sq] & friendlyPawns).isEmpty() {
				enemyAttacks := enemyPawns.shiftNorthwest() | enemyPawns.shiftNortheast()
				if (enemyAttacks & squareBB(sq-8)).isNotEmpty() {
					ps.backward |= b
				}
			}
		}
	}
	// A doubled passed pawn doesn't score as passed; the back pawn is obstructed.
	ps.passed &= ^ps.doubled
	return ps
}

// evalMaterialExtra returns a bishop-pair bonus (+30 cp) when both color bishops are present.
func evalMaterialExtra(pos *position, c color) int {
	bishops := pos.bishops() & pos.colorBB(c)
	if bishops&whiteSqs != 0 && bishops&blackSqs != 0 {
		return 30
	}
	return 0
}

// evalPawnStructure scores pawn structure for color c.
func evalPawnStructure(ps *pawnStructure, c color) int {
	flip := flips[c]
	score := 0
	for p := ps.passed; p.isNotEmpty(); {
		sq := p.popLsb()
		score += passedPawnSquare[sq^flip]
	}
	score += ps.defended.popCount() * 5
	score -= ps.backward.popCount() * 10
	score -= ps.doubled.popCount() * 10
	score -= ps.isolated.popCount() * 20
	return score
}

// evalMobility scores bishop and rook mobility for color c.
// Bishop squares defended by enemy pawns are excluded from the count.
func evalMobility(pos *position, c color, ps *[2]pawnStructure) int {
	friendly := pos.colorBB(c)
	occ := pos.occupied()
	enemyDefended := ps[enemyOf(c)].defended
	score := 0
	for b := pos.bishops() & friendly; b.isNotEmpty(); {
		sq := b.popLsb()
		attacks := bishopAttacks(occ, sq) & ^friendly & ^enemyDefended
		score += bishopAttackScores[attacks.popCount()]
	}
	for r := pos.rooks() & friendly; r.isNotEmpty(); {
		sq := r.popLsb()
		attacks := rookAttacks(occ, sq) & ^friendly
		score += rookAttackScores[attacks.popCount()]
	}
	return score
}

// evalKing scores king safety: PST bonus, three-row pawn shelter, open-file penalties,
// and a bonus for friendly non-pawn pieces near the king.
func evalKing(pos *position, c color, endgame bool) int {
	flip := flips[c]
	friendly := pos.colorBB(c)
	enemyColor := enemyOf(c)
	friendlyPawns := friendly & pos.pawns()
	enemyPawns := pos.colorBB(enemyColor) & pos.pawns()
	kingLoc := pos.kingLocn[c]

	var pstScore int
	if endgame {
		pstScore = kingSqEndgame[kingLoc^flip]
	} else {
		pstScore = kingSqMidgame[kingLoc^flip]
	}

	safetyScore := 0
	if !endgame {
		var shelter1 bitboard
		if c == white {
			shelter1 = kingPawnShelterWhite[kingLoc]
		} else {
			shelter1 = kingPawnShelterBlack[kingLoc]
		}
		var shelter2, shelter3 bitboard
		if c == white {
			shelter2 = shelter1.shiftNorth()
			shelter3 = shelter2.shiftNorth()
		} else {
			shelter2 = shelter1.shiftSouth()
			shelter3 = shelter2.shiftSouth()
		}
		safetyScore = 15*(friendlyPawns&shelter1).popCount() +
			10*(friendlyPawns&shelter2).popCount() +
			5*(friendlyPawns&shelter3).popCount() -
			15*(enemyPawns&shelter1).popCount() -
			10*(enemyPawns&shelter2).popCount() -
			5*(enemyPawns&shelter3).popCount()

		// Penalise open files near the king.
		kingFile := fileBBs[kingLoc.file()]
		if (kingFile & friendlyPawns).isEmpty() {
			safetyScore -= 15
		}
		if adjW := kingFile.shiftWest(); adjW.isNotEmpty() && (adjW&friendlyPawns).isEmpty() {
			safetyScore -= 10
		}
		if adjE := kingFile.shiftEast(); adjE.isNotEmpty() && (adjE&friendlyPawns).isEmpty() {
			safetyScore -= 10
		}
	}

	// Friendly non-pawn pieces near the king provide protection.
	nonPawns := friendly ^ friendlyPawns
	adj1 := kingAttacks[kingLoc]
	adj2 := kingAttacks2[kingLoc] ^ adj1
	safetyScore += 10*(adj1&nonPawns).popCount() + 5*(adj2&nonPawns).popCount()

	return pstScore + safetyScore
}

// evaluatePosition returns the score from the side-to-move's perspective.
// Returns drawScore for positions with insufficient material.
// If the material score alone is more than 200 cp outside [alpha, beta], it returns early.
func evaluatePosition(game *Game, alpha, beta int) int {
	pos := game.currentPosition()
	if pos.isDrawByMaterial() {
		return drawScore
	}

	wScore := pos.scoreWhite + evalMaterialExtra(pos, white)
	bScore := pos.scoreBlack + evalMaterialExtra(pos, black)

	// Early exit: if material alone is far outside the search window, skip full eval.
	matScore := wScore - bScore
	if pos.colorToMove() == black {
		matScore = -matScore
	}
	if matScore > beta+200 {
		return matScore
	}
	if matScore < alpha-200 {
		return matScore
	}

	endgame := isEndgame(pos)
	var ps [2]pawnStructure
	ps[white] = determinePawnStructure(pos, white)
	ps[black] = determinePawnStructure(pos, black)
	wScore += evalPawnStructure(&ps[white], white)
	bScore += evalPawnStructure(&ps[black], black)
	wScore += evalMobility(pos, white, &ps)
	bScore += evalMobility(pos, black, &ps)
	wScore += evalKing(pos, white, endgame)
	bScore += evalKing(pos, black, endgame)

	if pos.colorToMove() == white {
		return wScore - bScore
	}
	return bScore - wScore
}
