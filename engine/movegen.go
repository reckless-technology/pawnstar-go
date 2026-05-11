package engine

// GenerateLegalMoves returns all legal moves from pos.
func (pos *Position) GenerateLegalMoves() MoveList {
	if pos.ColorToMove() == White {
		return pos.genMoves(White, true)
	}
	return pos.genMoves(Black, true)
}

// GenerateLegalCaptures returns captures and promotions only.
func (pos *Position) GenerateLegalCaptures() MoveList {
	if pos.ColorToMove() == White {
		return pos.genMoves(White, false)
	}
	return pos.genMoves(Black, false)
}

func (pos *Position) genMoves(color Color, doAllMoves bool) MoveList {
	var moves MoveList
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
	kingTargets := KingAttacksTable[kingLoc] &^ forbidden
	for b := kingTargets & enemyPieces; b != 0; {
		var to Square
		b, to = b.PopLsb()
		moves.Push(MoveCapture(kingLoc, to, King, pos.squares[to]))
	}
	if doAllMoves {
		for b := kingTargets &^ occ; b != 0; {
			var to Square
			b, to = b.PopLsb()
			moves.Push(MoveNonCapture(kingLoc, to, King))
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
				moves.Push(MoveCapture(from, to, pt, pos.squares[to]))
			}
			if doAllMoves {
				for nc := attacks & allowedNonCaptures; nc != 0; {
					var to Square
					nc, to = nc.PopLsb()
					moves.Push(MoveNonCapture(from, to, pt))
				}
			}
		}
	}

	// Castling.
	if doAllMoves && !pos.IsInCheck() {
		if color == White {
			f1 := SquareFromString("f1")
			g1 := SquareFromString("g1")
			b1 := SquareFromString("b1")
			c1 := SquareFromString("c1")
			d1 := SquareFromString("d1")
			if pos.castling.MayWhiteCastleKingside() &&
				(occ&(SquareBB(f1)|SquareBB(g1))) == 0 &&
				!pos.IsAttacked(f1, Black) && !pos.IsAttacked(g1, Black) {
				moves.Push(MoveCastling(SquareFromString("e1"), g1))
			}
			if pos.castling.MayWhiteCastleQueenside() &&
				(occ&(SquareBB(b1)|SquareBB(c1)|SquareBB(d1))) == 0 &&
				!pos.IsAttacked(d1, Black) && !pos.IsAttacked(c1, Black) {
				moves.Push(MoveCastling(SquareFromString("e1"), c1))
			}
		} else {
			f8 := SquareFromString("f8")
			g8 := SquareFromString("g8")
			b8 := SquareFromString("b8")
			c8 := SquareFromString("c8")
			d8 := SquareFromString("d8")
			if pos.castling.MayBlackCastleKingside() &&
				(occ&(SquareBB(f8)|SquareBB(g8))) == 0 &&
				!pos.IsAttacked(f8, White) && !pos.IsAttacked(g8, White) {
				moves.Push(MoveCastling(SquareFromString("e8"), g8))
			}
			if pos.castling.MayBlackCastleQueenside() &&
				(occ&(SquareBB(b8)|SquareBB(c8)|SquareBB(d8))) == 0 &&
				!pos.IsAttacked(d8, White) && !pos.IsAttacked(c8, White) {
				moves.Push(MoveCastling(SquareFromString("e8"), c8))
			}
		}
	}

	// Pawn moves.
	pos.genPawnMoves(color, occ, friendly, enemyPieces, allowedCaptures, allowedNonCaptures, &pins, &moves, doAllMoves)

	return moves
}

func (pos *Position) genPawnMoves(
	color Color, occ, friendly, enemyPieces, allowedCaptures, allowedNonCaptures Bitboard,
	pins *Pins, moves *MoveList, doAllMoves bool,
) {
	var (
		pawns            Bitboard
		singlePushes     Bitboard
		doublePushes     Bitboard
		capturesWest     Bitboard
		capturesEast     Bitboard
		epSources        Bitboard
		promotions       Bitboard
		promotionsWest   Bitboard
		promotionsEast   Bitboard
		pushDelta        int
		westDelta        int
		eastDelta        int
		promoRank        Bitboard
		doublePushRank   Bitboard
	)

	if color == White {
		pawns = pos.Pawns() & friendly
		singlePushes = pawns.ShiftNorth() &^ occ
		doublePushes = singlePushes.ShiftNorth() &^ occ & Rank4
		capturesWest = pawns.ShiftNorthwest() & enemyPieces
		capturesEast = pawns.ShiftNortheast() & enemyPieces
		if pos.epSquare != NoSquare {
			epSources = PawnAttacksBlack[pos.epSquare] & pawns
		}
		promoRank = Rank8
		doublePushRank = Rank4
		pushDelta = 8
		westDelta = 7
		eastDelta = 9
	} else {
		pawns = pos.Pawns() & friendly
		singlePushes = pawns.ShiftSouth() &^ occ
		doublePushes = singlePushes.ShiftSouth() &^ occ & Rank5
		capturesWest = pawns.ShiftSouthwest() & enemyPieces
		capturesEast = pawns.ShiftSoutheast() & enemyPieces
		if pos.epSquare != NoSquare {
			epSources = PawnAttacksWhite[pos.epSquare] & pawns
		}
		promoRank = Rank1
		doublePushRank = Rank5
		pushDelta = -8
		westDelta = -9
		eastDelta = -7
	}
	_ = doublePushRank

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
				cap := pos.squares[to]
				moves.Push(MovePromotion(from, to, Queen, cap))
				moves.Push(MovePromotion(from, to, Rook, cap))
				moves.Push(MovePromotion(from, to, Bishop, cap))
				moves.Push(MovePromotion(from, to, Knight, cap))
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
				moves.Push(MovePromotion(from, to, Queen, NoPiece))
				moves.Push(MovePromotion(from, to, Rook, NoPiece))
				moves.Push(MovePromotion(from, to, Bishop, NoPiece))
				moves.Push(MovePromotion(from, to, Knight, NoPiece))
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
				moves.Push(MoveCapture(from, to, Pawn, pos.squares[to]))
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
				moves.Push(MoveNonCapture(from, to, Pawn))
			}
		}
		// Double pawn pushes.
		b = doublePushes & allowedNonCaptures
		for b != 0 {
			var to Square
			b, to = b.PopLsb()
			from := Square(int(to) - pushDelta*2)
			if pins.AllowedSquares(from)&SquareBB(to) != 0 {
				moves.Push(MoveDoublePush(from, to))
			}
		}
	}

	// En passant captures.
	occ = pos.Occupied()
	kingLoc := pos.kingLoc[color]
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
					(West[kingLoc] | East[kingLoc])
				if hAttacks&pos.colors[EnemyOf(color)]&(pos.Rooks()|pos.Queens()) != 0 {
					continue
				}
			}
			moves.Push(MoveEpCapture(from, to))
		}
	}
}

// pieceAttacks returns the attack set for piece pt on sq given occupied squares.
func pieceAttacks(pt Piece, occ Bitboard, sq Square) Bitboard {
	switch pt {
	case Knight:
		return KnightAttacks[sq]
	case Bishop:
		return BishopAttacks(occ, sq)
	case Rook:
		return RookAttacks(occ, sq)
	case Queen:
		return QueenAttacks(occ, sq)
	case King:
		return KingAttacksTable[sq]
	}
	return 0
}
