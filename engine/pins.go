package engine

// Pins tracks pinned pieces and their allowed destination squares.
type Pins struct {
	pinned  Bitboard
	allowed [64]Bitboard
}

func computePins(pos *Position) Pins {
	var pins Pins
	color := pos.ColorToMove()
	occ := pos.Occupied()
	friendly := pos.colors[color]
	kingLoc := pos.kingLoc[color]

	// Candidate pinners: enemy sliders that are on a bishop/rook ray from the king.
	enemySliders :=
		((BishopAttacksEmpty[kingLoc] & (pos.Bishops() | pos.Queens())) |
			(RookAttacksEmpty[kingLoc] & (pos.Rooks() | pos.Queens()))) &^
			friendly

	for enemySliders != 0 {
		var s Square
		enemySliders, s = enemySliders.PopLsb()
		between := InterveningSquares[kingLoc][s]
		betweenPieces := between & occ
		betweenFriendly := betweenPieces & friendly
		if betweenFriendly != 0 && betweenPieces.PopCount() == 1 {
			pinnedSq := betweenFriendly.Lsb()
			pins.pinned |= betweenFriendly
			pins.allowed[pinnedSq] = between | SquareBB(s)
		}
	}
	return pins
}

func (p *Pins) AllowedSquares(sq Square) Bitboard {
	if SquareBB(sq)&p.pinned != 0 {
		return p.allowed[sq]
	}
	return BBAll
}
