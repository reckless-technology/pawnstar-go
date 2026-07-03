package engine

// historyTable counts how often each (ply, from→to) pair raised alpha or caused a
// beta cutoff.  The count is added to a move's sort score so that quiet moves that
// have been good at the same ply in earlier iterations are tried sooner.
// Indexed by ply and move.fromTo(), a 12-bit encoding of the from and to squares.
type historyTable struct {
	// counts are uint32, not uint: at one increment per alpha-raise/cutoff a search cannot
	// approach 2^32, and halving the element size halves the table to 1 MiB per search thread.
	counts [maxPly][4096]uint32
}

// recordGoodMove increments the counter for m at ply, indicating that m raised alpha
// or caused a beta cutoff at that ply.
func (h *historyTable) recordGoodMove(ply int, m move) {
	h.counts[ply][m.fromTo()]++
}

// count returns the accumulated count for m at ply.
func (h *historyTable) count(ply int, m move) uint32 {
	return h.counts[ply][m.fromTo()]
}
