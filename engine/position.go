package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// position is the central board state.  It is a value type: makeMove and
// makeNullMove modify a copy of the position; the caller is responsible for
// maintaining the game-history stack in Game.posStack.
//
// All bitboard fields are kept in sync with squares at all times.
// The Zobrist hash is maintained incrementally by addPiece, removePiece, and
// movePiece so hashing is O(1) at every node.
type position struct {
	pieces      [7]bitboard    // per-piece-type occupancy; index = piece constant (pawn=1..king=6); index 0 (noPiece) holds all occupied squares
	colors      [2]bitboard    // per-color occupancy; colors[white]|colors[black] == occupied()
	squares     [64]piece      // reverse lookup: squares[sq] gives the piece type on sq, or noPiece
	checkers    bitboard       // pieces currently giving check to the side to move; refreshed by makeMove
	hash        zobrist        // Zobrist hash; updated incrementally, recomputed from scratch only in positionFromFen
	epSquare    square         // target square of the last double pawn push, or noSquare if none
	halfMove    uint8          // half-move clock; reset on pawn moves and captures; draw at 100
	fullMove    uint8          // full-move number stored 0-based (add 1 when writing FEN)
	flags       uint8          // transient move flags; currently only flagNullMove
	castling    castlingRights // which castling moves remain legal for each side
	colorToMove color          // side whose turn it is to move
}

const (
	flagNullMove uint8 = 0x01 // set in position.flags after makeNullMove
)

// isNullMove reports whether this position was reached by a null move.
func (pos *position) isNullMove() bool { return (pos.flags & flagNullMove) != 0 }

// isInCheck reports whether the side to move is in check.
func (pos *position) isInCheck() bool { return pos.checkers.isNotEmpty() }

// occupied returns a bitboard of all squares containing any piece. pieces[noPiece] holds the occupancy,
// maintained incrementally by addPiece/removePiece/movePiece, so this is a plain load (mirrors the C++
// Position::OccupiedSquares reading pieces_[kNone]).
func (pos *position) occupied() bitboard { return pos.pieces[noPiece] }

// kingSquare returns the square occupied by the king of color c.
func (pos *position) kingSquare(c color) square {
	return (pos.pieces[king] & pos.colors[c]).lsb()
}

// addPiece places p of color c on sq and updates the bitboards, squares array,
// and Zobrist hash.
func (pos *position) addPiece(c color, p piece, sq square) {
	bb := squareBB(sq)
	pos.pieces[p] ^= bb
	pos.colors[c] ^= bb
	pos.pieces[noPiece] ^= bb // occupied-squares bitboard
	pos.hash ^= pieceSquareHashes[c][p-1][sq]
	pos.squares[sq] = p
}

// removePiece removes the piece of color c on sq and updates the same fields.
func (pos *position) removePiece(c color, p piece, sq square) {
	bb := squareBB(sq)
	pos.pieces[p] ^= bb
	pos.colors[c] ^= bb
	pos.pieces[noPiece] ^= bb // occupied-squares bitboard
	pos.hash ^= pieceSquareHashes[c][p-1][sq]
	pos.squares[sq] = noPiece
}

// movePiece slides piece p of color c from from to to, updating all incremental state.
func (pos *position) movePiece(c color, p piece, from, to square) {
	fromTo := squareBB(from) | squareBB(to)
	pos.pieces[p] ^= fromTo
	pos.colors[c] ^= fromTo
	pos.pieces[noPiece] ^= fromTo // occupied-squares bitboard (from cleared, to set)
	pos.hash ^= pieceSquareHashes[c][p-1][from] ^ pieceSquareHashes[c][p-1][to]
	pos.squares[from] = noPiece
	pos.squares[to] = p
}

// attacksTo returns a bitboard of all pieces of color c that attack sq.
// Sliding-piece attacks are computed against the full occupancy, so X-ray
// attacks through other pieces are not included — only direct attacks.
func (pos *position) attacksTo(sq square, c color) bitboard {
	occ := pos.occupied()
	var result bitboard
	if c == white {
		result = pawnAttacksBlack[sq] & pos.pieces[pawn]
	} else {
		result = pawnAttacksWhite[sq] & pos.pieces[pawn]
	}
	result |= knightAttacks[sq] & pos.pieces[knight]
	result |= kingAttacks[sq] & pos.pieces[king]
	result |= rookAttacks(occ, sq) & (pos.pieces[rook] | pos.pieces[queen])
	result |= bishopAttacks(occ, sq) & (pos.pieces[bishop] | pos.pieces[queen])
	result &= pos.colors[c]
	return result
}

// isAttacked reports whether sq is attacked by any piece of color by.
func (pos *position) isAttacked(sq square, by color) bool {
	return pos.attacksTo(sq, by).isNotEmpty()
}

// positionFromFen constructs a position from a FEN string.
func positionFromFen(fen string) position {
	pos := position{}
	fields := strings.Fields(fen)

	// Pieces
	y, x := 7, 0
	for _, ch := range fields[0] {
		switch ch {
		case '/':
			y--
			x = 0
		case '0', '1', '2', '3', '4', '5', '6', '7', '8':
			x += int(ch - '0')
		default:
			c := white
			idx := strings.IndexRune("PNBRQKpnbrqk", ch)
			if idx >= 6 {
				idx -= 6
				c = black
			}
			p := piece(idx + 1)
			pos.addPiece(c, p, squareXY(x, y))
			x++
		}
	}

	// Side to move
	if len(fields) > 1 && fields[1] == "b" {
		pos.colorToMove = black
	}

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
	c := pos.colorToMove
	pos.checkers = pos.attacksTo(pos.kingSquare(c), enemyOf(c))
	return pos
}

// computeHash builds the Zobrist hash from scratch.  Used once in positionFromFen;
// all subsequent updates are applied incrementally in makeMove.
func (pos *position) computeHash() zobrist {
	var hash zobrist
	if pos.colorToMove == black {
		hash ^= blackMoveHash
	}
	hash ^= pos.castling.hash()
	hash ^= enPassantHashes[pos.epSquare]
	for p := pawn; p <= king; p++ {
		b := pos.pieces[p] & pos.colors[white]
		for b.isNotEmpty() {
			sq := b.popLsb()
			hash ^= pieceSquareHashes[white][p-1][sq]
		}
		b = pos.pieces[p] & pos.colors[black]
		for b.isNotEmpty() {
			sq := b.popLsb()
			hash ^= pieceSquareHashes[black][p-1][sq]
		}
	}
	return hash
}

// toFenString serialises the position to a FEN string.
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
	if pos.colorToMove == black {
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
		sb.WriteString(pos.epSquare.toString())
	}
	sb.WriteString(fmt.Sprintf(" %d %d", pos.halfMove, int(pos.fullMove)+1))
	return sb.String()
}

// makeMove applies m to the position.  The caller is responsible for saving
// the previous position before calling (copy-make pattern).
func (pos *position) makeMove(m move) {
	c := pos.colorToMove
	prevCastling := pos.castling

	from, to, pc := m.from(), m.to(), m.piece()
	pos.flags &= ^flagNullMove
	pos.castling = prevCastling.afterMove(m)
	pos.hash ^= prevCastling.hash() ^ pos.castling.hash()
	pos.hash ^= enPassantHashes[pos.epSquare]
	pos.epSquare = noSquare

	switch m.kind() {
	case nonCapture:
		pos.halfMove++
		pos.movePiece(c, pc, from, to)

	case capture:
		pos.halfMove = 0
		pos.removePiece(enemyOf(c), m.captured(), to)
		pos.movePiece(c, pc, from, to)

	case promotionKnight, promotionBishop, promotionRook, promotionQueen:
		pos.halfMove = 0
		if m.captured() != noPiece {
			pos.removePiece(enemyOf(c), m.captured(), to)
		}
		pos.removePiece(c, pawn, from)
		pos.addPiece(c, m.promoted(), to)

	case pawnSinglePush:
		pos.halfMove = 0
		pos.movePiece(c, pawn, from, to)

	case pawnDoublePush:
		pos.halfMove = 0
		pos.movePiece(c, pawn, from, to)
		// Set the ep square to the skipped square (midpoint of from and to).
		pos.epSquare = (from + to) >> 1
		pos.hash ^= enPassantHashes[pos.epSquare]

	case epCapture:
		pos.halfMove = 0
		// The captured pawn is on the same rank as from, same file as to.
		capturedSq := epCapturedSquare(from, to)
		pos.removePiece(enemyOf(c), pawn, capturedSq)
		pos.movePiece(c, pawn, from, to)

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

	pos.colorToMove = enemyOf(c)
	pos.hash ^= blackMoveHash
	if c == black {
		pos.fullMove++
	}
	pos.checkers = pos.attacksTo(pos.kingSquare(enemyOf(c)), c)
}

// makeNullMove passes the turn without moving a piece.  Used by null-move pruning.
// The caller is responsible for saving the previous position before calling.
func (pos *position) makeNullMove() {
	pos.hash ^= enPassantHashes[pos.epSquare]
	pos.hash ^= blackMoveHash
	pos.epSquare = noSquare
	pos.flags |= flagNullMove
	pos.colorToMove = enemyOf(pos.colorToMove)
}

// materialDrawCategory reports the insufficient-mating-material draw category by total piece count:
// 2 (K vs K), 3 (K+N vs K or K+B vs K), or 4 (K+B vs K+B, both bishops on the same colour) — or 0 if the
// position is not a material draw. The category feeds the drawsByMaterial{2,3,4} diagnostic counters.
func (pos *position) materialDrawCategory() int {
	if pos.pieces[pawn].isNotEmpty() || pos.pieces[rook].isNotEmpty() || pos.pieces[queen].isNotEmpty() {
		return 0
	}
	// Only kings, knights, and/or bishops remain.
	minorPieces := pos.pieces[knight] | pos.pieces[bishop]
	switch minorPieces.popCount() {
	case 0:
		return 2 // K vs K
	case 1:
		return 3 // K + single minor vs K
	case 2:
		// Two bishops on the same colour squares is also a draw.
		if pos.pieces[knight] == 0 && (minorPieces&whiteSqs == minorPieces || minorPieces&blackSqs == minorPieces) {
			return 4
		}
	}
	return 0
}
