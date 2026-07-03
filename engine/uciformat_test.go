package engine

import "testing"

// TestFormatScore pins the UCI `score` rendering, in particular the mate-distance arithmetic GUIs
// display: mate scores are ±(mateValue − plies_to_mate), and the move count rounds plies up. A sign
// or rounding bug here misreports "mate N" to every GUI.
func TestFormatScore(t *testing.T) {
	const mateValue = -checkmatedScore // 31900
	cases := []struct {
		score int
		want  string
	}{
		{0, "cp 0"},
		{123, "cp 123"},
		{-4500, "cp -4500"},
		{winThreshold - 1, "cp 30999"},   // last cp score below the mate band
		{mateValue - 1, "mate 1"},        // mate in 1 ply → mate in 1
		{mateValue - 2, "mate 1"},        // 2 plies → still 1 full move, rounds up
		{mateValue - 5, "mate 3"},        // 5 plies → 3 moves
		{checkmatedScore + 4, "mate -2"}, // we are mated in 4 plies → mate -2
		{checkmatedScore + 1, "mate -1"}, // mated next ply
		{checkmatedScore, "mate 0"},      // mated now (score at the base)
	}
	for _, tc := range cases {
		if got := formatScore(tc.score); got != tc.want {
			t.Errorf("formatScore(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}
