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
func newSeeBoard(pos *position) seeBoard {
	return seeBoard{pieces: pos.pieces, colors: pos.colors}
}

// evaluateStaticExchange returns the SEE score for m (positive = winning capture) and whether m gives
// check. It applies only the capturing move to a lightweight bitboard board — no makeMove, so no
// Zobrist/castling/squares[] bookkeeping — detects check by testing whether the moving side now attacks
// the enemy king (seeAttackersTo, which catches discovered, promotion and en-passant-discovered checks),
// and scores the recapture sequence with evaluateSwapOff. Mirrors the C++ EvaluateStaticExchange.
func evaluateStaticExchange(pos *position, m move) (int, bool) {
	color := pos.colorToMove
	enemy := enemyOf(color)
	from := m.from()
	to := m.to()
	mover := m.piece()
	promoted := m.promoted()

	// En passant captures the pawn on to's file at from's rank; otherwise the captured piece sits on
	// `to` (noPiece for a non-capturing promotion). The piece that lands on `to` is the promoted type.
	capturedSquare := to
	captured := m.captured()
	if m.kind() == epCapture {
		capturedSquare = (from & 0x38) | (to & 0x07)
		captured = pawn
	}
	pieceOnTo := mover
	if promoted != noPiece {
		pieceOnTo = promoted
	}

	// Apply the capturing move to a working board via bitboard XORs.
	bb := newSeeBoard(pos)
	fromBB := squareBB(from)
	toBB := squareBB(to)
	if captured != noPiece { // a non-capturing promotion leaves `to` empty
		capturedBB := squareBB(capturedSquare)
		bb.pieces[captured] ^= capturedBB
		bb.colors[enemy] ^= capturedBB
	}
	bb.pieces[mover] ^= fromBB
	bb.colors[color] ^= fromBB
	bb.pieces[pieceOnTo] ^= toBB
	bb.colors[color] ^= toBB

	// Check = the side that just moved now attacks the opposing king.
	enemyKing := (bb.pieces[king] & bb.colors[enemy]).lsb()
	isChecking := seeAttackersTo(&bb, enemyKing, color).isNotEmpty()

	if promoted != noPiece {
		return seeValues[captured] + seeValues[promoted] - seeValues[pawn] -
			evaluateSwapOff(&bb, to, enemy, promoted), isChecking
	}
	return seeValues[captured] - evaluateSwapOff(&bb, to, enemy, mover), isChecking
}

// seeAttackersTo returns the set of `color` pieces attacking `location` on the working board (using its
// colour bitboards for occupancy). Used by evaluateStaticExchange for MakeMove-free check detection.
func seeAttackersTo(bb *seeBoard, location square, color color) bitboard {
	occupied := bb.colors[white] | bb.colors[black]
	pawnSrc := pawnAttacksWhite[location]
	if color == white {
		pawnSrc = pawnAttacksBlack[location]
	}
	result := pawnSrc & bb.pieces[pawn]
	result |= (knightAttacks[location] & bb.pieces[knight]) | (kingAttacks[location] & bb.pieces[king])
	result |= rookAttacks(occupied, location) & (bb.pieces[rook] | bb.pieces[queen])
	result |= bishopAttacks(occupied, location) & (bb.pieces[bishop] | bb.pieces[queen])
	return result & bb.colors[color]
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
	// Source squares of the pawn attackers of `location`, by attacking colour. `location` is fixed for
	// the whole swap-off, but the side to move flips every ply, so the correct set must be selected per
	// ply below. (Computing it once for the initial colour missed the opposite colour's pawn recaptures
	// — e.g. a white c4 pawn recapturing after a black e6xd5, which the swap-off must follow.)
	pawnSrc := [2]bitboard{white: pawnAttacksBlack[location], black: pawnAttacksWhite[location]}
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

		attackers = pawnSrc[color] & attackingPieces & bb.pieces[pawn]
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
