package engine

// In-process coverage for the UCI command paths a real GUI hits every game but the suite never
// exercised: replaying `position ... moves` (promotion / castling / en passant), setoption parsing
// (multi-word names), mate-distance reporting, root positions with no legal move, the allocation-free
// move-gen invariant, and the EvalFile error path. These drive the real Game via HandleCommand /
// searchRootNode (no subprocess), so they are fast and deterministic.

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPositionMovesReplay checks that `position ... moves <uci>...` reconstructs the exact board — the
// single most-used command in every game, and previously at 0% coverage. Each case replays a move
// sequence and asserts the resulting position matches an independently parsed expected FEN, on both the
// incremental Zobrist hash (pieces + side + castling + en-passant) and the raw squares array. A
// promotion, castling, or en-passant replay bug would break every GUI game while every other gate
// stayed green.
func TestPositionMovesReplay(t *testing.T) {
	cases := []struct {
		name    string
		command string
		wantFen string
	}{
		{
			"ruy lopez from startpos",
			"position startpos moves e2e4 e7e5 g1f3 b8c6 f1b5",
			"r1bqkbnr/pppp1ppp/2n5/1B2p3/4P3/5N2/PPPP1PPP/RNBQK2R b KQkq - 3 3",
		},
		{
			"white kingside castling (king + rook move)",
			"position fen r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1 moves e1g1",
			"r3k2r/8/8/8/8/8/8/R4RK1 b kq - 1 1",
		},
		{
			"white queenside castling",
			"position fen r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1 moves e1c1",
			"r3k2r/8/8/8/8/8/8/2KR3R b kq - 1 1",
		},
		{
			"black kingside castling",
			"position fen r3k2r/8/8/8/8/8/8/R3K2R b KQkq - 0 1 moves e8g8",
			"r4rk1/8/8/8/8/8/8/R3K2R w KQ - 1 2",
		},
		{
			"queen promotion",
			"position fen 4k3/P7/8/8/8/8/8/4K3 w - - 0 1 moves a7a8q",
			"Q3k3/8/8/8/8/8/8/4K3 b - - 0 1",
		},
		{
			"knight underpromotion",
			"position fen 4k3/P7/8/8/8/8/8/4K3 w - - 0 1 moves a7a8n",
			"N3k3/8/8/8/8/8/8/4K3 b - - 0 1",
		},
		{
			"en-passant capture removes the passed pawn",
			"position fen 4k3/8/8/3pP3/8/8/8/4K3 w - d6 0 1 moves e5d6",
			"4k3/8/3P4/8/8/8/8/4K3 b - - 0 1",
		},
		{
			"capture + a longer line",
			"position startpos moves e2e4 d7d5 e4d5 d8d5 b1c3 d5a5",
			"rnb1kbnr/ppp1pppp/8/q7/8/2N5/PPPP1PPP/R1BQKBNR w KQkq - 3 4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			game := NewGame()
			game.HandleCommand(tc.command)
			got := game.currentPosition()
			want := positionFromFen(tc.wantFen)
			// Hash covers pieces + side-to-move + castling + en-passant; squares covers placement. Zobrist
			// excludes the clocks, so the clock fields in wantFen do not affect the assertion.
			if got.hash != want.hash {
				t.Errorf("hash mismatch after %q\n got %s\nwant %s", tc.command, got.toFenString(), tc.wantFen)
			}
			if got.squares != want.squares {
				t.Errorf("board mismatch after %q\n got %s\nwant %s", tc.command, got.toFenString(), tc.wantFen)
			}
		})
	}
}

// TestSetOption covers the multi-word-name setoption parser and each option's effect — none of which was
// exercised before.
func TestSetOption(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()

	game.HandleCommand("setoption name Move Overhead value 50")
	if game.moveOverhead != 50*time.Millisecond {
		t.Errorf("Move Overhead: got %v, want 50ms", game.moveOverhead)
	}

	game.HandleCommand("setoption name Threads value 4")
	if game.threadCount != 4 {
		t.Errorf("Threads: got %d, want 4", game.threadCount)
	}
	game.HandleCommand("setoption name Threads value 99999") // clamp
	if game.threadCount != maxSearchThreads {
		t.Errorf("Threads clamp: got %d, want %d", game.threadCount, maxSearchThreads)
	}

	game.HandleCommand("setoption name Hash value 1") // 1 MB -> 65536 entries (16 B each), power of two
	if got, want := len(game.transpositionTable.table), roundUpToPow2(1024*1024/16); got != want {
		t.Errorf("Hash resize: got %d entries, want %d", got, want)
	}

	game.HandleCommand("setoption name OwnBook value false")
	if game.useOwnBook {
		t.Error("OwnBook value false: still enabled")
	}

	// Clear Hash empties both the TT and the eval cache. Seed entries via a short search (book cleared so
	// it actually searches rather than returning a book move), then clear.
	game.newGameFromFen(startFen)
	game.openingBook.clear()
	game.timeControl.clockType = clockFixedDepth
	game.timeControl.depth = 6
	searchRootNode(game)
	if game.transpositionTable.hashfull() == 0 {
		t.Fatal("expected a populated TT after a search")
	}
	game.HandleCommand("setoption name Clear Hash")
	if hf := game.transpositionTable.hashfull(); hf != 0 {
		t.Errorf("Clear Hash: hashfull still %d", hf)
	}
}

// TestParseSetOption unit-tests the span-wise name/value split directly.
func TestParseSetOption(t *testing.T) {
	cases := []struct{ in, wantName, wantValue string }{
		{"name Move Overhead value 50", "Move Overhead", "50"},
		{"name Clear Hash", "Clear Hash", ""},
		{"name Threads value 8", "Threads", "8"},
		{"name EvalFile value /some path/net.bin", "EvalFile", "/some path/net.bin"},
	}
	for _, tc := range cases {
		gotName, gotValue := parseSetOption(strings.Fields(tc.in))
		if gotName != tc.wantName || gotValue != tc.wantValue {
			t.Errorf("parseSetOption(%q) = (%q, %q), want (%q, %q)", tc.in, gotName, gotValue, tc.wantName, tc.wantValue)
		}
	}
}

// TestMateDistance drives a real search and asserts the reported `score mate N` distance and best move.
// This is the guard on the mate-distance arithmetic (checkmatedScore + ply, and the score sign), which
// TestFormatScore only pins the *formatting* of. Searching a mate-in-1 deeper must still report mate 1
// (the shortest mate), not a longer distance.
func TestMateDistance(t *testing.T) {
	skipWithoutTheShippedNet(t)
	scoreRe := regexp.MustCompile(`score mate (-?\d+)`)
	cases := []struct {
		name, fen, wantMove string
		depth, wantMate     int
	}{
		// iterativeDeepen prints its first info line at startDepth+1 (=4), so a printed score needs depth>=4.
		{"white mates in 1", "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1", "a1a8", 4, 1},
		{"black mates in 1", "r3k3/8/8/8/8/8/5PPP/6K1 b - - 0 1", "a8a1", 4, 1},
		{"white mate-in-1 still reported as mate 1 at depth 6", "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1", "a1a8", 6, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			game := NewGame()
			game.newGameFromFen(tc.fen)
			game.openingBook.clear()
			game.timeControl.clockType = clockFixedDepth
			game.timeControl.depth = tc.depth
			var best move
			out := captureStdout(t, func() { best = searchRootNode(game) })
			if best.String() != tc.wantMove {
				t.Errorf("bestmove = %s, want %s", best.String(), tc.wantMove)
			}
			ms := scoreRe.FindAllStringSubmatch(out, -1)
			if len(ms) == 0 {
				t.Fatalf("no `score mate N` info line emitted; output:\n%s", out)
			}
			last := ms[len(ms)-1][1]
			if last != strconv.Itoa(tc.wantMate) {
				t.Errorf("final score = mate %s, want mate %d", last, tc.wantMate)
			}
		})
	}
}

// TestSearchRootNoLegalMove: a checkmated or stalemated side to move has no legal move, so searchRootNode
// must return moveNone (which the UCI layer prints as `bestmove 0000`) rather than crash.
func TestSearchRootNoLegalMove(t *testing.T) {
	cases := []struct{ name, fen string }{
		{"checkmate (fool's mate)", "rnb1kbnr/pppp1ppp/8/4p3/6Pq/5P2/PPPPP2P/RNBQKBNR w KQkq - 1 3"},
		{"stalemate", "7k/5Q2/6K1/8/8/8/8/8 b - - 0 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			game := NewGame()
			game.newGameFromFen(tc.fen)
			game.openingBook.clear()
			game.timeControl.clockType = clockFixedDepth
			game.timeControl.depth = 4
			if m := searchRootNode(game); !m.isNone() {
				t.Errorf("expected moveNone at a no-legal-move root, got %s", m.String())
			}
		})
	}
}

// TestMovegenAllocationFree pins the documented invariant that legal move generation into a stack
// moveList performs zero heap allocations — the property that fixed a ~3x perft regression and is fragile
// under escape analysis (one added field/call to moveList silently re-heaps it).
func TestMovegenAllocationFree(t *testing.T) {
	pos := positionFromFen("r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1") // Kiwipete
	if allocs := testing.AllocsPerRun(200, func() {
		var ml moveList
		pos.generateLegalMovesInto(&ml)
	}); allocs != 0 {
		t.Errorf("generateLegalMovesInto allocated %.1f times/run, want 0", allocs)
	}
}

// TestLoadNNUEFileRefusesWithoutFallingBack: a missing or refused EvalFile must return an error and
// leave NO evaluator installed — not the net that was loaded before it, and not the embedded one. The
// engine must then refuse to search loudly rather than play on with weights nobody asked for, which is
// how a match once reported a plausible Elo for a net that never played a move. EnableNNUE is the
// deliberate way back.
func TestLoadNNUEFileRefusesWithoutFallingBack(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	if err := game.LoadNNUEFile(filepath.Join(t.TempDir(), "does-not-exist.bin")); err == nil {
		t.Error("LoadNNUEFile on a missing path: expected an error")
	}
	if game.network != nil {
		t.Error("a missing EvalFile left an evaluator installed; a refusal must not fall back")
	}
	bad := filepath.Join(t.TempDir(), "garbage.bin")
	if err := os.WriteFile(bad, []byte("not an RNNUE1 net"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := game.LoadNNUEFile(bad); err == nil {
		t.Error("LoadNNUEFile on a malformed file: expected an error")
	}
	if game.network != nil {
		t.Error("a refused EvalFile left an evaluator installed; a refusal must not fall back")
	}

	// With no net installed the search refuses loudly and returns no move, rather than evaluating a
	// node with no network (which would crash on the first `go`).
	game.newGameFromFen(startFen)
	game.timeControl.clockType = clockFixedDepth
	game.timeControl.depth = 4
	var refused move
	out := captureStdout(t, func() { refused = searchRootNode(game) })
	if !refused.isNone() {
		t.Errorf("a search with no net installed returned %s, want no move", refused.String())
	}
	if !strings.Contains(out, "info string NNUE: no net loaded") {
		t.Errorf("a search with no net installed printed %q, want a loud refusal", out)
	}

	// EnableNNUE is the deliberate way back to the embedded net, and the engine searches again.
	if err := game.EnableNNUE(); err != nil {
		t.Fatalf("EnableNNUE after a refusal: %v", err)
	}
	if m := searchRootNode(game); m.isNone() {
		t.Error("engine cannot search after EnableNNUE restored the embedded net")
	}
}

// captureStdout redirects os.Stdout for the duration of f and returns what was written. Safe because this
// package's tests never run in parallel (they share the global search diagnostics), so there is no
// concurrent writer to os.Stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r) // reads the pipe to EOF
		done <- b.String()
	}()
	f()
	_ = w.Close()
	os.Stdout = old
	return <-done
}
