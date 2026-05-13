package engine

// Perft counts leaf nodes at the given depth (bulk-counting at depth 1).
func Perft(pos *Position, depth int) uint64 {
	if depth == 0 {
		return 1
	}
	moves := pos.GenerateLegalMoves()
	if depth == 1 {
		return uint64(len(moves))
	}
	var nodes uint64
	for i := 0; i < len(moves); i++ {
		pos.MakeMove(moves[i])
		nodes += Perft(pos, depth-1)
		pos.UnmakeMove()
	}
	return nodes
}
