package engine

// generateLegalMoves returns all legal moves from pos.
func (pos *position) generateLegalMoves() []move {
	return pos.generateMoves(pos.colorToMove(), true)
}

// generateLegalCaptures returns captures and promotions only.
func (pos *position) generateLegalCaptures() []move {
	return pos.generateMoves(pos.colorToMove(), false)
}

// generateMoves returns legal moves for color.  When doAllMoves is false only
// captures and promotions are generated (used for quiescence search).
//
// The algorithm:
//  1. Build the forbidden-square set for the king by computing all enemy attacks,
//     temporarily removing the king from the occupancy so that X-ray attacks
//     through the king are included.
//  2. Generate king moves to non-forbidden squares.
//  3. If in double check, return — only king moves are legal.
//  4. For single check, restrict non-king destinations to the checker's square
//     (capture) or the squares between the king and the checker (interposition).
//  5. Compute absolute pins via computePins.
//  6. Generate piece moves filtered by pin constraints.
//  7. Generate castling if not in check and squares are clear and unattacked.
//  8. Generate pawn moves, with en-passant horizontal-pin detection.
func (pos *position) generateMoves(color color, doAllMoves bool) []move {
	moves := make([]move, 0, 64)
	occupied := pos.occupied()
	friendlyPieces := pos.piecesOfColor(color)
	enemyPieces := occupied ^ friendlyPieces
	enemyPawns := pos.pawns & enemyPieces
	kingLocation := pos.kingLocn[color]

	// Build forbidden squares for the king by computing all enemy attacks
	// (temporarily removing our king so X-ray attacks are included).
	var forbidden bitboard
	if color == white {
		forbidden = enemyPawns.shiftSouthwest() | enemyPawns.shiftSoutheast()
	} else {
		forbidden = enemyPawns.shiftNorthwest() | enemyPawns.shiftNortheast()
	}
	occExceptKing := occupied ^ squareBB(kingLocation)
	for piece := knight; piece <= king; piece++ {
		for b := *pos.piecesOfType(piece) & enemyPieces; b.isNotEmpty(); {
			s := b.popLsb()
			forbidden |= pieceAttacks(piece, occExceptKing, s)
		}
	}

	// King moves.
	kingTargets := kingAttacks[kingLocation] & ^forbidden
	for b := kingTargets & enemyPieces; b.isNotEmpty(); {
		to := b.popLsb()
		moves = append(moves, moveCapture(kingLocation, to, king, pos.squares[to]))
	}
	if doAllMoves {
		for b := kingTargets & ^occupied; b.isNotEmpty(); {
			to := b.popLsb()
			moves = append(moves, moveNonCapture(kingLocation, to, king))
		}
	}

	allowedCaptures := enemyPieces
	allowedNonCaptures := ^occupied

	// In check: restrict allowed targets.
	if pos.isInCheck() {
		if pos.checkers.popCount() > 1 {
			return moves // Double check — only king moves are legal.
		}
		checkerSq := pos.checkers.lsb()
		allowedCaptures = pos.checkers
		allowedNonCaptures = interveningSquares[kingLocation][checkerSq]
	}

	pinInfo := computePins(pos)

	// Piece moves (knight, bishop, rook, queen).
	for piece := knight; piece <= queen; piece++ {
		for b := *pos.piecesOfType(piece) & friendlyPieces; b.isNotEmpty(); {
			from := b.popLsb()
			attacks := pieceAttacks(piece, occupied, from) & pinInfo.allowedSquares(from)
			for captures := attacks & allowedCaptures; captures.isNotEmpty(); {
				to := captures.popLsb()
				moves = append(moves, moveCapture(from, to, piece, pos.squares[to]))
			}
			if doAllMoves {
				for nonCaptures := attacks & allowedNonCaptures; nonCaptures.isNotEmpty(); {
					to := nonCaptures.popLsb()
					moves = append(moves, moveNonCapture(from, to, piece))
				}
			}
		}
	}

	// Castling (only when not in check; squares between king and rook must be
	// empty and unattacked).
	if doAllMoves && !pos.isInCheck() {
		if color == white {
			if pos.castling.mayWhiteCastleKingside() &&
				(occupied & (squareBB(f1) | squareBB(g1))).isEmpty() &&
				!pos.isAttacked(f1, black) && !pos.isAttacked(g1, black) {
				moves = append(moves, moveCastling(e1, g1))
			}
			if pos.castling.mayWhiteCastleQueenside() &&
				(occupied & (squareBB(b1) | squareBB(c1) | squareBB(d1))).isEmpty() &&
				!pos.isAttacked(d1, black) && !pos.isAttacked(c1, black) {
				moves = append(moves, moveCastling(e1, c1))
			}
		} else {
			if pos.castling.mayBlackCastleKingside() &&
				(occupied & (squareBB(f8) | squareBB(g8))).isEmpty() &&
				!pos.isAttacked(f8, white) && !pos.isAttacked(g8, white) {
				moves = append(moves, moveCastling(e8, g8))
			}
			if pos.castling.mayBlackCastleQueenside() &&
				(occupied & (squareBB(b8) | squareBB(c8) | squareBB(d8))).isEmpty() &&
				!pos.isAttacked(d8, white) && !pos.isAttacked(c8, white) {
				moves = append(moves, moveCastling(e8, c8))
			}
		}
	}

	// Pawn move generation — direction and rank constants differ by color.
	var (
		pawns          bitboard
		singlePushes   bitboard
		doublePushes   bitboard
		capturesWest   bitboard
		capturesEast   bitboard
		epSources      bitboard
		promotions     bitboard
		promotionsWest bitboard
		promotionsEast bitboard
		promoRank      bitboard
		pushDelta      int
		westDelta      int
		eastDelta      int
	)

	if color == white {
		pawns = pos.pawns & friendlyPieces
		singlePushes = pawns.shiftNorth() & ^occupied
		doublePushes = singlePushes.shiftNorth() & ^occupied & rank4
		capturesWest = pawns.shiftNorthwest() & enemyPieces
		capturesEast = pawns.shiftNortheast() & enemyPieces
		if pos.epSquare != noSquare {
			epSources = pawnAttacksBlack[pos.epSquare] & pawns
		}
		pushDelta = 8
		westDelta = 7
		eastDelta = 9
		promoRank = rank8
	} else {
		pawns = pos.pawns & friendlyPieces
		singlePushes = pawns.shiftSouth() & ^occupied
		doublePushes = singlePushes.shiftSouth() & ^occupied & rank5
		capturesWest = pawns.shiftSouthwest() & enemyPieces
		capturesEast = pawns.shiftSoutheast() & enemyPieces
		if pos.epSquare != noSquare {
			epSources = pawnAttacksWhite[pos.epSquare] & pawns
		}
		pushDelta = -8
		westDelta = -9
		eastDelta = -7
		promoRank = rank1
	}

	promotions = singlePushes & promoRank
	promotionsWest = capturesWest & promoRank
	promotionsEast = capturesEast & promoRank
	capturesWest ^= promotionsWest
	capturesEast ^= promotionsEast
	singlePushes ^= promotions

	// Capture promotions.
	for _, pair := range [2]struct {
		bb    bitboard
		delta int
	}{{promotionsWest, westDelta}, {promotionsEast, eastDelta}} {
		for b := pair.bb & allowedCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pair.delta)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				captured := pos.squares[to]
				moves = append(moves, movePromotion(from, to, queen, captured))
				moves = append(moves, movePromotion(from, to, rook, captured))
				moves = append(moves, movePromotion(from, to, bishop, captured))
				moves = append(moves, movePromotion(from, to, knight, captured))
			}
		}
	}

	// Push promotions.
	{
		for b := promotions & allowedNonCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pushDelta)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				moves = append(moves, movePromotion(from, to, queen, noPiece))
				moves = append(moves, movePromotion(from, to, rook, noPiece))
				moves = append(moves, movePromotion(from, to, bishop, noPiece))
				moves = append(moves, movePromotion(from, to, knight, noPiece))
			}
		}
	}

	// Captures (non-promotion).
	for _, pair := range [2]struct {
		bb    bitboard
		delta int
	}{{capturesWest, westDelta}, {capturesEast, eastDelta}} {
		for b := pair.bb & allowedCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pair.delta)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				moves = append(moves, moveCapture(from, to, pawn, pos.squares[to]))
			}
		}
	}

	if doAllMoves {
		// Single pawn pushes.
		for b := singlePushes & allowedNonCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pushDelta)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				moves = append(moves, moveSinglePush(from, to))
			}
		}
		// Double pawn pushes.
		for b := doublePushes & allowedNonCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pushDelta*2)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				moves = append(moves, moveDoublePush(from, to))
			}
		}
	}

	// En passant captures.  Each candidate pawn must pass both the pin check and
	// a horizontal discovered-check test: after removing both pawns from their rank,
	// check whether a rook or queen on that rank attacks the king.
	for epSources.isNotEmpty() {
		from := epSources.popLsb()
		to := pos.epSquare
		// The captured pawn is on the source rank, destination file.
		capturedSq := square((from & 0x38) | (to & 0x07))
		if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() &&
			(allowedCaptures & squareBB(capturedSq)).isNotEmpty() {
			// Check for a horizontal discovered check through the king's rank.
			if kingLocation.rank() == from.rank() {
				pseudoOcc := occupied ^ squareBB(capturedSq) ^ squareBB(from)
				horizontalAttacks := rookAttacks(pseudoOcc, kingLocation) & (westOf[kingLocation] | eastOf[kingLocation])
				if (horizontalAttacks & pos.piecesOfColor(enemyOf(color)) & (pos.rooks | pos.queens)).isNotEmpty() {
					continue
				}
			}
			moves = append(moves, moveEpCapture(from, to))
		}
	}
	return moves
}

// pieceAttacks returns the attack set for piece type pt on sq given occupied squares.
func pieceAttacks(pt piece, occupied bitboard, sq square) bitboard {
	switch pt {
	case knight:
		return knightAttacks[sq]
	case bishop:
		return bishopAttacks(occupied, sq)
	case rook:
		return rookAttacks(occupied, sq)
	case queen:
		return bishopAttacks(occupied, sq) | rookAttacks(occupied, sq)
	case king:
		return kingAttacks[sq]
	}
	return 0
}
