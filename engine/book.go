package engine

import (
	"bufio"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// openingBook maps position Zobrist hashes to candidate next moves.
type openingBook struct {
	moves map[zobrist][]move
}

// newOpeningBookFromFile reads book.txt from the same directory as the running binary
// and builds the hash→moves index.  Returns an empty book if the file is absent.
// Each line is a sequence of moves from the starting position; for every prefix
// of a line, the next move is recorded as a candidate for that position's hash.
func newOpeningBookFromFile() openingBook {
	exe, err := os.Executable()
	if err != nil {
		return openingBook{moves: make(map[zobrist][]move)}
	}
	f, err := os.Open(filepath.Join(filepath.Dir(exe), "book.txt"))
	if err != nil {
		return openingBook{moves: make(map[zobrist][]move)}
	}
	defer f.Close()
	return loadOpeningBook(f)
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
