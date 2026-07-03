package engine

// maxMoves bounds a moveList's fixed backing array. The maximum number of legal moves in any
// legal chess position is 218; 256 gives headroom and keeps the array a power-of-two size
// (mirrors the C++ engine's MoveList capacity).
const maxMoves = 256

// moveList is a fixed-capacity, stack-resident move buffer. Generating into a caller-owned
// moveList (the *Into methods) keeps the whole list on the stack — no per-node heap slice
// allocation, which was the dominant cost of move generation in perft/search. Mirrors the C++
// MoveList value type.
type moveList struct {
	moves [maxMoves]move
	count int
}

// add appends a move. Move generation is bounded by the 218-move legal maximum, so count never
// reaches maxMoves.
func (ml *moveList) add(m move) {
	ml.moves[ml.count] = m
	ml.count++
}

// slice returns the populated prefix as a []move for iteration/sorting. The backing array is ml's,
// so the returned slice must not outlive ml (true for the local-var usage in the hot paths).
func (ml *moveList) slice() []move {
	return ml.moves[:ml.count]
}

// generateLegalMovesInto fills ml with all legal moves from pos (allocation-free hot path).
func (pos *position) generateLegalMovesInto(ml *moveList) {
	pos.generateMovesInto(ml, pos.colorToMove, true)
}

// generateLegalCapturesInto fills ml with captures and promotions only (quiescence hot path).
func (pos *position) generateLegalCapturesInto(ml *moveList) {
	pos.generateMovesInto(ml, pos.colorToMove, false)
}

// generateLegalMoves returns all legal moves as a freshly allocated slice — a convenience wrapper
// for cold callers (UCI setup, opening book, tests). Hot paths use generateLegalMovesInto instead.
func (pos *position) generateLegalMoves() []move {
	var ml moveList
	pos.generateMovesInto(&ml, pos.colorToMove, true)
	return append([]move(nil), ml.slice()...)
}

// generateLegalCaptures returns captures and promotions as a freshly allocated slice (cold callers).
func (pos *position) generateLegalCaptures() []move {
	var ml moveList
	pos.generateMovesInto(&ml, pos.colorToMove, false)
	return append([]move(nil), ml.slice()...)
}

// Used for castling legality detection.
const (
	f1g1bb   bitboard = bitboard(1)<<f1 | bitboard(1)<<g1
	f8g8bb   bitboard = bitboard(1)<<f8 | bitboard(1)<<g8
	b1c1d1bb bitboard = bitboard(1)<<b1 | bitboard(1)<<c1 | bitboard(1)<<d1
	b8c8d8bb bitboard = bitboard(1)<<b8 | bitboard(1)<<c8 | bitboard(1)<<d8
)

// generateMovesInto fills ml with legal moves for color.  When doAllMoves is false only
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
func (pos *position) generateMovesInto(ml *moveList, c color, doAllMoves bool) {
	occupied := pos.occupied()
	friendlyPieces := pos.colors[c]
	enemyPieces := occupied ^ friendlyPieces
	enemyPawns := pos.pieces[pawn] & enemyPieces
	kingLocation := pos.kingSquare(c)

	// Build forbidden squares for the king by computing all enemy attacks
	// (temporarily removing our king so X-ray attacks are included).
	var forbidden bitboard
	if c == white {
		forbidden = enemyPawns.shiftSouthwest() | enemyPawns.shiftSoutheast()
	} else {
		forbidden = enemyPawns.shiftNorthwest() | enemyPawns.shiftNortheast()
	}
	occExceptKing := occupied ^ squareBB(kingLocation)
	for pt := knight; pt <= king; pt++ {
		for b := pos.pieces[pt] & enemyPieces; b.isNotEmpty(); {
			s := b.popLsb()
			forbidden |= pieceAttacks(pt, occExceptKing, s)
		}
	}

	// King moves.
	kingTargets := kingAttacks[kingLocation] & ^forbidden
	for b := kingTargets & enemyPieces; b.isNotEmpty(); {
		to := b.popLsb()
		ml.add(moveCapture(kingLocation, to, king, pos.squares[to]))
	}
	if doAllMoves {
		for b := kingTargets & ^occupied; b.isNotEmpty(); {
			to := b.popLsb()
			ml.add(moveNonCapture(kingLocation, to, king))
		}
	}

	allowedCaptures := enemyPieces
	allowedNonCaptures := ^occupied

	// In check: restrict allowed targets.
	if pos.isInCheck() {
		if pos.checkers.popCount() > 1 {
			return // Double check — only king moves are legal.
		}
		checkerSq := pos.checkers.lsb()
		allowedCaptures = pos.checkers
		allowedNonCaptures = interveningSquares[kingLocation][checkerSq]
	}

	pinInfo := computePins(pos)

	// Piece moves (knight, bishop, rook, queen).
	for pt := knight; pt <= queen; pt++ {
		for b := pos.pieces[pt] & friendlyPieces; b.isNotEmpty(); {
			from := b.popLsb()
			attacks := pieceAttacks(pt, occupied, from) & pinInfo.allowedSquares(from)
			for captures := attacks & allowedCaptures; captures.isNotEmpty(); {
				to := captures.popLsb()
				ml.add(moveCapture(from, to, pt, pos.squares[to]))
			}
			if doAllMoves {
				for nonCaptures := attacks & allowedNonCaptures; nonCaptures.isNotEmpty(); {
					to := nonCaptures.popLsb()
					ml.add(moveNonCapture(from, to, pt))
				}
			}
		}
	}

	// Castling (only when not in check; squares between king and rook must be
	// empty and unattacked).
	if doAllMoves && !pos.isInCheck() {
		if c == white {
			if pos.castling.mayWhiteCastleKingside() &&
				(occupied & f1g1bb).isEmpty() &&
				!pos.isAttacked(f1, black) && !pos.isAttacked(g1, black) {
				ml.add(moveCastling(e1, g1))
			}
			if pos.castling.mayWhiteCastleQueenside() &&
				(occupied & b1c1d1bb).isEmpty() &&
				!pos.isAttacked(d1, black) && !pos.isAttacked(c1, black) {
				ml.add(moveCastling(e1, c1))
			}
		} else {
			if pos.castling.mayBlackCastleKingside() &&
				(occupied & f8g8bb).isEmpty() &&
				!pos.isAttacked(f8, white) && !pos.isAttacked(g8, white) {
				ml.add(moveCastling(e8, g8))
			}
			if pos.castling.mayBlackCastleQueenside() &&
				(occupied & b8c8d8bb).isEmpty() &&
				!pos.isAttacked(d8, white) && !pos.isAttacked(c8, white) {
				ml.add(moveCastling(e8, c8))
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

	pawns = pos.pieces[pawn] & friendlyPieces
	if c == white {
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
				ml.add(movePromotion(from, to, queen, captured))
				ml.add(movePromotion(from, to, rook, captured))
				ml.add(movePromotion(from, to, bishop, captured))
				ml.add(movePromotion(from, to, knight, captured))
			}
		}
	}

	// Push promotions.
	{
		for b := promotions & allowedNonCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pushDelta)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				ml.add(movePromotion(from, to, queen, noPiece))
				ml.add(movePromotion(from, to, rook, noPiece))
				ml.add(movePromotion(from, to, bishop, noPiece))
				ml.add(movePromotion(from, to, knight, noPiece))
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
				ml.add(moveCapture(from, to, pawn, pos.squares[to]))
			}
		}
	}

	if doAllMoves {
		// Single pawn pushes.
		for b := singlePushes & allowedNonCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pushDelta)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				ml.add(moveSinglePush(from, to))
			}
		}
		// Double pawn pushes.
		for b := doublePushes & allowedNonCaptures; b.isNotEmpty(); {
			to := b.popLsb()
			from := square(int(to) - pushDelta*2)
			if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() {
				ml.add(moveDoublePush(from, to))
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
		capturedSq := epCapturedSquare(from, to)
		if (pinInfo.allowedSquares(from) & squareBB(to)).isNotEmpty() &&
			(allowedCaptures & squareBB(capturedSq)).isNotEmpty() {
			// Check for a horizontal discovered check through the king's rank.
			if kingLocation.rank() == from.rank() {
				pseudoOcc := occupied ^ squareBB(capturedSq) ^ squareBB(from)
				horizontalAttacks := rookAttacks(pseudoOcc, kingLocation) & (westOf[kingLocation] | eastOf[kingLocation])
				if (horizontalAttacks & pos.colors[enemyOf(c)] & (pos.pieces[rook] | pos.pieces[queen])).isNotEmpty() {
					continue
				}
			}
			ml.add(moveEpCapture(from, to))
		}
	}
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
