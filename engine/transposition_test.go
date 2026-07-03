package engine

import "testing"

// ttProbe fetches hash from the table and fails the test on a miss.
func ttProbe(t *testing.T, tt *transpositionTable, hash zobrist) transposition {
	t.Helper()
	entry, ok := tt.find(hash)
	if !ok {
		t.Fatalf("expected a hit for hash %#x", hash)
	}
	return entry
}

// TestTTRoundTrip pins record→find: the stored move comes back with its score, depth, node type and
// age intact (all packed into the move's spare bits — a mask or shift bug here corrupts every probe).
func TestTTRoundTrip(t *testing.T) {
	tt := newTranspositionTable(1)
	tt.age() // currentAge = 1, so a stamped age is distinguishable from the zero value
	hash := zobrist(0xDEADBEEFCAFEF00D)
	m := moveCapture(e2, d3, pawn, knight).withScore(-1234)

	tt.record(hash, m, 7, nodeCut)

	got := ttProbe(t, &tt, hash)
	if !got.move.equal(m) {
		t.Errorf("move identity: got %v want %v", got.move, m)
	}
	if s := got.move.score(); s != -1234 {
		t.Errorf("score: got %d want -1234", s)
	}
	if d := got.depth(); d != 7 {
		t.Errorf("depth: got %d want 7", d)
	}
	if nt := got.nodeType(); nt != nodeCut {
		t.Errorf("nodeType: got %v want nodeCut", nt)
	}
	if age := got.move.ttAge(); age != 1 {
		t.Errorf("age: got %d want 1", age)
	}

	if _, ok := tt.find(hash ^ 1); ok {
		t.Error("foreign hash unexpectedly hit")
	}
}

// TestTTReplacementPolicy pins record's precedence: keep the deeper entry against a shallower
// same-generation non-PV write, but replace for (a) greater or equal depth, (b) a PV node at any
// depth, and (c) any write once the stored entry's generation is stale.
func TestTTReplacementPolicy(t *testing.T) {
	hash := zobrist(0x123456789ABCDEF0)
	deep := moveNonCapture(e2, e4, pawn).withScore(50)
	shallow := moveNonCapture(d2, d4, pawn).withScore(60)

	tt := newTranspositionTable(1)
	tt.age()

	tt.record(hash, deep, 10, nodeCut)
	tt.record(hash, shallow, 5, nodeAll) // shallower, non-PV, same generation → must NOT replace
	if got := ttProbe(t, &tt, hash); !got.move.equal(deep) || got.depth() != 10 {
		t.Fatalf("shallow non-PV write replaced a deeper entry: got %v depth %d", got.move, got.depth())
	}

	tt.record(hash, shallow, 10, nodeAll) // equal depth → replaces
	if got := ttProbe(t, &tt, hash); !got.move.equal(shallow) {
		t.Fatal("equal-depth write did not replace")
	}

	tt.record(hash, deep, 1, nodePV) // PV overrides depth
	if got := ttProbe(t, &tt, hash); !got.move.equal(deep) || got.depth() != 1 {
		t.Fatalf("PV write did not replace: got %v depth %d", got.move, got.depth())
	}

	tt.age()                             // new generation: the stored entry is now stale
	tt.record(hash, shallow, 0, nodeCut) // shallower and non-PV, but old generation → replaces
	if got := ttProbe(t, &tt, hash); !got.move.equal(shallow) || got.depth() != 0 {
		t.Fatalf("stale-generation entry not replaced: got %v depth %d", got.move, got.depth())
	}
}

// TestTTTornRead pins the lockless XOR consistency check: a cell whose key and data disagree (as left
// by two interleaved concurrent stores) must read as a miss, never as a hit with garbage.
func TestTTTornRead(t *testing.T) {
	tt := newTranspositionTable(1)
	tt.age()
	hash := zobrist(0x0F1E2D3C4B5A6978)
	tt.record(hash, moveNonCapture(g1, f3, knight).withScore(7), 3, nodePV)

	// Corrupt the key word only — simulating a reader catching key from one store and data from another.
	e := &tt.table[hash&tt.mask]
	e.key.Store(e.key.Load() ^ 0xFFFF)

	if _, ok := tt.find(hash); ok {
		t.Fatal("torn cell (key ^ data != hash) read as a hit")
	}
}
