package engine

import (
	"strings"
	"testing"
)

// TestFENRoundTrip pins positionFromFen → toFenString as the identity over every canonical FEN in the
// test corpus (all perft EPD positions and the bench set), plus hand-picked cases covering partial
// castling rights, an en-passant target, and non-trivial clocks. A serialization drift here would
// corrupt every position exchanged over UCI or logged for debugging.
func TestFENRoundTrip(t *testing.T) {
	var fens []string
	for _, epd := range perftEPD {
		fen, _ := parseEPD(epd)
		fens = append(fens, fen)
	}
	fens = append(fens, benchFens...)
	fens = append(fens,
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"rnbqkbnr/ppppppp1/8/7p/4P3/8/PPPP1PPP/RNBQKBNR w KQkq h6 0 2", // en-passant target square
		"r3k2r/8/8/8/8/8/8/R3K2R w Kq - 12 34",                         // partial castling + clocks
		"8/8/8/8/8/4k3/8/4K3 b - - 99 120",                             // near the fifty-move limit
	)

	for _, fen := range fens {
		fen = strings.TrimSpace(fen)
		// Some corpus FENs omit the halfmove/fullmove fields; the parser defaults them to 0/1 and
		// toFenString always emits the canonical 6-field form, so compare against that.
		if len(strings.Fields(fen)) == 4 {
			fen += " 0 1"
		}
		pos := positionFromFen(fen)
		if got := pos.toFenString(); got != fen {
			t.Errorf("round-trip drift:\n in: %s\nout: %s", fen, got)
		}
	}
}

// TestCastlingAdvancesFiftyMoveClock pins makeMove's halfmove clock to FIDE Article 9.3 and FEN: only a pawn
// move or a capture (en passant and a capturing promotion included) resets it, and every other move advances
// it by one - castling too, which is irreversible but neither. Each case's FEN after the move is checked
// whole (the clock, the full-move number, the castling rights and the en-passant square written by
// toFenString) and read back to the same FEN and the same hash.
func TestCastlingAdvancesFiftyMoveClock(t *testing.T) {
	cases := []struct {
		name, fen, move, want string
	}{
		{"white O-O", "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 12 34", "e1g1", "r3k2r/8/8/8/8/8/8/R4RK1 b kq - 13 34"},
		{"white O-O-O", "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 12 34", "e1c1", "r3k2r/8/8/8/8/8/8/2KR3R b kq - 13 34"},
		{"black O-O", "r3k2r/8/8/8/8/8/8/R3K2R b KQkq - 12 34", "e8g8", "r4rk1/8/8/8/8/8/8/R3K2R w KQ - 13 35"},
		{"black O-O-O", "r3k2r/8/8/8/8/8/8/R3K2R b KQkq - 12 34", "e8c8", "2kr3r/8/8/8/8/8/8/R3K2R w KQ - 13 35"},
		{"quiet rook move", "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 12 34", "a1b1", "r3k2r/8/8/8/8/8/8/1R2K2R b Kkq - 13 34"},
		{"pawn single push", "4k3/8/8/8/8/8/4P3/4K3 w - - 12 34", "e2e3", "4k3/8/8/8/8/4P3/8/4K3 b - - 0 34"},
		{"pawn double push", "4k3/8/8/8/8/8/4P3/4K3 w - - 12 34", "e2e4", "4k3/8/8/8/4P3/8/8/4K3 b - e3 0 34"},
		{"capture", "4k3/8/8/8/8/8/3n4/4K3 w - - 12 34", "e1d2", "4k3/8/8/8/8/8/3K4/8 b - - 0 34"},
		{"en passant", "4k3/8/8/3pP3/8/8/8/4K3 w - d6 12 34", "e5d6", "4k3/8/3P4/8/8/8/8/4K3 b - - 0 34"},
		{"promotion", "4k3/P7/8/8/8/8/8/4K3 w - - 12 34", "a7a8q", "Q3k3/8/8/8/8/8/8/4K3 b - - 0 34"},
		{"capturing promotion", "1r2k3/P7/8/8/8/8/8/4K3 w - - 12 34", "a7b8n", "1N2k3/8/8/8/8/8/8/4K3 b - - 0 34"},
	}
	for _, c := range cases {
		pos := positionFromFen(c.fen)
		m := bookFindMove(&pos, c.move)
		if m.isNone() {
			t.Fatalf("%s: %s is not legal in %s", c.name, c.move, c.fen)
		}
		pos.makeMove(m)
		if got := pos.toFenString(); got != c.want {
			t.Errorf("%s: after %s\n got: %s\nwant: %s", c.name, c.move, got, c.want)
			continue
		}
		back := positionFromFen(c.want)
		if got := back.toFenString(); got != c.want {
			t.Errorf("%s: the FEN after the move does not round-trip:\n in: %s\nout: %s", c.name, c.want, got)
		}
		if back.hash != pos.hash || back.halfMove != pos.halfMove {
			t.Errorf("%s: the FEN after the move reads back with hash %x clock %d, makeMove gave %x clock %d",
				c.name, back.hash, back.halfMove, pos.hash, pos.halfMove)
		}
	}
}
