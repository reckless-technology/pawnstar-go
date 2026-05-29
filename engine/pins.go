package engine

// pins records which friendly pieces are absolutely pinned to the king and, for
// each pinned square, the set of squares the piece may legally move to.
type pins struct {
	pinned  bitboard     // squares occupied by pinned pieces
	allowed [64]bitboard // allowed[sq] is the legal destination set for the pinned piece on sq
}

// computePins finds all absolutely pinned pieces for the side to move.
//
// A piece is pinned if it is the sole occupant between the king and an enemy
// slider on a shared ray.  For each such piece, the legal destination set is
// restricted to that ray (including the pinner's square, so the pinner can be
// captured).
func computePins(pos *position) pins {
	var p pins
	color := pos.colorToMove
	occupied := pos.occupied()
	friendlyPieces := pos.colors[color]
	kingLocation := pos.kingLocation[color]
	intervening := &interveningSquares[kingLocation]

	// Candidate pinners: enemy sliders on a bishop or rook ray from the king.
	enemySliders := (bishopAttacksEmpty[kingLocation] & (pos.pieces[bishop] | pos.pieces[queen])) |
		(rookAttacksEmpty[kingLocation] & (pos.pieces[rook] | pos.pieces[queen]))
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

// allowedSquares returns the squares that the piece on sq may legally move to,
// accounting for pin constraints.  Returns all squares for an unpinned piece.
func (p *pins) allowedSquares(sq square) bitboard {
	if (squareBB(sq) & p.pinned).isNotEmpty() {
		return p.allowed[sq]
	}
	return ^bitboard(0)
}
