package engine

import (
	"bufio"
	"io"
	"math/rand"
	"strings"
)

// openingBook maps position Zobrist hashes to candidate next moves.
type openingBook struct {
	moves map[zobrist][]move
}

// newOpeningBook builds the hash→moves index from the embedded book (engine/book.txt, compiled in via
// go:embed), so the engine always has a book with no external file to ship. Each line is a sequence of
// moves from the starting position; for every prefix of a line, the next move is recorded as a candidate
// for that position's hash.
func newOpeningBook() openingBook {
	return loadOpeningBook(strings.NewReader(embeddedBook))
}

// loadOpeningBook builds a hash→moves index from book text (one move-sequence per line). A move written
// with a trailing "?" is *questionable*: it is replayed so the rest of the line still parses, but is
// never recorded as a candidate (getMove will never return it) — matching the C++ book. Parsing of a
// line stops at the first token that is not a legal move in the reached position.
func loadOpeningBook(r io.Reader) openingBook {
	b := openingBook{moves: make(map[zobrist][]move)}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		pos := positionFromFen(startFen)
		for _, token := range strings.Fields(line) {
			questionable := strings.HasSuffix(token, "?")
			m := bookFindMove(&pos, strings.TrimSuffix(token, "?"))
			if m.isNone() {
				break
			}
			if !questionable {
				b.moves[pos.hash] = append(b.moves[pos.hash], m)
			}
			pos.makeMove(m)
		}
	}
	return b
}

// clearOpeningBook empties the book so the engine will not use book moves.
func (book *openingBook) clearOpeningBook() {
	clear(book.moves)
}

// getMove returns a randomly chosen book move for hash, or moveNone if the
// position is not in the book.  Random selection gives variety across games.
func (b openingBook) getMove(hash zobrist) move {
	candidates := b.moves[hash]
	if len(candidates) == 0 {
		return moveNone()
	}
	return candidates[rand.Intn(len(candidates))]
}

// bookFindMove returns the legal move in pos matching the UCI string s,
// or moveNone if no such move exists.
func bookFindMove(pos *position, s string) move {
	moves := pos.generateLegalMoves()
	for i := 0; i < len(moves); i++ {
		if m := moves[i]; m.String() == s {
			return m
		}
	}
	return moveNone()
}
