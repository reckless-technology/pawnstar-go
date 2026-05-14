package engine

// NodeType indicates what kind of alpha-beta node was stored.
type NodeType uint8

const (
	NodeCut NodeType = iota // Beta cutoff occurred.
	NodeAll                 // No move exceeded alpha.
	NodePV                  // Principal variation node.
)

// Transposition is a cached search result.
type Transposition struct {
	hash     uint64
	move     Move // includes score
	depth    int16
	nodeType NodeType
	isOld    bool
}

// TranspositionTable is a fixed-size hash table of search results.
type TranspositionTable struct {
	table []Transposition
}

// NewTranspositionTable allocates a table of the given size in megabytes.
func NewTranspositionTable(megabytes int) TranspositionTable {
	const entrySize = 24 // approx bytes per entry
	n := (megabytes * 1024 * 1024) / entrySize
	if n < 1 {
		n = 1
	}
	if (n & 1) == 0 {
		n += 1
	}
	return TranspositionTable{table: make([]Transposition, n)}
}

// Find returns the entry for hash if present.
func (tt *TranspositionTable) Find(hash uint64) (Transposition, bool) {
	e := &tt.table[hash%uint64(len(tt.table))]
	if e.hash == hash {
		return *e, true
	}
	return Transposition{}, false
}

// Record stores a new transposition entry, applying replacement policy.
func (tt *TranspositionTable) Record(t *Transposition) {
	idx := t.hash % uint64(len(tt.table))
	existing := &tt.table[idx]
	// Replace if: old entry is from previous search, or new entry is deeper, or it's a PV node.
	if existing.hash == 0 || existing.isOld || t.depth >= existing.depth || t.nodeType == NodePV {
		*existing = *t
	}
}

// Age marks all entries as old so they can be replaced next search.
func (tt *TranspositionTable) Age() {
	for i := range tt.table {
		tt.table[i].isOld = true
	}
}
