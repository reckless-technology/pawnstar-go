package engine

// bishopAttacks returns the squares attacked by a bishop on sq given the occupied squares.
func bishopAttacks(occ bitboard, sq square) bitboard {
	return bishopTable[sq][(uint64(occ&bishopMask[sq])*bishopMagic[sq])>>bishopShift[sq]]
}

// rookAttacks returns the squares attacked by a rook on sq given the occupied squares.
func rookAttacks(occ bitboard, sq square) bitboard {
	return rookTable[sq][(uint64(occ&rookMask[sq])*rookMagic[sq])>>rookShift[sq]]
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
// corresponding attack set (from attackFn) in targets, and returns n.
func enumerateOccupancies(mask bitboard, occupancies, targets []bitboard, attackFn func(bitboard, square) bitboard, sq square) int {
	count := 0
	occupancy := bitboard(0)
	for {
		occupancies[count] = occupancy
		targets[count] = attackFn(occupancy, sq)
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
