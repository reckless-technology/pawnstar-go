package engine

import (
	"sync/atomic"
)

// nodeType indicates what kind of alpha-beta node produced the stored result.
type nodeType uint8

const (
	nodeCut nodeType = iota // Beta cutoff: stored score is a lower bound.
	nodeAll                 // All-node: no move raised alpha; score is an upper bound.
	nodePV                  // PV node: score is exact.
)

// transposition is a cached search result.  The score is packed into the move's
// upper 32 bits (via move.withScore) so no separate score field is needed.
// The lock field occupies the 4 bytes of natural padding after age, keeping
// the struct at 24 bytes.
type transposition struct {
	hash     zobrist
	move     move // best move found; upper 32 bits hold the score
	depth    int16
	nodeType nodeType
	age      uint8  // generation counter at write time; compared against transpositionTable.currentAge
	lock     uint32 // per-entry spinlock: 0 = unlocked, 1 = locked
}

// transpositionTable is a fixed-size hash table of search results, indexed by
// hash % tableSize.  The table size is odd to improve distribution of entries.
// Each entry carries its own spinlock so concurrent workers contend only when
// they happen to access the same slot.
type transpositionTable struct {
	table      []transposition
	currentAge uint8
}

// newTranspositionTable allocates a table sized to megabytes MB.
func newTranspositionTable(megabytes int) transpositionTable {
	const entrySize = 24 // bytes per entry
	n := (megabytes * 1024 * 1024) / entrySize
	if n < 1 {
		n = 1
	}
	if (n & 1) == 0 {
		n += 1
	}
	return transpositionTable{table: make([]transposition, n)}
}

// find returns the entry for hash if one exists (collision check included).
func (tt *transpositionTable) find(hash zobrist) (transposition, bool) {
	idx := hash % zobrist(len(tt.table))
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

// record stores a search result, applying the replacement policy:
// replace if the slot is empty, the existing entry is from a previous
// search, the new entry is at least as deep, or the new entry is a PV node.
func (tt *transpositionTable) record(hash zobrist, m move, depth int16, nt nodeType) {
	idx := hash % zobrist(len(tt.table))
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
