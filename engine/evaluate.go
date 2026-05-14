package engine

// Piece-square tables from https://www.chessprogramming.org/Simplified_Evaluation_Function
// Stored with wiki rank 8 at low indices, rank 1 at high indices (index 0 = a1 convention).
// Black reads the table directly; White reads via sq^RankFlip (0x38).

var pawnSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	50, 50, 50, 50, 50, 50, 50, 50,
	10, 10, 20, 30, 30, 20, 10, 10,
	5, 5, 10, 25, 25, 10, 5, 5,
	0, 0, 0, 20, 20, 0, 0, 0,
	5, -5, -10, 0, 0, -10, -5, 5,
	5, 10, 10, -20, -20, 10, 10, 5,
	0, 0, 0, 0, 0, 0, 0, 0,
}

var knightSquare = [64]int{
	-50, -40, -30, -30, -30, -30, -40, -50,
	-40, -20, 0, 0, 0, 0, -20, -40,
	-30, 0, 10, 15, 15, 10, 0, -30,
	-30, 5, 15, 20, 20, 15, 5, -30,
	-30, 0, 15, 20, 20, 15, 0, -30,
	-30, 5, 10, 15, 15, 10, 5, -30,
	-40, -20, 0, 5, 5, 0, -20, -40,
	-50, -40, -30, -30, -30, -30, -40, -50,
}

var bishopSquare = [64]int{
	-20, -10, -10, -10, -10, -10, -10, -20,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 5, 5, 10, 10, 5, 5, -10,
	-10, 0, 10, 10, 10, 10, 0, -10,
	-10, 10, 10, 10, 10, 10, 10, -10,
	-10, 5, 0, 0, 0, 0, 5, -10,
	-20, -10, -10, -10, -10, -10, -10, -20,
}

var rookSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	5, 10, 10, 10, 10, 10, 10, 5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	0, 0, 0, 5, 5, 0, 0, 0,
}

var queenSquare = [64]int{
	-20, -10, -10, -5, -5, -10, -10, -20,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-5, 0, 5, 5, 5, 5, 0, -5,
	0, 0, 5, 5, 5, 5, 0, -5,
	-10, 5, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 0, 0, 0, 0, -10,
	-20, -10, -10, -5, -5, -10, -10, -20,
}

var kingSqMidgame = [64]int{
	-30, -40, -40, -50, -50, -40, -40, -30,
	-30, -40, -40, -50, -50, -40, -40, -30,
	-30, -40, -40, -50, -50, -40, -40, -30,
	-30, -40, -40, -50, -50, -40, -40, -30,
	-20, -30, -30, -40, -40, -30, -30, -20,
	-10, -20, -20, -20, -20, -20, -20, -10,
	20, 20, 0, 0, 0, 0, 20, 20,
	20, 30, 10, 0, 0, 10, 30, 20,
}

var kingSqEndgame = [64]int{
	-50, -40, -30, -20, -20, -30, -40, -50,
	-30, -20, -10, 0, 0, -10, -20, -30,
	-30, -10, 20, 30, 30, 20, -10, -30,
	-30, -10, 30, 40, 40, 30, -10, -30,
	-30, -10, 30, 40, 40, 30, -10, -30,
	-30, -10, 20, 30, 30, 20, -10, -30,
	-30, -30, 0, 0, 0, 0, -30, -30,
	-50, -30, -30, -30, -30, -30, -30, -50,
}

// isEndgame returns true when neither side has a queen, or each side that has
// a queen has at most one additional minor piece and no rooks.
func isEndgame(pos *Position) bool {
	for c := White; c <= Black; c++ {
		friendlyPieces := pos.ColorBB(c)
		if (pos.Queens() & friendlyPieces).PopCount() == 0 {
			continue
		}
		if (pos.Rooks()&friendlyPieces).PopCount() > 0 ||
			((pos.Knights()|pos.Bishops())&friendlyPieces).PopCount() > 1 {
			return false
		}
	}
	return true
}

var flips = [2]Square{RankFlip, 0}

func evaluateSide(position *Position, color Color, isEndgame bool) int {
	friendlyPieces := position.ColorBB(color)
	rankFlip := flips[color]
	score := 0
	for b := position.Pawns() & friendlyPieces; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += 100 + pawnSquare[sq^rankFlip]
	}
	for b := position.Knights() & friendlyPieces; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += 320 + knightSquare[sq^rankFlip]
	}
	for b := position.Bishops() & friendlyPieces; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += 330 + bishopSquare[sq^rankFlip]
	}
	for b := position.Rooks() & friendlyPieces; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += 500 + rookSquare[sq^rankFlip]
	}
	for b := position.Queens() & friendlyPieces; b != 0; {
		var sq Square
		b, sq = b.PopLsb()
		score += 900 + queenSquare[sq^rankFlip]
	}
	kingSq := position.KingLocation(color) ^ rankFlip
	if isEndgame {
		score += kingSqEndgame[kingSq]
	} else {
		score += kingSqMidgame[kingSq]
	}
	return score
}

// evaluatePosition scores the position from the side-to-move's perspective.
func evaluatePosition(game *Game, _, _ int) int {
	if !game.CurrentPosition().IsLegal() {
		panic(game.CurrentPosition().ToFen())
	}
	position := game.CurrentPosition()
	if position.IsDrawByMaterial() || game.IsDrawByFiftyMoves() || game.IsDrawByRepetition() {
		return DrawScore
	}
	endgame := isEndgame(position)
	white := evaluateSide(position, White, endgame)
	black := evaluateSide(position, Black, endgame)
	if position.ColorToMove() == White {
		return white - black
	}
	return black - white
}
