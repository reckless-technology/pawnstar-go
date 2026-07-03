package engine

// pins records which friendly pieces are absolutely pinned to the king and, for
// each pinned square, the set of squares the piece may legally move to.
type pins struct {
	pinned  bitboard     // squares occupied by pinned pieces
	allowed [64]bitboard // allowed[sq] is the legal destination set for the pinned piece on sq
}

// computePins finds all absolutely pinned pieces for the side to move and
// records the set of squares each pinned piece may legally move to.
//
// A piece is absolutely pinned if it is the only piece on the ray between the
// king and an enemy slider (bishop, rook, or queen).  A pinned piece may only
// move along that ray — which includes the pinner's square so the pinner can
// be captured, but excludes moving off the ray entirely.
func computePins(pos *position) pins {
	var p pins
	c := pos.colorToMove
	occupied := pos.occupied()
	friendlyPieces := pos.colors[c]
	kingLocation := pos.kingSquare(c)
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

// allowedSquares returns the set of squares the piece on sq may legally move to,
// given the pin constraints computed by computePins.  For an unpinned piece this
// is the full board (^bitboard(0)); for a pinned piece it is the ray between the
// king and the pinner, including the pinner's square.
func (p *pins) allowedSquares(sq square) bitboard {
	if (squareBB(sq) & p.pinned).isNotEmpty() {
		return p.allowed[sq]
	}
	return ^bitboard(0)
}
