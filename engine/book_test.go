package engine

import (
	"strings"
	"testing"
)

// hashAfter replays a sequence of coordinate moves from the start position and returns the resulting
// Zobrist hash — the key the opening book is indexed by.
func hashAfter(t *testing.T, moves ...string) zobrist {
	t.Helper()
	pos := positionFromFen(startFen)
	for _, coord := range moves {
		m := bookFindMove(&pos, coord)
		if m.isNone() {
			t.Fatalf("test bug: illegal move %q", coord)
		}
		pos.makeMove(m)
	}
	return pos.hash
}

// TestOpeningBookQuestionableMove ports the C++ opening_book_test: a "?" (questionable) move must be
// replayed so the rest of the line parses, but must never be returned by getMove; the move that follows
// it in the line must still be recorded and selectable.
func TestOpeningBookQuestionableMove(t *testing.T) {
	// Line: 1.e4 e5 2.f4? (King's Gambit, marked questionable) exf4 3.Nf3
	book := loadOpeningBook(strings.NewReader("e2e4 e7e5 f2f4? e5f4 g1f3\n"))

	// Playable moves along the line are selectable.
	if book.getMove(hashAfter(t)).isNone() {
		t.Error("e2e4 should be a playable book move from the start")
	}
	if book.getMove(hashAfter(t, "e2e4")).isNone() {
		t.Error("e7e5 should be playable after e2e4")
	}

	// The questionable move f2f4 must never be returned, even over many random draws.
	afterE4E5 := hashAfter(t, "e2e4", "e7e5")
	for n := 0; n < 1000; n++ {
		if !book.getMove(afterE4E5).isNone() {
			t.Fatal("questionable move f2f4? must never be returned by getMove")
		}
	}

	// The line continued past the '?' move: the reply exf4 is recorded and selectable.
	reply := book.getMove(hashAfter(t, "e2e4", "e7e5", "f2f4"))
	if reply.isNone() || reply.String() != "e5f4" {
		t.Errorf("exf4 should be playable after the questionable f2f4, got %q", reply.String())
	}
}
