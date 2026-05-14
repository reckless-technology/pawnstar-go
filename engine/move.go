package engine

import (
	"cmp"
	"slices"
)

// move is a packed int64, bit-layout identical to the C++ version:
//
//	 0- 5: to square
//	 6-11: from square
//	12-14: moving piece
//	15-17: captured piece
//	18-21: move type
//	   22: gives-check flag
//	32-63: score
type move int64

// moveType classifies a move.
type moveType uint8

const (
	nonCapture moveType = iota
	capture
	promotionKnight
	promotionBishop
	promotionRook
	promotionQueen
	pawnSinglePush
	pawnDoublePush
	epCapture
	castling
)

const isCheckingBit int64 = 1 << 22

func constructMove(from, to square, p piece, mt moveType, captured piece) move {
	return move(int64(to) | int64(from)<<6 | int64(p)<<12 | int64(captured)<<15 | int64(mt)<<18)
}

func moveNonCapture(from, to square, p piece) move {
	return constructMove(from, to, p, nonCapture, noPiece)
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
func moveNone() move { return move(0) }

func (m move) to() square       { return square(m & 0x3F) }
func (m move) from() square     { return square((m >> 6) & 0x3F) }
func (m move) piece() piece     { return piece((m >> 12) & 0x07) }
func (m move) captured() piece  { return piece((m >> 15) & 0x07) }
func (m move) kind() moveType   { return moveType((m >> 18) & 0x0F) }
func (m move) score() int       { return int(m >> 32) }
func (m move) isChecking() bool { return (int64(m) & isCheckingBit) != 0 }
func (m move) fromTo() int      { return int(m) & 0xFFF }

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

func (m move) withScore(s int) move {
	return move((int64(m) & 0xFFFFFFFF) | int64(s)<<32)
}

func (m move) equal(other move) bool {
	return (int64(m) & 0x3FFFFF) == (int64(other) & 0x3FFFFF)
}

func (m move) String() string {
	if m == moveNone() {
		return "0000"
	}
	s := m.from().String() + m.to().String()
	if m.promoted() != noPiece {
		s += "=" + m.promoted().toString()
	}
	return s
}

func (m move) GoString() string {
	if m == moveNone() {
		return "NONE"
	}
	s := m.piece().toString() + m.from().String() + m.to().String()
	if m.captured() != noPiece {
		s += "x" + m.captured().toString()
	}
	if m.promoted() != noPiece {
		s += "=" + m.promoted().toString()
	}
	return s
}

func (m move) isNone() bool { return (m & 0x3FFFFF) == 0 }

func sortMovesUnstable(moves []move) {
	slices.SortFunc(moves, func(a, b move) int { return cmp.Compare(b.score(), a.score()) })
}

func sortMovesStable(moves []move) {
	slices.SortStableFunc(moves, func(a, b move) int { return cmp.Compare(b.score(), a.score()) })
}
