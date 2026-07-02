package engine

import "math/bits"

// bitboard is a 64-bit integer where each bit represents one square.
// Bit 0 = a1, bit 7 = h1, bit 8 = a2, …, bit 63 = h8
// (little-endian rank mapping: files A–H occupy bits 0–7 within each rank).
type bitboard uint64

// Rank and file masks used throughout move generation and evaluation.
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
	whiteSqs bitboard = 0x55AA55AA55AA55AA // light squares
	blackSqs bitboard = ^whiteSqs          // dark squares
)

// squareBB returns a bitboard with only sq's bit set.
func squareBB(s square) bitboard { return bitboard(1) << s }

// isEmpty reports whether the bitboard has no bits set.
func (b bitboard) isEmpty() bool { return b == 0 }

// isNotEmpty reports whether the bitboard has at least one bit set.
func (b bitboard) isNotEmpty() bool { return b != 0 }

// popCount returns the number of set bits (squares occupied).
func (b bitboard) popCount() int { return bits.OnesCount64(uint64(b)) }

// lsb returns the index of the least-significant set bit (lowest square number).
func (b bitboard) lsb() square { return square(bits.TrailingZeros64(uint64(b))) }

// msb returns the index of the most-significant set bit (highest square number).
func (b bitboard) msb() square { return square(63 - bits.LeadingZeros64(uint64(b))) }

// shiftNorth shifts every set square one rank up (toward rank 8).
func (b bitboard) shiftNorth() bitboard { return b << 8 }

// shiftSouth shifts every set square one rank down (toward rank 1).
func (b bitboard) shiftSouth() bitboard { return b >> 8 }

// shiftEast shifts every set square one file right, masking off the h-file first so squares never wrap.
func (b bitboard) shiftEast() bitboard { return (b & notFileH) << 1 }

// shiftWest shifts every set square one file left, masking off the a-file first so squares never wrap.
func (b bitboard) shiftWest() bitboard { return (b & notFileA) >> 1 }

// shiftNortheast shifts every set square one rank up and one file right (wrap-safe).
func (b bitboard) shiftNortheast() bitboard { return (b & notFileH) << 9 }

// shiftNorthwest shifts every set square one rank up and one file left (wrap-safe).
func (b bitboard) shiftNorthwest() bitboard { return (b & notFileA) << 7 }

// shiftSoutheast shifts every set square one rank down and one file right (wrap-safe).
func (b bitboard) shiftSoutheast() bitboard { return (b & notFileH) >> 7 }

// shiftSouthwest shifts every set square one rank down and one file left (wrap-safe).
func (b bitboard) shiftSouthwest() bitboard { return (b & notFileA) >> 9 }

// popLsb clears the least-significant set bit and returns its square index.
func (b *bitboard) popLsb() square { s := b.lsb(); *b &= (*b - 1); return s }
