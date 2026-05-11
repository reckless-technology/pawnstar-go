package engine

// Piece represents a chess piece type.
type Piece uint8

const (
	NoPiece Piece = 0
	Pawn    Piece = 1
	Knight  Piece = 2
	Bishop  Piece = 3
	Rook    Piece = 4
	Queen   Piece = 5
	King    Piece = 6
)
