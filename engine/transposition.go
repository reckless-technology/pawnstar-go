package engine

import (
	"sync/atomic"
	"unsafe"
)

// nodeType indicates the bound on the score stored in a transposition entry.
type nodeType uint8

const (
	nodeCut nodeType = iota // Beta cutoff: score is a lower bound (at least this good for the side to move).
	nodeAll                 // All-node: no move raised alpha; score is an upper bound.
	nodePV                  // PV-node: score is exact within the search window.
)

// transposition is a single cached search result: the best move, with the search score, depth, node
// type and generation packed into its spare bits (see move.go). find has already validated the hash
// (via the XOR key check), so the caller never needs it back — the move alone is the result.
type transposition struct {
	move move // best move, with score/depth/nodeType/age packed into its spare bits.
}

// depth returns the depth the stored result was searched to.
func (t transposition) depth() int { return t.move.ttDepth() }

// nodeType returns the stored bound type (PV/cut/all), which decides how the score may be used.
func (t transposition) nodeType() nodeType { return t.move.ttNodeType() }

// atomicEntry is one lockless table cell.  It stores key = hash ^ data alongside data (the packed
// move bits).  A reader accepts the cell only when key ^ data equals the probe hash, so a torn pair —
// two 64-bit words left over from different concurrent stores — fails the check and reads as a miss
// (Hyatt's XOR trick).  Each word is read/written atomically with relaxed ordering; the XOR check
// supplies the cross-word consistency that per-word atomicity alone does not.
type atomicEntry struct {
	key  atomic.Uint64 // hash ^ data
	data atomic.Uint64 // packed move bits (move + score/depth/nodeType/age)
}

// transpositionTable is a fixed-size, lockless hash table of cached search results.  The size is
// rounded up to a power of 2 so entries index with a bitmask instead of a modulo.  Concurrent workers
// read and write cells without locks; consistency comes from the per-cell XOR key, and racing writers
// resolve to a benign last-writer-wins.
type transpositionTable struct {
	table      []atomicEntry
	currentAge uint8   // generation; bumped by age() at the start of each root search.
	mask       zobrist // len(table)-1; used to index entries via hash & mask.
}

// newTranspositionTable allocates a transposition table sized to approximately megabytes MB.  The
// entry count is rounded up to the next power of 2 so hash & mask can index instead of a modulo.
func newTranspositionTable(megabytes int) transpositionTable {
	const entrySize = 16 // bytes per atomicEntry (two 64-bit words)
	n := max((megabytes*1024*1024)/entrySize, 1)
	var count zobrist
	for count = 1; count < zobrist(n); count <<= 1 {
	}
	return transpositionTable{table: make([]atomicEntry, count), mask: count - 1}
}

// find returns the entry for hash and true, or a zero value and false on a miss.  Lockless: it reads
// the two words with relaxed atomics and accepts the cell only if key ^ data == hash, so a torn pair
// written concurrently is detected and read as a miss.
func (tt *transpositionTable) find(hash zobrist) (transposition, bool) {
	e := &tt.table[hash&tt.mask]
	data := e.data.Load()
	key := e.key.Load()
	if key^data == uint64(hash) {
		return transposition{moveFromBits(data)}, true
	}
	return transposition{}, false
}

// prefetch issues a hardware prefetch of the cell a future find(hash) will read, so the random-access,
// cache-missing load overlaps the caller's intervening work (move legality, the child's search
// prologue) instead of stalling find. A pure hint — it touches no state and cannot change a result, so
// the bench signature is unaffected. Mirrors the C++ TranspositionTable::Prefetch.
func (tt *transpositionTable) prefetch(hash zobrist) {
	prefetchAddr(unsafe.Pointer(&tt.table[hash&tt.mask]))
}

// record writes a search result into the table under the replacement policy: overwrite when the slot
// is empty, the existing entry is from an earlier generation, the new depth is at least as great, or
// the new node is a PV node.  m must carry the search score in its score field (via move.withScore).
// Lockless read-decide-write: a concurrent writer to the same cell resolves to last-writer-wins, and
// the XOR-encoded key keeps each stored pair self-consistent.
func (tt *transpositionTable) record(hash zobrist, m move, depth int, nt nodeType) {
	e := &tt.table[hash&tt.mask]
	curKey := e.key.Load()
	curData := e.data.Load()
	cur := moveFromBits(curData)
	isEmpty := curKey == 0 && curData == 0
	isOld := cur.ttAge() != tt.currentAge
	if isEmpty || isOld || cur.ttDepth() <= depth || nt == nodePV {
		packed := m.withTTData(m.score(), depth, nt).setTTAge(tt.currentAge)
		data := packed.bits()
		e.key.Store(uint64(hash) ^ data)
		e.data.Store(data)
	}
}

// age advances the generation counter so entries written in earlier searches are treated as stale and
// replaced preferentially.  Called once at the start of each root search (single-threaded, no locking
// needed).  The uint8 counter wraps every 256 searches, which is harmless.
func (tt *transpositionTable) age() {
	tt.currentAge++
}

// clear zeroes every entry and resets the generation (backs the UCI `Clear Hash` button). Not safe to
// call during a search.
func (tt *transpositionTable) clear() {
	for i := range tt.table {
		tt.table[i].key.Store(0)
		tt.table[i].data.Store(0)
	}
	tt.currentAge = 0
}

// hashfull returns the approximate fill level in per mille (0..1000) for the UCI `info hashfull` field.
// It samples the first min(len, 1000) cells — positions hash uniformly, so the prefix is representative
// — rather than scanning the whole table, so it is cheap enough to call on every info line.
func (tt *transpositionTable) hashfull() int {
	sample := min(len(tt.table), 1000)
	if sample == 0 {
		return 0
	}
	count := 0
	for i := 0; i < sample; i++ {
		if tt.table[i].data.Load() != 0 {
			count++
		}
	}
	return count * 1000 / sample
}
