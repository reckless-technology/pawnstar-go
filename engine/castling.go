package engine

type CastlingRights uint8

const (
	CastleWhiteKingside  CastlingRights = 0x01
	CastleWhiteQueenside CastlingRights = 0x02
	CastleBlackKingside  CastlingRights = 0x04
	CastleBlackQueenside CastlingRights = 0x08
)

func (cr CastlingRights) MayWhiteCastleKingside() bool  { return cr&CastleWhiteKingside != 0 }
func (cr CastlingRights) MayWhiteCastleQueenside() bool { return cr&CastleWhiteQueenside != 0 }
func (cr CastlingRights) MayBlackCastleKingside() bool  { return cr&CastleBlackKingside != 0 }
func (cr CastlingRights) MayBlackCastleQueenside() bool { return cr&CastleBlackQueenside != 0 }
func (cr CastlingRights) IsNone() bool                  { return cr == 0 }
func (cr CastlingRights) Value() uint8                  { return uint8(cr) }

// AfterMove returns the castling rights that result after making a move.
func (cr CastlingRights) AfterMove(m Move) CastlingRights {
	return cr & castleMoveMasks[m.From()] & castleMoveMasks[m.To()]
}

func CastlingRightsFromFen(fen string) CastlingRights {
	if fen == "-" {
		return 0
	}
	var cr CastlingRights
	for _, c := range fen {
		switch c {
		case 'K':
			cr |= CastleWhiteKingside
		case 'Q':
			cr |= CastleWhiteQueenside
		case 'k':
			cr |= CastleBlackKingside
		case 'q':
			cr |= CastleBlackQueenside
		}
	}
	return cr
}

func (cr CastlingRights) ToFenString() string {
	if cr == 0 {
		return "-"
	}
	s := ""
	if cr&CastleWhiteKingside != 0 {
		s += "K"
	}
	if cr&CastleWhiteQueenside != 0 {
		s += "Q"
	}
	if cr&CastleBlackKingside != 0 {
		s += "k"
	}
	if cr&CastleBlackQueenside != 0 {
		s += "q"
	}
	return s
}

// per-square AND masks: applied to castling rights when a piece moves from or to that square.
var castleMoveMasks [64]CastlingRights

func initCastleMasks() {
	okm := CastlingRights(0x0F)
	a1m := okm & ^CastleWhiteQueenside
	e1m := okm & ^(CastleWhiteKingside | CastleWhiteQueenside)
	h1m := okm & ^CastleWhiteKingside
	a8m := okm & ^CastleBlackQueenside
	e8m := okm & ^(CastleBlackKingside | CastleBlackQueenside)
	h8m := okm & ^CastleBlackKingside
	for i := range castleMoveMasks {
		castleMoveMasks[i] = okm
	}
	castleMoveMasks[a1] = a1m
	castleMoveMasks[e1] = e1m
	castleMoveMasks[h1] = h1m
	castleMoveMasks[a8] = a8m
	castleMoveMasks[e8] = e8m
	castleMoveMasks[h8] = h8m
}
