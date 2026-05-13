package engine

import "math/rand"

var (
	NorthOf                [64]Bitboard
	NortheastOf            [64]Bitboard
	EastOf                 [64]Bitboard
	SoutheastOf            [64]Bitboard
	SouthOf                [64]Bitboard
	SouthwestOf            [64]Bitboard
	WestOf                 [64]Bitboard
	NorthwestOf            [64]Bitboard
	PawnAttacksWhite       [64]Bitboard
	PawnAttacksBlack       [64]Bitboard
	KnightAttacks          [64]Bitboard
	BishopAttacksEmpty     [64]Bitboard
	RookAttacksEmpty       [64]Bitboard
	QueenAttacksEmpty      [64]Bitboard
	KingAttacks            [64]Bitboard
	KingAttacks2           [64]Bitboard
	KingPawnShelterWhite   [64]Bitboard
	KingPawnShelterBlack   [64]Bitboard
	PassedPawnMaskWhite    [64]Bitboard
	PassedPawnMaskBlack    [64]Bitboard
	IsolatedPawnMask       [64]Bitboard
	SupportedPawnMaskWhite [64]Bitboard
	SupportedPawnMaskBlack [64]Bitboard
	DoubledPawnMaskWhite   [64]Bitboard
	DoubledPawnMaskBlack   [64]Bitboard
	InterveningSquares     [64][64]Bitboard
	PieceSquareHashes      [2][6][64]uint64 // [color][piece-1][square]
	CastlingRightsHashes   [16]uint64
	EnPassantHashes        [64]uint64
	BlackMoveHash          uint64
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

type shiftFn func(Bitboard) Bitboard

var shiftFns = map[Direction]shiftFn{
	North:     Bitboard.ShiftNorth,
	Northeast: Bitboard.ShiftNortheast,
	East:      Bitboard.ShiftEast,
	Southeast: Bitboard.ShiftSoutheast,
	South:     Bitboard.ShiftSouth,
	Southwest: Bitboard.ShiftSouthwest,
	West:      Bitboard.ShiftWest,
	Northwest: Bitboard.ShiftNorthwest,
}

func rayFrom(sq Square, dir Direction) Bitboard {
	var result Bitboard
	fn := shiftFns[dir]
	for b := fn(SquareBB(sq)); b != 0; b = fn(b) {
		result |= b
	}
	return result
}

func initRays() {
	var sq Square
	for sq = range 64 {
		NorthOf[sq] = rayFrom(sq, North)
		NortheastOf[sq] = rayFrom(sq, Northeast)
		EastOf[sq] = rayFrom(sq, East)
		SoutheastOf[sq] = rayFrom(sq, Southeast)
		SouthOf[sq] = rayFrom(sq, South)
		SouthwestOf[sq] = rayFrom(sq, Southwest)
		WestOf[sq] = rayFrom(sq, West)
		NorthwestOf[sq] = rayFrom(sq, Northwest)
	}
}

func kingFill(b Bitboard) Bitboard {
	return b.ShiftNorth() |
		b.ShiftNortheast() |
		b.ShiftEast() |
		b.ShiftSoutheast() |
		b.ShiftSouth() |
		b.ShiftSouthwest() |
		b.ShiftWest() |
		b.ShiftNorthwest()
}

func initAttacks() {
	knightVectors := [8][2]int{{-2, -1}, {-2, 1}, {-1, -2}, {-1, 2}, {1, -2}, {1, 2}, {2, -1}, {2, 1}}
	for sq := range Square(64) {
		file, rank := sq.File(), sq.Rank()

		// Pawn attacks
		b := SquareBB(sq)
		PawnAttacksWhite[sq] = b.ShiftNorthwest() | b.ShiftNortheast()
		PawnAttacksBlack[sq] = b.ShiftSouthwest() | b.ShiftSoutheast()

		// Knight attacks
		var ka Bitboard
		for _, v := range knightVectors {
			f, r := int(file)+v[0], int(rank)+v[1]
			if f >= 0 && f < 8 && r >= 0 && r < 8 {
				ka |= SquareBB(SquareXY(f, r))
			}
		}
		KnightAttacks[sq] = ka

		// Sliding piece attacks on empty board
		BishopAttacksEmpty[sq] = NortheastOf[sq] | NorthwestOf[sq] | SoutheastOf[sq] | SouthwestOf[sq]
		RookAttacksEmpty[sq] = NorthOf[sq] | SouthOf[sq] | EastOf[sq] | WestOf[sq]
		QueenAttacksEmpty[sq] = BishopAttacksEmpty[sq] | RookAttacksEmpty[sq]

		// King attacks
		KingAttacks[sq] = kingFill(b)
		KingAttacks2[sq] = kingFill(kingFill(b)) & ^b
	}
}

func initPawnMasks() {
	for sq := Square(0); sq < 64; sq++ {
		b := SquareBB(sq)
		file := FileA << uint(sq.File())

		// Pawn shelter
		if sq != e1 {
			KingPawnShelterWhite[sq] = b.ShiftNorthwest() | b.ShiftNorth() | b.ShiftNortheast()
		}
		if sq != e8 {
			KingPawnShelterBlack[sq] = b.ShiftSouthwest() | b.ShiftSouth() | b.ShiftSoutheast()
		}

		// Passed pawn masks
		fwd := NorthOf[sq]
		PassedPawnMaskWhite[sq] = fwd | fwd.ShiftWest() | fwd.ShiftEast()
		bwd := SouthOf[sq]
		PassedPawnMaskBlack[sq] = bwd | bwd.ShiftWest() | bwd.ShiftEast()

		// Isolated pawn mask (adjacent files, all ranks)
		IsolatedPawnMask[sq] = file.ShiftWest() | file.ShiftEast()

		// Supported pawn mask
		colW := (SouthOf[sq] | b).ShiftWest() | (SouthOf[sq] | b).ShiftEast()
		SupportedPawnMaskWhite[sq] = colW
		colB := (NorthOf[sq] | b).ShiftWest() | (NorthOf[sq] | b).ShiftEast()
		SupportedPawnMaskBlack[sq] = colB

		// Doubled pawn mask
		DoubledPawnMaskWhite[sq] = NorthOf[sq]
		DoubledPawnMaskBlack[sq] = SouthOf[sq]
	}
}

// ---- intervening squares ----

func initInterveningSquares() {
	for from := range Square(64) {
		for to := range Square(64) {
			for dir := range Direction(8) {
				ray := rayFrom(from, dir)
				if (ray & SquareBB(to)) != 0 {
					InterveningSquares[from][to] = ray ^ rayFrom(to, dir) ^ SquareBB(to)
					break
				}
			}
		}
	}
}

// ---- Zobrist hashing ----

func initZobrist() {
	for c := range 2 {
		for p := range 6 {
			for sq := range 64 {
				PieceSquareHashes[c][p][sq] = rand.Uint64()
			}
		}
	}
	for i := range CastlingRightsHashes {
		CastlingRightsHashes[i] = rand.Uint64()
	}
	for i := range Square(64) {
		if i.Rank() == 2 || i.Rank() == 5 {
			EnPassantHashes[i] = rand.Uint64()
		}
	}
	BlackMoveHash = rand.Uint64()
}
