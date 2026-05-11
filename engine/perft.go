package engine

// Perft counts leaf nodes at the given depth (bulk-counting at depth 1).
func Perft(pos Position, depth int) uint64 {
	if depth == 0 {
		return 1
	}
	moves := pos.GenerateLegalMoves()
	if depth == 1 {
		return uint64(moves.Len())
	}
	var nodes uint64
	for i := 0; i < moves.Len(); i++ {
		next := pos.MakeMove(moves.At(i))
		nodes += Perft(next, depth-1)
	}
	return nodes
}
