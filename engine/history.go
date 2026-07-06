package engine

// historyTable is a butterfly table scoring how good each (side-to-move, from→to) move has been at
// raising alpha or causing a beta cutoff anywhere in the search tree. Pooling across plies (rather
// than the per-ply table this replaced) concentrates the statistics where positions repeat across
// branches and transpositions; the side-to-move dimension keeps the two colours' moves separate
// (which ply parity previously did only by accident, and null-move subtrees polluted). The table is
// per-thread (owned by searchState) under Lazy SMP, so the counts are plain (non-atomic).
//
// The score is updated with a depth-weighted bonus/malus and a gravity term (applyBonus): a good move
// is credited, the quiet moves that failed before a cutoff are debited, and gravity self-saturates the
// entry toward ±historyMax so recent evidence overwrites stale evidence. Storage is signed (int32)
// because the malus drives entries negative. This replaced an increment-only +1 counter: SPRT +17.2 ±
// 9.8 Elo at 10+0.1 (3295 games, LLR 2.96 → H1), even though it *raises* fixed-depth node counts —
// the extra nodes buy better move choices, a win the node count alone does not show.
type historyTable struct {
	counts [2][4096]int32 // [side-to-move][move.fromTo()]
}

const (
	// historyMax bounds a gravity-managed entry to ±historyMax. 16384 fits int16 (continuation history)
	// with margin.
	historyMax = 16384
	// maxHistoryBonus caps the per-update bonus so a single very deep cutoff cannot dominate; depth²
	// only reaches it near depth 20.
	maxHistoryBonus = 400
)

// historyBonus is the depth-weighted magnitude credited to a good move (and debited from the quiets
// that failed before it). Quadratic in depth, clamped to maxHistoryBonus.
func historyBonus(depth int) int32 {
	if depth < 1 {
		depth = 1
	}
	bonus := int32(depth * depth)
	if bonus > maxHistoryBonus {
		bonus = maxHistoryBonus
	}
	return bonus
}

func absInt32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

// applyGravity nudges *entry by bonus with a gravity term that keeps it within ±historyMax.
func applyGravity(entry *int32, bonus int32) {
	*entry += bonus - (*entry)*absInt32(bonus)/historyMax
}

// applyBonus credits (bonus > 0) or debits (bonus < 0) the butterfly-history entry for m by side c.
func (h *historyTable) applyBonus(c color, m move, bonus int32) {
	applyGravity(&h.counts[c][m.fromTo()], bonus)
}

// recordGoodMove increments the butterfly counter for m by side c. Used only by quiescence, which does
// not carry a meaningful depth and stays on the simple increment; the main search uses applyBonus.
func (h *historyTable) recordGoodMove(c color, m move) {
	h.counts[c][m.fromTo()]++
}

// count returns the accumulated score for m by side c.
func (h *historyTable) count(c color, m move) int32 {
	return h.counts[c][m.fromTo()]
}
