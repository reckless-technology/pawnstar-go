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
