package engine

// historyTable tracks which moves raised alpha at each ply, indexed by from/to square pair.
type historyTable struct {
	counts [maxPly * 4096]uint32
}

func (h *historyTable) reset() {
	for i := range h.counts {
		h.counts[i] = 0
	}
}

func (h *historyTable) recordGoodMove(ply int, m move) {
	if ply >= 0 && ply < maxPly {
		h.counts[ply*4096+m.fromTo()]++
	}
}

func (h *historyTable) getCount(ply int, m move) uint32 {
	if ply >= 0 && ply < maxPly {
		return h.counts[ply*4096+m.fromTo()]
	}
	return 0
}
