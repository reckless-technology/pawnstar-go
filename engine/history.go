package engine

// historyTable tracks which moves raised alpha at each ply, indexed by from/to square pair.
type historyTable struct {
	counts [maxPly][4096]uint
}

func (h *historyTable) reset() {
	*h = historyTable{}
}

func (h *historyTable) recordGoodMove(ply int, m move) {
	if ply >= 0 && ply < maxPly {
		h.counts[ply][m.fromTo()]++
	}
}

func (h *historyTable) getCount(ply int, m move) uint {
	if ply >= 0 && ply < maxPly {
		return h.counts[ply][m.fromTo()]
	}
	return 0
}
