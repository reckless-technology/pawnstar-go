package engine

// historyTable is a butterfly table counting how often each (side-to-move, from→to) move raised
// alpha or caused a beta cutoff anywhere in the search tree. The count is added to a move's sort
// score so quiet moves that have refuted other lines are tried sooner. Pooling across plies (rather
// than the per-ply table this replaced) concentrates the statistics where positions repeat across
// branches and transpositions; the side-to-move dimension keeps the two colours' moves separate
// (which ply parity previously did only by accident, and null-move subtrees polluted). The table is
// per-thread (owned by searchState) under Lazy SMP, so the counts are plain (non-atomic).
type historyTable struct {
	counts [2][4096]uint32 // [side-to-move][move.fromTo()]
}

// recordGoodMove increments the counter for m by side c, indicating that m raised alpha or caused
// a beta cutoff.
func (h *historyTable) recordGoodMove(c color, m move) {
	h.counts[c][m.fromTo()]++
}

// count returns the accumulated count for m by side c.
func (h *historyTable) count(c color, m move) uint32 {
	return h.counts[c][m.fromTo()]
}
