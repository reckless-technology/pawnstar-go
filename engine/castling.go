package engine

type castlingRights uint8

const (
	castleWhiteKingside  castlingRights = 0x01
	castleWhiteQueenside castlingRights = 0x02
	castleBlackKingside  castlingRights = 0x04
	castleBlackQueenside castlingRights = 0x08
)

func (cr castlingRights) mayWhiteCastleKingside() bool  { return cr&castleWhiteKingside != 0 }
func (cr castlingRights) mayWhiteCastleQueenside() bool { return cr&castleWhiteQueenside != 0 }
func (cr castlingRights) mayBlackCastleKingside() bool  { return cr&castleBlackKingside != 0 }
func (cr castlingRights) mayBlackCastleQueenside() bool { return cr&castleBlackQueenside != 0 }
func (cr castlingRights) isNone() bool                  { return cr == 0 }
func (cr castlingRights) hash() uint64                  { return castlingRightsHashes[cr] }

func (cr castlingRights) afterMove(m move) castlingRights {
	return cr & castleMoveMasks[m.from()] & castleMoveMasks[m.to()]
}

func castlingRightsFromFen(fen string) castlingRights {
	if fen == "-" {
		return 0
	}
	var cr castlingRights
	for _, c := range fen {
		switch c {
		case 'K':
			cr |= castleWhiteKingside
		case 'Q':
			cr |= castleWhiteQueenside
		case 'k':
			cr |= castleBlackKingside
		case 'q':
			cr |= castleBlackQueenside
		}
	}
	return cr
}

func (cr castlingRights) toFenString() string {
	if cr == 0 {
		return "-"
	}
	s := ""
	if cr&castleWhiteKingside != 0 {
		s += "K"
	}
	if cr&castleWhiteQueenside != 0 {
		s += "Q"
	}
	if cr&castleBlackKingside != 0 {
		s += "k"
	}
	if cr&castleBlackQueenside != 0 {
		s += "q"
	}
	return s
}

// per-square AND masks: applied to castling rights when a piece moves from or to that square.
var castleMoveMasks [64]castlingRights

func initCastleMasks() {
	okm := castlingRights(0x0F)
	a1m := okm & ^castleWhiteQueenside
	e1m := okm & ^(castleWhiteKingside | castleWhiteQueenside)
	h1m := okm & ^castleWhiteKingside
	a8m := okm & ^castleBlackQueenside
	e8m := okm & ^(castleBlackKingside | castleBlackQueenside)
	h8m := okm & ^castleBlackKingside
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
