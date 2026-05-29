package engine

// generateLegalMoves returns all legal moves from pos.
func (position *position) generateLegalMoves() []move {
	return position.generateMoves(position.colorToMove, true)
}

// generateLegalCaptures returns captures and promotions only.
func (position *position) generateLegalCaptures() []move {
	return position.generateMoves(position.colorToMove, false)
}

// Used for castling legality detection.
const (
	f1g1bb   bitboard = bitboard(1)<<f1 | bitboard(1)<<g1
	f8g8bb   bitboard = bitboard(1)<<f8 | bitboard(1)<<g8
	b1c1d1bb bitboard = bitboard(1)<<b1 | bitboard(1)<<c1 | bitboard(1)<<d1
	b8c8d8bb bitboard = bitboard(1)<<b8 | bitboard(1)<<c8 | bitboard(1)<<d8
)

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
func (position *position) generateMoves(color color, doAllMoves bool) []move {
	moves := make([]move, 0, 64)
	occupied := position.occupied()
	friendlyPieces := position.piecesOfColor(color)
	enemyPieces := occupied ^ friendlyPieces
	enemyPawns := position.pieces[pawn] & enemyPieces
	kingLocation := position.kingLocation[color]

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
		for b := *position.piecesOfType(piece) & enemyPieces; b.isNotEmpty(); {
			s := b.popLsb()
			forbidden |= pieceAttacks(piece, occExceptKing, s)
		}
	}

	// King moves.
	kingTargets := kingAttacks[kingLocation] & ^forbidden
	for b := kingTargets & enemyPieces; b.isNotEmpty(); {
		to := b.popLsb()
		moves = append(moves, moveCapture(kingLocation, to, king, position.squares[to]))
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
	if position.isInCheck() {
		if position.checkers.popCount() > 1 {
			return moves // Double check — only king moves are legal.
		}
		checkerSq := position.checkers.lsb()
		allowedCaptures = position.checkers
		allowedNonCaptures = interveningSquares[kingLocation][checkerSq]
	}

	pinInfo := computePins(position)

	// Piece moves (knight, bishop, rook, queen).
	for piece := knight; piece <= queen; piece++ {
		for b := *position.piecesOfType(piece) & friendlyPieces; b.isNotEmpty(); {
			from := b.popLsb()
			attacks := pieceAttacks(piece, occupied, from) & pinInfo.allowedSquares(from)
			for captures := attacks & allowedCaptures; captures.isNotEmpty(); {
				to := captures.popLsb()
				moves = append(moves, moveCapture(from, to, piece, position.squares[to]))
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
	if doAllMoves && !position.isInCheck() {
		if color == white {
			if position.castling.mayWhiteCastleKingside() &&
				(occupied & f1g1bb).isEmpty() &&
				!position.isAttacked(f1, black) && !position.isAttacked(g1, black) {
				moves = append(moves, moveCastling(e1, g1))
			}
			if position.castling.mayWhiteCastleQueenside() &&
				(occupied & b1c1d1bb).isEmpty() &&
				!position.isAttacked(d1, black) && !position.isAttacked(c1, black) {
				moves = append(moves, moveCastling(e1, c1))
			}
		} else {
			if position.castling.mayBlackCastleKingside() &&
				(occupied & f8g8bb).isEmpty() &&
				!position.isAttacked(f8, white) && !position.isAttacked(g8, white) {
				moves = append(moves, moveCastling(e8, g8))
			}
			if position.castling.mayBlackCastleQueenside() &&
				(occupied & b8c8d8bb).isEmpty() &&
				!position.isAttacked(d8, white) && !position.isAttacked(c8, white) {
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
		pawns = position.pieces[pawn] & friendlyPieces
		singlePushes = pawns.shiftNorth() & ^occupied
		doublePushes = singlePushes.shiftNorth() & ^occupied & rank4
		capturesWest = pawns.shiftNorthwest() & enemyPieces
		capturesEast = pawns.shiftNortheast() & enemyPieces
		if position.epSquare != noSquare {
			epSources = pawnAttacksBlack[position.epSquare] & pawns
		}
		pushDelta = 8
		westDelta = 7
		eastDelta = 9
		promoRank = rank8
	} else {
		pawns = position.pieces[pawn] & friendlyPieces
		singlePushes = pawns.shiftSouth() & ^occupied
		doublePushes = singlePushes.shiftSouth() & ^occupied & rank5
		capturesWest = pawns.shiftSouthwest() & enemyPieces
		capturesEast = pawns.shiftSoutheast() & enemyPieces
		if position.epSquare != noSquare {
			epSources = pawnAttacksWhite[position.epSquare] & pawns
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
				captured := position.squares[to]
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
				moves = append(moves, moveCapture(from, to, pawn, position.squares[to]))
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
		to := position.epSquare
		// The captured pawn is on the source rank, destination file.
		capturedSq := square((from & 0x38) | (to & 0x07))
		if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() &&
			(allowedCaptures & squareBB(capturedSq)).isNotEmpty() {
			// Check for a horizontal discovered check through the king's rank.
			if kingLocation.rank() == from.rank() {
				pseudoOcc := occupied ^ squareBB(capturedSq) ^ squareBB(from)
				horizontalAttacks := rookAttacks(pseudoOcc, kingLocation) & (westOf[kingLocation] | eastOf[kingLocation])
				if (horizontalAttacks & position.piecesOfColor(enemyOf(color)) & (position.pieces[rook] | position.pieces[queen])).isNotEmpty() {
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
