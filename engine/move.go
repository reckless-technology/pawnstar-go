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
//	23–24  transposition node type (nodeType)
//	25–32  transposition depth searched (int8)
//	33–40  transposition age / generation (uint8)
//	41–63  score (signed, 23-bit): move-ordering sort key AND the stored TT score
//
// Bits 23–63 hold either a move-ordering sort score (via withScore) or, inside a
// transposition-table entry, the packed {score, depth, nodeType, age} metadata (via withTTData /
// setTTAge). A move is never both at once, so the two views share the spare bits — this lets a TT
// cell be exactly two 64-bit words (hash-derived key + packed move), which the lockless XOR trick
// needs. equal and isNone compare only the identity bits (0–21), so metadata never affects identity.
type move int64

// Move bit-field shifts and masks. All magic numbers for the packed layout live here so the accessors
// and constructors read as named constants (mirrors the C++ move.h layout).
const (
	moveFromShift     = 6  // bits 6–11:  from square
	movePieceShift    = 12 // bits 12–14: moving piece
	moveCapturedShift = 15 // bits 15–17: captured piece
	moveTypeShift     = 18 // bits 18–21: move type
	moveCheckShift    = 22 // bit 22:     gives-check flag
	moveNodeTypeShift = 23 // bits 23–24: TT node type
	moveDepthShift    = 25 // bits 25–32: TT depth (int8)
	moveAgeShift      = 33 // bits 33–40: TT age / generation (uint8)
	moveScoreShift    = 41 // bits 41–63: score (signed 23-bit)

	moveSquareMask   = 0x3F     // 6-bit to / from square field
	movePieceMask    = 0x07     // 3-bit piece field
	moveTypeMask     = 0x0F     // 4-bit move-type field
	moveNodeTypeMask = 0x03     // 2-bit TT node-type field
	moveByteMask     = 0xFF     // 8-bit TT depth / age field
	moveFromToMask   = 0xFFF    // bits 0–11: from+to (history-table index)
	moveIdentityMask = 0x3FFFFF // bits 0–21: to/from/piece/captured/type (move identity)

	moveCheckBit = int64(1) << moveCheckShift                    // bit 22
	moveAgeMask  = int64(moveByteMask) << moveAgeShift           // bits 33–40
	moveScoreMask = ^((int64(1) << moveScoreShift) - 1)          // bits 41–63
	moveTTDataMask = ^((int64(1) << moveNodeTypeShift) - 1)      // bits 23–63 (node type, depth, age, score)
)

// moveType classifies a move for dispatch in makeMove.
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
)

// constructMove encodes all move fields into a move value.
func constructMove(from, to square, p piece, mt moveType, captured piece) move {
	return move(int64(to) | int64(from)<<6 | int64(p)<<12 | int64(captured)<<15 | int64(mt)<<18)
}

// Constructor helpers — each encodes the move type and fills in fixed fields.

// moveNonCapture encodes a quiet (non-capturing) move.
func moveNonCapture(from, to square, p piece) move {
	return move(int64(to) | int64(from)<<6 | int64(p)<<12)
}

// moveCapture encodes a standard capture move.
func moveCapture(from, to square, p, captured piece) move {
	return constructMove(from, to, p, capture, captured)
}

// movePromotion encodes a pawn promotion, optionally capturing on the target square.
func movePromotion(from, to square, promoted piece, captured piece) move {
	return constructMove(from, to, pawn, moveType(promoted), captured)
}

// moveCastling encodes a castling move (king from/to squares only; rook is implicit).
func moveCastling(from, to square) move {
	return constructMove(from, to, king, castling, noPiece)
}

// moveEpCapture encodes an en-passant capture.
func moveEpCapture(from, to square) move {
	return constructMove(from, to, pawn, epCapture, pawn)
}

// moveDoublePush encodes a pawn double-push (sets the en-passant square).
func moveDoublePush(from, to square) move {
	return constructMove(from, to, pawn, pawnDoublePush, noPiece)
}

// moveSinglePush encodes a quiet single-square pawn push.
func moveSinglePush(from, to square) move {
	return constructMove(from, to, pawn, pawnSinglePush, noPiece)
}

// moveNone is the null sentinel (all identity bits zero, which is not a legal move).
func moveNone() move { return move(0) }

func (m move) to() square      { return square(int64(m) & moveSquareMask) }                       // destination square
func (m move) from() square    { return square((int64(m) >> moveFromShift) & moveSquareMask) }     // origin square
func (m move) piece() piece    { return piece((int64(m) >> movePieceShift) & movePieceMask) }      // moving piece type
func (m move) captured() piece { return piece((int64(m) >> moveCapturedShift) & movePieceMask) }   // captured piece (noPiece if none)
func (m move) kind() moveType  { return moveType((int64(m) >> moveTypeShift) & moveTypeMask) }     // move classification
func (m move) score() int      { return int(int64(m) >> moveScoreShift) }                          // score (bits 41–63, sign-extended)

// TT-metadata accessors: read the fields packed into a move stored in a transposition entry.
func (m move) ttNodeType() nodeType { return nodeType((int64(m) >> moveNodeTypeShift) & moveNodeTypeMask) } // bits 23–24
func (m move) ttDepth() int         { return int(int8((int64(m) >> moveDepthShift) & moveByteMask)) }       // bits 25–32 (int8)
func (m move) ttAge() uint8         { return uint8((int64(m) >> moveAgeShift) & moveByteMask) }             // bits 33–40

// bits / moveFromBits convert to and from the raw 64-bit representation used by the lockless TT.
func (m move) bits() uint64        { return uint64(m) }
func moveFromBits(b uint64) move   { return move(int64(b)) }

// isQuiet reports whether the move is neither a capture nor a promotion (mirrors the C++
// Move::IsQuiet). Pawn pushes and castling count as quiet. Used by late-move pruning.
func (m move) isQuiet() bool { return m.captured() == noPiece && m.promoted() == noPiece }

// pieceTo returns a (piece, to-square) index in [0, contKeys) used to key the countermove and
// continuation-history tables. moveNone maps to 0 (a harmless slot). Mirrors the C++ Move::piece_to.
func (m move) pieceTo() int { return int(m.piece())<<6 | int(m.to()) }

// fromTo returns the combined from+to index (bits 0–11) used as a hash key in
// the history table.
func (m move) fromTo() int { return int(m) & moveFromToMask }

// promoted returns the promotion piece type, or noPiece for non-promotion moves.
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

// withScore returns a copy of m with the score field (bits 41–63) replaced by s, leaving all lower
// bits (identity, check flag, TT node-type/depth/age) unchanged. s must fit in signed 23 bits, which
// covers every move-ordering score and every stored search score.
func (m move) withScore(s int) move {
	return move((int64(m) &^ moveScoreMask) | (int64(s) << moveScoreShift))
}

// withTTData returns a copy of m with the transposition metadata (bits 23–63) replaced by the given
// score, depth and node type, clearing any prior metadata or sort score. Age is set to 0; the table
// stamps the generation on store via setTTAge. Mirrors the C++ Move::WithTTData.
func (m move) withTTData(score, depth int, nt nodeType) move {
	v := int64(m) &^ moveTTDataMask
	v |= int64(nt) << moveNodeTypeShift
	v |= (int64(depth) & moveByteMask) << moveDepthShift
	v |= int64(score) << moveScoreShift
	return move(v)
}

// setTTAge returns a copy of m with the TT age/generation field (bits 33–40) set to age, leaving all
// other bits unchanged.
func (m move) setTTAge(age uint8) move {
	return move((int64(m) &^ moveAgeMask) | (int64(age) << moveAgeShift))
}

// equal compares move identity (from/to/piece/kind/captured), ignoring the score and TT metadata.
func (m move) equal(other move) bool {
	return (int64(m) & moveIdentityMask) == (int64(other) & moveIdentityMask)
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
func (m move) isNone() bool { return (int64(m) & moveIdentityMask) == 0 }

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
