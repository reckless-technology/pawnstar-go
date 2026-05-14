package engine

// pins tracks pinned pieces and their allowed destination squares.
type pins struct {
	pinned  bitboard
	allowed [64]bitboard
}

func computePins(pos *position) pins {
	var p pins
	color := pos.colorToMove()
	occupied := pos.occupied()
	friendlyPieces := pos.colors[color]
	kingLocation := pos.kingLocn[color]
	intervening := &interveningSquares[kingLocation]

	// Candidate pinners: enemy sliders that are on a bishop/rook ray from the king.
	enemySliders := (bishopAttacksEmpty[kingLocation] & (pos.bishops() | pos.queens())) |
		(rookAttacksEmpty[kingLocation] & (pos.rooks() | pos.queens()))
	enemySliders &= ^friendlyPieces

	for enemySliders.isNotEmpty() {
		s := enemySliders.popLsb()
		betweenSquares := intervening[s]
		betweenPieces := betweenSquares & occupied
		betweenFriendly := betweenPieces & friendlyPieces
		if betweenFriendly.isNotEmpty() && betweenPieces.popCount() == 1 {
			pinnedSq := betweenFriendly.lsb()
			p.pinned |= betweenFriendly
			p.allowed[pinnedSq] = betweenSquares | squareBB(s)
		}
	}
	return p
}

func (p *pins) allowedSquares(sq square) bitboard {
	if (squareBB(sq) & p.pinned).isNotEmpty() {
		return p.allowed[sq]
	}
	return ^bitboard(0)
}
