package engine

// HistoryTable tracks which moves raised alpha at each ply, indexed by from/to square pair.
type HistoryTable struct {
	counts [MaxPly * 4096]uint32
}

func (h *HistoryTable) Reset() {
	for i := range h.counts {
		h.counts[i] = 0
	}
}

func (h *HistoryTable) RecordGoodMove(ply int, m Move) {
	if ply >= 0 && ply < MaxPly {
		h.counts[ply*4096+m.FromTo()]++
	}
}

func (h *HistoryTable) GetCount(ply int, m Move) uint32 {
	if ply >= 0 && ply < MaxPly {
		return h.counts[ply*4096+m.FromTo()]
	}
	return 0
}
