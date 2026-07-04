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

// evaluateStaticExchange returns the SEE score for m (positive = winning capture). It applies only the
// capturing move to a lightweight bitboard board — no makeMove, so no Zobrist/castling/squares[]
// bookkeeping — and scores the recapture sequence with evaluateSwapOff. Mirrors the C++
// EvaluateStaticExchange, minus its gives-check byproduct: that flag was only ever consumed by a late
// move pruning guard already gated on m.isQuiet(), and the flag is set only on non-quiet moves, so the
// guard was vacuous (always true) — computing check here was dead work. Dropped in both the read site
// and this producer; the search is bit-identical (bench signature unchanged).
func evaluateStaticExchange(pos *position, m move) int {
	c := pos.colorToMove
	enemy := enemyOf(c)
	from := m.from()
	to := m.to()
	mover := m.piece()
	promoted := m.promoted()

	// En passant captures the pawn on to's file at from's rank; otherwise the captured piece sits on
	// `to` (noPiece for a non-capturing promotion). The piece that lands on `to` is the promoted type.
	capturedSquare := to
	captured := m.captured()
	if m.kind() == epCapture {
		capturedSquare = epCapturedSquare(from, to)
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
	bb.colors[c] ^= fromBB
	bb.pieces[pieceOnTo] ^= toBB
	bb.colors[c] ^= toBB

	if promoted != noPiece {
		return seeValues[captured] + seeValues[promoted] - seeValues[pawn] -
			evaluateSwapOff(&bb, to, enemy, promoted)
	}
	return seeValues[captured] - evaluateSwapOff(&bb, to, enemy, mover)
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
func evaluateSwapOff(bb *seeBoard, sq square, c color, pieceOnSquare piece) int {
	// Source squares of the pawn attackers of `location`, by attacking colour. `location` is fixed for
	// the whole swap-off, but the side to move flips every ply, so the correct set must be selected per
	// ply below. (Computing it once for the initial colour missed the opposite colour's pawn recaptures
	// — e.g. a white c4 pawn recapturing after a black e6xd5, which the swap-off must follow.)
	pawnSrc := [2]bitboard{white: pawnAttacksBlack[sq], black: pawnAttacksWhite[sq]}
	knightTargets := knightAttacks[sq]
	kingTargets := kingAttacks[sq]
	sqBB := squareBB(sq)
	occupied := bb.colors[white] | bb.colors[black]
	var scores [32]int
	ply := 0
	for ; ply < 32; ply++ {
		attackingPieces := bb.colors[c]
		var capturingPiece piece
		var attackers, bishopTargets, rookTargets bitboard

		attackers = pawnSrc[c] & attackingPieces & bb.pieces[pawn]
		if attackers.isNotEmpty() {
			capturingPiece = pawn
			goto AttackFound
		}

		attackers = knightTargets & attackingPieces & bb.pieces[knight]
		if attackers.isNotEmpty() {
			capturingPiece = knight
			goto AttackFound
		}

		bishopTargets = bishopAttacks(occupied, sq)
		attackers = bishopTargets & attackingPieces & bb.pieces[bishop]
		if attackers.isNotEmpty() {
			capturingPiece = bishop
			goto AttackFound
		}

		rookTargets = rookAttacks(occupied, sq)
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
		enemy := enemyOf(c)
		bb.pieces[pieceOnSquare] ^= sqBB
		bb.colors[enemy] ^= sqBB
		bb.pieces[capturingPiece] ^= attacker | sqBB
		bb.colors[c] ^= attacker | sqBB
		occupied ^= attacker
		scores[ply] = seeValues[pieceOnSquare]
		pieceOnSquare = capturingPiece
		c = enemy

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
