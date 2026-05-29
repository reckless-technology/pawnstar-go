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
// Piece-square scores and the Zobrist hash are maintained incrementally by
// addPiece, removePiece, and movePiece so evaluation and hashing are O(1)
// at every node.
type position struct {
	pieces       [7]bitboard    // per-piece-type occupancy; index = piece constant (pawn=1..king=6, 0 unused)
	colors       [2]bitboard    // per-color occupancy; colors[white]|colors[black] == occupied()
	squares      [64]piece      // reverse lookup: squares[sq] gives the piece type on sq, or noPiece
	checkers     bitboard       // pieces currently giving check to the side to move; refreshed by makeMove
	hash         zobrist        // Zobrist hash; updated incrementally, recomputed from scratch only in positionFromFen
	scores       [2]int         // material + PST totals per color, king excluded (king PST is phase-dependent)
	kingLocation [2]square      // king square per color; mirrors pieces[king] for direct O(1) access in hot paths
	epSquare     square         // target square of the last double pawn push, or noSquare if none
	halfMove     uint8          // half-move clock; reset on pawn moves and captures; draw at 100
	fullMove     uint8          // full-move number stored 0-based (add 1 when writing FEN)
	flags        uint8          // transient move flags; currently only flagNullMove
	castling     castlingRights // which castling moves remain legal for each side
	colorToMove  color          // side whose turn it is to move
}

const (
	flagNullMove uint8 = 0x01 // set in position.flags after makeNullMove
)

// isNullMove reports whether this position was reached by a null move.
func (position *position) isNullMove() bool { return (position.flags & flagNullMove) != 0 }

// isInCheck reports whether the side to move is in check.
func (position *position) isInCheck() bool { return position.checkers.isNotEmpty() }

// occupied returns a bitboard of all squares containing any piece.
func (position *position) occupied() bitboard { return position.colors[white] | position.colors[black] }

// addPiece places p of color c on sq and updates the bitboards, squares array,
// Zobrist hash, and incremental PST score.
func (position *position) addPiece(c color, p piece, sq square) {
	bb := squareBB(sq)
	position.pieces[p] ^= bb
	position.colors[c] ^= bb
	position.hash ^= pieceSquareHashes[c][p-1][sq]
	position.squares[sq] = p
	position.scores[c] += pstValue(c, p, sq)
}

// removePiece removes the piece of color c on sq and updates the same fields.
func (position *position) removePiece(c color, p piece, sq square) {
	bb := squareBB(sq)
	position.pieces[p] ^= bb
	position.colors[c] ^= bb
	position.hash ^= pieceSquareHashes[c][p-1][sq]
	position.squares[sq] = noPiece
	position.scores[c] -= pstValue(c, p, sq)
}

// movePiece slides piece p of color c from from to to, updating all incremental state.
func (position *position) movePiece(c color, p piece, from, to square) {
	fromTo := squareBB(from) | squareBB(to)
	position.pieces[p] ^= fromTo
	position.colors[c] ^= fromTo
	position.hash ^= pieceSquareHashes[c][p-1][from] ^ pieceSquareHashes[c][p-1][to]
	position.squares[from] = noPiece
	position.squares[to] = p
	position.scores[c] += pstValue(c, p, to) - pstValue(c, p, from)
}

// attacksTo returns a bitboard of all pieces of color c that attack sq.
// Sliding-piece attacks are computed against the full occupancy, so X-ray
// attacks through other pieces are not included — only direct attacks.
func (position *position) attacksTo(sq square, c color) bitboard {
	occ := position.occupied()
	var result bitboard
	if c == white {
		result = pawnAttacksBlack[sq] & position.pieces[pawn]
	} else {
		result = pawnAttacksWhite[sq] & position.pieces[pawn]
	}
	result |= knightAttacks[sq] & position.pieces[knight]
	result |= kingAttacks[sq] & position.pieces[king]
	result |= rookAttacks(occ, sq) & (position.pieces[rook] | position.pieces[queen])
	result |= bishopAttacks(occ, sq) & (position.pieces[bishop] | position.pieces[queen])
	result &= position.colors[c]
	return result
}

// isAttacked reports whether sq is attacked by any piece of color by.
func (position *position) isAttacked(sq square, by color) bool {
	return position.attacksTo(sq, by).isNotEmpty()
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
		pos.colorToMove = black
	}

	pos.kingLocation[white] = (pos.pieces[king] & pos.colors[white]).lsb()
	pos.kingLocation[black] = (pos.pieces[king] & pos.colors[black]).lsb()

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
	pos.checkers = pos.attacksTo(pos.kingLocation[c], enemyOf(c))
	return pos
}

// computeHash builds the Zobrist hash from scratch.  Used once in positionFromFen;
// all subsequent updates are applied incrementally in makeMove.
func (position *position) computeHash() zobrist {
	var hash zobrist
	if position.colorToMove == black {
		hash ^= blackMoveHash
	}
	hash ^= position.castling.hash()
	hash ^= enPassantHashes[position.epSquare]
	for p := pawn; p <= king; p++ {
		b := position.pieces[p] & position.colors[white]
		for b.isNotEmpty() {
			sq := b.popLsb()
			hash ^= pieceSquareHashes[white][p-1][sq]
		}
		b = position.pieces[p] & position.colors[black]
		for b.isNotEmpty() {
			sq := b.popLsb()
			hash ^= pieceSquareHashes[black][p-1][sq]
		}
	}
	return hash
}

// toFenString serialises the position to a FEN string.
func (position *position) toFenString() string {
	var sb strings.Builder
	occ := position.occupied()
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
				p := position.squares[sq]
				isWhite := (position.colors[white] & squareBB(sq)).isNotEmpty()
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
	if position.colorToMove == black {
		sb.WriteByte('b')
	} else {
		sb.WriteByte('w')
	}
	sb.WriteByte(' ')
	sb.WriteString(position.castling.toFenString())
	sb.WriteByte(' ')
	if position.epSquare == noSquare {
		sb.WriteByte('-')
	} else {
		sb.WriteString(position.epSquare.toString())
	}
	sb.WriteString(fmt.Sprintf(" %d %d", position.halfMove, int(position.fullMove)+1))
	return sb.String()
}

// makeMove applies m to the position.  The caller is responsible for saving
// the previous position before calling (copy-make pattern).
func (position *position) makeMove(m move) {
	color := position.colorToMove
	prevCastling := position.castling

	from, to, piece := m.from(), m.to(), m.piece()
	position.flags &= ^flagNullMove
	position.castling = prevCastling.afterMove(m)
	position.hash ^= prevCastling.hash() ^ position.castling.hash()
	position.hash ^= enPassantHashes[position.epSquare]
	position.epSquare = noSquare

	switch m.kind() {
	case nonCapture:
		position.halfMove++
		position.movePiece(color, piece, from, to)

	case capture:
		position.halfMove = 0
		position.removePiece(enemyOf(color), m.captured(), to)
		position.movePiece(color, piece, from, to)

	case promotionKnight, promotionBishop, promotionRook, promotionQueen:
		position.halfMove = 0
		if m.captured() != noPiece {
			position.removePiece(enemyOf(color), m.captured(), to)
		}
		position.removePiece(color, pawn, from)
		position.addPiece(color, m.promoted(), to)

	case pawnSinglePush:
		position.halfMove = 0
		position.movePiece(color, pawn, from, to)

	case pawnDoublePush:
		position.halfMove = 0
		position.movePiece(color, pawn, from, to)
		// Set the ep square to the skipped square (midpoint of from and to).
		position.epSquare = (from + to) >> 1
		position.hash ^= enPassantHashes[position.epSquare]

	case epCapture:
		position.halfMove = 0
		// The captured pawn is on the same rank as from, same file as to.
		capturedSq := (from & 0x38) | (to & 0x07)
		position.removePiece(enemyOf(color), pawn, capturedSq)
		position.movePiece(color, pawn, from, to)

	case castling:
		position.halfMove = 0
		switch to {
		case g1:
			position.movePiece(white, king, e1, g1)
			position.movePiece(white, rook, h1, f1)
		case c1:
			position.movePiece(white, king, e1, c1)
			position.movePiece(white, rook, a1, d1)
		case g8:
			position.movePiece(black, king, e8, g8)
			position.movePiece(black, rook, h8, f8)
		case c8:
			position.movePiece(black, king, e8, c8)
			position.movePiece(black, rook, a8, d8)
		}
	}

	position.colorToMove = enemyOf(color)
	position.hash ^= blackMoveHash
	if color == black {
		position.fullMove++
	}
	position.kingLocation[color] = (position.pieces[king] & position.colors[color]).lsb()
	position.checkers = position.attacksTo(position.kingLocation[enemyOf(color)], color)
}

// makeNullMove passes the turn without moving a piece.  Used by null-move pruning.
// The caller is responsible for saving the previous position before calling.
func (position *position) makeNullMove() {
	position.hash ^= enPassantHashes[position.epSquare]
	position.hash ^= blackMoveHash
	position.epSquare = noSquare
	position.flags |= flagNullMove
	position.colorToMove = enemyOf(position.colorToMove)
}

// isDrawByMaterial reports whether the position is a theoretical draw by
// insufficient mating material: K vs K, K+N vs K, K+B vs K, or K+B vs K+B
// where both bishops travel on the same colour squares.
func (position *position) isDrawByMaterial() bool {
	if position.pieces[pawn].isNotEmpty() || position.pieces[rook].isNotEmpty() || position.pieces[queen].isNotEmpty() {
		return false
	}
	// Only kings, knights, and/or bishops remain.
	minorPieces := position.pieces[knight] | position.pieces[bishop]
	if minorPieces.popCount() <= 1 {
		return true
	}
	// Two bishops on the same colour squares is also a draw.
	if position.pieces[knight] == 0 && minorPieces.popCount() == 2 {
		if minorPieces&whiteSqs == minorPieces || minorPieces&blackSqs == minorPieces {
			return true
		}
	}
	return false
}
