package engine

// seeValues uses nominal 1-3-3-5-9 material values for SEE.
var seeValues = [7]int{0, 100, 300, 300, 500, 900, 10000}

type seeBoard struct {
	pawns       Bitboard
	knights     Bitboard
	bishops     Bitboard
	rooks       Bitboard
	queens      Bitboard
	kings       Bitboard
	whitePieces Bitboard
	blackPieces Bitboard
}

func newSeeBoard(pos *Position) seeBoard {
	return seeBoard{
		pawns:       pos.Pawns(),
		knights:     pos.Knights(),
		bishops:     pos.Bishops(),
		rooks:       pos.Rooks(),
		queens:      pos.Queens(),
		kings:       pos.Kings(),
		whitePieces: pos.ColorBB(White),
		blackPieces: pos.ColorBB(Black),
	}
}

func (b *seeBoard) piecesOfType(p Piece) *Bitboard {
	switch p {
	case Pawn:
		return &b.pawns
	case Knight:
		return &b.knights
	case Bishop:
		return &b.bishops
	case Rook:
		return &b.rooks
	case Queen:
		return &b.queens
	case King:
		return &b.kings
	}
	panic("bad piece")
}

func (b *seeBoard) piecesOfColor(c Color) *Bitboard {
	if c == White {
		return &b.whitePieces
	}
	return &b.blackPieces
}

// EvaluateStaticExchange returns the SEE score for move and whether move gives check.
func EvaluateStaticExchange(pos *Position, m Move) (int, bool) {
	dst := pos.MakeMove(m)
	isChecking := dst.IsInCheck()
	bb := newSeeBoard(&dst)
	piece := m.Piece()
	var captured Piece
	if m.Type() == EpCapture {
		captured = Pawn
	} else {
		captured = pos.PieceAt(m.To())
	}
	promoted := m.Promoted()
	if promoted != NoPiece {
		score := seeValues[captured] + seeValues[promoted] - seeValues[Pawn] -
			evaluateSwapOff(&bb, m.To(), dst.ColorToMove(), promoted)
		return score, isChecking
	}
	score := seeValues[captured] - evaluateSwapOff(&bb, m.To(), dst.ColorToMove(), piece)
	return score, isChecking
}

func evaluateSwapOff(bb *seeBoard, location Square, color Color, pieceOnSquare Piece) int {
	var pawnAttacks Bitboard
	if color == White {
		pawnAttacks = PawnAttacksBlack[location]
	} else {
		pawnAttacks = PawnAttacksWhite[location]
	}
	knightAttacks := KnightAttacks[location]
	kingAttacks := KingAttacks[location]
	squareBB := SquareBB(location)

	occupied := bb.whitePieces | bb.blackPieces
	var scores [32]int
	ply := 0

	for ; ply < 32; ply++ {
		attackingPieces := *bb.piecesOfColor(color)
		var capturingPiece Piece
		var attackers Bitboard
		found := false

		attackers = pawnAttacks & attackingPieces & bb.pawns
		if attackers != 0 {
			capturingPiece = Pawn
			found = true
		}
		if !found {
			attackers = knightAttacks & attackingPieces & bb.knights
			if attackers != 0 {
				capturingPiece = Knight
				found = true
			}
		}
		var bishopAtks, rookAtks Bitboard
		if !found {
			bishopAtks = BishopAttacks(occupied, location)
			attackers = bishopAtks & attackingPieces & bb.bishops
			if attackers != 0 {
				capturingPiece = Bishop
				found = true
			}
		}
		if !found {
			rookAtks = RookAttacks(occupied, location)
			attackers = rookAtks & attackingPieces & bb.rooks
			if attackers != 0 {
				capturingPiece = Rook
				found = true
			}
		}
		if !found {
			if bishopAtks == 0 {
				bishopAtks = BishopAttacks(occupied, location)
			}
			if rookAtks == 0 {
				rookAtks = RookAttacks(occupied, location)
			}
			attackers = (bishopAtks | rookAtks) & attackingPieces & bb.queens
			if attackers != 0 {
				capturingPiece = Queen
				found = true
			}
		}
		if !found {
			attackers = kingAttacks & attackingPieces & bb.kings
			if attackers != 0 {
				capturingPiece = King
				found = true
			}
		}
		if !found {
			scores[ply] = 0
			break
		}

		// Use only the LSB attacker.
		lsb := attackers & (-attackers)
		enemy := EnemyOf(color)
		*bb.piecesOfType(pieceOnSquare) ^= squareBB
		*bb.piecesOfColor(enemy) ^= squareBB
		*bb.piecesOfType(capturingPiece) ^= lsb | squareBB
		*bb.piecesOfColor(color) ^= lsb | squareBB
		occupied ^= lsb

		scores[ply] = seeValues[pieceOnSquare]
		pieceOnSquare = capturingPiece
		color = enemy

		// Pawn promotion on back rank.
		if pieceOnSquare == Pawn && (squareBB&(Rank1|Rank8)) != 0 {
			pieceOnSquare = Queen
			bb.pawns ^= squareBB
			bb.queens ^= squareBB
		}
	}

	// Unwind: propagate values back.
	for ply--; ply >= 0; ply-- {
		scores[ply] -= scores[ply+1]
		if scores[ply] < 0 {
			scores[ply] = 0
		}
	}
	return scores[0]
}
