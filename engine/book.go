package engine

import (
	"bufio"
	_ "embed"
	"math/rand"
	"strings"
)

//go:embed book.txt
var bookData string

// OpeningBook maps position hashes to candidate moves parsed from book.txt.
type OpeningBook struct {
	moves map[uint64][]Move
}

// LoadOpeningBook parses the embedded book.txt and builds the position→moves index.
// Each line is a sequence of moves from the starting position; for every prefix,
// the next move is recorded as a candidate for that position's Zobrist hash.
func LoadOpeningBook() OpeningBook {
	b := OpeningBook{moves: make(map[uint64][]Move)}
	startFen := "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"
	scanner := bufio.NewScanner(strings.NewReader(bookData))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		pos := FromFen(startFen)
		for _, token := range strings.Fields(line) {
			m := bookFindMove(&pos, token)
			if m.IsNone() {
				break
			}
			hash := pos.Hash()
			if !bookContains(b.moves[hash], m) {
				b.moves[hash] = append(b.moves[hash], m)
			}
			pos.MakeMove(m)
		}
	}
	return b
}

// GetMove returns a randomly chosen book move for the given position hash, or MoveNone.
func (b OpeningBook) GetMove(hash uint64) Move {
	candidates := b.moves[hash]
	if len(candidates) == 0 {
		return MoveNone()
	}
	return candidates[rand.Intn(len(candidates))]
}

func bookFindMove(pos *Position, s string) Move {
	moves := pos.GenerateLegalMoves()
	for i := 0; i < len(moves); i++ {
		if m := moves[i]; m.String() == s {
			return m
		}
	}
	return MoveNone()
}

func bookContains(moves []Move, m Move) bool {
	for _, existing := range moves {
		if existing.Equal(m) {
			return true
		}
	}
	return false
}
