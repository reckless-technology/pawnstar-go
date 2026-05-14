package engine

const (
	initialBeta            = 11000
	initialAlpha           = -11000
	checkmatedScore        = -10000
	winThreshold           = 9000
	loseThreshold          = -9000
	drawScore              = 0
	maxPly                 = 64
	rankFlip               = 0x38
	searchCancelledScore   = -12345678
	scoreInstabilityThresh = 50
	startDepth             = 3
	hashTableMB            = 64
)

type direction uint8

const (
	north     direction = iota
	northeast direction = iota
	east      direction = iota
	southeast direction = iota
	south     direction = iota
	southwest direction = iota
	west      direction = iota
	northwest direction = iota
)

type color uint8

const (
	white color = iota
	black color = iota
)

func enemyOf(c color) color {
	return c ^ 1
}

type piece uint8

const (
	noPiece piece = iota
	pawn    piece = iota
	knight  piece = iota
	bishop  piece = iota
	rook    piece = iota
	queen   piece = iota
	king    piece = iota
)

func (p piece) toString() string {
	return string("-PNBRQK"[p])
}

type clockType uint8

const (
	clockStandard   clockType = iota // Time-per-game with remaining time.
	clockFixedDepth                  // Search to a fixed depth.
	clockFixedTime                   // Search for a fixed number of ms.
	clockInfinite                    // Search until stop.
)
