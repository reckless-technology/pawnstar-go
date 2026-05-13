package engine

// GenerateLegalMoves returns all legal moves from pos.
func (pos *Position) GenerateLegalMoves() []Move {
	return pos.generateMoves(pos.ColorToMove(), true)
}

// GenerateLegalCaptures returns captures and promotions only.
func (pos *Position) GenerateLegalCaptures() []Move {
	return pos.generateMoves(pos.ColorToMove(), false)
}

func (pos *Position) generateMoves(color Color, doAllMoves bool) []Move {
	moves := make([]Move, 0, 64)
	occ := pos.Occupied()
	friendly := pos.colors[color]
	enemyPieces := occ ^ friendly
	enemyPawns := pos.Pawns() & enemyPieces
	kingLoc := pos.kingLoc[color]

	// Build forbidden squares for the king by computing all enemy attacks
	// (temporarily removing our king so X-ray attacks are included).
	var forbidden Bitboard
	if color == White {
		forbidden = enemyPawns.ShiftSouthwest() | enemyPawns.ShiftSoutheast()
	} else {
		forbidden = enemyPawns.ShiftNorthwest() | enemyPawns.ShiftNortheast()
	}
	occExceptKing := occ ^ SquareBB(kingLoc)
	for _, pt := range []Piece{Knight, Bishop, Rook, Queen, King} {
		b := pos.PieceBB(pt) & enemyPieces
		for b != 0 {
			var s Square
			b, s = b.PopLsb()
			forbidden |= pieceAttacks(pt, occExceptKing, s)
		}
	}

	// King moves.
	kingTargets := KingAttacks[kingLoc] & ^forbidden
	for b := kingTargets & enemyPieces; b != 0; {
		var to Square
		b, to = b.PopLsb()
		moves = append(moves, MoveCapture(kingLoc, to, King, pos.squares[to]))
	}
	if doAllMoves {
		for b := kingTargets &^ occ; b != 0; {
			var to Square
			b, to = b.PopLsb()
			moves = append(moves, MoveNonCapture(kingLoc, to, King))
		}
	}

	allowedCaptures := enemyPieces
	allowedNonCaptures := ^occ

	// In check: restrict allowed targets.
	if pos.IsInCheck() {
		if pos.checkers.PopCount() > 1 {
			return moves // Double check — only king moves.
		}
		checkerSq := pos.checkers.Lsb()
		allowedCaptures = pos.checkers
		allowedNonCaptures = InterveningSquares[kingLoc][checkerSq]
	}

	pins := computePins(pos)

	// Piece moves (knight, bishop, rook, queen).
	for _, pt := range []Piece{Knight, Bishop, Rook, Queen} {
		b := pos.PieceBB(pt) & friendly
		for b != 0 {
			var from Square
			b, from = b.PopLsb()
			attacks := pieceAttacks(pt, occ, from) & pins.AllowedSquares(from)
			for c := attacks & allowedCaptures; c != 0; {
				var to Square
				c, to = c.PopLsb()
				moves = append(moves, MoveCapture(from, to, pt, pos.squares[to]))
			}
			if doAllMoves {
				for nc := attacks & allowedNonCaptures; nc != 0; {
					var to Square
					nc, to = nc.PopLsb()
					moves = append(moves, MoveNonCapture(from, to, pt))
				}
			}
		}
	}

	// Castling.
	if doAllMoves && !pos.IsInCheck() {
		if color == White {
			if pos.castling.MayWhiteCastleKingside() &&
				(occ&(SquareBB(f1)|SquareBB(g1))) == 0 &&
				!pos.IsAttacked(f1, Black) && !pos.IsAttacked(g1, Black) {
				moves = append(moves, MoveCastling(e1, g1))
			}
			if pos.castling.MayWhiteCastleQueenside() &&
				(occ&(SquareBB(b1)|SquareBB(c1)|SquareBB(d1))) == 0 &&
				!pos.IsAttacked(d1, Black) && !pos.IsAttacked(c1, Black) {
				moves = append(moves, MoveCastling(e1, c1))
			}
		} else {
			if pos.castling.MayBlackCastleKingside() &&
				(occ&(SquareBB(f8)|SquareBB(g8))) == 0 &&
				!pos.IsAttacked(f8, White) && !pos.IsAttacked(g8, White) {
				moves = append(moves, MoveCastling(e8, g8))
			}
			if pos.castling.MayBlackCastleQueenside() &&
				(occ&(SquareBB(b8)|SquareBB(c8)|SquareBB(d8))) == 0 &&
				!pos.IsAttacked(d8, White) && !pos.IsAttacked(c8, White) {
				moves = append(moves, MoveCastling(e8, c8))
			}
		}
	}

	// Pawn moves.
	var (
		pawns          Bitboard
		singlePushes   Bitboard
		doublePushes   Bitboard
		capturesWest   Bitboard
		capturesEast   Bitboard
		epSources      Bitboard
		promotions     Bitboard
		promotionsWest Bitboard
		promotionsEast Bitboard
		promoRank      Bitboard
		pushDelta      int
		westDelta      int
		eastDelta      int
	)

	if color == White {
		pawns = pos.Pawns() & friendly
		singlePushes = pawns.ShiftNorth() & ^occ
		doublePushes = singlePushes.ShiftNorth() & ^occ & Rank4
		capturesWest = pawns.ShiftNorthwest() & enemyPieces
		capturesEast = pawns.ShiftNortheast() & enemyPieces
		if pos.epSquare != NoSquare {
			epSources = PawnAttacksBlack[pos.epSquare] & pawns
		}
		pushDelta = 8
		westDelta = 7
		eastDelta = 9
		promoRank = Rank8
	} else {
		pawns = pos.Pawns() & friendly
		singlePushes = pawns.ShiftSouth() & ^occ
		doublePushes = singlePushes.ShiftSouth() & ^occ & Rank5
		capturesWest = pawns.ShiftSouthwest() & enemyPieces
		capturesEast = pawns.ShiftSoutheast() & enemyPieces
		if pos.epSquare != NoSquare {
			epSources = PawnAttacksWhite[pos.epSquare] & pawns
		}
		pushDelta = -8
		westDelta = -9
		eastDelta = -7
		promoRank = Rank1
	}

	promotions = singlePushes & promoRank
	promotionsWest = capturesWest & promoRank
	promotionsEast = capturesEast & promoRank
	capturesWest ^= promotionsWest
	capturesEast ^= promotionsEast
	singlePushes ^= promotions

	// Capture promotions.
	for _, pair := range [2]struct {
		bb    Bitboard
		delta int
	}{{promotionsWest, westDelta}, {promotionsEast, eastDelta}} {
		b := pair.bb & allowedCaptures
		for b != 0 {
			var to Square
			b, to = b.PopLsb()
			from := Square(int(to) - pair.delta)
			if pins.AllowedSquares(from)&SquareBB(to) != 0 {
				captured := pos.squares[to]
				moves = append(moves, MovePromotion(from, to, Queen, captured))
				moves = append(moves, MovePromotion(from, to, Rook, captured))
				moves = append(moves, MovePromotion(from, to, Bishop, captured))
				moves = append(moves, MovePromotion(from, to, Knight, captured))
			}
		}
	}

	// Push promotions.
	{
		b := promotions & allowedNonCaptures
		for b != 0 {
			var to Square
			b, to = b.PopLsb()
			from := Square(int(to) - pushDelta)
			if pins.AllowedSquares(from)&SquareBB(to) != 0 {
				moves = append(moves, MovePromotion(from, to, Queen, NoPiece))
				moves = append(moves, MovePromotion(from, to, Rook, NoPiece))
				moves = append(moves, MovePromotion(from, to, Bishop, NoPiece))
				moves = append(moves, MovePromotion(from, to, Knight, NoPiece))
			}
		}
	}

	// Captures (non-promotion).
	for _, pair := range [2]struct {
		bb    Bitboard
		delta int
	}{{capturesWest, westDelta}, {capturesEast, eastDelta}} {
		b := pair.bb & allowedCaptures
		for b != 0 {
			var to Square
			b, to = b.PopLsb()
			from := Square(int(to) - pair.delta)
			if pins.AllowedSquares(from)&SquareBB(to) != 0 {
				moves = append(moves, MoveCapture(from, to, Pawn, pos.squares[to]))
			}
		}
	}

	if doAllMoves {
		// Single pawn pushes.
		b := singlePushes & allowedNonCaptures
		for b != 0 {
			var to Square
			b, to = b.PopLsb()
			from := Square(int(to) - pushDelta)
			if pins.AllowedSquares(from)&SquareBB(to) != 0 {
				moves = append(moves, MoveNonCapture(from, to, Pawn))
			}
		}
		// Double pawn pushes.
		b = doublePushes & allowedNonCaptures
		for b != 0 {
			var to Square
			b, to = b.PopLsb()
			from := Square(int(to) - pushDelta*2)
			if pins.AllowedSquares(from)&SquareBB(to) != 0 {
				moves = append(moves, MoveDoublePush(from, to))
			}
		}
	}

	// En passant captures.
	for epSources != 0 {
		var from Square
		epSources, from = epSources.PopLsb()
		to := pos.epSquare
		capturedSq := Square((int(from) & 0x38) | (int(to) & 0x07))
		if pins.AllowedSquares(from)&SquareBB(to) != 0 &&
			allowedCaptures&SquareBB(capturedSq) != 0 {
			// Check for horizontal discovered check through the king rank.
			if kingLoc.Rank() == from.Rank() {
				pseudoOcc := occ ^ SquareBB(capturedSq) ^ SquareBB(from)
				hAttacks := RookAttacks(pseudoOcc, kingLoc) &
					(WestOf[kingLoc] | EastOf[kingLoc])
				if hAttacks&pos.colors[EnemyOf(color)]&(pos.Rooks()|pos.Queens()) != 0 {
					continue
				}
			}
			moves = append(moves, MoveEpCapture(from, to))
		}
	}
	return moves
}

// pieceAttacks returns the attack set for piece pt on sq given occupied squares.
func pieceAttacks(piece Piece, occupied Bitboard, sq Square) Bitboard {
	switch piece {
	case Knight:
		return KnightAttacks[sq]
	case Bishop:
		return BishopAttacks(occupied, sq)
	case Rook:
		return RookAttacks(occupied, sq)
	case Queen:
		return QueenAttacks(occupied, sq)
	case King:
		return KingAttacks[sq]
	}
	return 0
}
