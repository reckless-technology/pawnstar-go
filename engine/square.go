package engine

// square identifies a board square as an integer in [0, 63].
// a1 = 0, h1 = 7, a2 = 8, …, h8 = 63 (same encoding as the move bit layout).
type square uint8

// squareXY constructs a square from file x (0 = a … 7 = h) and rank y (0 = rank 1 … 7 = rank 8).
func squareXY(x, y int) square { return square(x + (8 * y)) }
func (s square) file() uint8   { return uint8(s) & 7 }
func (s square) rank() uint8   { return uint8(s) >> 3 }

// String returns the square name in algebraic notation, e.g. "e4".
func (s square) String() string { return string([]byte{'a' + byte(s.file()), '1' + byte(s.rank())}) }

// squareFromString parses an algebraic square name such as "e4".
func squareFromString(str string) square {
	f := int(str[0]|0x20) - 'a'
	r := int(str[1]) - '1'
	return squareXY(f, r)
}

// noSquare is the null sentinel for optional square fields (e.g. the en-passant target
// square when no double pawn push was just played).  Its numeric value (0 = a1) never
// arises in those contexts.
const noSquare square = 0

const (
	a1 square = iota
	b1
	c1
	d1
	e1
	f1
	g1
	h1
	a2
	b2
	c2
	d2
	e2
	f2
	g2
	h2
	a3
	b3
	c3
	d3
	e3
	f3
	g3
	h3
	a4
	b4
	c4
	d4
	e4
	f4
	g4
	h4
	a5
	b5
	c5
	d5
	e5
	f5
	g5
	h5
	a6
	b6
	c6
	d6
	e6
	f6
	g6
	h6
	a7
	b7
	c7
	d7
	e7
	f7
	g7
	h7
	a8
	b8
	c8
	d8
	e8
	f8
	g8
	h8
)
