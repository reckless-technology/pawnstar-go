package engine

import "math/bits"

type bitboard uint64

const (
	rank1    bitboard = 0x00000000000000FF
	rank2    bitboard = rank1 << 8
	rank3    bitboard = rank2 << 8
	rank4    bitboard = rank3 << 8
	rank5    bitboard = rank4 << 8
	rank6    bitboard = rank5 << 8
	rank7    bitboard = rank6 << 8
	rank8    bitboard = rank7 << 8
	fileA    bitboard = 0x0101010101010101
	fileB    bitboard = fileA << 1
	fileC    bitboard = fileB << 1
	fileD    bitboard = fileC << 1
	fileE    bitboard = fileD << 1
	fileF    bitboard = fileE << 1
	fileG    bitboard = fileF << 1
	fileH    bitboard = fileG << 1
	notFileA bitboard = ^fileA
	notFileH bitboard = ^fileH
	whiteSqs bitboard = 0x55AA55AA55AA55AA
	blackSqs bitboard = ^whiteSqs
)

func squareBB(s square) bitboard            { return bitboard(1) << s }
func (b bitboard) isEmpty() bool            { return b == 0 }
func (b bitboard) isNotEmpty() bool         { return b != 0 }
func (b bitboard) popCount() int            { return bits.OnesCount64(uint64(b)) }
func (b bitboard) lsb() square              { return square(bits.TrailingZeros64(uint64(b))) }
func (b bitboard) msb() square              { return square(63 - bits.LeadingZeros64(uint64(b))) }
func (b bitboard) shiftNorth() bitboard     { return b << 8 }
func (b bitboard) shiftSouth() bitboard     { return b >> 8 }
func (b bitboard) shiftEast() bitboard      { return (b & notFileH) << 1 }
func (b bitboard) shiftWest() bitboard      { return (b & notFileA) >> 1 }
func (b bitboard) shiftNortheast() bitboard { return (b & notFileH) << 9 }
func (b bitboard) shiftNorthwest() bitboard { return (b & notFileA) << 7 }
func (b bitboard) shiftSoutheast() bitboard { return (b & notFileH) >> 7 }
func (b bitboard) shiftSouthwest() bitboard { return (b & notFileA) >> 9 }
func (b *bitboard) popLsb() square          { s := b.lsb(); *b &= (*b - 1); return s }
