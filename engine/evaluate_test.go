package engine

import "testing"

// TestScaleByRule50 pins the fifty-move eval ramp: identity through the threshold (20 plies), then a
// linear scale toward 0 as the clock approaches 100 — the gradient that stops the search shuffling in
// won-but-unconverted endgames (the KBN-vs-K leak guard). Truncation is toward zero, matching C++.
func TestScaleByRule50(t *testing.T) {
	cases := []struct {
		score, rule50, want int
	}{
		{800, 0, 800},    // clock untouched
		{800, 20, 800},   // at the threshold: still identity
		{800, 21, 790},   // 800*79/80
		{800, 60, 400},   // 800*40/80: half way
		{800, 99, 10},    // 800*1/80
		{800, 100, 0},    // at the hard draw
		{-800, 60, -400}, // symmetric for negative scores
		{-800, 99, -10},
		{0, 99, 0},
		{33, 99, 0},    // 33*1/80 truncates toward zero
		{-33, 99, 0},   // and identically for negatives (C++ integer division)
		{801, 60, 400}, // 801*40/80 = 400.5 truncates
	}
	for _, tc := range cases {
		if got := scaleByRule50(tc.score, tc.rule50); got != tc.want {
			t.Errorf("scaleByRule50(%d, %d) = %d, want %d", tc.score, tc.rule50, got, tc.want)
		}
	}
}

// TestMaterialDrawCategory pins the insufficient-material classification: category 2 (K vs K),
// 3 (K + one minor vs K), 4 (KB vs KB with both bishops on the same square colour), and 0 for
// everything else — including opposite-coloured bishops, two knights, and any pawn/rook/queen.
func TestMaterialDrawCategory(t *testing.T) {
	cases := []struct {
		fen  string
		want int
	}{
		{"8/8/8/8/8/4k3/8/4K3 w - - 0 1", 2},                            // K vs K
		{"8/8/8/8/8/4k3/8/3NK3 w - - 0 1", 3},                           // K+N vs K
		{"8/8/8/8/8/2b1k3/8/4K3 w - - 0 1", 3},                          // K vs K+B
		{"2b5/8/8/8/8/4k3/8/4KB2 w - - 0 1", 4},                         // bishops on c8+f1: both light squares
		{"1b6/8/8/8/8/4k3/8/4KB2 w - - 0 1", 0},                         // bishops on b8(dark)+f1(light): not a draw
		{"8/8/8/8/8/1n2k3/8/3NK3 w - - 0 1", 0},                         // two knights: not a material draw category
		{"8/8/8/8/8/4k3/4P3/4K3 w - - 0 1", 0},                          // a pawn always ends it
		{"8/8/8/8/8/4k3/8/3RK3 w - - 0 1", 0},                           // a rook always ends it
		{"8/8/8/8/8/3qk3/8/4K3 w - - 0 1", 0},                           // a queen always ends it
		{"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 0}, // full board
	}
	for _, tc := range cases {
		pos := positionFromFen(tc.fen)
		if got := pos.materialDrawCategory(); got != tc.want {
			t.Errorf("materialDrawCategory(%s) = %d, want %d", tc.fen, got, tc.want)
		}
	}
}
