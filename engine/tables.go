package engine

import "math/rand"

var (
	northOf                [64]bitboard
	northeastOf            [64]bitboard
	eastOf                 [64]bitboard
	southeastOf            [64]bitboard
	southOf                [64]bitboard
	southwestOf            [64]bitboard
	westOf                 [64]bitboard
	northwestOf            [64]bitboard
	pawnAttacksWhite       [64]bitboard
	pawnAttacksBlack       [64]bitboard
	knightAttacks          [64]bitboard
	bishopAttacksEmpty     [64]bitboard
	rookAttacksEmpty       [64]bitboard
	queenAttacksEmpty      [64]bitboard
	kingAttacks            [64]bitboard
	kingAttacks2           [64]bitboard
	kingPawnShelterWhite   [64]bitboard
	kingPawnShelterBlack   [64]bitboard
	passedPawnMaskWhite    [64]bitboard
	passedPawnMaskBlack    [64]bitboard
	isolatedPawnMask       [64]bitboard
	supportedPawnMaskWhite [64]bitboard
	supportedPawnMaskBlack [64]bitboard
	doubledPawnMaskWhite   [64]bitboard
	doubledPawnMaskBlack   [64]bitboard
	interveningSquares     [64][64]bitboard
	pieceSquareHashes      [2][6][64]uint64 // [color][piece-1][square]
	castlingRightsHashes   [16]uint64
	enPassantHashes        [64]uint64
	blackMoveHash          uint64
)

func init() {
	initRays()
	initAttacks()
	initMagics()
	initPawnMasks()
	initInterveningSquares()
	initZobrist()
	initCastleMasks()
}

type shiftFn func(bitboard) bitboard

var shiftFns = map[direction]shiftFn{
	north:     bitboard.shiftNorth,
	northeast: bitboard.shiftNortheast,
	east:      bitboard.shiftEast,
	southeast: bitboard.shiftSoutheast,
	south:     bitboard.shiftSouth,
	southwest: bitboard.shiftSouthwest,
	west:      bitboard.shiftWest,
	northwest: bitboard.shiftNorthwest,
}

func rayFrom(sq square, dir direction) bitboard {
	var result bitboard
	fn := shiftFns[dir]
	for b := fn(squareBB(sq)); b != 0; b = fn(b) {
		result |= b
	}
	return result
}

func initRays() {
	var sq square
	for sq = range 64 {
		northOf[sq] = rayFrom(sq, north)
		northeastOf[sq] = rayFrom(sq, northeast)
		eastOf[sq] = rayFrom(sq, east)
		southeastOf[sq] = rayFrom(sq, southeast)
		southOf[sq] = rayFrom(sq, south)
		southwestOf[sq] = rayFrom(sq, southwest)
		westOf[sq] = rayFrom(sq, west)
		northwestOf[sq] = rayFrom(sq, northwest)
	}
}

func kingFill(b bitboard) bitboard {
	return b.shiftNorth() |
		b.shiftNortheast() |
		b.shiftEast() |
		b.shiftSoutheast() |
		b.shiftSouth() |
		b.shiftSouthwest() |
		b.shiftWest() |
		b.shiftNorthwest()
}

func initAttacks() {
	knightVectors := [8][2]int{{-2, -1}, {-2, 1}, {-1, -2}, {-1, 2}, {1, -2}, {1, 2}, {2, -1}, {2, 1}}
	for sq := range square(64) {
		f, r := sq.file(), sq.rank()

		// Pawn attacks
		b := squareBB(sq)
		pawnAttacksWhite[sq] = b.shiftNorthwest() | b.shiftNortheast()
		pawnAttacksBlack[sq] = b.shiftSouthwest() | b.shiftSoutheast()

		// Knight attacks
		var ka bitboard
		for _, v := range knightVectors {
			nf, nr := int(f)+v[0], int(r)+v[1]
			if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
				ka |= squareBB(squareXY(nf, nr))
			}
		}
		knightAttacks[sq] = ka

		// Sliding piece attacks on empty board
		bishopAttacksEmpty[sq] = northeastOf[sq] | northwestOf[sq] | southeastOf[sq] | southwestOf[sq]
		rookAttacksEmpty[sq] = northOf[sq] | southOf[sq] | eastOf[sq] | westOf[sq]
		queenAttacksEmpty[sq] = bishopAttacksEmpty[sq] | rookAttacksEmpty[sq]

		// King attacks
		kingAttacks[sq] = kingFill(b)
		kingAttacks2[sq] = kingFill(kingFill(b)) & ^b
	}
}

func initPawnMasks() {
	for sq := square(0); sq < 64; sq++ {
		b := squareBB(sq)
		file := fileA << uint(sq.file())

		// Pawn shelter
		if sq != e1 {
			kingPawnShelterWhite[sq] = b.shiftNorthwest() | b.shiftNorth() | b.shiftNortheast()
		}
		if sq != e8 {
			kingPawnShelterBlack[sq] = b.shiftSouthwest() | b.shiftSouth() | b.shiftSoutheast()
		}

		// Passed pawn masks
		fwd := northOf[sq]
		passedPawnMaskWhite[sq] = fwd | fwd.shiftWest() | fwd.shiftEast()
		bwd := southOf[sq]
		passedPawnMaskBlack[sq] = bwd | bwd.shiftWest() | bwd.shiftEast()

		// Isolated pawn mask (adjacent files, all ranks)
		isolatedPawnMask[sq] = file.shiftWest() | file.shiftEast()

		// Supported pawn mask
		colW := (southOf[sq] | b).shiftWest() | (southOf[sq] | b).shiftEast()
		supportedPawnMaskWhite[sq] = colW
		colB := (northOf[sq] | b).shiftWest() | (northOf[sq] | b).shiftEast()
		supportedPawnMaskBlack[sq] = colB

		// Doubled pawn mask
		doubledPawnMaskWhite[sq] = northOf[sq]
		doubledPawnMaskBlack[sq] = southOf[sq]
	}
}

// ---- intervening squares ----

func initInterveningSquares() {
	for from := range square(64) {
		for to := range square(64) {
			for dir := range direction(8) {
				ray := rayFrom(from, dir)
				if (ray & squareBB(to)) != 0 {
					interveningSquares[from][to] = ray ^ rayFrom(to, dir) ^ squareBB(to)
					break
				}
			}
		}
	}
}

// ---- Zobrist hashing ----

func initZobrist() {
	rng := rand.New(rand.NewSource(1968))
	for c := range 2 {
		for p := range 6 {
			for sq := range 64 {
				pieceSquareHashes[c][p][sq] = rng.Uint64()
			}
		}
	}
	for i := range castlingRightsHashes {
		castlingRightsHashes[i] = rng.Uint64()
	}
	for i := range square(64) {
		if i.rank() == 2 || i.rank() == 5 {
			enPassantHashes[i] = rng.Uint64()
		}
	}
	blackMoveHash = rng.Uint64()
}
