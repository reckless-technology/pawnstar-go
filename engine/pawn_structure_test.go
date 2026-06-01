package engine

import "testing"

// TestDeterminePawnStructure verifies each structural category returned by
// determinePawnStructure for both colours.  Each sub-test targets a specific
// property; comments show why each pawn falls into (or out of) each category.
func TestDeterminePawnStructure(t *testing.T) {

	// sq checks whether sq is a member of bb.
	has := func(bb bitboard, sq square) bool { return bb&squareBB(sq) != 0 }

	t.Run("single white pawn: isolated, unsupported, passed, not doubled, not defended", func(t *testing.T) {
		// Only one white pawn on e4; no enemy pawns anywhere.
		// isolated   – no white pawns on the d- or f-files.
		// unsupported – supportedMask[e4] = {d1–d4, f1–f4}; all empty.
		// passed     – passedMask[e4] = d5–d8, e5–e8, f5–f8; no black pawns.
		// not doubled – northOf[e4] = e5–e8; all empty.
		// not defended – no white pawn immediately at d3 or f3.
		pos := positionFromFen("7k/8/8/8/4P3/8/8/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, white)

		if !has(ps.passed, e4) {
			t.Error("e4 should be passed")
		}
		if !has(ps.isolated, e4) {
			t.Error("e4 should be isolated")
		}
		if !has(ps.unsupported, e4) {
			t.Error("e4 should be unsupported")
		}
		if has(ps.doubled, e4) {
			t.Error("e4 should not be doubled")
		}
		if has(ps.defended, e4) {
			t.Error("e4 should not be defended")
		}
	})

	t.Run("defended and supported: d3 immediately defends e4", func(t *testing.T) {
		// White pawns on d3 and e4.
		// d3 is at the immediate diagonal-behind square for e4 (Δfile=+1, Δrank=+1).
		// defended   – shiftNE(d3) = e4.  e4 is in the defended set.
		// supported  – d3 ∈ supportedMask[e4] = {d1–d4, f1–f4}.
		// not isolated – d-file has a white pawn.
		// d3 itself: not defended (no pawn at c2 or e2), unsupported (supportedMask[d3]
		//   = {c1–c3, e1–e3}; e4 is rank 4, above that window, so d3 is unsupported).
		pos := positionFromFen("7k/8/8/8/4P3/3P4/8/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, white)

		if !has(ps.defended, e4) {
			t.Error("e4 should be defended by d3")
		}
		if has(ps.unsupported, e4) {
			t.Error("e4 should be supported (d3 is in its support window)")
		}
		if has(ps.isolated, e4) {
			t.Error("e4 should not be isolated (d-file has a pawn)")
		}
		if has(ps.defended, d3) {
			t.Error("d3 should not be defended")
		}
		if !has(ps.unsupported, d3) {
			t.Error("d3 should be unsupported (e4 is rank 4, above supportedMask[d3])")
		}
	})

	t.Run("supported but NOT defended: d2 is in support window but not immediately behind e4", func(t *testing.T) {
		// White pawns on d2 and e4.
		// supportedMask[e4] = {d1–d4, f1–f4}.  d2 ∈ that set → e4 is supported.
		// defended check: shiftNE(d2) = e3 ≠ e4.  e4 is NOT defended.
		// This is the key distinction: supported ≠ defended.
		// d2 would need to advance to d3 before e4 becomes defended.
		pos := positionFromFen("7k/8/8/8/4P3/8/3P4/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, white)

		if has(ps.unsupported, e4) {
			t.Error("e4 should be supported (d2 is in its support window)")
		}
		if has(ps.defended, e4) {
			t.Error("e4 should NOT be defended (d2 is two ranks behind, not immediately diagonal)")
		}
		if has(ps.isolated, e4) {
			t.Error("e4 should not be isolated (d-file has a pawn)")
		}
	})

	t.Run("doubled pawns: back pawn is doubled and excluded from passed", func(t *testing.T) {
		// White pawns on e5 and e6; no black pawns.
		// doubled: doubledMask[e5] = northOf[e5] = {e6–e8}.  e6 is there → e5 is doubled.
		//          doubledMask[e6] = northOf[e6] = {e7, e8}.  Empty → e6 not doubled.
		// passed: both would pass the enemy-pawn check (no black pawns), but
		//         determinePawnStructure applies `ps.passed &= ^ps.doubled`, so e5
		//         is removed from passed even though no enemy pawn blocks it.
		// e6 is the front pawn: not doubled, so it remains passed.
		pos := positionFromFen("7k/8/4P3/4P3/8/8/8/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, white)

		if !has(ps.doubled, e5) {
			t.Error("e5 should be doubled (e6 is ahead on same file)")
		}
		if has(ps.doubled, e6) {
			t.Error("e6 should not be doubled (nothing ahead)")
		}
		if has(ps.passed, e5) {
			t.Error("e5 should not be passed: doubled pawns are excluded from passed")
		}
		if !has(ps.passed, e6) {
			t.Error("e6 should be passed (front pawn, not doubled, no blockers)")
		}
	})

	t.Run("not passed: enemy pawn on adjacent file ahead", func(t *testing.T) {
		// White pawn on e4; black pawn on f5.
		// passedMask[e4] covers d5–d8, e5–e8, f5–f8.  f5 ∈ that set → e4 not passed.
		// e4 is also blocked diagonally, not just on the same file.
		pos := positionFromFen("7k/8/8/5p2/4P3/8/8/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, white)

		if has(ps.passed, e4) {
			t.Error("e4 should not be passed (black pawn on f5 is in the passed-pawn mask)")
		}
		if !has(ps.unsupported, e4) {
			t.Error("e4 should be unsupported")
		}
		if !has(ps.isolated, e4) {
			t.Error("e4 should be isolated")
		}
	})

	t.Run("black pawn defended and supported by pawn on adjacent file ahead (black's behind)", func(t *testing.T) {
		// Black pawns on d6 and e5.  For black, "behind" is higher rank.
		// defended: ps.defended uses shiftSW/shiftSE.  shiftSE(d6) = e5.  e5 is defended. ✓
		// supported: supportedMask for black e5 = (northOf[e5] | e5) shifted ±1 file
		//            = {d5–d8, f5–f8}.  d6 ∈ {d5–d8} → e5 is supported.
		// d6 is not defended (no black pawn immediately at c7 or e7).
		pos := positionFromFen("7k/8/3p4/4p3/8/8/8/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, black)

		if !has(ps.defended, e5) {
			t.Error("black e5 should be defended by d6")
		}
		if has(ps.unsupported, e5) {
			t.Error("black e5 should be supported (d6 is in its support window)")
		}
		if has(ps.isolated, e5) {
			t.Error("black e5 should not be isolated")
		}
		if has(ps.defended, d6) {
			t.Error("black d6 should not be defended")
		}
	})

	t.Run("black pawn: supported-but-not-defended (pawn two ranks behind)", func(t *testing.T) {
		// Black pawns on d7 and e5.  For black, "behind e5" is rank 6 or higher.
		// supportedMask for black e5 = {d5–d8, f5–f8}.  d7 ∈ {d5–d8} → e5 supported.
		// defended: shiftSE(d7) = e6 ≠ e5.  e5 is NOT defended (d7 is two ranks behind).
		pos := positionFromFen("7k/3p4/8/4p3/8/8/8/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, black)

		if has(ps.unsupported, e5) {
			t.Error("black e5 should be supported (d7 is in its support window)")
		}
		if has(ps.defended, e5) {
			t.Error("black e5 should NOT be defended (d7 is two ranks behind, not immediately diagonal)")
		}
	})

	t.Run("multiple pawns: chain with mixed properties", func(t *testing.T) {
		// White pawns on c3, d4, e5.
		// c3: defended by no one (b2 and d2 empty); d4 is rank 4, ahead of c3, so d4
		//     does not contribute to c3's support window (supportedMask[c3] covers b1–b3, d1–d3).
		//     c3 is unsupported and not defended.
		// d4: shiftNE(c3) = d4 → d4 is defended.  c3 ∈ supportedMask[d4] = {c1–c4, e1–e4}.
		//     d4 is supported (not unsupported) and defended.
		// e5: shiftNE(d4) = e5 → e5 is defended.  d4 ∈ supportedMask[e5] = {d1–d5, f1–f5}.
		//     e5 is supported and defended.
		// None are doubled (no pawn ahead on same file for any).
		// All are passed (no black pawns in the position).
		pos := positionFromFen("7k/8/8/4P3/3P4/2P5/8/K7 w - - 0 1")
		ps := determinePawnStructure(&pos, white)

		if !has(ps.defended, d4) {
			t.Error("d4 should be defended by c3")
		}
		if !has(ps.defended, e5) {
			t.Error("e5 should be defended by d4")
		}
		if has(ps.defended, c3) {
			t.Error("c3 should not be defended")
		}
		if !has(ps.unsupported, c3) {
			t.Error("c3 should be unsupported (d4 is ahead, not behind)")
		}
		if has(ps.unsupported, d4) {
			t.Error("d4 should be supported by c3")
		}
		if has(ps.unsupported, e5) {
			t.Error("e5 should be supported by d4")
		}
		if has(ps.doubled, c3) || has(ps.doubled, d4) || has(ps.doubled, e5) {
			t.Error("no pawn in the chain should be doubled")
		}
		if !has(ps.passed, c3) || !has(ps.passed, d4) || !has(ps.passed, e5) {
			t.Error("all pawns should be passed (no enemy pawns)")
		}
	})
}
