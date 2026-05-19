package engine

// historyTable counts how often each (ply, from→to) pair raised alpha or caused a
// beta cutoff.  The count is added to a move's sort score so that quiet moves that
// have been good at the same ply in earlier iterations are tried sooner.
// Indexed by ply and move.fromTo(), a 12-bit encoding of the from and to squares.
type historyTable struct {
	counts [maxPly][4096]uint
}

// reset zeroes all counts.  Called at the start of each root search.
func (h *historyTable) reset() {
	*h = historyTable{}
}

// recordGoodMove increments the counter for m at ply, indicating that m raised alpha
// or caused a beta cutoff at that ply.
func (h *historyTable) recordGoodMove(ply int, m move) {
	if ply >= 0 && ply < maxPly {
		h.counts[ply][m.fromTo()]++
	}
}

// getCount returns the accumulated count for m at ply, or 0 if ply is out of range.
func (h *historyTable) getCount(ply int, m move) uint {
	if ply >= 0 && ply < maxPly {
		return h.counts[ply][m.fromTo()]
	}
	return 0
}
