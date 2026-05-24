package engine

import (
	"slices"
)

// move is a packed int64.  Bit layout (identical to the C++ version):
//
//	 0– 5  to square
//	 6–11  from square
//	12–14  moving piece
//	15–17  captured piece (noPiece if non-capture)
//	18–21  move type (moveType value)
//	   22  gives-check flag
//	32–63  sort score (not part of move identity; ignored by equal and isNone)
type move int64

// moveType classifies a move for make/unmake dispatch.
type moveType uint8

const (
	nonCapture      moveType = iota
	capture         moveType = iota
	promotionKnight moveType = iota
	promotionBishop moveType = iota
	promotionRook   moveType = iota
	promotionQueen  moveType = iota
	pawnSinglePush  moveType = iota
	pawnDoublePush  moveType = iota
	epCapture       moveType = iota
	castling        moveType = iota
	isCheckingBit   move     = 1 << 22
)

func constructMove(from, to square, p piece, mt moveType, captured piece) move {
	return move(int64(to) | int64(from)<<6 | int64(p)<<12 | int64(captured)<<15 | int64(mt)<<18)
}

// Constructor helpers — each encodes the move type and fills in fixed fields.
func moveNonCapture(from, to square, p piece) move {
	return move(int64(to) | int64(from)<<6 | int64(p)<<12)
}
func moveCapture(from, to square, p, captured piece) move {
	return constructMove(from, to, p, capture, captured)
}
func movePromotion(from, to square, promoted piece, captured piece) move {
	return constructMove(from, to, pawn, moveType(promoted), captured)
}
func moveCastling(from, to square) move {
	return constructMove(from, to, king, castling, noPiece)
}
func moveEpCapture(from, to square) move {
	return constructMove(from, to, pawn, epCapture, pawn)
}
func moveDoublePush(from, to square) move {
	return constructMove(from, to, pawn, pawnDoublePush, noPiece)
}
func moveSinglePush(from, to square) move {
	return constructMove(from, to, pawn, pawnSinglePush, noPiece)
}

// moveNone is the null sentinel (all identity bits zero, which is not a legal move).
func moveNone() move { return move(0) }

func (m move) to() square       { return square(m & 0x3F) }
func (m move) from() square     { return square((m >> 6) & 0x3F) }
func (m move) piece() piece     { return piece((m >> 12) & 0x07) }
func (m move) captured() piece  { return piece((m >> 15) & 0x07) }
func (m move) kind() moveType   { return moveType((m >> 18) & 0x0F) }
func (m move) score() int       { return int(m >> 32) }
func (m move) isChecking() bool { return (m & isCheckingBit) != 0 }

// fromTo returns the combined from+to index (bits 0–11) used as a hash key in
// the history table.
func (m move) fromTo() int { return int(m) & 0xFFF }

func (m move) promoted() piece {
	switch m.kind() {
	case promotionKnight:
		return knight
	case promotionBishop:
		return bishop
	case promotionRook:
		return rook
	case promotionQueen:
		return queen
	}
	return noPiece
}

// withScore returns a copy of m with the sort score replaced by s.
// The identity bits (0–31) are preserved unchanged.
func (m move) withScore(s int) move {
	return move((int64(m) & 0xFFFFFFFF) | int64(s)<<32)
}

// equal compares move identity (from/to/piece/kind/captured), ignoring the score.
func (m move) equal(other move) bool {
	return (m & 0x3FFFFF) == (other & 0x3FFFFF)
}

// String returns the move in UCI long-algebraic notation ("e2e4", "e7e8q", "0000" for null).
func (m move) String() string {
	if m.isNone() {
		return "0000"
	}
	s := m.from().toString() + m.to().toString()
	if m.promoted() != noPiece {
		s += m.promoted().toString()
	}
	return s
}

// GoString returns a human-readable debug representation, e.g. "pe2e4" or "NONE".
func (m move) GoString() string {
	if m == moveNone() {
		return "NONE"
	}
	s := m.piece().toString() + m.from().toString() + m.to().toString()
	if m.captured() != noPiece {
		s += "x" + m.captured().toString()
	}
	if m.promoted() != noPiece {
		s += "=" + m.promoted().toString()
	}
	return s
}

// isNone reports whether m is the null sentinel (all identity bits zero).
func (m move) isNone() bool { return (m & 0x3FFFFF) == 0 }

// sortMovesUnstable sorts moves by descending score.  Does not preserve order of
// equal-score moves; faster than the stable variant.
func sortMovesUnstable(moves []move) {
	slices.SortFunc(moves, func(a, b move) int { return b.score() - a.score() })
}

// sortMovesStable sorts moves by descending score, preserving the relative order
// of equal-score moves.  Used at the root so the previous iteration's ordering
// is maintained among moves with the same score.
func sortMovesStable(moves []move) {
	slices.SortStableFunc(moves, func(a, b move) int { return b.score() - a.score() })
}
