package engine

// nodeType indicates what kind of alpha-beta node was stored.
type nodeType uint8

const (
	nodeCut nodeType = iota // Beta cutoff occurred.
	nodeAll                 // No move exceeded alpha.
	nodePV                  // Principal variation node.
)

// transposition is a cached search result.
type transposition struct {
	hash     uint64
	move     move // includes score
	depth    int16
	nodeType nodeType
	isOld    bool
}

// transpositionTable is a fixed-size hash table of search results.
type transpositionTable struct {
	table []transposition
}

// newTranspositionTable allocates a table of the given size in megabytes.
func newTranspositionTable(megabytes int) transpositionTable {
	const entrySize = 24 // approx bytes per entry
	n := (megabytes * 1024 * 1024) / entrySize
	if n < 1 {
		n = 1
	}
	if (n & 1) == 0 {
		n += 1
	}
	return transpositionTable{table: make([]transposition, n)}
}

// find returns the entry for hash if present.
func (tt *transpositionTable) find(hash uint64) (transposition, bool) {
	e := &tt.table[hash%uint64(len(tt.table))]
	if e.hash == hash {
		return *e, true
	}
	return transposition{}, false
}

// record stores a new transposition entry, applying replacement policy.
func (tt *transpositionTable) record(t *transposition) {
	idx := t.hash % uint64(len(tt.table))
	existing := &tt.table[idx]
	// Replace if: old entry is from previous search, or new entry is deeper, or it's a PV node.
	if existing.hash == 0 || existing.isOld || t.depth >= existing.depth || t.nodeType == nodePV {
		*existing = *t
	}
}

// age marks all entries as old so they can be replaced next search.
func (tt *transpositionTable) age() {
	for i := range tt.table {
		tt.table[i].isOld = true
	}
}
