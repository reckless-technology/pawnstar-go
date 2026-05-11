package engine

import "fmt"

// Move is a packed int64, bit-layout identical to the C++ version:
//   0- 5: to square
//   6-11: from square
//  12-14: moving piece
//  15-17: captured piece
//  18-21: move type
//     22: gives-check flag
//  32-63: sort score
type Move int64

// MoveType classifies a move.
type MoveType uint8

const (
	NonCapture      MoveType = 0
	Capture         MoveType = 1
	PromotionKnight MoveType = MoveType(Knight)
	PromotionBishop MoveType = MoveType(Bishop)
	PromotionRook   MoveType = MoveType(Rook)
	PromotionQueen  MoveType = MoveType(Queen)
	PawnDoublePush  MoveType = 7
	EpCapture       MoveType = 8
	Castling        MoveType = 9
)

const isCheckingBit int64 = 1 << 22

func makeMove(from, to Square, piece Piece, mt MoveType, captured Piece) Move {
	return Move(int64(to) | int64(from)<<6 | int64(piece)<<12 | int64(captured)<<15 | int64(mt)<<18)
}

func MoveNonCapture(from, to Square, piece Piece) Move {
	return makeMove(from, to, piece, NonCapture, NoPiece)
}
func MoveCapture(from, to Square, piece, captured Piece) Move {
	return makeMove(from, to, piece, Capture, captured)
}
func MovePromotion(from, to Square, promoted Piece, captured Piece) Move {
	return makeMove(from, to, Pawn, MoveType(promoted), captured)
}
func MoveCastling(from, to Square) Move {
	return makeMove(from, to, King, Castling, NoPiece)
}
func MoveEpCapture(from, to Square) Move {
	return makeMove(from, to, Pawn, EpCapture, Pawn)
}
func MoveDoublePush(from, to Square) Move {
	return makeMove(from, to, Pawn, PawnDoublePush, NoPiece)
}
func MoveNone() Move { return Move(0) }

func (m Move) To() Square    { return Square(m & 0x3F) }
func (m Move) From() Square  { return Square((m >> 6) & 0x3F) }
func (m Move) Piece() Piece  { return Piece((m >> 12) & 0x07) }
func (m Move) Captured() Piece { return Piece((m >> 15) & 0x07) }
func (m Move) Type() MoveType  { return MoveType((m >> 18) & 0x0F) }
func (m Move) Score() int      { return int(m >> 32) }
func (m Move) IsChecking() bool { return (int64(m) & isCheckingBit) != 0 }
func (m Move) FromTo() int { return int(m) & 0xFFF }

func (m Move) Promoted() Piece {
	switch m.Type() {
	case PromotionKnight:
		return Knight
	case PromotionBishop:
		return Bishop
	case PromotionRook:
		return Rook
	case PromotionQueen:
		return Queen
	}
	return NoPiece
}

func (m Move) WithScore(score int) Move {
	return Move((int64(m) & 0xFFFFFFFF) | int64(score)<<32)
}

func (m Move) GivesCheck() Move {
	return Move(int64(m) | isCheckingBit)
}

func (m Move) Equal(other Move) bool {
	return (int64(m) & 0x3FFFFF) == (int64(other) & 0x3FFFFF)
}

func (m Move) String() string {
	if m == MoveNone() {
		return "0000"
	}
	s := m.From().String() + m.To().String()
	if p := m.Promoted(); p != NoPiece {
		s += string(" pnbrqk"[p])
	}
	return s
}

func (m Move) IsNone() bool { return m == 0 }

// MoveList is a fixed-capacity stack-allocated move list.
const MaxMoves = 220

type MoveList struct {
	moves [MaxMoves]Move
	n     int
}

func (ml *MoveList) Push(m Move)        { ml.moves[ml.n] = m; ml.n++ }
func (ml *MoveList) Len() int           { return ml.n }
func (ml *MoveList) At(i int) Move      { return ml.moves[i] }
func (ml *MoveList) Set(i int, m Move)  { ml.moves[i] = m }
func (ml *MoveList) Slice() []Move      { return ml.moves[:ml.n] }

func (ml *MoveList) SortDescending() {
	s := ml.Slice()
	// Insertion sort is fast for small lists (typically < 50 moves).
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j].Score() < key.Score() {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}

func (m Move) GoString() string {
	return fmt.Sprintf("Move(%s type=%d piece=%d cap=%d)", m.String(), m.Type(), m.Piece(), m.Captured())
}
