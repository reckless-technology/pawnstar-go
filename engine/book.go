package engine

import (
	"bufio"
	_ "embed"
	"math/rand"
	"strings"
)

//go:embed book.txt
var bookData string

// openingBook maps position hashes to candidate moves parsed from book.txt.
type openingBook struct {
	moves map[uint64][]move
}

// loadOpeningBook parses the embedded book.txt and builds the position→moves index.
// Each line is a sequence of moves from the starting position; for every prefix,
// the next move is recorded as a candidate for that position's Zobrist hash.
func loadOpeningBook() openingBook {
	b := openingBook{moves: make(map[uint64][]move)}
	scanner := bufio.NewScanner(strings.NewReader(bookData))
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
			hash := pos.getHash()
			b.moves[hash] = append(b.moves[hash], m)
			pos.makeMove(m)
		}
	}
	return b
}

func (book *openingBook) clearOpeningBook() {
	clear(book.moves)
}

// getMove returns a randomly chosen book move for the given position hash, or moveNone.
func (b openingBook) getMove(hash uint64) move {
	candidates := b.moves[hash]
	if len(candidates) == 0 {
		return moveNone()
	}
	return candidates[rand.Intn(len(candidates))]
}

func bookFindMove(pos *position, s string) move {
	moves := pos.generateLegalMoves()
	for i := 0; i < len(moves); i++ {
		if m := moves[i]; m.String() == s {
			return m
		}
	}
	return moveNone()
}
