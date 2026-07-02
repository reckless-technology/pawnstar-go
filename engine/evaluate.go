package engine

import "sync/atomic"

// evalHashEntry is one lockless slot in the evaluation cache: a single 64-bit atomic packing the top
// 48 bits of the position hash (a key check) in the high bits and a 16-bit score in the low bits. A
// load or store is atomic with no torn value and no lock, so the cache is shared freely by all Lazy SMP
// threads; a foreign or evicted entry fails the key check and reads as a miss, and racing writers
// resolve to a benign last-writer-wins. Mirrors the C++ EvalCache. Because the packed score depends on
// the loaded NNUE net, clear() must be called whenever the net changes.
type evalHashEntry struct {
	packed atomic.Uint64 // (hash & 0xFFFF_FFFF_FFFF_0000) | uint16(int16(score))
}

// evalHashTable is a fixed-size, lockless direct-mapped cache for static evaluations. The table size
// is a power of 2 so entries are indexed with a bitmask.
type evalHashTable struct {
	entries []evalHashEntry // flat array; entry for hash is at entries[hash & mask]
	mask    zobrist         // len(entries)-1; used to index entries via hash & mask
}

// evalCacheClampMax bounds a cached static eval so it always fits the 16-bit score field. Real static
// evals (NNUE clamped to ±nnueSearchClamp) never reach it — it is only a guard against int16 truncation
// corrupting the cache. Mate scores are never cached.
const evalCacheClampMax = 30000

// newEvalHashTable allocates an evaluation cache with count slots (rounded up to a power of 2).
func newEvalHashTable(count int) evalHashTable {
	var i int
	for i = 1; i < count; i <<= 1 {
	}
	return evalHashTable{entries: make([]evalHashEntry, i), mask: zobrist(i) - 1}
}

// find returns the cached score for hash, or hashNotFoundScore on a miss.
func (t *evalHashTable) find(hash zobrist) int {
	entry := t.entries[hash&t.mask].packed.Load()
	if entry>>16 != uint64(hash)>>16 {
		return hashNotFoundScore
	}
	return int(int16(uint16(entry)))
}

// record stores score for hash, overwriting any existing entry.
func (t *evalHashTable) record(hash zobrist, score int) {
	if score > evalCacheClampMax {
		score = evalCacheClampMax
	} else if score < -evalCacheClampMax {
		score = -evalCacheClampMax
	}
	packed := (uint64(hash) & 0xFFFFFFFFFFFF0000) | uint64(uint16(int16(score)))
	t.entries[hash&t.mask].packed.Store(packed)
}

// clear resets every entry. Call whenever the loaded NNUE net changes (the cached score depends on it),
// since the key check keys only on the hash, not on which network produced the score.
func (t *evalHashTable) clear() {
	for i := range t.entries {
		t.entries[i].packed.Store(0)
	}
}

// rule50ScaleThreshold is the halfmove-clock ply below which the static eval is untouched, so normal
// play (where the clock is reset often by pawn moves/captures) is unaffected.
const rule50ScaleThreshold = 20

// scaleByRule50 scales a static eval toward draw as the fifty-move clock climbs, giving the search a
// standing incentive to make progress (reset the clock) rather than shuffle in a won-but-unconverted
// position — without it a placement-only NNUE eval is identical at clock 0 and clock 99, and leaks won
// endgames (notably KBN-vs-K) to the rule. Identity for rule50 ≤ threshold, then a linear ramp to 0 as
// the clock approaches 100 (the hard draw handled by isDrawByFiftyMoves). Mirrors the C++ ScaleByRule50.
func scaleByRule50(score, rule50 int) int {
	if rule50 <= rule50ScaleThreshold {
		return score
	}
	return int(int64(score) * int64(100-rule50) / int64(100-rule50ScaleThreshold))
}

// evaluatePosition returns the NNUE score from the side-to-move's perspective, drawScore for positions
// with insufficient material. The result is cached raw (clock-independent) in the eval table; the
// fifty-move scaling (scaleByRule50) is applied on the way out, never stored — the hash excludes the
// clock, so the same position can be probed at different clock values.
func evaluatePosition(ss *searchState) int {
	ss.dbg.evalCalls++
	pos := ss.currentPosition()
	if cat := pos.materialDrawCategory(); cat != 0 {
		ss.recordMaterialDraw(cat)
		return drawScore
	}

	hashScore := ss.game.evalTable.find(pos.hash)
	if hashScore != hashNotFoundScore {
		ss.dbg.evalHashHits++
		return scaleByRule50(hashScore, int(pos.halfMove))
	}

	score := ss.game.nnue.searchEvalAcc(ss.currentAccumulator(), pos.colorToMove)
	ss.game.evalTable.record(pos.hash, score)
	return scaleByRule50(score, int(pos.halfMove))
}
