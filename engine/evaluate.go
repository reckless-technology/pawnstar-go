package engine

// Piece-square tables from https://www.chessprogramming.org/Simplified_Evaluation_Function
// Stored with rank 8 at low indices, rank 1 at high indices.
// Black reads the table directly; White reads via sq^rankFlip (0x38) to mirror ranks.

var pawnSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	50, 50, 50, 50, 50, 50, 50, 50,
	10, 10, 20, 30, 30, 20, 10, 10,
	5, 5, 10, 25, 25, 10, 5, 5,
	0, 0, 0, 20, 20, 0, 0, 0,
	5, -5, -10, 0, 0, -10, -5, 5,
	5, 10, 10, -20, -20, 10, 10, 5,
	0, 0, 0, 0, 0, 0, 0, 0,
}

var knightSquare = [64]int{
	-50, -40, -30, -30, -30, -30, -40, -50,
	-40, -20, 0, 0, 0, 0, -20, -40,
	-30, 0, 10, 15, 15, 10, 0, -30,
	-30, 5, 15, 20, 20, 15, 5, -30,
	-30, 0, 15, 20, 20, 15, 0, -30,
	-30, 5, 10, 15, 15, 10, 5, -30,
	-40, -20, 0, 5, 5, 0, -20, -40,
	-50, -40, -30, -30, -30, -30, -40, -50,
}

var bishopSquare = [64]int{
	-20, -10, -10, -10, -10, -10, -10, -20,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 5, 5, 10, 10, 5, 5, -10,
	-10, 0, 10, 10, 10, 10, 0, -10,
	-10, 10, 10, 10, 10, 10, 10, -10,
	-10, 5, 0, 0, 0, 0, 5, -10,
	-20, -10, -10, -10, -10, -10, -10, -20,
}

var rookSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	5, 10, 10, 10, 10, 10, 10, 5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	0, 0, 0, 5, 5, 0, 0, 0,
}

var queenSquare = [64]int{
	-20, -10, -10, -5, -5, -10, -10, -20,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-5, 0, 5, 5, 5, 5, 0, -5,
	0, 0, 5, 5, 5, 5, 0, -5,
	-10, 5, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 0, 0, 0, 0, -10,
	-20, -10, -10, -5, -5, -10, -10, -20,
}

// King tables are phase-dependent and therefore not maintained incrementally.
// kingSqMidgame rewards castling; kingSqEndgame rewards centralisation.
var kingSqMidgame = [64]int{
	-30, -40, -40, -50, -50, -40, -40, -30,
	-30, -40, -40, -50, -50, -40, -40, -30,
	-30, -40, -40, -50, -50, -40, -40, -30,
	-30, -40, -40, -50, -50, -40, -40, -30,
	-20, -30, -30, -40, -40, -30, -30, -20,
	-10, -20, -20, -20, -20, -20, -20, -10,
	20, 20, 0, 0, 0, 0, 20, 20,
	20, 30, 10, 0, 0, 10, 30, 20,
}

var kingSqEndgame = [64]int{
	-50, -40, -30, -20, -20, -30, -40, -50,
	-30, -20, -10, 0, 0, -10, -20, -30,
	-30, -10, 20, 30, 30, 20, -10, -30,
	-30, -10, 30, 40, 40, 30, -10, -30,
	-30, -10, 30, 40, 40, 30, -10, -30,
	-30, -10, 20, 30, 30, 20, -10, -30,
	-30, -30, 0, 0, 0, 0, -30, -30,
	-50, -30, -30, -30, -30, -30, -30, -50,
}

// isEndgame returns true when neither side has a queen, or every side that has a
// queen also has no rooks and at most one minor piece.
func isEndgame(pos *position) bool {
	for c := white; c <= black; c++ {
		friendlyPieces := pos.colorBB(c)
		if (pos.queens() & friendlyPieces).popCount() == 0 {
			continue
		}
		if (pos.rooks()&friendlyPieces).popCount() > 0 ||
			((pos.knights()|pos.bishops())&friendlyPieces).popCount() > 1 {
			return false
		}
	}
	return true
}

var flips = [2]square{rankFlip, 0}

// pstValue returns material value plus piece-square bonus for piece p of color c
// on sq.  Returns 0 for the king; the king's PST is phase-dependent and applied
// separately in score.
func pstValue(c color, p piece, sq square) int {
	flip := flips[c]
	switch p {
	case pawn:
		return 100 + pawnSquare[sq^flip]
	case knight:
		return 320 + knightSquare[sq^flip]
	case bishop:
		return 330 + bishopSquare[sq^flip]
	case rook:
		return 500 + rookSquare[sq^flip]
	case queen:
		return 900 + queenSquare[sq^flip]
	}
	return 0
}

// score returns material + PST score relative to the side to move.
// scoreWhite/scoreBlack accumulate all non-king piece values incrementally;
// king PST and pawn shelter are computed here because they are phase-dependent
// or too cheap to bother with incremental updates.
func (pos *position) score(endgame bool) int {
	var kingTable *[64]int
	if endgame {
		kingTable = &kingSqEndgame
	} else {
		kingTable = &kingSqMidgame
	}
	wScore := pos.scoreWhite + kingTable[pos.kingLocn[white]^rankFlip]
	bScore := pos.scoreBlack + kingTable[pos.kingLocn[black]]

	// Pawn shelter: +10 cp per pawn directly in front of the king (midgame only).
	if !endgame {
		wScore += 10 * (pos.pawns() & pos.colors[white] & kingPawnShelterWhite[pos.kingLocation(white)]).popCount()
		wScore += 10 * (pos.pawns() & pos.colors[black] & kingPawnShelterBlack[pos.kingLocation(black)]).popCount()
	}

	if pos.colorToMove() == white {
		return wScore - bScore
	}
	return bScore - wScore
}

// evaluatePosition scores the position from the side-to-move's perspective.
// Returns drawScore for positions with insufficient material.
func evaluatePosition(game *Game, _, _ int) int {
	pos := game.currentPosition()
	if pos.isDrawByMaterial() {
		return drawScore
	}
	return pos.score(isEndgame(pos))
}
