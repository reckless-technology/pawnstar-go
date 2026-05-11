package engine

// Square is a board square index in LERF order (a1=0 … h8=63).
type Square uint8

func NewSquare(file, rank int) Square { return Square(file + 8*rank) }

func (s Square) File() int { return int(s) & 7 }
func (s Square) Rank() int { return int(s) >> 3 }

func (s Square) String() string {
	return string([]byte{'a' + byte(s.File()), '1' + byte(s.Rank())})
}

func SquareFromString(str string) Square {
	file := int(str[0]|0x20) - 'a'
	rank := int(str[1]) - '1'
	return NewSquare(file, rank)
}

const NoSquare Square = 0
