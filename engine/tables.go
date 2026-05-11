package engine

// Precomputed attack and hash tables, initialised once at startup.
// The sliding-piece attack functions use the ray-table approach identical to
// the software fallback in attacks.h (no PEXT required).

var (
	North     [64]Bitboard
	Northeast [64]Bitboard
	East      [64]Bitboard
	Southeast [64]Bitboard
	South     [64]Bitboard
	Southwest [64]Bitboard
	West      [64]Bitboard
	Northwest [64]Bitboard

	PawnAttacksWhite [64]Bitboard
	PawnAttacksBlack [64]Bitboard
	KnightAttacks    [64]Bitboard
	BishopAttacksEmpty [64]Bitboard
	RookAttacksEmpty   [64]Bitboard
	QueenAttacksEmpty  [64]Bitboard
	KingAttacksTable   [64]Bitboard
	KingAttacks2Table  [64]Bitboard

	KingPawnShelterWhite [64]Bitboard
	KingPawnShelterBlack [64]Bitboard
	PassedPawnMaskWhite  [64]Bitboard
	PassedPawnMaskBlack  [64]Bitboard
	IsolatedPawnMask     [64]Bitboard
	SupportedPawnMaskWhite [64]Bitboard
	SupportedPawnMaskBlack [64]Bitboard
	DoubledPawnMaskWhite   [64]Bitboard
	DoubledPawnMaskBlack   [64]Bitboard

	InterveningSquares [64][64]Bitboard

	// Zobrist hashes.
	PieceSquareHashes    [2][6][64]uint64 // [color][piece-1][square]
	CastlingRightsHashes [16]uint64
	EnPassantHashes      [64]uint64
	BlackMoveHash        uint64
)

func init() {
	initRays()
	initAttacks()
	initPawnMasks()
	initInterveningSquares()
	initZobrist()
	initCastleMasks()
}

// ---- ray generation ----

type shiftFn func(Bitboard) Bitboard

var shiftFns = [8]shiftFn{
	func(b Bitboard) Bitboard { return b << 8 },                   // N
	func(b Bitboard) Bitboard { return (b & NotFileH) << 9 },      // NE
	func(b Bitboard) Bitboard { return (b & NotFileH) << 1 },      // E
	func(b Bitboard) Bitboard { return (b & NotFileH) >> 7 },      // SE
	func(b Bitboard) Bitboard { return b >> 8 },                   // S
	func(b Bitboard) Bitboard { return (b & NotFileA) >> 9 },      // SW
	func(b Bitboard) Bitboard { return (b & NotFileA) >> 1 },      // W
	func(b Bitboard) Bitboard { return (b & NotFileA) << 7 },      // NW
}

func rayFrom(sq Square, dir int) Bitboard {
	var result Bitboard
	fn := shiftFns[dir]
	for b := fn(SquareBB(sq)); b != 0; b = fn(b) {
		result |= b
	}
	return result
}

func initRays() {
	for sq := Square(0); sq < 64; sq++ {
		North[sq] = rayFrom(sq, 0)
		Northeast[sq] = rayFrom(sq, 1)
		East[sq] = rayFrom(sq, 2)
		Southeast[sq] = rayFrom(sq, 3)
		South[sq] = rayFrom(sq, 4)
		Southwest[sq] = rayFrom(sq, 5)
		West[sq] = rayFrom(sq, 6)
		Northwest[sq] = rayFrom(sq, 7)
	}
}

// ---- attack table generation ----

func kingFill(b Bitboard) Bitboard {
	return b.ShiftNorth() | b.ShiftNortheast() | b.ShiftEast() | b.ShiftSoutheast() |
		b.ShiftSouth() | b.ShiftSouthwest() | b.ShiftWest() | b.ShiftNorthwest()
}

func initAttacks() {
	knightVectors := [][2]int{{-2, -1}, {-2, 1}, {-1, -2}, {-1, 2}, {1, -2}, {1, 2}, {2, -1}, {2, 1}}
	for sq := Square(0); sq < 64; sq++ {
		file, rank := sq.File(), sq.Rank()

		// Pawn attacks
		b := SquareBB(sq)
		PawnAttacksWhite[sq] = b.ShiftNorthwest() | b.ShiftNortheast()
		PawnAttacksBlack[sq] = b.ShiftSouthwest() | b.ShiftSoutheast()

		// Knight attacks
		var ka Bitboard
		for _, v := range knightVectors {
			f, r := file+v[0], rank+v[1]
			if f >= 0 && f < 8 && r >= 0 && r < 8 {
				ka |= SquareBB(NewSquare(f, r))
			}
		}
		KnightAttacks[sq] = ka

		// Sliding piece attacks on empty board
		BishopAttacksEmpty[sq] = Northeast[sq] | Northwest[sq] | Southeast[sq] | Southwest[sq]
		RookAttacksEmpty[sq] = North[sq] | South[sq] | East[sq] | West[sq]
		QueenAttacksEmpty[sq] = BishopAttacksEmpty[sq] | RookAttacksEmpty[sq]

		// King attacks
		KingAttacksTable[sq] = kingFill(b)
		KingAttacks2Table[sq] = kingFill(kingFill(b)) &^ b
	}
}

// lsbX: LSB with sentinel at bit 63 (returns sq 63 if b is empty, whose ray is 0).
func lsbX(b Bitboard) Square { return (b | 0x8000000000000000).Lsb() }

// msbX: MSB with sentinel at bit 0 (returns sq 0 if b is empty, whose ray is 0).
func msbX(b Bitboard) Square { return (b | 1).Msb() }

// BishopAttacks returns squares attacked by a bishop on sq given occupied squares.
func BishopAttacks(occupied Bitboard, sq Square) Bitboard {
	result := BishopAttacksEmpty[sq]
	result ^= Northeast[lsbX(Northeast[sq]&occupied)]
	result ^= Northwest[lsbX(Northwest[sq]&occupied)]
	result ^= Southwest[msbX(Southwest[sq]&occupied)]
	result ^= Southeast[msbX(Southeast[sq]&occupied)]
	return result
}

// RookAttacks returns squares attacked by a rook on sq given occupied squares.
func RookAttacks(occupied Bitboard, sq Square) Bitboard {
	result := RookAttacksEmpty[sq]
	result ^= North[lsbX(North[sq]&occupied)]
	result ^= East[lsbX(East[sq]&occupied)]
	result ^= South[msbX(South[sq]&occupied)]
	result ^= West[msbX(West[sq]&occupied)]
	return result
}

// QueenAttacks returns squares attacked by a queen on sq given occupied squares.
func QueenAttacks(occupied Bitboard, sq Square) Bitboard {
	return BishopAttacks(occupied, sq) | RookAttacks(occupied, sq)
}

// ---- pawn structure masks ----

func initPawnMasks() {
	for sq := Square(0); sq < 64; sq++ {
		b := SquareBB(sq)
		file := FileA << uint(sq.File())

		// Pawn shelter
		if sq != SquareFromString("e1") {
			KingPawnShelterWhite[sq] = b.ShiftNorthwest() | b.ShiftNorth() | b.ShiftNortheast()
		}
		if sq != SquareFromString("e8") {
			KingPawnShelterBlack[sq] = b.ShiftSouthwest() | b.ShiftSouth() | b.ShiftSoutheast()
		}

		// Passed pawn masks
		fwd := North[sq]
		PassedPawnMaskWhite[sq] = fwd | fwd.ShiftWest() | fwd.ShiftEast()
		bwd := South[sq]
		PassedPawnMaskBlack[sq] = bwd | bwd.ShiftWest() | bwd.ShiftEast()

		// Isolated pawn mask (adjacent files, all ranks)
		IsolatedPawnMask[sq] = file.ShiftWest() | file.ShiftEast()

		// Supported pawn mask
		colW := (South[sq] | b).ShiftWest() | (South[sq] | b).ShiftEast()
		SupportedPawnMaskWhite[sq] = colW
		colB := (North[sq] | b).ShiftWest() | (North[sq] | b).ShiftEast()
		SupportedPawnMaskBlack[sq] = colB

		// Doubled pawn mask
		DoubledPawnMaskWhite[sq] = North[sq]
		DoubledPawnMaskBlack[sq] = South[sq]
	}
}

// ---- intervening squares ----

func initInterveningSquares() {
	dirs := []int{0, 1, 2, 3, 4, 5, 6, 7}
	for from := Square(0); from < 64; from++ {
		for to := Square(0); to < 64; to++ {
			for _, dir := range dirs {
				ray := rayFrom(from, dir)
				if ray&SquareBB(to) != 0 {
					InterveningSquares[from][to] = ray ^ rayFrom(to, dir) ^ SquareBB(to)
					break
				}
			}
		}
	}
}

// ---- Zobrist hashing ----

// xorshift64 — simple deterministic PRNG with fixed seed.
type xorshift64 struct{ state uint64 }

func (x *xorshift64) next() uint64 {
	x.state ^= x.state << 13
	x.state ^= x.state >> 7
	x.state ^= x.state << 17
	return x.state
}

func initZobrist() {
	rng := &xorshift64{state: 0xAA55AA55AA55AA55}
	for c := 0; c < 2; c++ {
		for p := 0; p < 6; p++ {
			for sq := 0; sq < 64; sq++ {
				PieceSquareHashes[c][p][sq] = rng.next()
			}
		}
	}
	for i := range CastlingRightsHashes {
		CastlingRightsHashes[i] = rng.next()
	}
	for i := range EnPassantHashes {
		EnPassantHashes[i] = rng.next()
	}
	BlackMoveHash = rng.next()
}
