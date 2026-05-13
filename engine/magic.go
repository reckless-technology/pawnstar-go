package engine

// BishopAttacks returns squares attacked by a bishop on sq given occupied squares.
func BishopAttacks(occ Bitboard, sq Square) Bitboard {
	return bishopTable[sq][(uint64(occ&bishopMask[sq])*bishopMagic[sq])>>bishopShift[sq]]
}

// RookAttacks returns squares attacked by a rook on sq given occupied squares.
func RookAttacks(occ Bitboard, sq Square) Bitboard {
	return rookTable[sq][(uint64(occ&rookMask[sq])*rookMagic[sq])>>rookShift[sq]]
}

// QueenAttacks returns squares attacked by a queen on sq given occupied squares.
func QueenAttacks(occupied Bitboard, sq Square) Bitboard {
	return BishopAttacks(occupied, sq) | RookAttacks(occupied, sq)
}

var (
	rookMask  [64]Bitboard
	rookShift [64]uint
	rookTable [64][]Bitboard

	bishopMask  [64]Bitboard
	bishopShift [64]uint
	bishopTable [64][]Bitboard
)

// Magic numbers found offline with seed 1337. It was taking over 400ms to compute them at startup.
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

func initMagics() {
	var occupancies [4096]Bitboard
	var target_squares [4096]Bitboard

	for sq := range Square(64) {
		mask := (NorthOf[sq] & ^Rank8) |
			(SouthOf[sq] & ^Rank1) |
			(EastOf[sq] & ^FileH) |
			(WestOf[sq] & ^FileA)
		rookMask[sq] = mask
		rookShift[sq] = uint(64 - mask.PopCount())
		rookTable[sq] = make([]Bitboard, 1<<mask.PopCount())
		n := enumerateOccupancies(mask, occupancies[:], target_squares[:], rookAttacksClassic, sq)
		fillMagicTable(rookMagic[sq], occupancies[:n], target_squares[:n], rookShift[sq], rookTable[sq])
	}

	for sq := range Square(64) {
		mask := (NortheastOf[sq] & ^(Rank8 | FileH)) |
			(NorthwestOf[sq] & ^(Rank8 | FileA)) |
			(SoutheastOf[sq] & ^(Rank1 | FileH)) |
			(SouthwestOf[sq] & ^(Rank1 | FileA))
		bishopMask[sq] = mask
		bishopShift[sq] = uint(64 - mask.PopCount())
		bishopTable[sq] = make([]Bitboard, 1<<mask.PopCount())

		n := enumerateOccupancies(mask, occupancies[:], target_squares[:], bishopAttacksClassic, sq)
		fillMagicTable(bishopMagic[sq], occupancies[:n], target_squares[:n], bishopShift[sq], bishopTable[sq])
	}
}

func fillMagicTable(magic uint64, occs, refs []Bitboard, shift uint, table []Bitboard) {
	for i, occ := range occs {
		table[(uint64(occ)*magic)>>shift] = refs[i]
	}
}

func enumerateOccupancies(mask Bitboard, occupancies, targets []Bitboard, attackFn func(Bitboard, Square) Bitboard, sq Square) int {
	n := 0
	occupancy := Bitboard(0)
	for {
		occupancies[n] = occupancy
		targets[n] = attackFn(occupancy, sq)
		n++
		occupancy = (occupancy - 1) & mask
		if occupancy == 0 {
			break
		}
	}
	return n
}

func lsbX(b Bitboard) Square { return (b | 0x8000000000000000).Lsb() }
func msbX(b Bitboard) Square { return (b | 1).Msb() }

func rookAttacksClassic(occ Bitboard, sq Square) Bitboard {
	result := RookAttacksEmpty[sq]
	result ^= NorthOf[lsbX(NorthOf[sq]&occ)]
	result ^= EastOf[lsbX(EastOf[sq]&occ)]
	result ^= SouthOf[msbX(SouthOf[sq]&occ)]
	result ^= WestOf[msbX(WestOf[sq]&occ)]
	return result
}

func bishopAttacksClassic(occ Bitboard, sq Square) Bitboard {
	result := BishopAttacksEmpty[sq]
	result ^= NortheastOf[lsbX(NortheastOf[sq]&occ)]
	result ^= NorthwestOf[lsbX(NorthwestOf[sq]&occ)]
	result ^= SouthwestOf[msbX(SouthwestOf[sq]&occ)]
	result ^= SoutheastOf[msbX(SoutheastOf[sq]&occ)]
	return result
}
