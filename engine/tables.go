package engine

import "math/rand"

type zobrist uint64
type shiftFn func(bitboard) bitboard
type attackFn func(bitboard, square) bitboard

// bishopAttacks returns the squares attacked by a bishop on sq given the occupied squares.
func bishopAttacks(occ bitboard, sq square) bitboard {
	return bishopTable[sq][(uint64(occ&bishopMask[sq])*bishopMagic[sq])>>bishopShift[sq]]
}

// rookAttacks returns the squares attacked by a rook on sq given the occupied squares.
func rookAttacks(occ bitboard, sq square) bitboard {
	return rookTable[sq][(uint64(occ&rookMask[sq])*rookMagic[sq])>>rookShift[sq]]
}

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
	return b.shiftNorth() | b.shiftNortheast() | b.shiftEast() | b.shiftSoutheast() |
		b.shiftSouth() | b.shiftSouthwest() | b.shiftWest() | b.shiftNorthwest()
}

// initAttacks populates pawn, knight, sliding-piece (empty board), and king attack tables.
func initAttacks() {
	knightVectors := [8][2]int{{-2, -1}, {-2, 1}, {-1, -2}, {-1, 2}, {1, -2}, {1, 2}, {2, -1}, {2, 1}}
	for sq := range square(64) {
		f, r := sq.file(), sq.rank()

		b := squareBB(sq)
		pawnAttacksWhite[sq] = b.shiftNorthwest() | b.shiftNortheast()
		pawnAttacksBlack[sq] = b.shiftSouthwest() | b.shiftSoutheast()

		var knightTargets bitboard
		for _, v := range knightVectors {
			nf, nr := int(f)+v[0], int(r)+v[1]
			if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
				knightTargets |= squareBB(squareXY(nf, nr))
			}
		}
		knightAttacks[sq] = knightTargets

		bishopAttacksEmpty[sq] = northeastOf[sq] | northwestOf[sq] | southeastOf[sq] | southwestOf[sq]
		rookAttacksEmpty[sq] = northOf[sq] | southOf[sq] | eastOf[sq] | westOf[sq]
		queenAttacksEmpty[sq] = bishopAttacksEmpty[sq] | rookAttacksEmpty[sq]

		kingAttacks[sq] = kingFill(b)
		kingAttacks2[sq] = kingFill(kingFill(b)) & ^b
	}
}

// initPawnMasks populates all pawn-structure and king-shelter masks.
func initPawnMasks() {
	for sq := range square(64) {
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

var (
	rookMask  [64]bitboard
	rookShift [64]uint
	rookTable [64][]bitboard

	bishopMask  [64]bitboard
	bishopShift [64]uint
	bishopTable [64][]bitboard
)

// Magic numbers found offline with seed 1337.  Pre-computing them avoids the
// ~400 ms startup cost of searching for them at runtime.
var rookMagic = [64]uint64{
	0x2280024000142482, 0x0040002000100040, 0x01801000A0018008, 0x8100050010000822,
	0x2200020020081004, 0x02000A0054031008, 0x0280020001000080, 0x0480088007402100,
	0x0100800080400022, 0x2B01802000400088, 0x8002002010408200, 0x0015002089009000,
	0x0002802400820800, 0x0000800200800400, 0x4304000201041008, 0x48820010A2040041,
	0x00008C8000400020, 0xA010014000200048, 0x0005010020041040, 0x8012020040081020,
	0x04A0808004000800, 0x0180808004000200, 0x8220040008415002, 0x01000A0001410694,
	0x1084400880008232, 0xE000500040002000, 0x0820002080100084, 0x0080601B00100102,
	0x0000040080800800, 0x2008040080020080, 0x0000011400300218, 0x400C21060020418C,
	0x408000200C400044, 0x4040201009400044, 0x0215408822001200, 0x0000084012002200,
	0x000200100A002004, 0x8002800200800400, 0x81A0800200800100, 0x0000440C86000041,
	0x6280400080208000, 0x0060002050004002, 0x0020002010008080, 0x0808008010028008,
	0x0008000400088080, 0x4002000400028080, 0x0800020108840010, 0x8000040080520021,
	0x0100410022008200, 0x9010C608A1008200, 0x2222008020401A00, 0x4080300028008180,
	0x0404040082080080, 0x4100020080040080, 0x0801100801020400, 0x0000800100004080,
	0x1001041420800041, 0x0102248040001301, 0x000041040870A001, 0x008C086200104322,
	0x1813000800041043, 0x0002000804019002, 0x20060008110400C2, 0x8900010020804412,
}

var bishopMagic = [64]uint64{
	0xA04010022095A180, 0x900238152102008A, 0x0410414200200000, 0x0061050102400002,
	0xA00A021130482040, 0x4001042044000884, 0x00A4009208220104, 0x0001040104020202,
	0x0000100202042408, 0x0040900420840040, 0x8000900100411240, 0x1401042400908000,
	0x0004011040000000, 0x3000009004204280, 0x253200808808C000, 0x8030210888010810,
	0x400428111012A800, 0x0304004828080060, 0x1814000241020600, 0x00040016C0428000,
	0x2282012400940000, 0x0842000040622004, 0x8021100201012000, 0x8200400084008828,
	0xC089400004900201, 0x04C8084012920800, 0x0001405018020840, 0x5000802028020020,
	0x00868C0000802018, 0x041400200500A000, 0x0281020000480410, 0x0204002000491408,
	0x823410C408400481, 0x0002082088040110, 0x0032002410020801, 0x2420600501080090,
	0x0040008020020020, 0x2001060A00448804, 0x8001010408010400, 0x0048344044C50108,
	0x0C88011013020800, 0x2000820120801005, 0x0401001082241000, 0x2C20412018008100,
	0x4080A86100400400, 0x0820202420401020, 0x4002040404310881, 0x0102408200801200,
	0x0806081442088000, 0x0000444210300109, 0x0000811088041240, 0x502202A042020080,
	0x9080401002088800, 0x0041609410622280, 0x0420824401041000, 0x0082A80200A20008,
	0x8041008084200244, 0x0500110401218810, 0x0010120200840422, 0x9002204001040900,
	0x408280415120220C, 0x0100004010020898, 0x0005410808451440, 0x4060182101003200,
}

// initMagics builds the rook and bishop magic lookup tables.
// For each square it enumerates all relevant occupancy subsets, computes the
// corresponding attack set with a classical ray-sliding function, then stores
// the result at the index given by the magic hash.
func initMagics() {
	var occupancies [4096]bitboard
	var target_squares [4096]bitboard

	for sq := range square(64) {
		mask := (northOf[sq] & ^rank8) | (southOf[sq] & ^rank1) | (eastOf[sq] & ^fileH) | (westOf[sq] & ^fileA)
		rookMask[sq] = mask
		rookShift[sq] = uint(64 - mask.popCount())
		rookTable[sq] = make([]bitboard, 1<<mask.popCount())
		n := enumerateOccupancies(mask, occupancies[:], target_squares[:], rookAttacksClassic, sq)
		fillMagicTable(rookMagic[sq], occupancies[:n], target_squares[:n], rookShift[sq], rookTable[sq])
	}

	for sq := range square(64) {
		mask := (northeastOf[sq] & ^(rank8 | fileH)) | (northwestOf[sq] & ^(rank8 | fileA)) |
			(southeastOf[sq] & ^(rank1 | fileH)) | (southwestOf[sq] & ^(rank1 | fileA))
		bishopMask[sq] = mask
		bishopShift[sq] = uint(64 - mask.popCount())
		bishopTable[sq] = make([]bitboard, 1<<mask.popCount())
		n := enumerateOccupancies(mask, occupancies[:], target_squares[:], bishopAttacksClassic, sq)
		fillMagicTable(bishopMagic[sq], occupancies[:n], target_squares[:n], bishopShift[sq], bishopTable[sq])
	}
}

// fillMagicTable stores each (occupancy, attack-set) pair into table at the
// index given by the magic hash: index = (occ * magic) >> shift.
func fillMagicTable(magic uint64, occs, refs []bitboard, shift uint, table []bitboard) {
	for i, occ := range occs {
		table[(uint64(occ)*magic)>>shift] = refs[i]
	}
}

// enumerateOccupancies generates all 2^n subsets of mask (where n = popcount(mask))
// using the Carry-Rippler technique, stores each subset in occupancies and its
// corresponding attack set (from getAttacks) in targets, and returns n.
func enumerateOccupancies(mask bitboard, occupancies, targets []bitboard, getAttacks attackFn, sq square) int {
	count := 0
	occupancy := bitboard(0)
	for {
		occupancies[count] = occupancy
		targets[count] = getAttacks(occupancy, sq)
		count++
		occupancy = (occupancy - 1) & mask
		if occupancy == 0 {
			break
		}
	}
	return count
}

// lsbX and msbX are sentinel-safe wrappers around lsb/msb for use in
// rookAttacksClassic and bishopAttacksClassic.  When the input bitboard is empty
// (no blocker in a direction) they return a fixed boundary square so that the
// XOR trick still produces the correct full ray.
func lsbX(b bitboard) square { return (b | 0x8000000000000000).lsb() }
func msbX(b bitboard) square { return (b | 1).msb() }

// rookAttacksClassic computes rook attacks from sq on occ using classical ray
// subtraction.  Used only during magic table initialisation; the hot path uses
// the precomputed lookup in rookAttacks.
func rookAttacksClassic(occ bitboard, sq square) bitboard {
	result := rookAttacksEmpty[sq]
	result ^= northOf[lsbX(northOf[sq]&occ)]
	result ^= eastOf[lsbX(eastOf[sq]&occ)]
	result ^= southOf[msbX(southOf[sq]&occ)]
	result ^= westOf[msbX(westOf[sq]&occ)]
	return result
}

// bishopAttacksClassic computes bishop attacks from sq on occ using classical ray
// subtraction.  Used only during magic table initialisation.
func bishopAttacksClassic(occ bitboard, sq square) bitboard {
	result := bishopAttacksEmpty[sq]
	result ^= northeastOf[lsbX(northeastOf[sq]&occ)]
	result ^= northwestOf[lsbX(northwestOf[sq]&occ)]
	result ^= southwestOf[msbX(southwestOf[sq]&occ)]
	result ^= southeastOf[msbX(southeastOf[sq]&occ)]
	return result
}
