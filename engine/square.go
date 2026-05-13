package engine

type Square uint8

func SquareXY(x, y int) Square  { return Square(x + (8 * y)) }
func (s Square) File() uint8    { return (uint8(s) & 7) }
func (s Square) Rank() uint8    { return (uint8(s) >> 3) }
func (s Square) String() string { return string([]byte{'a' + byte(s.File()), '1' + byte(s.Rank())}) }

func SquareFromString(str string) Square {
	file := int(str[0]|0x20) - 'a'
	rank := int(str[1]) - '1'
	return SquareXY(file, rank)
}

const NoSquare Square = 0

const (
	a1 Square = iota
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
