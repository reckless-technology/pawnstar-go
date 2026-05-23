package engine

// nodeType indicates what kind of alpha-beta node produced the stored result.
type nodeType uint8

const (
	nodeCut nodeType = iota // Beta cutoff: stored score is a lower bound.
	nodeAll                 // All-node: no move raised alpha; score is an upper bound.
	nodePV                  // PV node: score is exact.
)

// transposition is a cached search result.  The score is packed into the move's
// upper 32 bits (via move.withScore) so no separate score field is needed.
type transposition struct {
	hash     zobrist
	move     move // best move found; upper 32 bits hold the score
	depth    int16
	nodeType nodeType
	isOld    bool // set by age(); old entries are overwritten preferentially
}

// transpositionTable is a fixed-size hash table of search results, indexed by
// hash % tableSize.  The table size is odd to improve distribution of entries.
type transpositionTable struct {
	table []transposition
}

// newTranspositionTable allocates a table sized to megabytes MB.
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

// find returns the entry for hash if one exists (collision check included).
func (tt *transpositionTable) find(hash zobrist) (transposition, bool) {
	e := &tt.table[hash%zobrist(len(tt.table))]
	if e.hash == hash {
		return *e, true
	}
	return transposition{}, false
}

// record stores t, applying the replacement policy:
// replace if the slot is empty, the existing entry is old (from a previous
// search), the new entry is at least as deep, or the new entry is a PV node.
func (tt *transpositionTable) record(t *transposition) {
	idx := t.hash % zobrist(len(tt.table))
	existing := &tt.table[idx]
	if existing.hash == 0 || existing.isOld || t.depth >= existing.depth || t.nodeType == nodePV {
		*existing = *t
	}
}

// age marks all entries as old so they can be preferentially replaced in the
// next search.  Called once at the start of each root search.
func (tt *transpositionTable) age() {
	for i := range tt.table {
		tt.table[i].isOld = true
	}
}
