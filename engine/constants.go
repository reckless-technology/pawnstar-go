package engine

const (
	Beta                   = 11000
	Alpha                  = -11000
	CheckmatedScore        = -10000
	WinThreshold           = 9000
	LoseThreshold          = -9000
	DrawScore              = 0
	MaxPly                 = 64
	RankFlip               = 0x38
	SearchCancelledScore   = -12345678
	ScoreInstabilityThresh = 50
	StartDepth             = 3
	HashTableMB            = 64
	QHashTableMB           = 8
)

type Direction uint8

const (
	North Direction = iota
	Northeast
	East
	Southeast
	South
	Southwest
	West
	Northwest
)

type Color uint8

const (
	White Color = iota
	Black
)

func EnemyOf(c Color) Color {
	return c ^ 1
}

type Piece uint8

const (
	NoPiece Piece = iota
	Pawn
	Knight
	Bishop
	Rook
	Queen
	King
)
