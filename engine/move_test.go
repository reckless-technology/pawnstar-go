package engine

import "testing"

// TestMovePackingRoundTrip pins the packed-move bit layout: every field written by the constructors
// and metadata setters reads back exactly, including the sign-extended score (23-bit) and TT depth
// (int8) — a mask or shift bug here silently corrupts every TT probe and every move-ordering sort.
func TestMovePackingRoundTrip(t *testing.T) {
	m := constructMove(e2, d3, knight, capture, rook)
	if m.from() != e2 || m.to() != d3 || m.piece() != knight || m.captured() != rook || m.kind() != capture {
		t.Fatalf("identity fields: from=%v to=%v piece=%v captured=%v kind=%v", m.from(), m.to(), m.piece(), m.captured(), m.kind())
	}

	// Score round-trip: full signed 23-bit range, sign extension both ways.
	for _, score := range []int{0, 1, -1, 12345, -12345, 1<<22 - 1, -(1<<22 - 1)} {
		s := m.withScore(score)
		if got := s.score(); got != score {
			t.Errorf("score %d round-tripped as %d", score, got)
		}
		if !s.equal(m) {
			t.Errorf("score %d changed move identity", score)
		}
	}

	// TT metadata round-trip: negative depths (int8) and every node type.
	for _, depth := range []int{0, 1, 7, 127, -1, -8} {
		for _, nt := range []nodeType{nodeCut, nodeAll, nodePV} {
			e := m.withTTData(-777, depth, nt).setTTAge(255)
			if got := e.score(); got != -777 {
				t.Errorf("tt score: got %d want -777", got)
			}
			if got := e.ttDepth(); got != depth {
				t.Errorf("tt depth %d round-tripped as %d", depth, got)
			}
			if got := e.ttNodeType(); got != nt {
				t.Errorf("tt nodeType %v round-tripped as %v", nt, got)
			}
			if got := e.ttAge(); got != 255 {
				t.Errorf("tt age: got %d want 255", got)
			}
			if !e.equal(m) {
				t.Errorf("TT metadata (depth %d, nt %v) changed move identity", depth, nt)
			}
		}
	}

	// bits/moveFromBits is the raw TT word representation.
	if rt := moveFromBits(m.withScore(-9).bits()); rt != m.withScore(-9) {
		t.Error("bits/moveFromBits round-trip failed")
	}
}

// TestMoveStringPromotion pins UCI notation for promotions — move.String must emit the e7e8q form
// (GoString's pe7e8=q debug form must never leak into engine output).
func TestMoveStringPromotion(t *testing.T) {
	cases := []struct {
		m    move
		want string
	}{
		{moveNonCapture(e2, e4, pawn), "e2e4"},
		{movePromotion(e7, e8, queen, noPiece), "e7e8q"},
		{movePromotion(a2, a1, knight, noPiece), "a2a1n"},
		{movePromotion(b7, a8, rook, bishop), "b7a8r"},
		{movePromotion(h7, h8, bishop, noPiece), "h7h8b"},
	}
	for _, tc := range cases {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}
