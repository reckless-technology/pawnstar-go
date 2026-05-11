package engine

import "math/bits"

// Bitboard is a 64-bit set of squares.
type Bitboard uint64

const (
	BBEmpty    Bitboard = 0
	BBAll      Bitboard = ^Bitboard(0)
	Rank1      Bitboard = 0x00000000000000FF
	Rank2      Bitboard = 0x000000000000FF00
	Rank4      Bitboard = 0x00000000FF000000
	Rank5      Bitboard = 0x000000FF00000000
	Rank7      Bitboard = 0x00FF000000000000
	Rank8      Bitboard = 0xFF00000000000000
	FileA      Bitboard = 0x0101010101010101
	FileB      Bitboard = FileA << 1
	FileC      Bitboard = FileA << 2
	FileD      Bitboard = FileA << 3
	FileE      Bitboard = FileA << 4
	FileF      Bitboard = FileA << 5
	FileG      Bitboard = FileA << 6
	FileH      Bitboard = 0x8080808080808080
	NotFileA   Bitboard = ^FileA
	NotFileH   Bitboard = ^FileH
	WhiteSqs   Bitboard = 0x55AA55AA55AA55AA
	BlackSqs   Bitboard = ^WhiteSqs
)

func SquareBB(s Square) Bitboard       { return Bitboard(1) << s }
func (b Bitboard) IsEmpty() bool       { return b == 0 }
func (b Bitboard) IsNotEmpty() bool    { return b != 0 }
func (b Bitboard) PopCount() int       { return bits.OnesCount64(uint64(b)) }
func (b Bitboard) Lsb() Square        { return Square(bits.TrailingZeros64(uint64(b))) }
func (b Bitboard) Msb() Square        { return Square(63 - bits.LeadingZeros64(uint64(b))) }
func (b Bitboard) PopLsb() (Bitboard, Square) {
	s := b.Lsb()
	return b & (b - 1), s
}

func (b Bitboard) ShiftNorth() Bitboard     { return b << 8 }
func (b Bitboard) ShiftSouth() Bitboard     { return b >> 8 }
func (b Bitboard) ShiftEast() Bitboard      { return (b & NotFileH) << 1 }
func (b Bitboard) ShiftWest() Bitboard      { return (b & NotFileA) >> 1 }
func (b Bitboard) ShiftNortheast() Bitboard { return (b & NotFileH) << 9 }
func (b Bitboard) ShiftNorthwest() Bitboard { return (b & NotFileA) << 7 }
func (b Bitboard) ShiftSoutheast() Bitboard { return (b & NotFileH) >> 7 }
func (b Bitboard) ShiftSouthwest() Bitboard { return (b & NotFileA) >> 9 }

// Squares iterates over set bits, yielding each square.
func (b Bitboard) Squares() []Square {
	out := make([]Square, 0, b.PopCount())
	for b != 0 {
		var s Square
		b, s = b.PopLsb()
		out = append(out, s)
	}
	return out
}
