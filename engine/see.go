package engine

// seeValues uses simplified 1-3-3-5-9 material values for the exchange evaluator.
// These are intentionally coarser than the search piece values to keep SEE fast.
var seeValues = [7]int{0, 100, 300, 300, 500, 900, 10000}

// seeBoard is a lightweight copy of the piece bitboards used during SEE so that
// pieces can be removed as the exchange sequence is simulated without mutating
// the real position.
type seeBoard struct {
	pawns       bitboard
	knights     bitboard
	bishops     bitboard
	rooks       bitboard
	queens      bitboard
	kings       bitboard
	whitePieces bitboard
	blackPieces bitboard
}

func newSeeBoard(pos *position) seeBoard {
	return seeBoard{
		pawns:       pos.pawns(),
		knights:     pos.knights(),
		bishops:     pos.bishops(),
		rooks:       pos.rooks(),
		queens:      pos.queens(),
		kings:       pos.kings(),
		whitePieces: pos.colorBB(white),
		blackPieces: pos.colorBB(black),
	}
}

func (b *seeBoard) piecesOfType(p piece) *bitboard {
	switch p {
	case pawn:
		return &b.pawns
	case knight:
		return &b.knights
	case bishop:
		return &b.bishops
	case rook:
		return &b.rooks
	case queen:
		return &b.queens
	case king:
		return &b.kings
	}
	return nil
}

func (b *seeBoard) piecesOfColor(c color) *bitboard {
	if c == white {
		return &b.whitePieces
	}
	return &b.blackPieces
}

// evaluateStaticExchange returns the SEE score for m (positive = winning capture)
// and whether m gives check.  The score accounts for all recaptures on the target
// square by the least-valuable available attacker on each side in turn.
func evaluateStaticExchange(pos *position, m move) (int, bool) {
	var captured piece
	if m.kind() == epCapture {
		captured = pawn
	} else {
		captured = pos.pieceAt(m.to())
	}
	pos.makeMove(m)
	isChecking := pos.isInCheck()
	nextColor := pos.colorToMove()
	bb := newSeeBoard(pos)
	pos.unmakeMove()

	p := m.piece()
	promoted := m.promoted()
	if promoted != noPiece {
		s := seeValues[captured] + seeValues[promoted] - seeValues[pawn] -
			evaluateSwapOff(&bb, m.to(), nextColor, promoted)
		return s, isChecking
	}
	s := seeValues[captured] - evaluateSwapOff(&bb, m.to(), nextColor, p)
	return s, isChecking
}

// evaluateSwapOff simulates the sequence of recaptures on location starting with
// color playing pieceOnSquare.  Returns the gain for the side that just captured,
// accounting for the fact that each side will only recapture when it is beneficial.
// Uses the classic "negamax unwind" technique: scores are computed forward then
// propagated backward so each side can choose whether to recapture.
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
	occupied := bb.whitePieces | bb.blackPieces
	var scores [32]int
	ply := 0
	for ; ply < 32; ply++ {
		attackingPieces := *bb.piecesOfColor(color)
		var capturingPiece piece
		var attackers, bishopTargets, rookTargets bitboard

		attackers = pawnTargets & attackingPieces & bb.pawns
		if attackers.isNotEmpty() {
			capturingPiece = pawn
			goto AttackFound
		}

		attackers = knightTargets & attackingPieces & bb.knights
		if attackers.isNotEmpty() {
			capturingPiece = knight
			goto AttackFound
		}

		bishopTargets = bishopAttacks(occupied, location)
		attackers = bishopTargets & attackingPieces & bb.bishops
		if attackers.isNotEmpty() {
			capturingPiece = bishop
			goto AttackFound
		}

		rookTargets = rookAttacks(occupied, location)
		attackers = rookTargets & attackingPieces & bb.rooks
		if attackers.isNotEmpty() {
			capturingPiece = rook
			goto AttackFound
		}
		attackers = (bishopTargets | rookTargets) & attackingPieces & bb.queens
		if attackers.isNotEmpty() {
			capturingPiece = queen
			goto AttackFound
		}

		attackers = kingTargets & attackingPieces & bb.kings
		if attackers.isNotEmpty() {
			capturingPiece = king
			goto AttackFound
		}

		// No more attackers for this side.
		scores[ply] = 0
		break

	AttackFound:
		// Use only the least-valuable attacker (LSB of attackers).
		lsb := attackers & (-attackers)
		enemy := enemyOf(color)
		*bb.piecesOfType(pieceOnSquare) ^= sqBB
		*bb.piecesOfColor(enemy) ^= sqBB
		*bb.piecesOfType(capturingPiece) ^= lsb | sqBB
		*bb.piecesOfColor(color) ^= lsb | sqBB
		occupied ^= lsb
		scores[ply] = seeValues[pieceOnSquare]
		pieceOnSquare = capturingPiece
		color = enemy

		// Pawn reaching the back rank promotes to queen.
		if pieceOnSquare == pawn && (sqBB&(rank1|rank8)).isNotEmpty() {
			pieceOnSquare = queen
			bb.pawns ^= sqBB
			bb.queens ^= sqBB
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
