package engine

// The lockless evaluation cache (the analogue of transposition.go for static evals), ported from
// the C++ EvalCache. Scores are cached raw (clock-independent); scaleByRule50 is applied on the way
// out by evaluatePosition, never stored.

import "sync/atomic"

// hashNotFoundScore is returned by evalHashTable.find when there is no cached entry for the queried
// hash; it is outside the range of any real score.
const hashNotFoundScore = -2345678

// evalHashEntry is one lockless slot in the evaluation cache: a single 64-bit atomic packing the top
// 48 bits of the position hash (a key check) in the high bits and a 16-bit score in the low bits —
// (hash & 0xFFFF_FFFF_FFFF_0000) | uint16(int16(score)). A load or store is atomic with no torn value
// and no lock, so the cache is shared freely by all Lazy SMP threads; a foreign or evicted entry fails
// the key check and reads as a miss, and racing writers resolve to a benign last-writer-wins. Mirrors
// the C++ EvalCache. Because the packed score depends on the loaded NNUE net, clear() must be called
// whenever the net changes. A plain alias: the slot needs no struct around the one atomic word.
type evalHashEntry = atomic.Uint64

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
	entry := t.entries[hash&t.mask].Load()
	if entry>>16 != uint64(hash)>>16 {
		return hashNotFoundScore
	}
	return int(int16(uint16(entry)))
}

// record stores score for hash, overwriting any existing entry.
func (t *evalHashTable) record(hash zobrist, score int) {
	score = min(max(score, -evalCacheClampMax), evalCacheClampMax)
	packed := (uint64(hash) & 0xFFFFFFFFFFFF0000) | uint64(uint16(int16(score)))
	t.entries[hash&t.mask].Store(packed)
}

// clear resets every entry. Call whenever the loaded NNUE net changes (the cached score depends on it),
// since the key check keys only on the hash, not on which network produced the score.
func (t *evalHashTable) clear() {
	for i := range t.entries {
		t.entries[i].Store(0)
	}
}
