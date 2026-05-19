package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// irreversibleState holds the fields that cannot be reconstructed from a move
// alone and must be saved before makeMove and restored by unmakeMove.
type irreversibleState struct {
	hash     uint64
	checkers bitboard
	move     move
	castling castlingRights
	epSquare square
	halfMove uint8
	flags    uint8
}

// position is the central board state, mutated in place during search.
//
// Bitboards and the squares array are always kept in sync.  Piece-square table
// scores (scoreWhite, scoreBlack) are maintained incrementally by addPiece,
// removePiece, and movePiece so that evaluation is a simple subtraction at leaf
// nodes.  The Zobrist hash is similarly maintained incrementally.
//
// The stateStack records the irreversible fields pushed by makeMove and popped
// by unmakeMove, allowing the position to be restored without copying the struct.
type position struct {
	pieces     [6]bitboard        // indexed by piece-1 (pawn=0 … king=5)
	colors     [2]bitboard        // indexed by color (white=0, black=1)
	squares    [64]piece          // O(1) piece lookup by square
	checkers   bitboard           // squares of pieces giving check to the side to move
	hash       uint64             // Zobrist hash of the current position
	stateStack []irreversibleState
	scoreWhite int // incremental material + PST score for White (excludes king)
	scoreBlack int // incremental material + PST score for Black (excludes king)
	kingLocn   [2]square          // king location per side; updated separately from pieces[]
	epSquare   square             // en-passant target square, or noSquare
	halfMove   uint8              // half-move clock (for 50-move rule)
	fullMove   uint8              // full-move number minus 1 (stored 0-based)
	castling   castlingRights
	flags      uint8
}

const (
	flagBlackToMove uint8 = 0x01
	flagNullMove    uint8 = 0x02
)

func (pos *position) colorToMove() color {
	if pos.flags&flagBlackToMove != 0 {
		return black
	}
	return white
}

func (pos *position) isNullMove() bool            { return pos.flags&flagNullMove != 0 }
func (pos *position) getHash() uint64             { return pos.hash }
func (pos *position) isInCheck() bool             { return pos.checkers.isNotEmpty() }
func (pos *position) getHalfMove() int            { return int(pos.halfMove) }
func (pos *position) kingLocation(c color) square { return pos.kingLocn[c] }
func (pos *position) pieceAt(sq square) piece     { return pos.squares[sq] }
func (pos *position) pieceBB(p piece) bitboard    { return pos.pieces[p-1] }
func (pos *position) colorBB(c color) bitboard    { return pos.colors[c] }
func (pos *position) occupied() bitboard          { return pos.colors[white] | pos.colors[black] }
func (pos *position) pawns() bitboard             { return pos.pieces[pawn-1] }
func (pos *position) knights() bitboard           { return pos.pieces[knight-1] }
func (pos *position) bishops() bitboard           { return pos.pieces[bishop-1] }
func (pos *position) rooks() bitboard             { return pos.pieces[rook-1] }
func (pos *position) queens() bitboard            { return pos.pieces[queen-1] }
func (pos *position) kings() bitboard             { return pos.pieces[king-1] }

// addPiece places p of color c on sq and updates the bitboards, squares array,
// Zobrist hash, and incremental PST score.
func (pos *position) addPiece(c color, p piece, sq square) {
	bb := squareBB(sq)
	pos.pieces[p-1] |= bb
	pos.colors[c] |= bb
	pos.hash ^= pieceSquareHashes[c][p-1][sq]
	pos.squares[sq] = p
	if c == white {
		pos.scoreWhite += pstValue(c, p, sq)
	} else {
		pos.scoreBlack += pstValue(c, p, sq)
	}
}

// removePiece removes the piece of color c on sq and updates the same fields.
func (pos *position) removePiece(c color, p piece, sq square) {
	bb := squareBB(sq)
	pos.pieces[p-1] &^= bb
	pos.colors[c] &^= bb
	pos.hash ^= pieceSquareHashes[c][p-1][sq]
	pos.squares[sq] = noPiece
	if c == white {
		pos.scoreWhite -= pstValue(c, p, sq)
	} else {
		pos.scoreBlack -= pstValue(c, p, sq)
	}
}

// movePiece slides piece p of color c from from to to, updating all incremental state.
func (pos *position) movePiece(c color, p piece, from, to square) {
	fromBB, toBB := squareBB(from), squareBB(to)
	fromTo := fromBB | toBB
	pos.pieces[p-1] ^= fromTo
	pos.colors[c] ^= fromTo
	pos.hash ^= pieceSquareHashes[c][p-1][from] ^ pieceSquareHashes[c][p-1][to]
	pos.squares[from] = noPiece
	pos.squares[to] = p
	if c == white {
		pos.scoreWhite += pstValue(c, p, to) - pstValue(c, p, from)
	} else {
		pos.scoreBlack += pstValue(c, p, to) - pstValue(c, p, from)
	}
}

// attacksTo returns the bitboard of pieces of color c that attack sq.
func (pos *position) attacksTo(sq square, c color) bitboard {
	occ := pos.occupied()
	var result bitboard
	if c == white {
		result = pawnAttacksBlack[sq] & pos.pawns()
	} else {
		result = pawnAttacksWhite[sq] & pos.pawns()
	}
	result |= knightAttacks[sq] & pos.knights()
	result |= kingAttacks[sq] & pos.kings()
	result |= rookAttacks(occ, sq) & (pos.rooks() | pos.queens())
	result |= bishopAttacks(occ, sq) & (pos.bishops() | pos.queens())
	result &= pos.colors[c]
	return result
}

func (pos *position) isAttacked(sq square, by color) bool {
	return pos.attacksTo(sq, by).isNotEmpty()
}

// positionFromFen constructs a position from a FEN string.
func positionFromFen(fen string) position {
	pos := position{stateStack: make([]irreversibleState, 0, maxPly)}
	fields := strings.Fields(fen)

	// Pieces
	y, x := 7, 0
	for _, ch := range fields[0] {
		switch ch {
		case '/':
			y--
			x = 0
		case '1', '2', '3', '4', '5', '6', '7', '8':
			x += int(ch - '0')
		default:
			color := white
			idx := strings.IndexRune("PNBRQKpnbrqk", ch)
			if idx >= 6 {
				idx -= 6
				color = black
			}
			p := piece(idx + 1)
			pos.addPiece(color, p, squareXY(x, y))
			x++
		}
	}

	// Side to move
	if len(fields) > 1 && fields[1] == "b" {
		pos.flags |= flagBlackToMove
	}

	pos.kingLocn[white] = (pos.kings() & pos.colors[white]).lsb()
	pos.kingLocn[black] = (pos.kings() & pos.colors[black]).lsb()

	// Castling rights
	if len(fields) > 2 {
		pos.castling = castlingRightsFromFen(fields[2])
	}

	// En passant
	if len(fields) > 3 && fields[3] != "-" {
		pos.epSquare = squareFromString(fields[3])
	}

	// Half-move clock
	if len(fields) > 4 {
		hm, _ := strconv.Atoi(fields[4])
		pos.halfMove = uint8(hm)
	}

	// Full-move number (stored 0-based internally)
	if len(fields) > 5 {
		fm, _ := strconv.Atoi(fields[5])
		pos.fullMove = uint8(fm - 1)
	}

	pos.hash = pos.computeHash()
	c := pos.colorToMove()
	pos.checkers = pos.attacksTo(pos.kingLocn[c], enemyOf(c))
	return pos
}

// computeHash builds the Zobrist hash from scratch.  Used once in positionFromFen;
// all subsequent updates are applied incrementally in makeMove/unmakeMove.
func (pos *position) computeHash() uint64 {
	var h uint64
	if pos.flags&flagBlackToMove != 0 {
		h ^= blackMoveHash
	}
	h ^= pos.castling.hash()
	h ^= enPassantHashes[pos.epSquare]
	for p := pawn; p <= king; p++ {
		b := pos.pieceBB(p) & pos.colors[white]
		for b.isNotEmpty() {
			sq := b.popLsb()
			h ^= pieceSquareHashes[white][p-1][sq]
		}
		b = pos.pieceBB(p) & pos.colors[black]
		for b.isNotEmpty() {
			sq := b.popLsb()
			h ^= pieceSquareHashes[black][p-1][sq]
		}
	}
	return h
}

func (pos *position) toFenString() string {
	var sb strings.Builder
	occ := pos.occupied()
	for y := 7; y >= 0; y-- {
		numEmpty := 0
		for x := range 8 {
			sq := squareXY(x, y)
			if (occ & squareBB(sq)).isEmpty() {
				numEmpty++
			} else {
				if numEmpty > 0 {
					sb.WriteByte(byte('0' + numEmpty))
					numEmpty = 0
				}
				p := pos.squares[sq]
				isWhite := (pos.colors[white] & squareBB(sq)).isNotEmpty()
				ch := " PNBRQK"[p]
				if !isWhite {
					ch = " pnbrqk"[p]
				}
				sb.WriteByte(ch)
			}
		}
		if numEmpty > 0 {
			sb.WriteByte(byte('0' + numEmpty))
		}
		if y > 0 {
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
	sb.WriteString(pos.castling.toFenString())
	sb.WriteByte(' ')
	if pos.epSquare == noSquare {
		sb.WriteByte('-')
	} else {
		sb.WriteString(pos.epSquare.String())
	}
	sb.WriteString(fmt.Sprintf(" %d %d", pos.halfMove, int(pos.fullMove)+1))
	return sb.String()
}

// makeMove applies m to the position.  All irreversible state is saved onto
// stateStack so that unmakeMove can restore it exactly.
func (pos *position) makeMove(m move) {
	color := pos.colorToMove()
	prevCastling := pos.castling

	pos.stateStack = append(pos.stateStack, irreversibleState{
		hash:     pos.hash,
		checkers: pos.checkers,
		castling: pos.castling,
		epSquare: pos.epSquare,
		halfMove: pos.halfMove,
		flags:    pos.flags,
		move:     m,
	})

	from, to, piece := m.from(), m.to(), m.piece()
	pos.flags &= ^flagNullMove
	pos.castling = prevCastling.afterMove(m)
	pos.hash ^= prevCastling.hash() ^ pos.castling.hash()
	pos.hash ^= enPassantHashes[pos.epSquare]
	pos.epSquare = noSquare

	switch m.kind() {
	case nonCapture:
		pos.halfMove++
		pos.movePiece(color, piece, from, to)

	case capture:
		pos.halfMove = 0
		pos.removePiece(enemyOf(color), m.captured(), to)
		pos.movePiece(color, piece, from, to)

	case promotionKnight, promotionBishop, promotionRook, promotionQueen:
		pos.halfMove = 0
		if m.captured() != noPiece {
			pos.removePiece(enemyOf(color), m.captured(), to)
		}
		pos.removePiece(color, pawn, from)
		pos.addPiece(color, m.promoted(), to)

	case pawnSinglePush:
		pos.halfMove = 0
		pos.movePiece(color, pawn, from, to)

	case pawnDoublePush:
		pos.halfMove = 0
		pos.movePiece(color, pawn, from, to)
		// Set the ep square to the skipped square (midpoint of from and to).
		pos.epSquare = square((int(from) + int(to)) >> 1)
		pos.hash ^= enPassantHashes[pos.epSquare]

	case epCapture:
		pos.halfMove = 0
		// The captured pawn is on the same rank as from, same file as to.
		capturedSq := square((int(from) & 0x38) | (int(to) & 0x07))
		pos.removePiece(enemyOf(color), pawn, capturedSq)
		pos.movePiece(color, pawn, from, to)

	case castling:
		pos.halfMove = 0
		switch to {
		case g1:
			pos.movePiece(white, king, e1, g1)
			pos.movePiece(white, rook, h1, f1)
		case c1:
			pos.movePiece(white, king, e1, c1)
			pos.movePiece(white, rook, a1, d1)
		case g8:
			pos.movePiece(black, king, e8, g8)
			pos.movePiece(black, rook, h8, f8)
		case c8:
			pos.movePiece(black, king, e8, c8)
			pos.movePiece(black, rook, a8, d8)
		}
	}

	pos.flags ^= flagBlackToMove
	pos.hash ^= blackMoveHash
	if color == black {
		pos.fullMove++
	}
	pos.kingLocn[color] = (pos.kings() & pos.colors[color]).lsb()
	pos.checkers = pos.attacksTo(pos.kingLocn[enemyOf(color)], color)
}

// unmakeMove reverses the last makeMove by popping the state stack and undoing
// all piece movements.  It must be called in strict LIFO order with makeMove.
func (pos *position) unmakeMove() {
	n := len(pos.stateStack)
	saved := pos.stateStack[n-1]
	pos.stateStack = pos.stateStack[:n-1]

	pos.flags = saved.flags
	color := pos.colorToMove() // color that made the move being undone

	move := saved.move
	if !move.isNone() {
		from, to, piece := move.from(), move.to(), move.piece()
		enemy := enemyOf(color)
		switch move.kind() {

		case nonCapture, pawnSinglePush, pawnDoublePush:
			pos.movePiece(color, piece, to, from)

		case capture:
			pos.movePiece(color, piece, to, from)
			pos.addPiece(enemy, move.captured(), to)

		case promotionKnight, promotionBishop, promotionRook, promotionQueen:
			pos.removePiece(color, move.promoted(), to)
			pos.addPiece(color, pawn, from)
			if move.captured() != noPiece {
				pos.addPiece(enemy, move.captured(), to)
			}

		case epCapture:
			capturedSq := square((int(from) & 0x38) | (int(to) & 0x07))
			pos.movePiece(color, pawn, to, from)
			pos.addPiece(enemy, pawn, capturedSq)

		case castling:
			switch to {
			case g1:
				pos.movePiece(white, king, g1, e1)
				pos.movePiece(white, rook, f1, h1)
			case c1:
				pos.movePiece(white, king, c1, e1)
				pos.movePiece(white, rook, d1, a1)
			case g8:
				pos.movePiece(black, king, g8, e8)
				pos.movePiece(black, rook, f8, h8)
			case c8:
				pos.movePiece(black, king, c8, e8)
				pos.movePiece(black, rook, d8, a8)
			}
		}
		pos.kingLocn[color] = (pos.kings() & pos.colors[color]).lsb()
		if color == black {
			pos.fullMove--
		}
	}

	pos.hash = saved.hash
	pos.checkers = saved.checkers
	pos.castling = saved.castling
	pos.epSquare = saved.epSquare
	pos.halfMove = saved.halfMove
}

// makeNullMove passes the turn without moving a piece.  Used by null-move pruning.
// The position is restored by the normal unmakeMove path (saved move is moveNone).
func (pos *position) makeNullMove() {
	pos.stateStack = append(pos.stateStack, irreversibleState{
		hash:     pos.hash,
		checkers: pos.checkers,
		castling: pos.castling,
		epSquare: pos.epSquare,
		halfMove: pos.halfMove,
		flags:    pos.flags,
		move:     moveNone(),
	})
	pos.hash ^= enPassantHashes[pos.epSquare]
	pos.hash ^= blackMoveHash
	pos.epSquare = noSquare
	pos.flags |= flagNullMove
	pos.flags ^= flagBlackToMove
}

// isDrawByRepetition returns true if the current position has occurred twice
// before in the game history recorded on stateStack (i.e. three occurrences total).
// Only positions with the same hash are considered; the search steps back by two
// plies at a time (same side to move) and stops early at an irreversible move
// (halfMove == 0).
func (pos *position) isDrawByRepetition() bool {
	h := pos.hash
	k := len(pos.stateStack)
	repetitions := 2
	for i := k - 4; i >= 0; i -= 2 {
		s := pos.stateStack[i]
		if s.hash == h {
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

// isLegal returns true if the position is a legal chess position.
func (pos *position) isLegal() bool {
	color := pos.colorToMove()
	whiteKing := pos.kings() & pos.colors[white]
	blackKing := pos.kings() & pos.colors[black]
	return whiteKing.popCount() == 1 && blackKing.popCount() == 1 &&
		whiteKing != blackKing &&
		(kingAttacks[whiteKing.lsb()]&blackKing).isEmpty() &&
		!pos.isAttacked(pos.kingLocn[enemyOf(color)], color) &&
		pos.kingLocn[white] == whiteKing.lsb() &&
		pos.kingLocn[black] == blackKing.lsb()
}

// isDrawByMaterial returns true when neither side has enough material to checkmate:
// KK, KNK, KBK, or KBKB with both bishops on the same colour.
func (pos *position) isDrawByMaterial() bool {
	if pos.pawns().isNotEmpty() || pos.rooks().isNotEmpty() || pos.queens().isNotEmpty() {
		return false
	}
	// Only kings, knights, and/or bishops remain.
	minorPieces := pos.knights() | pos.bishops()
	if minorPieces.popCount() <= 1 {
		return true
	}
	// Two bishops on the same colour squares is also a draw.
	if pos.knights() == 0 && minorPieces.popCount() == 2 {
		if minorPieces&whiteSqs == minorPieces || minorPieces&blackSqs == minorPieces {
			return true
		}
	}
	return false
}
