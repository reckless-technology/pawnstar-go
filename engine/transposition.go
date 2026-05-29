package engine

import (
	"sync/atomic"
)

// nodeType indicates the bound on the score stored in a transposition entry.
type nodeType uint8

const (
	nodeCut nodeType = iota // Beta cutoff: score is a lower bound (at least this good for the side to move).
	nodeAll                 // All-node: no move raised alpha; score is an upper bound.
	nodePV                  // PV-node: score is exact within the search window.
)

// transposition is a single cached search result.  The search score is packed
// into the move field's upper 32 bits (via move.withScore) so no separate score
// field is needed.  The lock field sits in the 4 bytes of natural padding that
// would otherwise follow age, keeping the struct at exactly 24 bytes.
type transposition struct {
	hash     zobrist  // Zobrist hash of the stored position; used to detect index collisions
	move     move     // best move found at this node; search score in bits 32–63
	depth    int16    // search depth at which this entry was written
	nodeType nodeType // bound type for the stored score (Cut / All / PV)
	age      uint8    // generation counter at write time; entries from older generations are replaced preferentially
	lock     uint32   // per-entry CAS spinlock: 0 = unlocked, 1 = locked
}

// transpositionTable is a fixed-size hash table of cached search results.
// The size is rounded up to a power of 2 so entries can be indexed with a
// bitmask instead of a modulo.  Each entry carries its own spinlock so
// concurrent workers only contend when they access the exact same slot.
type transpositionTable struct {
	table      []transposition
	currentAge uint8   // incremented by age() at the start of each root search
	mask       zobrist // len(table)-1; used to index entries via hash & mask
}

// newTranspositionTable allocates a transposition table sized to approximately
// megabytes MB.  The entry count is rounded up to the next power of 2 so that
// hash & mask can be used for indexing instead of a modulo division.
func newTranspositionTable(megabytes int) transpositionTable {
	const entrySize = 24 // bytes per transposition entry
	n := (megabytes * 1024 * 1024) / entrySize
	if n < 1 {
		n = 1
	}
	var count zobrist
	for count = 1; count < zobrist(n); count <<= 1 {
	}
	return transpositionTable{table: make([]transposition, count), mask: count - 1}
}

// find returns a copy of the entry for hash and true, or a zero value and
// false on a miss.  The entry is copied while the spinlock is held so the
// caller receives a consistent snapshot; the lock is released before the
// hash comparison to minimise critical-section time.
func (tt *transpositionTable) find(hash zobrist) (transposition, bool) {
	idx := hash & tt.mask
	entry := &tt.table[idx]
	for !atomic.CompareAndSwapUint32(&entry.lock, 0, 1) {
		dbgx.spinlocksFind++
	}
	e := *entry
	atomic.StoreUint32(&entry.lock, 0)
	if e.hash == hash {
		return e, true
	}
	return transposition{}, false
}

// record writes a search result into the table under the replacement policy:
// overwrite when the slot is empty, the existing entry is from an earlier
// generation, the new depth is at least as great, or the new node is a PV node.
// m must carry the search score in its upper 32 bits (use move.withScore before calling).
func (tt *transpositionTable) record(hash zobrist, m move, depth int16, nt nodeType) {
	idx := hash & tt.mask
	entry := &tt.table[idx]
	for !atomic.CompareAndSwapUint32(&entry.lock, 0, 1) {
		dbgx.spinlocksRecord++
	}
	if entry.hash == 0 || entry.age != tt.currentAge || depth >= entry.depth || nt == nodePV {
		entry.hash = hash
		entry.move = m
		entry.depth = depth
		entry.nodeType = nt
		entry.age = tt.currentAge
	}
	atomic.StoreUint32(&entry.lock, 0)
}

// age advances the generation counter so entries written in earlier searches
// are treated as stale and replaced preferentially.  Called once at the start
// of each root search (no concurrent access at that point, so no locking needed).
func (tt *transpositionTable) age() {
	tt.currentAge++
}
