package engine

import (
	"testing"
)

// Standard perft test positions with known node counts.
// Source: https://www.chessprogramming.org/Perft_Results
var perftTests = []struct {
	fen    string
	depth  int
	nodes  uint64
}{
	{"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 1, 20},
	{"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 2, 400},
	{"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 3, 8902},
	{"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 4, 197281},
	{"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 5, 4865609},

	// Position 2 — tricky castling and pins
	{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 1, 48},
	{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 2, 2039},
	{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 3, 97862},
	{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 4, 4085603},

	// Position 3 — endgame with castling
	{"4k3/8/8/8/8/8/8/4K2R w K - 0 1", 1, 15},
	{"4k3/8/8/8/8/8/8/4K2R w K - 0 1", 2, 66},
	{"4k3/8/8/8/8/8/8/4K2R w K - 0 1", 3, 1197},
	{"4k3/8/8/8/8/8/8/4K2R w K - 0 1", 4, 7059},
	{"4k3/8/8/8/8/8/8/4K2R w K - 0 1", 5, 133987},
	{"4k3/8/8/8/8/8/8/4K2R w K - 0 1", 6, 764643},

	// Position 4 — en passant
	{"rnbqkb1r/ppppp1pp/7n/4Pp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3", 5, 11139762},

	// Position 5 — promotion and en passant
	{"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 1, 6},
	{"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 2, 264},
	{"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 3, 9467},
	{"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", 4, 422333},

	// Position 6 — promotions with captures
	{"n1n5/PPPk4/8/8/8/8/4Kppp/5N1N b - - 0 1", 1, 24},
	{"n1n5/PPPk4/8/8/8/8/4Kppp/5N1N b - - 0 1", 2, 496},
	{"n1n5/PPPk4/8/8/8/8/4Kppp/5N1N b - - 0 1", 3, 9483},
	{"n1n5/PPPk4/8/8/8/8/4Kppp/5N1N b - - 0 1", 4, 182838},
}

func TestPerft(t *testing.T) {
	for _, tt := range perftTests {
		pos := FromFen(tt.fen)
		got := Perft(pos, tt.depth)
		if got != tt.nodes {
			t.Errorf("perft(%d) FEN=%s\n  got %d, want %d", tt.depth, tt.fen, got, tt.nodes)
		}
	}
}

// BenchmarkPerft7 times perft(7) on the start position.
func BenchmarkPerft7(b *testing.B) {
	pos := FromFen("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Perft(pos, 7)
	}
}
