package engine

import (
	"bufio"
	"io"
	"math/rand"
	"strings"
	"sync"
)

// openingBook maps position Zobrist hashes to candidate next moves.
type openingBook struct {
	moves map[zobrist][]move
}

var (
	embeddedBookOnce  sync.Once
	embeddedBookMoves map[zobrist][]move
)

// newOpeningBook returns the hash→moves index for the embedded book (engine/book.txt, compiled in with a
// `go:embed` directive), so the engine always has a book with no external file to ship. Each line is a sequence of
// moves from the starting position; for every prefix of a line, the next move is recorded as a candidate
// for that position's hash. Parsing replays every line with full legal move generation (~10 ms), so the
// index is built once and shared read-only by every Game — clear() drops a game's reference instead of
// mutating the shared map.
func newOpeningBook() openingBook {
	embeddedBookOnce.Do(func() {
		embeddedBookMoves = loadOpeningBook(strings.NewReader(embeddedBook)).moves
	})
	return openingBook{moves: embeddedBookMoves}
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

// clear empties this game's book so the engine will not use book moves (parity with the TT and
// eval-cache clear). It drops the map reference rather than emptying the map, because the parsed
// embedded book is shared read-only across games; getMove on the nil map returns no candidates.
func (book *openingBook) clear() {
	book.moves = nil
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
