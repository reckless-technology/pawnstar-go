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

	// The book has been through tools/filter_book, which lives in ../pawnstar because it needs the engine's
	// own evaluation: a move scoring far enough below its position's best carries a "?", which the parser
	// replays but never selects. Pinned for the same reason as the line count above - an unfiltered book
	// loads perfectly and plays every move the database offered, including the ones this engine evaluates as
	// losing a pawn, and nothing else here would notice. Regenerating means make_book THEN filter_book, and
	// this is what catches forgetting the second step.
	if !strings.Contains(embeddedBook, "?") {
		t.Error("the embedded book carries no questionable-move marks")
	}
	// Where every move a position had is marked, filter_book adds the engine's own choice there, unmarked.
	// Those are the only moves in the file that are not the database's, so the file has to say so: the
	// header's CC0 claim is about the database's lines and a reader must not take it to cover these.
	if !strings.Contains(embeddedBook, "NOT THE DATABASE'S") {
		t.Error("the embedded book does not distinguish the engine's own moves from the database's")
	}

	// And the invariant that addition exists for: no position the book reaches is left with nothing to play.
	// A marked move is not selectable, so a position whose every move is marked would return moveNone and
	// drop the engine out of book - silently, and exactly where an opponent had just played something
	// unusual. Every position a line continues from must offer a selectable move; the last position of a
	// line is a leaf and is allowed to have none.
	deadEnds := 0
	for _, raw := range strings.Split(embeddedBook, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if hash := strings.IndexByte(line, '#'); hash >= 0 {
			line = line[:hash]
		}
		var moves []string
		for _, token := range strings.Fields(line) {
			moves = append(moves, strings.TrimSuffix(token, "?"))
		}
		for ply := 1; ply < len(moves); ply++ {
			if embedded.getMove(hashAfter(t, moves[:ply]...)).isNone() {
				deadEnds++
			}
		}
	}
	if deadEnds > 0 {
		t.Errorf("%d positions the embedded book continues from have no move it may actually play", deadEnds)
	}
}
