package engine

import "math/rand"

// Precomputed ray tables: for each square, the set of squares in each direction.
var (
	northOf     [64]bitboard
	northeastOf [64]bitboard
	eastOf      [64]bitboard
	southeastOf [64]bitboard
	southOf     [64]bitboard
	southwestOf [64]bitboard
	westOf      [64]bitboard
	northwestOf [64]bitboard
)

// Piece attack tables on an empty board.
var (
	pawnAttacksWhite   [64]bitboard // pawn attacks from White's perspective
	pawnAttacksBlack   [64]bitboard // pawn attacks from Black's perspective
	knightAttacks      [64]bitboard
	bishopAttacksEmpty [64]bitboard
	rookAttacksEmpty   [64]bitboard
	queenAttacksEmpty  [64]bitboard
	kingAttacks        [64]bitboard
	kingAttacks2       [64]bitboard // squares two king-steps away (used for zone control)
)

// Pawn structure masks used in evaluation and move generation.
var (
	kingPawnShelterWhite   [64]bitboard // squares directly in front of the White king
	kingPawnShelterBlack   [64]bitboard // squares directly in front of the Black king
	passedPawnMaskWhite    [64]bitboard // squares ahead and on adjacent files (no enemy pawn = passed)
	passedPawnMaskBlack    [64]bitboard
	isolatedPawnMask       [64]bitboard // adjacent files at all ranks (no friendly pawn = isolated)
	supportedPawnMaskWhite [64]bitboard // squares from which a White pawn on sq could be defended
	supportedPawnMaskBlack [64]bitboard
	doubledPawnMaskWhite   [64]bitboard // squares ahead on the same file (friendly pawn = doubled)
	doubledPawnMaskBlack   [64]bitboard
)

// interveningSquares[from][to] is the set of squares strictly between from and to
// on the shared ray, or 0 if they share no ray.  Used for check interposition and
// pin detection.
var interveningSquares [64][64]bitboard

type zobrist uint64

// Zobrist hashing tables.
var (
	pieceSquareHashes    [2][6][64]zobrist // [color][piece-1][square]
	castlingRightsHashes [16]zobrist
	enPassantHashes      [64]zobrist // non-zero only for ep-target squares (ranks 3 and 6)
	blackMoveHash        zobrist     // XORed into the hash when Black is to move
)

// init runs all table initialisation in dependency order at program startup.
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

// rayFrom returns all squares reachable from sq by repeated application of dir.
func rayFrom(sq square, dir direction) bitboard {
	var result bitboard
	fn := shiftFns[dir]
	for b := fn(squareBB(sq)); b != 0; b = fn(b) {
		result |= b
	}
	return result
}

// initRays populates the eight directional ray tables.
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

// kingFill returns the set of squares one king-step from any bit set in b.
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

// initAttacks populates pawn, knight, sliding-piece (empty board), and king attack tables.
func initAttacks() {
	knightVectors := [8][2]int{{-2, -1}, {-2, 1}, {-1, -2}, {-1, 2}, {1, -2}, {1, 2}, {2, -1}, {2, 1}}
	for sq := range square(64) {
		f, r := sq.file(), sq.rank()

		b := squareBB(sq)
		pawnAttacksWhite[sq] = b.shiftNorthwest() | b.shiftNortheast()
		pawnAttacksBlack[sq] = b.shiftSouthwest() | b.shiftSoutheast()

		var ka bitboard
		for _, v := range knightVectors {
			nf, nr := int(f)+v[0], int(r)+v[1]
			if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
				ka |= squareBB(squareXY(nf, nr))
			}
		}
		knightAttacks[sq] = ka

		bishopAttacksEmpty[sq] = northeastOf[sq] | northwestOf[sq] | southeastOf[sq] | southwestOf[sq]
		rookAttacksEmpty[sq] = northOf[sq] | southOf[sq] | eastOf[sq] | westOf[sq]
		queenAttacksEmpty[sq] = bishopAttacksEmpty[sq] | rookAttacksEmpty[sq]

		kingAttacks[sq] = kingFill(b)
		kingAttacks2[sq] = kingFill(kingFill(b)) & ^b
	}
}

// initPawnMasks populates all pawn-structure and king-shelter masks.
func initPawnMasks() {
	for sq := square(0); sq < 64; sq++ {
		b := squareBB(sq)
		file := fileA << uint(sq.file())

		// King pawn shelter: the three squares directly in front of the king.
		// The e-file exclusions prevent the shelter mask from wrapping around
		// to the other side of the board for corner kings.
		if sq != e1 {
			kingPawnShelterWhite[sq] = b.shiftNorthwest() | b.shiftNorth() | b.shiftNortheast()
		}
		if sq != e8 {
			kingPawnShelterBlack[sq] = b.shiftSouthwest() | b.shiftSouth() | b.shiftSoutheast()
		}

		fwd := northOf[sq]
		passedPawnMaskWhite[sq] = fwd | fwd.shiftWest() | fwd.shiftEast()
		bwd := southOf[sq]
		passedPawnMaskBlack[sq] = bwd | bwd.shiftWest() | bwd.shiftEast()

		isolatedPawnMask[sq] = file.shiftWest() | file.shiftEast()

		colW := (southOf[sq] | b).shiftWest() | (southOf[sq] | b).shiftEast()
		supportedPawnMaskWhite[sq] = colW
		colB := (northOf[sq] | b).shiftWest() | (northOf[sq] | b).shiftEast()
		supportedPawnMaskBlack[sq] = colB

		doubledPawnMaskWhite[sq] = northOf[sq]
		doubledPawnMaskBlack[sq] = southOf[sq]
	}
}

// initInterveningSquares populates interveningSquares[from][to] for all pairs
// that share a ray.  The result is the set of squares strictly between from and to.
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

// initZobrist generates pseudo-random Zobrist hashes using a seeded RNG so that
// the hashes are deterministic across runs.  Seed 1968 is arbitrary but fixed.
func initZobrist() {
	rng := rand.New(rand.NewSource(1968))
	for c := range 2 {
		for p := range 6 {
			for sq := range 64 {
				pieceSquareHashes[c][p][sq] = zobrist(rng.Uint64())
			}
		}
	}
	for i := range castlingRightsHashes {
		castlingRightsHashes[i] = zobrist(rng.Uint64())
	}
	// En-passant hashes are only needed for ranks 3 and 6 (the ep-target squares).
	for i := range square(64) {
		if i.rank() == 2 || i.rank() == 5 {
			enPassantHashes[i] = zobrist(rng.Uint64())
		}
	}
	blackMoveHash = zobrist(rng.Uint64())
}
