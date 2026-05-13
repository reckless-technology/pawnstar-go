package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// irreversibleState holds the fields that cannot be reconstructed by reversing
// a move's piece operations. Saved on MakeMove, restored on UnmakeMove.
type irreversibleState struct {
	hash     uint64
	checkers Bitboard
	castling CastlingRights
	epSquare Square
	halfMove uint8
	flags    uint8
	move     Move // the move that was made to reach the child state
}

// Position holds the complete mutable state of a chess position.
// Use MakeMove / UnmakeMove to traverse the game tree in place.
type Position struct {
	pieces   [6]Bitboard
	colors   [2]Bitboard
	squares  [64]Piece
	checkers Bitboard
	hash     uint64
	kingLoc  [2]Square
	epSquare Square
	halfMove uint8
	fullMove uint8
	castling CastlingRights
	flags    uint8
	// stack of saved state, one entry per MakeMove call not yet undone.
	stateStack []irreversibleState
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
	occupied := pos.Occupied()
	var result Bitboard
	if c == White {
		result = PawnAttacksBlack[sq] & pos.Pawns()
	} else {
		result = PawnAttacksWhite[sq] & pos.Pawns()
	}
	result |= KnightAttacks[sq] & pos.Knights()
	result |= KingAttacks[sq] & pos.Kings()
	result |= RookAttacks(occupied, sq) & (pos.Rooks() | pos.Queens())
	result |= BishopAttacks(occupied, sq) & (pos.Bishops() | pos.Queens())
	result &= pos.colors[c]
	return result
}

func (pos *Position) IsAttacked(sq Square, by Color) bool {
	return pos.AttacksTo(sq, by) != 0
}

// ---- FEN parsing ----

func FromFen(fen string) Position {
	pos := Position{stateStack: make([]irreversibleState, 0, MaxPly)}
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
			idx := strings.IndexRune("pnbrqkPNBRQK", ch)
			if idx >= 6 {
				idx -= 6
			}
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

// MakeMove applies move to pos in place. Call UnmakeMove to reverse it.
func (pos *Position) MakeMove(m Move) {
	color := pos.ColorToMove()
	oldCastling := pos.castling

	pos.stateStack = append(pos.stateStack, irreversibleState{
		hash:     pos.hash,
		checkers: pos.checkers,
		castling: pos.castling,
		epSquare: pos.epSquare,
		halfMove: pos.halfMove,
		flags:    pos.flags,
		move:     m,
	})

	from, to, piece := m.From(), m.To(), m.Piece()
	pos.flags &^= flagNullMove
	pos.castling = oldCastling.AfterMove(m)
	pos.hash ^= CastlingRightsHashes[pos.castling.Value()] ^ CastlingRightsHashes[oldCastling.Value()]
	pos.hash ^= EnPassantHashes[pos.epSquare]
	pos.epSquare = NoSquare

	switch m.Type() {
	case NonCapture:
		pos.halfMove++
		pos.movePiece(color, piece, from, to)

	case Capture:
		pos.halfMove = 0
		pos.removePiece(EnemyOf(color), m.Captured(), to)
		pos.movePiece(color, piece, from, to)

	case PromotionKnight, PromotionBishop, PromotionRook, PromotionQueen:
		pos.halfMove = 0
		if m.Captured() != NoPiece {
			pos.removePiece(EnemyOf(color), m.Captured(), to)
		}
		pos.removePiece(color, Pawn, from)
		pos.addPiece(color, m.Promoted(), to)

	case PawnDoublePush:
		pos.halfMove = 0
		pos.movePiece(color, Pawn, from, to)
		pos.epSquare = Square((int(from) + int(to)) >> 1)
		pos.hash ^= EnPassantHashes[pos.epSquare]

	case EpCapture:
		pos.halfMove = 0
		capturedSq := Square((int(from) & 0x38) | (int(to) & 0x07))
		pos.removePiece(EnemyOf(color), Pawn, capturedSq)
		pos.movePiece(color, Pawn, from, to)

	case Castling:
		pos.halfMove = 0
		switch to {
		case g1:
			pos.movePiece(White, King, e1, g1)
			pos.movePiece(White, Rook, h1, f1)
		case c1:
			pos.movePiece(White, King, e1, c1)
			pos.movePiece(White, Rook, a1, d1)
		case g8:
			pos.movePiece(Black, King, e8, g8)
			pos.movePiece(Black, Rook, h8, f8)
		case c8:
			pos.movePiece(Black, King, e8, c8)
			pos.movePiece(Black, Rook, a8, d8)
		}
	}

	pos.flags ^= flagBlackToMove
	pos.hash ^= BlackMoveHash
	if color == Black {
		pos.fullMove++
	}
	pos.kingLoc[color] = (pos.Kings() & pos.colors[color]).Lsb()
	pos.checkers = pos.AttacksTo(pos.kingLoc[EnemyOf(color)], color)
}

// UnmakeMove reverses the last MakeMove or MakeNullMove call.
func (pos *Position) UnmakeMove() {
	n := len(pos.stateStack)
	saved := pos.stateStack[n-1]
	pos.stateStack = pos.stateStack[:n-1]

	pos.flags = saved.flags
	color := pos.ColorToMove() // color that made the move

	m := saved.move
	if !m.IsNone() {
		from, to, piece := m.From(), m.To(), m.Piece()
		enemy := EnemyOf(color)
		switch m.Type() {
		case NonCapture, PawnDoublePush:
			pos.movePiece(color, piece, to, from)
		case Capture:
			pos.movePiece(color, piece, to, from)
			pos.addPiece(enemy, m.Captured(), to)
		case PromotionKnight, PromotionBishop, PromotionRook, PromotionQueen:
			pos.removePiece(color, m.Promoted(), to)
			pos.addPiece(color, Pawn, from)
			if m.Captured() != NoPiece {
				pos.addPiece(enemy, m.Captured(), to)
			}
		case EpCapture:
			capturedSq := Square((int(from) & 0x38) | (int(to) & 0x07))
			pos.movePiece(color, Pawn, to, from)
			pos.addPiece(enemy, Pawn, capturedSq)
		case Castling:
			switch to {
			case g1:
				pos.movePiece(White, King, g1, e1)
				pos.movePiece(White, Rook, f1, h1)
			case c1:
				pos.movePiece(White, King, c1, e1)
				pos.movePiece(White, Rook, d1, a1)
			case g8:
				pos.movePiece(Black, King, g8, e8)
				pos.movePiece(Black, Rook, f8, h8)
			case c8:
				pos.movePiece(Black, King, c8, e8)
				pos.movePiece(Black, Rook, d8, a8)
			}
		}
		pos.kingLoc[color] = (pos.Kings() & pos.colors[color]).Lsb()
		if color == Black {
			pos.fullMove--
		}
	}

	pos.hash = saved.hash
	pos.checkers = saved.checkers
	pos.castling = saved.castling
	pos.epSquare = saved.epSquare
	pos.halfMove = saved.halfMove
}

// MakeNullMove flips side to move without moving a piece.
func (pos *Position) MakeNullMove() {
	pos.stateStack = append(pos.stateStack, irreversibleState{
		hash:     pos.hash,
		checkers: pos.checkers,
		castling: pos.castling,
		epSquare: pos.epSquare,
		halfMove: pos.halfMove,
		flags:    pos.flags,
		move:     MoveNone(),
	})
	pos.hash ^= EnPassantHashes[pos.epSquare]
	pos.hash ^= BlackMoveHash
	pos.epSquare = NoSquare
	pos.flags |= flagNullMove
	pos.flags ^= flagBlackToMove
}

// IsDrawByRepetition returns true if the current position has appeared twice before
// in the game history recorded on the stateStack.
func (pos *Position) IsDrawByRepetition() bool {
	hash := pos.hash
	k := len(pos.stateStack)
	repetitions := 2
	for i := k - 4; i >= 0; i -= 2 {
		s := pos.stateStack[i]
		if s.hash == hash {
			repetitions--
			if repetitions == 0 {
				return true
			}
		}
		if s.halfMove == 0 {
			return false
		}
	}
	return false
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
