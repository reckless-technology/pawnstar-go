package engine

import "math/bits"

type Bitboard uint64

const (
	NoSqs    Bitboard = 0
	AllSqs   Bitboard = ^Bitboard(0)
	Rank1    Bitboard = 0x00000000000000FF
	Rank2    Bitboard = Rank1 << 8
	Rank3    Bitboard = Rank2 << 8
	Rank4    Bitboard = Rank3 << 8
	Rank5    Bitboard = Rank4 << 8
	Rank6    Bitboard = Rank5 << 8
	Rank7    Bitboard = Rank6 << 8
	Rank8    Bitboard = Rank7 << 8
	FileA    Bitboard = 0x0101010101010101
	FileB    Bitboard = FileA << 1
	FileC    Bitboard = FileB << 1
	FileD    Bitboard = FileC << 1
	FileE    Bitboard = FileD << 1
	FileF    Bitboard = FileE << 1
	FileG    Bitboard = FileF << 1
	FileH    Bitboard = FileG << 1
	NotFileA Bitboard = ^FileA
	NotFileH Bitboard = ^FileH
	WhiteSqs Bitboard = 0x55AA55AA55AA55AA
	BlackSqs Bitboard = ^WhiteSqs
)

func SquareBB(s Square) Bitboard              { return Bitboard(1) << s }
func (b Bitboard) IsEmpty() bool              { return b == 0 }
func (b Bitboard) IsNotEmpty() bool           { return b != 0 }
func (b Bitboard) PopCount() int              { return bits.OnesCount64(uint64(b)) }
func (b Bitboard) Lsb() Square                { return Square(bits.TrailingZeros64(uint64(b))) }
func (b Bitboard) Msb() Square                { return Square(63 - bits.LeadingZeros64(uint64(b))) }
func (b Bitboard) ShiftNorth() Bitboard       { return b << 8 }
func (b Bitboard) ShiftSouth() Bitboard       { return b >> 8 }
func (b Bitboard) ShiftEast() Bitboard        { return (b & NotFileH) << 1 }
func (b Bitboard) ShiftWest() Bitboard        { return (b & NotFileA) >> 1 }
func (b Bitboard) ShiftNortheast() Bitboard   { return (b & NotFileH) << 9 }
func (b Bitboard) ShiftNorthwest() Bitboard   { return (b & NotFileA) << 7 }
func (b Bitboard) ShiftSoutheast() Bitboard   { return (b & NotFileH) >> 7 }
func (b Bitboard) ShiftSouthwest() Bitboard   { return (b & NotFileA) >> 9 }
func (b Bitboard) PopLsb() (Bitboard, Square) { s := b.Lsb(); return b & (b - 1), s }
