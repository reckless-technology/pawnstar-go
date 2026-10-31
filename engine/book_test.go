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

// TestShippedBookProvenance: the book this engine embeds says where it came from, and does not say the
// old thing. The book it replaced was derived from TSCP's book.txt and was asserted to be public domain
// in about twenty places across the three ports; TSCP is Copyright 1997 Tom Kerrigan under an informal
// non-commercial permission, which GPLv3 cannot take, so the claim was false and the material had to go
// rather than be re-worded. This book is built from the Lichess open database, whose CC0 1.0 dedication
// makes the same claim true. It is asserted against the **embedded** bytes because that is what ships: a
// correct engine/book.txt beside a stale go:embed would pass a file check and still print the old claim.
func TestShippedBookProvenance(t *testing.T) {
	for _, want := range []string{
		"Lichess open database",
		"https://database.lichess.org/",
		"CC0 1.0",
		"https://creativecommons.org/publicdomain/zero/1.0/",
		// CC0 waives attribution, so the header must not imply an obligation that does not exist -
		// claiming a duty nobody owes is the mirror image of claiming a permission nobody gave.
		"asks for no attribution",
	} {
		if !strings.Contains(embeddedBook, want) {
			t.Errorf("the embedded book does not name %q", want)
		}
	}
	for _, unwanted := range []string{"TSCP", "Kerrigan"} {
		if strings.Contains(embeddedBook, unwanted) {
			t.Errorf("the embedded book still makes a claim about %q", unwanted)
		}
	}

	// Every line below the header parses, which loadOpeningBook proves by indexing them - but a line
	// that stops early is silent, so count the lines too: a book truncated to its header would load
	// perfectly and play nothing, and only a start-position check would catch it. The count pins the
	// width that was the other half of the point - 385 lines replayed each opening about 13 times in a
	// long match, which inflated every confidence interval the matches produced.
	lines := 0
	for _, line := range strings.Split(embeddedBook, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lines++
		}
	}
	if lines <= 385 {
		t.Errorf("the embedded book has %d opening lines, not wider than the 385-line book it replaced", lines)
	}

	// And it is a book, not just a header: the start position has a move.
	embedded := newOpeningBook()
	if embedded.getMove(hashAfter(t)).isNone() {
		t.Error("the embedded book has no move for the start position")
	}
}
