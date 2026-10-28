package engine

// In-process coverage for the UCI command paths a real GUI hits every game but the suite never
// exercised: replaying `position ... moves` (promotion / castling / en passant), setoption parsing
// (multi-word names), mate-distance reporting, root positions with no legal move, the allocation-free
// move-gen invariant, and the EvalFile error path. These drive the real Game via HandleCommand /
// searchRootNode (no subprocess), so they are fast and deterministic.

import (
	"fmt"
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

// TestLoadNNUEFileRefusesWithoutSubstituting: a missing or refused EvalFile must return an error and
// change nothing — the net already playing stays installed, networkOrigin still names it, and the
// engine goes on searching with it. That is what the container's no-substitution rule asks for: a
// refused net is never put into play, and a match can never report a plausible Elo for a net that
// never played a move, because keptNetMessage says on both streams which net did.
//
// This is the stronger property than the one this test asserted before (that the evaluator was
// UNINSTALLED). Uninstalling it fails the same rule from the other side: it leaves the engine with no
// defined play, answering `bestmove 0000` to every later `go`, so an operator who misses one message
// gets a thousand games of nothing rather than a thousand games of the previous net.
func TestLoadNNUEFileRefusesWithoutSubstituting(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	installed, origin := game.network, game.networkOrigin
	if installed == nil || origin != embeddedNetOrigin {
		t.Fatalf("a fresh Game has net %v from origin %q, want the embedded net", installed != nil, origin)
	}
	bad := filepath.Join(t.TempDir(), "garbage.bin")
	if err := os.WriteFile(bad, []byte("not an RNNUE1 net"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "does-not-exist.bin"), bad} {
		err := game.LoadNNUEFile(path)
		if err == nil {
			t.Errorf("LoadNNUEFile(%q): expected an error", path)
		}
		if game.network != installed || game.networkOrigin != origin {
			t.Errorf("LoadNNUEFile(%q) changed the installed net (origin now %q); a refused net must "+
				"leave the net already playing in place", path, game.networkOrigin)
		}
		// And the message an operator reads names the net still playing, not the refused one.
		kept := game.keptNetMessage(path)
		want := fmt.Sprintf("NNUE: keeping the net already loaded ('%s'); '%s' was refused and is not in play",
			origin, path)
		if kept != want {
			t.Errorf("keptNetMessage(%q) = %q, want %q", path, kept, want)
		}
	}

	// The engine still searches, with that net.
	game.newGameFromFen(startFen)
	game.timeControl.clockType = clockFixedDepth
	game.timeControl.depth = 4
	if m := searchRootNode(game); m.isNone() {
		t.Error("the engine stopped searching after a refused EvalFile; the kept net must still play")
	}

	// The no-net guard is the DEFENSIVE path now — reachable only when even the embedded net is
	// unusable (a corrupt build), which no EvalFile can cause. Force that state directly and check it
	// still refuses loudly rather than evaluating a node with no network.
	game.network, game.networkOrigin = nil, ""
	var refused move
	out := captureStdout(t, func() { refused = searchRootNode(game) })
	if !refused.isNone() {
		t.Errorf("a search with no net installed returned %s, want no move", refused.String())
	}
	if !strings.Contains(out, "info string NNUE: no net installed") {
		t.Errorf("a search with no net installed printed %q, want a loud refusal", out)
	}
	if kept := game.keptNetMessage(bad); !strings.Contains(kept, "no net is installed at all") {
		t.Errorf("with no net installed keptNetMessage said %q; it must not claim to keep a net", kept)
	}

	// EnableNNUE is the deliberate way back to the embedded net, and the engine searches again.
	if err := game.EnableNNUE(); err != nil {
		t.Fatalf("EnableNNUE after a refusal: %v", err)
	}
	if game.networkOrigin != embeddedNetOrigin {
		t.Errorf("EnableNNUE left networkOrigin %q, want %q", game.networkOrigin, embeddedNetOrigin)
	}
	if m := searchRootNode(game); m.isNone() {
		t.Error("engine cannot search after EnableNNUE restored the embedded net")
	}
}

// captureStdout redirects os.Stdout for the duration of f and returns what was written. Safe because this
// package's tests never run in parallel (they share the global search diagnostics), so there is no
// concurrent writer to os.Stdout. captureStderr does the same for os.Stderr.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	return captureStream(t, &os.Stdout, f)
}

// captureStderr is captureStdout for the other stream. The EvalFile refusal must reach both, because a
// match harness keeps stderr and routinely drops `info` lines — so a test that reads only stdout
// cannot show that the operator was told which net is playing.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	return captureStream(t, &os.Stderr, f)
}

// captureStream redirects *stream (os.Stdout or os.Stderr) for the duration of f and returns what was
// written to it.
func captureStream(t *testing.T, stream **os.File, f func()) string {
	t.Helper()
	old := *stream
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	*stream = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r) // reads the pipe to EOF
		done <- b.String()
	}()
	f()
	_ = w.Close()
	*stream = old
	return <-done
}
