package engine

// In-process coverage of the UCI dispatcher. uci_test.go drives the built binary as a subprocess, which
// proves the protocol end to end but is invisible to the coverage profile, so HandleCommand, the `go`
// parser, setoption, bench and the search goroutine's bestmove/ponder handshake are driven here through
// HandleCommand on a real Game, with stdout captured. Nothing here changes what the engine does: each test
// asserts the behaviour the protocol already has.

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"pawnstar-go/engine/nnue"
)

// runUCI sends each line to game.HandleCommand and returns everything printed, plus whether a line asked
// the engine to quit. After the last line it joins any search a `go` started (game.wg.Wait, not
// stopThinking, so a fixed-depth or book search runs to its own end rather than being cancelled), so the
// search goroutine's bestmove line is inside the captured output.
func runUCI(t *testing.T, game *Game, lines ...string) (out string, quit bool) {
	t.Helper()
	out = captureStdout(t, func() {
		for _, line := range lines {
			if game.HandleCommand(line) {
				quit = true
			}
		}
		game.wg.Wait()
	})
	return out, quit
}

// runUCIStreams is runUCI with stderr captured as well — for the EvalFile refusal, which must reach
// both streams because a match harness keeps stderr and routinely drops `info` lines. (The subprocess
// test engine/uci_evalfile_test.go proves the same of the real binary; this one is what the coverage
// profile sees.)
func runUCIStreams(t *testing.T, game *Game, lines ...string) (out, errOut string) {
	t.Helper()
	errOut = captureStderr(t, func() {
		out, _ = runUCI(t, game, lines...)
	})
	return out, errOut
}

// bestmoveOf returns the move named by the last `bestmove` line in out, or "" if there is none.
func bestmoveOf(out string) string {
	m := regexp.MustCompile(`(?m)^bestmove (\S+)`).FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// TestUCIHandshakeInProcess: `uci` names the engine and its options and ends with uciok, `isready` answers
// readyok, blank and unknown lines are ignored silently, and only `quit` reports quit.
func TestUCIHandshakeInProcess(t *testing.T) {
	game := NewGame()
	out, quit := runUCI(t, game, "", "   ", "frobnicate the knight", "uci", "isready")
	if quit {
		t.Fatal("a handshake line reported quit")
	}
	for _, want := range []string{
		"id name Pawnstar-Go " + versionString() + "\n",
		"id author Jonny Reckless\n",
		"option name OwnBook type check default true\n",
		"option name EvalFile type string default <embedded>\n",
		"option name Threads type spin default 1 min 1 max ",
		// The Hash default is pinned to the exact number, not just the prefix: the C++ and Rust ports
		// advertise 64 too, and the three are only bit-exact at a fixed depth while their table sizes
		// agree. `bench` pins 64 in all three, so it cannot see a default that has drifted - this
		// assertion is the only thing in this repo that can. Changing it means changing all three.
		"option name Hash type spin default 64 min 1 max 4096\n",
		"option name Clear Hash type button\n",
		"option name Move Overhead type spin default 30 min 0 max 5000\n",
		"option name Ponder type check default false\n",
		"uciok\nreadyok\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("handshake output lacks %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "frobnicate") {
		t.Errorf("an unknown command produced output:\n%s", out)
	}
	if _, quit := runUCI(t, game, "stop", "quit"); !quit {
		t.Error("quit did not report quit")
	}
}

// TestUCIPositionEdgeCases: the forms of `position` a GUI or a careless user sends that the replay test
// does not - no argument, an empty or malformed FEN, `moves` with nothing after it, an illegal move in
// the list (ignored, the rest replayed), and `ucinewgame` returning to the start.
func TestUCIPositionEdgeCases(t *testing.T) {
	start := positionFromFen(startFen)
	afterE4 := positionFromFen("rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1")
	cases := []struct {
		name, command string
		want          position
	}{
		{"bare position", "position", start},
		{"position fen with no FEN", "position fen", start},
		{"position fen garbage", "position fen xyz/abc w - - 0 1", start},
		{"position fen with no kings", "position fen 8/8/8/8/8/8/8/8 w - - 0 1", start},
		{"startpos moves with no moves", "position startpos moves", start},
		{"illegal move ignored", "position startpos moves e2e5 e2e4", afterE4},
		{"unknown position keyword", "position sideways", start},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			game := NewGame()
			game.HandleCommand("position startpos moves d2d4 d7d5") // something to replace
			game.HandleCommand(tc.command)
			if got := game.currentPosition(); got.hash != tc.want.hash || got.squares != tc.want.squares {
				t.Errorf("%q gave %s, want %s", tc.command, got.toFenString(), tc.want.toFenString())
			}
		})
	}

	game := NewGame()
	game.HandleCommand("position startpos moves e2e4 e7e5")
	game.HandleCommand("ucinewgame")
	if got := game.currentPosition(); got.hash != start.hash || len(game.posStack) != 1 {
		t.Errorf("ucinewgame left %s with %d positions on the stack", got.toFenString(), len(game.posStack))
	}
	if m := game.playMoveString("e1e8"); !m.isNone() {
		t.Errorf("playMoveString of an illegal move returned %s", m.String())
	}
}

// TestUCIGoParsing checks what each `go` keyword puts in the time control, the side-dependence of
// wtime/btime/winc/binc, the per-search reset of the limits, and that truncated or non-numeric values are
// ignored rather than crashing the engine. The start position and its first replies are in the opening
// book, so every search here returns at once with a book move and the test measures only the parsing.
func TestUCIGoParsing(t *testing.T) {
	skipWithoutTheShippedNet(t)
	cases := []struct {
		name, position, goLine string
		check                  func(t *testing.T, tc *timeControl)
	}{
		{"white clock", "position startpos", "go wtime 60000 btime 50000 winc 1000 binc 2000 movestogo 20",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockStandard || tc.timeRemaining != 60*time.Second ||
					tc.increment != time.Second || tc.numMovesRemaining != 20 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"black clock", "position startpos moves e2e4", "go wtime 60000 btime 50000 winc 1000 binc 2000",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockStandard || tc.timeRemaining != 50*time.Second ||
					tc.increment != 2*time.Second || tc.numMovesRemaining != 0 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"movetime", "position startpos", "go movetime 250",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockFixedTime || tc.timeRemaining != 250*time.Millisecond {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"depth", "position startpos", "go depth 7",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockFixedDepth || tc.depth != 7 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"nodes", "position startpos", "go nodes 5000",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockInfinite || tc.maxNodes != 5000 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"infinite", "position startpos", "go infinite",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockInfinite {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"truncated wtime keeps the default clock", "position startpos", "go wtime",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockStandard || tc.timeRemaining != 5*time.Minute {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"non-numeric values are ignored with their keywords", "position startpos",
			"go depth deep movestogo soon nodes many movetime later",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockStandard || tc.depth != 10 || tc.numMovesRemaining != 0 || tc.maxNodes != 0 ||
					tc.timeRemaining != 5*time.Minute {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"a malformed value does not swallow the rest of the line", "position startpos", "go movetime x depth 3",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockFixedDepth || tc.depth != 3 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"wtime with a malformed value keeps the default clock", "position startpos", "go wtime 12x",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockStandard || tc.timeRemaining != 5*time.Minute {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"a truncated nodes is not an unbounded search", "position startpos", "go nodes",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockStandard || tc.maxNodes != 0 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"a leading plus is read", "position startpos", "go depth +4",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockFixedDepth || tc.depth != 4 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"truncated after every keyword", "position startpos", "go binc",
			func(t *testing.T, tc *timeControl) {
				if tc.increment != 0 {
					t.Errorf("got %+v", *tc)
				}
			}},
		{"unknown keyword skipped", "position startpos", "go sideways depth 5",
			func(t *testing.T, tc *timeControl) {
				if tc.clockType != clockFixedDepth || tc.depth != 5 {
					t.Errorf("got %+v", *tc)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			game := NewGame()
			game.HandleCommand(c.position)
			legal := game.currentPosition().generateLegalMoves()
			out, _ := runUCI(t, game, c.goLine)
			c.check(t, &game.timeControl)
			best := bestmoveOf(out)
			if !slices.ContainsFunc(legal, func(m move) bool { return m.String() == best }) {
				t.Errorf("bestmove %q is not a legal move; output:\n%s", best, out)
			}
		})
	}

	// A `go` clears the previous search's limits: nodes, searchmoves, movestogo and the increment.
	game := NewGame()
	runUCI(t, game, "setoption name OwnBook value false", "position startpos",
		"go depth 3 searchmoves g1f3 b1c3")
	game.HandleCommand("position startpos")
	runUCI(t, game, "go nodes 10 movestogo 9 winc 500")
	game.HandleCommand("position startpos")
	runUCI(t, game, "setoption name OwnBook value true", "go movetime 10")
	if tc := game.timeControl; tc.searchMoves != nil || tc.maxNodes != 0 || tc.numMovesRemaining != 0 || tc.increment != 0 {
		t.Errorf("a bare go kept the previous limits: %+v", tc)
	}
}

// TestUCISearchMovesInProcess: `go searchmoves` restricts the root (skipping the book), and a list with
// no legal move in it is ignored rather than leaving the engine with nothing to search.
func TestUCISearchMovesInProcess(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	out, _ := runUCI(t, game, "position startpos", "go depth 4 searchmoves a2a3 h2h3")
	if best := bestmoveOf(out); best != "a2a3" && best != "h2h3" {
		t.Errorf("searchmoves a2a3 h2h3 gave bestmove %q; output:\n%s", best, out)
	}
	game.HandleCommand("position startpos")
	legal := game.currentPosition().generateLegalMoves()
	out, _ = runUCI(t, game, "go depth 4 searchmoves e2e5 a1a8")
	best := bestmoveOf(out)
	if !slices.ContainsFunc(legal, func(m move) bool { return m.String() == best }) {
		t.Errorf("an all-illegal searchmoves list gave bestmove %q; output:\n%s", best, out)
	}
}

// TestUCIGoSearchMovesStopsAtTheNextKeyword: the move list after `searchmoves` ends at the next recognised
// `go` keyword, which is then honoured - the rule the C++ and Rust parsers already had. Go used to append
// every remaining token to the move list and jump to the end of the line, so `go searchmoves e2e4 depth 10`
// set no depth limit at all and the search ran to the engine's own default depth (15) where the other two
// ports stopped at 10. Nothing caught it: the subprocess suite's SearchMoves case sends exactly that line
// and asserts only the bestmove, which the bug leaves right.
func TestUCIGoSearchMovesStopsAtTheNextKeyword(t *testing.T) {
	skipWithoutTheShippedNet(t)
	cases := []struct {
		name, command string
		wantMoves     []string
		wantClock     clockType
		wantDepth     int           // clockFixedDepth only
		wantNodes     int           // 0 = no node limit
		wantMovesToGo int           // 0 = none given
		wantRemaining time.Duration // clockFixedTime only
	}{
		{
			name: "depth after searchmoves", command: "go searchmoves e2e4 depth 10",
			wantMoves: []string{"e2e4"}, wantClock: clockFixedDepth, wantDepth: 10,
		},
		{
			name: "two moves then nodes", command: "go searchmoves e2e4 b1c3 nodes 2000",
			wantMoves: []string{"e2e4", "b1c3"}, wantClock: clockInfinite, wantNodes: 2000,
		},
		{
			name: "movetime and movestogo after searchmoves", command: "go searchmoves e2e4 movetime 20 movestogo 7",
			wantMoves: []string{"e2e4"}, wantClock: clockFixedTime, wantMovesToGo: 7, wantRemaining: 20 * time.Millisecond,
		},
		{
			// `searchmoves` terminates its own list and then starts collecting again, so the two lists join
			// (the C++ clears search_moves_ once at the top of handle_go and push_backs from there on).
			name: "a second searchmoves accumulates", command: "go searchmoves e2e4 searchmoves d2d4 movetime 20",
			wantMoves: []string{"e2e4", "d2d4"}, wantClock: clockFixedTime, wantRemaining: 20 * time.Millisecond,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			game := NewGame()
			out, _ := runUCI(t, game, "setoption name OwnBook value false", "position startpos", c.command)
			tc := game.timeControl
			if !slices.Equal(tc.searchMoves, c.wantMoves) {
				t.Errorf("searchMoves = %q, want %q", tc.searchMoves, c.wantMoves)
			}
			if tc.clockType != c.wantClock {
				t.Errorf("clockType = %d, want %d (the keyword after the move list was swallowed)", tc.clockType, c.wantClock)
			}
			if c.wantClock == clockFixedDepth && tc.depth != c.wantDepth {
				t.Errorf("depth = %d, want %d", tc.depth, c.wantDepth)
			}
			if tc.maxNodes != c.wantNodes {
				t.Errorf("maxNodes = %d, want %d", tc.maxNodes, c.wantNodes)
			}
			if tc.numMovesRemaining != c.wantMovesToGo {
				t.Errorf("numMovesRemaining = %d, want %d", tc.numMovesRemaining, c.wantMovesToGo)
			}
			if c.wantClock == clockFixedTime && tc.timeRemaining != c.wantRemaining {
				t.Errorf("timeRemaining = %v, want %v", tc.timeRemaining, c.wantRemaining)
			}
			if bestmoveOf(out) == "" {
				t.Errorf("no bestmove for %q; output:\n%s", c.command, out)
			}
		})
	}

	// The depth limit reaching the clock is not quite the whole claim: prove the search actually stops
	// there, by reading the deepest completed iteration out of the info lines.
	game := NewGame()
	out, _ := runUCI(t, game, "setoption name OwnBook value false", "position startpos",
		"go searchmoves e2e4 depth 10")
	deepest := 0
	for _, m := range regexp.MustCompile(`(?m)^info depth (\d+) `).FindAllStringSubmatch(out, -1) {
		if d, err := strconv.Atoi(m[1]); err == nil && d > deepest {
			deepest = d
		}
	}
	if deepest != 10 {
		t.Errorf("go searchmoves e2e4 depth 10 reached depth %d, want 10; output:\n%s", deepest, out)
	}
}

// TestUCIBestmoveForms covers the three shapes of the search goroutine's answer: `bestmove <m> ponder <r>`
// when the principal variation has a reply, plain `bestmove <m>` for a book move, and `bestmove 0000`
// when the side to move has no legal move.
func TestUCIBestmoveForms(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	out, _ := runUCI(t, game, "setoption name OwnBook value false", "position startpos", "go depth 5")
	if !regexp.MustCompile(`(?m)^bestmove [a-h][1-8][a-h][1-8] ponder [a-h][1-8][a-h][1-8]$`).MatchString(out) {
		t.Errorf("a depth-5 search did not answer `bestmove <m> ponder <r>`; output:\n%s", out)
	}
	if !strings.Contains(out, "info depth 5 ") {
		t.Errorf("no depth-5 info line; output:\n%s", out)
	}

	out, _ = runUCI(t, NewGame(), "position startpos", "go depth 5")
	if !regexp.MustCompile(`(?m)^bestmove [a-h][1-8][a-h][1-8]$`).MatchString(out) {
		t.Errorf("a book move was not answered with a plain bestmove; output:\n%s", out)
	}

	out, _ = runUCI(t, NewGame(), "position fen rnb1kbnr/pppp1ppp/8/4p3/6Pq/5P2/PPPPP2P/RNBQKBNR w KQkq - 1 3", "go depth 3")
	if bestmoveOf(out) != "0000" {
		t.Errorf("a checkmated side did not answer bestmove 0000; output:\n%s", out)
	}
}

// TestUCIPonderInProcess: a `go ponder` search that ends on its own (here, at once, with a book move) must
// hold its bestmove until the GUI resolves the ponder, by ponderhit or by stop; and a stray ponderhit with
// no ponder search running is harmless.
func TestUCIPonderInProcess(t *testing.T) {
	for _, resolve := range []string{"ponderhit", "stop"} {
		t.Run(resolve, func(t *testing.T) {
			game := NewGame()
			var pondering bool
			out := captureStdout(t, func() {
				game.HandleCommand("position startpos")
				game.HandleCommand("go ponder wtime 10000 btime 10000")
				time.Sleep(50 * time.Millisecond) // the book search has long finished: it is waiting on the GUI
				pondering = game.isPondering.Load()
				game.HandleCommand(resolve)
				game.wg.Wait()
			})
			if !pondering {
				t.Error("go ponder did not set the pondering flag")
			}
			if game.isPondering.Load() && resolve == "ponderhit" {
				t.Error("ponderhit left the pondering flag set")
			}
			if bestmoveOf(out) == "" {
				t.Errorf("no bestmove after %s; output:\n%s", resolve, out)
			}
		})
	}
	game := NewGame()
	if out, _ := runUCI(t, game, "ponderhit"); out != "" {
		t.Errorf("a ponderhit with no search printed %q", out)
	}
}

// TestUCIStopRacesInProcess: every command that stops a search, sent the instant after `go` with no pause,
// ends it with a move. The root search once cleared isCancelPending as it began, so a `stop` (or `quit`, a
// new `go`, `ucinewgame`) that reached stopThinking before the search goroutine got that far was lost: the
// search ran on and stopThinking waited in wg.Wait() for ever. Every iteration here hung before the fix, so
// the loop runs on a goroutine of its own and a hang fails the test rather than wedging the package.
func TestUCIStopRacesInProcess(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	runUCI(t, game, "setoption name OwnBook value false", "setoption name Threads value 1")
	cases := []struct {
		name  string
		lines []string // sent after `position startpos`, with no pause between them
		want  int      // the position stack's length once they are handled and any search is joined
	}{
		{"go infinite + stop", []string{"go infinite", "stop"}, 2},
		{"go ponder + stop", []string{"go ponder", "stop"}, 2},
		{"go infinite + go depth 1", []string{"go infinite", "go depth 1"}, 3},
		{"go infinite + ucinewgame", []string{"go infinite", "ucinewgame"}, 1},
		{"go infinite + quit", []string{"go infinite", "quit"}, 2},
	}
	const iterations, helperIterations = 300, 100
	answered := make([]int, len(cases))
	helpersAnswered := 0
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		captureStdout(t, func() {
			for range iterations {
				for c, tc := range cases {
					game.HandleCommand("position startpos")
					for _, line := range tc.lines {
						game.HandleCommand(line)
					}
					game.stopThinking() // joins the search (a no-op when a line above already did)
					if len(game.posStack) == tc.want {
						answered[c]++
					}
				}
			}
			// Lazy SMP: a stop that lands before the main search has launched its helpers, or while they run.
			game.HandleCommand("setoption name Threads value 3")
			for range helperIterations {
				game.HandleCommand("position startpos")
				game.HandleCommand("go infinite")
				game.HandleCommand("stop")
				if len(game.posStack) == 2 {
					helpersAnswered++
				}
			}
		})
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Minute):
		t.Fatal("a stop sent at once after go was lost: the search never ended")
	}
	for c, tc := range cases {
		if answered[c] != iterations {
			t.Errorf("%s at once: %d/%d answered", tc.name, answered[c], iterations)
		}
	}
	if helpersAnswered != helperIterations {
		t.Errorf("Threads 3, go infinite + stop at once: %d/%d answered", helpersAnswered, helperIterations)
	}
}

// TestUCIStandardClockSearch runs a real search on a short standard clock, so the soft-time management
// after each iteration (best move consistent, score stable, budget spent) is what ends it, well inside the
// hard deadline.
func TestUCIStandardClockSearch(t *testing.T) {
	game := NewGame()
	start := time.Now()
	out, _ := runUCI(t, game, "setoption name OwnBook value false",
		"position fen r1bqkbnr/pppp1ppp/2n5/4p3/4P3/5N2/PPPP1PPP/RNBQKB1R w KQkq - 2 3", "go wtime 2000 btime 2000")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a 2 s clock took %v to answer", elapsed)
	}
	if bestmoveOf(out) == "" {
		t.Errorf("no bestmove; output:\n%s", out)
	}
}

// TestSearchReportsCurrmove: once a search has run for more than three seconds the root reports each move
// it is searching (`info ... currmove`). The clock origin is set ten seconds back rather than waiting.
func TestSearchReportsCurrmove(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	game.openingBook.clear()
	game.timeControl.clockType = clockFixedDepth
	game.timeControl.depth = 4
	game.searchStartNanos.Store(time.Now().Add(-10 * time.Second).UnixNano())
	out := captureStdout(t, func() { searchRootNode(game) })
	if !regexp.MustCompile(`info depth 4 currmove [a-h][1-8][a-h][1-8] currmovenumber 1\n`).MatchString(out) {
		t.Errorf("no currmove line from a search older than 3 s; output:\n%s", out)
	}
}

// TestUCISetOptionInProcess covers every option's handling of good, clamped and malformed values, and
// loading a net from a file and returning to the embedded one.
func TestUCISetOptionInProcess(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	embedded := nnue.Embedded()

	out, _ := runUCI(t, game, "setoption name EvalFile value nnue/pawnstar-v20.bin")
	if !strings.Contains(out, "info string NNUE net loaded from nnue/pawnstar-v20.bin") || game.network == embedded {
		t.Errorf("loading the net from its file: output %q, still embedded %v", out, game.network == embedded)
	}
	for _, value := range []string{" value <embedded>", " value default", " value DEFAULT", ""} {
		runUCI(t, game, "setoption name EvalFile value nnue/pawnstar-v20.bin")
		if out, _ := runUCI(t, game, "setoption name EvalFile"+value); out != "" || game.network != embedded {
			t.Errorf("EvalFile%q: output %q, embedded restored %v", value, out, game.network == embedded)
		}
	}
	// A refused EvalFile reports why on BOTH streams, names the net still playing, and keeps it
	// installed — see LoadNNUEFile for why keeping it, rather than uninstalling the evaluator, is what
	// the container's no-substitution rule asks for. The engine must go on searching with that net.
	runUCI(t, game, "setoption name EvalFile value <embedded>")
	installed, origin := game.network, game.networkOrigin
	out, errOut := runUCIStreams(t, game, "setoption name EvalFile value no/such/net.bin")
	wantKept := "NNUE: keeping the net already loaded ('" + origin + "'); 'no/such/net.bin' was refused and is not in play"
	for _, s := range []struct{ name, text, prefix string }{
		{"stdout", out, "info string "}, // stderr carries the same text without the prefix
		{"stderr", errOut, ""},
	} {
		for _, want := range []string{s.prefix + `EvalFile "no/such/net.bin": `, s.prefix + wantKept} {
			if !strings.Contains(s.text, want) {
				t.Errorf("a refused EvalFile printed %q on %s, want it to contain %q", s.text, s.name, want)
			}
		}
	}
	if game.network != installed || game.networkOrigin != origin {
		t.Errorf("a refused EvalFile changed the installed net (origin now %q, was %q)", game.networkOrigin, origin)
	}
	game.timeControl.clockType = clockFixedDepth
	game.timeControl.depth = 4
	if m := searchRootNode(game); m.isNone() {
		t.Error("the engine stopped searching after a refused EvalFile; the kept net must still play")
	}

	out, _ = runUCI(t, game, "setoption name Threads value 3", "setoption name Threads value lots")
	if game.threadCount != 3 || out != "info string Threads set to 3\n" {
		t.Errorf("Threads: %d, output %q", game.threadCount, out)
	}

	out, _ = runUCI(t, game, "setoption name Hash value 0", "setoption name Hash value big")
	if out != "info string Hash set to 1 MB\n" || len(game.transpositionTable.table) != roundUpToPow2(1024*1024/16) {
		t.Errorf("Hash 0: %d entries, output %q", len(game.transpositionTable.table), out)
	}

	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"-5", 0}, {"99999", 5 * time.Second}, {"soon", 5 * time.Second}, {"120", 120 * time.Millisecond}} {
		game.HandleCommand("setoption name Move Overhead value " + tc.value)
		if game.moveOverhead != tc.want {
			t.Errorf("Move Overhead %s: got %v, want %v", tc.value, game.moveOverhead, tc.want)
		}
	}

	game.HandleCommand("setoption name OwnBook value false")
	game.HandleCommand("setoption name OwnBook value true")
	if !game.useOwnBook {
		t.Error("OwnBook value true: still disabled")
	}

	// Unknown options and a setoption with no name are ignored silently.
	if out, _ := runUCI(t, game, "setoption name Contempt value 20", "setoption", "setoption value 3"); out != "" {
		t.Errorf("an unknown option printed %q", out)
	}
	if name, value := parseSetOption([]string{"Hash", "value", "8"}); name != "" || value != "" {
		t.Errorf("parseSetOption with no name keyword = (%q, %q), want empty", name, value)
	}
}

// TestUCIBenchInProcess: `bench <depth>` searches the bench positions single-threaded with the book off
// and a pinned table, prints "<nodes> nodes <nps> nps", gives the same count every run, and leaves the
// caller's Threads, OwnBook and Hash as they were. PAWNSTAR_BENCH_DEBUG adds each position's best move and
// the diagnostic counters.
func TestUCIBenchInProcess(t *testing.T) {
	skipWithoutTheShippedNet(t)
	game := NewGame()
	game.HandleCommand("setoption name Threads value 3")
	game.HandleCommand("setoption name Hash value 1")
	game.HandleCommand("setoption name OwnBook value true")
	tableBefore := &game.transpositionTable.table[0]

	re := regexp.MustCompile(`(?m)^(\d+) nodes \d+ nps$`)
	first, _ := runUCI(t, game, "bench 4")
	second, _ := runUCI(t, game, "bench 4")
	m1, m2 := re.FindStringSubmatch(first), re.FindStringSubmatch(second)
	if m1 == nil || m2 == nil {
		t.Fatalf("bench did not print `<nodes> nodes <nps> nps`:\n%s\n%s", first, second)
	}
	if m1[1] != m2[1] || m1[1] == "0" {
		t.Errorf("bench 4 gave %s then %s nodes", m1[1], m2[1])
	}
	if game.threadCount != 3 || !game.useOwnBook || &game.transpositionTable.table[0] != tableBefore {
		t.Errorf("bench did not restore the caller's settings: threads %d, book %v, same table %v",
			game.threadCount, game.useOwnBook, &game.transpositionTable.table[0] == tableBefore)
	}

	t.Setenv("PAWNSTAR_BENCH_DEBUG", "1")
	out, _ := runUCI(t, game, "bench 2")
	for _, want := range []string{"### pos 01 bm=", "### pos 16 bm=", "alpha beta calls"} {
		if !strings.Contains(out, want) {
			t.Errorf("PAWNSTAR_BENCH_DEBUG output lacks %q", want)
		}
	}
	if re.FindString(out) == "" {
		t.Errorf("debug bench lost its summary line:\n%s", out)
	}
}

// TestMoveFormatting pins the two text forms of a move: UCI's (with "0000" for no move) and the debug form
// GoString gives %#v.
func TestMoveFormatting(t *testing.T) {
	e2, e4, d7, c8, d8 := squareFromString("e2"), squareFromString("e4"), squareFromString("d7"), squareFromString("c8"), squareFromString("d8")
	cases := []struct {
		m             move
		uci, goString string
	}{
		{moveNone(), "0000", "NONE"},
		{moveDoublePush(e2, e4), "e2e4", "Pe2e4"},
		{movePromotion(d7, c8, queen, bishop), "d7c8q", "Pd7c8xB=Q"},
		{movePromotion(d7, d8, knight, noPiece), "d7d8n", "Pd7d8=N"},
	}
	for _, tc := range cases {
		if got := tc.m.String(); got != tc.uci {
			t.Errorf("String() = %q, want %q", got, tc.uci)
		}
		if got := tc.m.GoString(); !strings.EqualFold(got, tc.goString) {
			t.Errorf("GoString() = %q, want %q (any case)", got, tc.goString)
		}
	}
}

// TestRootStabilityHelpers: the soft-time early exits rest on these two predicates over the best move of
// every completed depth.
func TestRootStabilityHelpers(t *testing.T) {
	a := moveDoublePush(squareFromString("e2"), squareFromString("e4"))
	b := moveDoublePush(squareFromString("d2"), squareFromString("d4"))
	if !allSame([]move{a.withScore(10), a.withScore(90)}, a) || allSame([]move{a, b}, a) || !allSame(nil, a) {
		t.Error("allSame compares move identity, ignoring the score")
	}
	stable := []move{a.withScore(10), b.withScore(10 + scoreInstabilityThresh)}
	if !allScoreStable(stable, a.withScore(10)) {
		t.Error("allScoreStable: scores within the threshold reported unstable")
	}
	if allScoreStable(append(stable, a.withScore(11+scoreInstabilityThresh)), a.withScore(10)) {
		t.Error("allScoreStable: a score past the threshold reported stable")
	}
}

// TestSmallHelpersEdgeCases covers the guards on untrusted or degenerate input in the small helpers.
func TestSmallHelpersEdgeCases(t *testing.T) {
	if sq := squareFromString("e"); sq != noSquare {
		t.Errorf("squareFromString(\"e\") = %v, want noSquare", sq)
	}
	for n, want := range map[int]int{-3: 1, 0: 1, 1: 1, 2: 2, 3: 4, 5: 8, 1024: 1024} {
		if got := roundUpToPow2(n); got != want {
			t.Errorf("roundUpToPow2(%d) = %d, want %d", n, got, want)
		}
	}
	var empty transpositionTable
	if hf := empty.hashfull(); hf != 0 {
		t.Errorf("hashfull of an empty table = %d", hf)
	}
	if bb := pieceAttacks(pawn, 0, squareFromString("e4")); bb != 0 {
		t.Errorf("pieceAttacks(pawn) = %v, want 0 (pawns are handled by colour elsewhere)", bb)
	}
}

// TestLoadOpeningBookMalformedLines: blank lines are skipped and a line stops at its first token that is
// not a legal move, keeping the candidates recorded before it.
func TestLoadOpeningBookMalformedLines(t *testing.T) {
	book := loadOpeningBook(strings.NewReader("\n   \ne2e4 e7e5\nd2d4 zz9 d7d5\n"))
	start := positionFromFen(startFen)
	got := make([]string, 0, 2)
	for _, m := range book.moves[start.hash] {
		got = append(got, m.String())
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"d2d4", "e2e4"}) {
		t.Errorf("start-position candidates = %v, want [d2d4 e2e4]", got)
	}
	afterD4 := positionFromFen("rnbqkbnr/pppppppp/8/8/3P4/8/PPP1PPPP/RNBQKBNR b KQkq - 0 1")
	if n := len(book.moves[afterD4.hash]); n != 0 {
		t.Errorf("the line was not cut at its illegal token: %d candidates after d2d4", n)
	}
	if len(book.moves) != 2 { // the start position and the one after e2e4
		t.Errorf("book holds %d positions, want 2", len(book.moves))
	}
}

// TestUCISearchesAFenClaimingUnsupportedCastlingRights searches the FEN of issue #30 on a table the
// searches before it have warmed, which is how it died: it claims KQ with White's rooks on c1 and g1,
// and once the search moved the c1 rook away the generator emitted e1c1 and makeMove moved a rook from
// an empty a1 - "panic: runtime error: index out of range [255] with length 6" in removePiece, at depth
// 8, on the eleventh of sixteen positions driven over UCI (an access violation in the C++ engine, where
// the issue was filed). The rights are dropped at the parse now, so what this asserts is the protocol's
// own promise: the search finishes and answers with a legal move.
func TestUCISearchesAFenClaimingUnsupportedCastlingRights(t *testing.T) {
	// A real search needs a net this build can read, and at the retired v12 shape this tree has
	// none - the parse rule above is what holds the invariant at either shape.
	skipWithoutTheShippedNet(t)
	const issue30Fen = "b2rk3/p2p1nr1/1p2n1p1/1Np2p1p/2P2P1P/1P1BP1P1/P4N2/2R1K1R1 w KQ - 1 16"
	game := NewGame()
	// Warm the transposition table, which the bug needed: on a cold table the castling is never
	// reached and the same FEN alone is harmless at any depth. The table persists across `position`.
	for _, fen := range benchFens[:10] {
		runUCI(t, game, "position fen "+fen, "go depth 8")
	}
	out, _ := runUCI(t, game, "position fen "+issue30Fen, "go depth 8")
	best := bestmoveOf(out)
	if best == "" {
		t.Fatalf("go depth 8 on a FEN claiming castling rights the board cannot support gave no bestmove:\n%s", out)
	}
	pos := positionFromFen(issue30Fen)
	isLegal := false
	for _, m := range pos.generateLegalMoves() {
		if m.String() == best {
			isLegal = true
		}
	}
	if !isLegal {
		t.Errorf("bestmove %s is not a legal move of %s", best, issue30Fen)
	}
}
