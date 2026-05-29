package engine

// seeValues uses simplified 1-3-3-5-9 material values for the exchange evaluator.
// These are intentionally coarser than the search piece values to keep SEE fast.
var seeValues = [7]int{0, 100, 300, 300, 500, 900, 10000}

// seeBoard is a lightweight copy of the piece bitboards used during SEE so that
// pieces can be removed as the exchange sequence is simulated without mutating
// the real position.  pieces is indexed by piece type (noPiece=0 unused, pawn=1
// through king=6); colors is indexed by color (white=0, black=1).
type seeBoard struct {
	pieces [7]bitboard
	colors [2]bitboard
}

// newSeeBoard initialises a seeBoard from the piece bitboards of pos.
func newSeeBoard(pos *position) *seeBoard {
	return &seeBoard{pieces: pos.pieces, colors: pos.colors}
}

// evaluateStaticExchange returns the SEE score for m (positive = winning capture)
// and whether m gives check.  The score accounts for all recaptures on the target
// square by the least-valuable available attacker on each side in turn.
func evaluateStaticExchange(pos *position, move move) (int, bool) {
	child := *pos
	child.makeMove(move)
	isChecking := child.isInCheck()
	nextColor := child.colorToMove
	bb := newSeeBoard(&child)
	piece := move.piece()
	promoted := move.promoted()
	captured := move.captured()
	if promoted != noPiece {
		s := seeValues[captured] + seeValues[promoted] - seeValues[pawn] -
			evaluateSwapOff(bb, move.to(), nextColor, promoted)
		return s, isChecking
	}
	s := seeValues[captured] - evaluateSwapOff(bb, move.to(), nextColor, piece)
	return s, isChecking
}

// evaluateSwapOff simulates the full exchange sequence on location, starting with
// color recapturing pieceOnSquare with its least-valuable available attacker.
// It returns the net material gain for the side that initiated the sequence (the
// caller), after both sides play optimally — each side only recaptures when it
// is profitable to do so.
//
// Implementation: gains are accumulated in a forward pass using the least-valuable
// attacker (LVA) ordering, then unwound with the recurrence
//
//	gain[i] = max(0, capturedValue − gain[i+1])
//
// which lets each side "stand pat" rather than recapture into a loss.
func evaluateSwapOff(bb *seeBoard, location square, color color, pieceOnSquare piece) int {
	var pawnTargets bitboard
	if color == white {
		pawnTargets = pawnAttacksBlack[location]
	} else {
		pawnTargets = pawnAttacksWhite[location]
	}
	knightTargets := knightAttacks[location]
	kingTargets := kingAttacks[location]
	sqBB := squareBB(location)
	occupied := bb.colors[white] | bb.colors[black]
	var scores [32]int
	ply := 0
	for ; ply < 32; ply++ {
		attackingPieces := bb.colors[color]
		var capturingPiece piece
		var attackers, bishopTargets, rookTargets bitboard

		attackers = pawnTargets & attackingPieces & bb.pieces[pawn]
		if attackers.isNotEmpty() {
			capturingPiece = pawn
			goto AttackFound
		}

		attackers = knightTargets & attackingPieces & bb.pieces[knight]
		if attackers.isNotEmpty() {
			capturingPiece = knight
			goto AttackFound
		}

		bishopTargets = bishopAttacks(occupied, location)
		attackers = bishopTargets & attackingPieces & bb.pieces[bishop]
		if attackers.isNotEmpty() {
			capturingPiece = bishop
			goto AttackFound
		}

		rookTargets = rookAttacks(occupied, location)
		attackers = rookTargets & attackingPieces & bb.pieces[rook]
		if attackers.isNotEmpty() {
			capturingPiece = rook
			goto AttackFound
		}

		attackers = (bishopTargets | rookTargets) & attackingPieces & bb.pieces[queen]
		if attackers.isNotEmpty() {
			capturingPiece = queen
			goto AttackFound
		}

		attackers = kingTargets & attackingPieces & bb.pieces[king]
		if attackers.isNotEmpty() {
			capturingPiece = king
			goto AttackFound
		}

		// No more attackers for this side.
		scores[ply] = 0
		break

	AttackFound:
		// Use only the least-valuable attacker (LSB of attackers).
		attacker := attackers & (-attackers)
		enemy := enemyOf(color)
		bb.pieces[pieceOnSquare] ^= sqBB
		bb.colors[enemy] ^= sqBB
		bb.pieces[capturingPiece] ^= attacker | sqBB
		bb.colors[color] ^= attacker | sqBB
		occupied ^= attacker
		scores[ply] = seeValues[pieceOnSquare]
		pieceOnSquare = capturingPiece
		color = enemy

		// Pawn reaching the back rank promotes to queen.
		if pieceOnSquare == pawn && (sqBB & (rank1 | rank8)).isNotEmpty() {
			pieceOnSquare = queen
			bb.pieces[pawn] ^= sqBB
			bb.pieces[queen] ^= sqBB
		}
	}

	// Unwind: each side only recaptures if it is profitable.
	for ply--; ply >= 0; ply-- {
		scores[ply] -= scores[ply+1]
		if scores[ply] < 0 {
			scores[ply] = 0
		}
	}
	return scores[0]
}
