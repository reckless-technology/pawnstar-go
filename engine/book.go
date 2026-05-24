package engine

import (
	"bufio"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// openingBook maps position Zobrist hashes to candidate next moves.
type openingBook struct {
	moves map[zobrist][]move
}

// loadOpeningBook reads book.txt from the same directory as the running binary
// and builds the hash→moves index.  Returns an empty book if the file is absent.
// Each line is a sequence of moves from the starting position; for every prefix
// of a line, the next move is recorded as a candidate for that position's hash.
func loadOpeningBook() openingBook {
	b := openingBook{moves: make(map[zobrist][]move)}
	exe, err := os.Executable()
	if err != nil {
		return b
	}
	f, err := os.Open(filepath.Join(filepath.Dir(exe), "book.txt"))
	if err != nil {
		return b
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		pos := positionFromFen(startFen)
		for _, token := range strings.Fields(line) {
			m := bookFindMove(&pos, token)
			if m.isNone() {
				break
			}
			hash := pos.hash
			b.moves[hash] = append(b.moves[hash], m)
			pos.makeMove(m)
		}
	}
	return b
}

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
