package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Position holds the complete state of a chess position.
// It is designed to be copied by value; MakeMove returns a new Position.
type Position struct {
	// Per-piece bitboards (indices match Piece constants minus 1: 0=pawn … 5=king).
	pieces [6]Bitboard
	// Per-color bitboards.
	colors [2]Bitboard
	// Square → piece lookup.
	squares [64]Piece
	// Squares attacking the side-to-move's king (if any).
	checkers Bitboard
	// Incremental Zobrist hash.
	hash uint64
	// King locations.
	kingLoc [2]Square
	// En passant target square (0 = none).
	epSquare Square
	// Half-move clock (reversible move counter).
	halfMove uint8
	// Full-move number (zero-indexed).
	fullMove uint8
	// Castling rights.
	castling CastlingRights
	// State flags.
	flags uint8
}

const (
	flagBlackToMove uint8 = 0x01
	flagNullMove    uint8 = 0x02
)

func (pos *Position) ColorToMove() Color {
	if pos.flags&flagBlackToMove != 0 {
		return Black
	}
	return White
}

func (pos *Position) IsNullMove() bool            { return pos.flags&flagNullMove != 0 }
func (pos *Position) Hash() uint64                { return pos.hash }
func (pos *Position) IsInCheck() bool             { return pos.checkers != 0 }
func (pos *Position) Checkers() Bitboard          { return pos.checkers }
func (pos *Position) EPSquare() Square            { return pos.epSquare }
func (pos *Position) HalfMove() int               { return int(pos.halfMove) }
func (pos *Position) FullMove() int               { return int(pos.fullMove) + 1 }
func (pos *Position) Castling() CastlingRights    { return pos.castling }
func (pos *Position) KingLocation(c Color) Square { return pos.kingLoc[c] }
func (pos *Position) PieceAt(sq Square) Piece     { return pos.squares[sq] }
func (pos *Position) PieceBB(p Piece) Bitboard    { return pos.pieces[p-1] }
func (pos *Position) ColorBB(c Color) Bitboard    { return pos.colors[c] }
func (pos *Position) Occupied() Bitboard          { return pos.colors[White] | pos.colors[Black] }
func (pos *Position) Vacant() Bitboard            { return ^pos.Occupied() }
func (pos *Position) Pawns() Bitboard             { return pos.pieces[Pawn-1] }
func (pos *Position) Knights() Bitboard           { return pos.pieces[Knight-1] }
func (pos *Position) Bishops() Bitboard           { return pos.pieces[Bishop-1] }
func (pos *Position) Rooks() Bitboard             { return pos.pieces[Rook-1] }
func (pos *Position) Queens() Bitboard            { return pos.pieces[Queen-1] }
func (pos *Position) Kings() Bitboard             { return pos.pieces[King-1] }

// ---- mutation helpers ----

func (pos *Position) addPiece(c Color, p Piece, sq Square) {
	bb := SquareBB(sq)
	pos.pieces[p-1] |= bb
	pos.colors[c] |= bb
	pos.hash ^= PieceSquareHashes[c][p-1][sq]
	pos.squares[sq] = p
}

func (pos *Position) removePiece(c Color, p Piece, sq Square) {
	bb := SquareBB(sq)
	pos.pieces[p-1] &^= bb
	pos.colors[c] &^= bb
	pos.hash ^= PieceSquareHashes[c][p-1][sq]
	pos.squares[sq] = NoPiece
}

func (pos *Position) movePiece(c Color, p Piece, from, to Square) {
	fromBB, toBB := SquareBB(from), SquareBB(to)
	fromTo := fromBB | toBB
	pos.pieces[p-1] ^= fromTo
	pos.colors[c] ^= fromTo
	pos.hash ^= PieceSquareHashes[c][p-1][from] ^ PieceSquareHashes[c][p-1][to]
	pos.squares[from] = NoPiece
	pos.squares[to] = p
}

// AttacksTo returns the set of squares of color c that attack sq.
func (pos *Position) AttacksTo(sq Square, c Color) Bitboard {
	occ := pos.Occupied()
	enemy := EnemyOf(c)
	var result Bitboard
	if c == White {
		result = PawnAttacksBlack[sq] & pos.Pawns()
	} else {
		result = PawnAttacksWhite[sq] & pos.Pawns()
	}
	result |= KnightAttacks[sq] & pos.Knights()
	result |= KingAttacks[sq] & pos.Kings()
	result |= RookAttacks(occ, sq) & (pos.Rooks() | pos.Queens())
	result |= BishopAttacks(occ, sq) & (pos.Bishops() | pos.Queens())
	_ = enemy
	result &= pos.colors[c]
	return result
}

func (pos *Position) IsAttacked(sq Square, by Color) bool {
	return pos.AttacksTo(sq, by) != 0
}

// ---- FEN parsing ----

func FromFen(fen string) Position {
	var pos Position
	fields := strings.Fields(fen)

	// Pieces
	rank, file := 7, 0
	for _, ch := range fields[0] {
		switch ch {
		case '/':
			rank--
			file = 0
		case '1', '2', '3', '4', '5', '6', '7', '8':
			file += int(ch - '0')
		default:
			c := White
			if ch >= 'a' && ch <= 'z' {
				c = Black
			}
			piece := "  pnbrqk  PNBRQK"
			idx := strings.IndexRune("pnbrqkPNBRQK", ch)
			if idx >= 6 {
				idx -= 6
			}
			_ = piece
			p := Piece(idx + 1)
			pos.addPiece(c, p, SquareXY(file, rank))
			file++
		}
	}

	// Side to move
	if len(fields) > 1 && fields[1] == "b" {
		pos.flags |= flagBlackToMove
	}

	pos.kingLoc[White] = (pos.Kings() & pos.colors[White]).Lsb()
	pos.kingLoc[Black] = (pos.Kings() & pos.colors[Black]).Lsb()

	// Castling rights
	if len(fields) > 2 {
		pos.castling = CastlingRightsFromFen(fields[2])
	}

	// En passant
	if len(fields) > 3 && fields[3] != "-" {
		pos.epSquare = SquareFromString(fields[3])
	}

	// Half-move clock
	if len(fields) > 4 {
		hm, _ := strconv.Atoi(fields[4])
		pos.halfMove = uint8(hm)
	}

	// Full-move number
	if len(fields) > 5 {
		fm, _ := strconv.Atoi(fields[5])
		pos.fullMove = uint8(fm - 1)
	}

	pos.hash = pos.computeHash()
	color := pos.ColorToMove()
	pos.checkers = pos.AttacksTo(pos.kingLoc[color], EnemyOf(color))
	return pos
}

func (pos *Position) computeHash() uint64 {
	var h uint64
	if pos.flags&flagBlackToMove != 0 {
		h ^= BlackMoveHash
	}
	h ^= CastlingRightsHashes[pos.castling.Value()]
	h ^= EnPassantHashes[pos.epSquare]
	for p := Pawn; p <= King; p++ {
		b := pos.PieceBB(p) & pos.colors[White]
		for b != 0 {
			var sq Square
			b, sq = b.PopLsb()
			h ^= PieceSquareHashes[White][p-1][sq]
		}
		b = pos.PieceBB(p) & pos.colors[Black]
		for b != 0 {
			var sq Square
			b, sq = b.PopLsb()
			h ^= PieceSquareHashes[Black][p-1][sq]
		}
	}
	return h
}

// ---- FEN serialisation ----

func (pos *Position) ToFen() string {
	var sb strings.Builder
	occ := pos.Occupied()
	for rank := 7; rank >= 0; rank-- {
		empty := 0
		for file := 0; file < 8; file++ {
			sq := SquareXY(file, rank)
			if occ&SquareBB(sq) == 0 {
				empty++
			} else {
				if empty > 0 {
					sb.WriteByte(byte('0' + empty))
					empty = 0
				}
				p := pos.squares[sq]
				isWhite := pos.colors[White]&SquareBB(sq) != 0
				ch := " PNBRQK"[p]
				if !isWhite {
					ch = " pnbrqk"[p]
				}
				sb.WriteByte(ch)
			}
		}
		if empty > 0 {
			sb.WriteByte(byte('0' + empty))
		}
		if rank > 0 {
			sb.WriteByte('/')
		}
	}
	sb.WriteByte(' ')
	if pos.flags&flagBlackToMove != 0 {
		sb.WriteByte('b')
	} else {
		sb.WriteByte('w')
	}
	sb.WriteByte(' ')
	sb.WriteString(pos.castling.ToFenString())
	sb.WriteByte(' ')
	if pos.epSquare == NoSquare {
		sb.WriteByte('-')
	} else {
		sb.WriteString(pos.epSquare.String())
	}
	sb.WriteString(fmt.Sprintf(" %d %d", pos.halfMove, int(pos.fullMove)+1))
	return sb.String()
}

// ---- MakeMove ----

// MakeMove applies move to pos and returns the resulting position.
func (pos *Position) MakeMove(m Move) Position {
	color := pos.ColorToMove()
	from := m.From()
	to := m.To()
	piece := m.Piece()

	next := *pos
	next.flags &^= flagNullMove
	next.castling = pos.castling.AfterMove(m)
	next.hash ^= CastlingRightsHashes[next.castling.Value()] ^ CastlingRightsHashes[pos.castling.Value()]
	next.hash ^= EnPassantHashes[next.epSquare]
	next.epSquare = NoSquare

	switch m.Type() {
	case NonCapture:
		next.halfMove++
		next.movePiece(color, piece, from, to)

	case Capture:
		next.halfMove = 0
		next.removePiece(EnemyOf(color), m.Captured(), to)
		next.movePiece(color, piece, from, to)

	case PromotionKnight, PromotionBishop, PromotionRook, PromotionQueen:
		next.halfMove = 0
		if m.Captured() != NoPiece {
			next.removePiece(EnemyOf(color), m.Captured(), to)
		}
		next.removePiece(color, Pawn, from)
		next.addPiece(color, m.Promoted(), to)

	case PawnDoublePush:
		next.halfMove = 0
		next.movePiece(color, Pawn, from, to)
		next.epSquare = Square((int(from) + int(to)) >> 1)
		next.hash ^= EnPassantHashes[next.epSquare]

	case EpCapture:
		next.halfMove = 0
		capturedSq := Square((int(from) & 0x38) | (int(to) & 0x07))
		next.removePiece(EnemyOf(color), Pawn, capturedSq)
		next.movePiece(color, Pawn, from, to)

	case Castling:
		next.halfMove = 0
		switch to {
		case g1:
			next.movePiece(White, King, e1, g1)
			next.movePiece(White, Rook, h1, f1)
		case c1:
			next.movePiece(White, King, e1, c1)
			next.movePiece(White, Rook, a1, d1)
		case g8:
			next.movePiece(Black, King, e8, g8)
			next.movePiece(Black, Rook, h8, f8)
		case c8:
			next.movePiece(Black, King, e8, c8)
			next.movePiece(Black, Rook, a8, d8)
		}
	}

	next.flags ^= flagBlackToMove
	next.hash ^= BlackMoveHash
	if color == Black {
		next.fullMove++
	}
	next.kingLoc[color] = (next.Kings() & next.colors[color]).Lsb()
	next.checkers = next.AttacksTo(next.kingLoc[EnemyOf(color)], color)
	return next
}

// MakeNullMove flips side to move without moving a piece.
func (pos *Position) MakeNullMove() Position {
	next := *pos
	next.flags |= flagNullMove
	next.flags ^= flagBlackToMove
	next.hash ^= EnPassantHashes[next.epSquare]
	next.hash ^= BlackMoveHash
	next.epSquare = NoSquare
	return next
}

// IsLegal returns true if the position is a legal chess position.
func (pos *Position) IsLegal() bool {
	color := pos.ColorToMove()
	wk := pos.Kings() & pos.colors[White]
	bk := pos.Kings() & pos.colors[Black]
	return wk.PopCount() == 1 && bk.PopCount() == 1 && wk != bk &&
		KingAttacks[wk.Lsb()]&bk == 0 &&
		!pos.IsAttacked(pos.kingLoc[EnemyOf(color)], color)
}

func (pos *Position) IsCheckmate() bool {
	ml := pos.GenerateLegalMoves()
	return pos.IsInCheck() && len(ml) == 0
}

func (pos *Position) IsStalemate() bool {
	ml := pos.GenerateLegalMoves()
	return !pos.IsInCheck() && len(ml) == 0
}

// IsDrawByMaterial returns true when neither side has enough material to checkmate.
func (pos *Position) IsDrawByMaterial() bool {
	if pos.Pawns() != 0 || pos.Rooks() != 0 || pos.Queens() != 0 {
		return false
	}
	// Only kings, knights, and/or bishops remain.
	// Insufficient material: KK, KNK, KBK, KBKB (same colour).
	minor := pos.Knights() | pos.Bishops()
	if minor.PopCount() <= 1 {
		return true
	}
	// Two bishops on same colour squares is also a draw.
	if pos.Knights() == 0 && minor.PopCount() == 2 {
		if minor&WhiteSqs == minor || minor&BlackSqs == minor {
			return true
		}
	}
	return false
}
