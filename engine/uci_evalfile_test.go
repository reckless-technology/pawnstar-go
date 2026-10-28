package engine

// `setoption name EvalFile <incompatible net>` driven through the REAL binary, both streams captured.
//
// This is the trap the test exists for: a match harness sets EvalFile, sees readyok, and runs a
// thousand games attributing the result to a net that never played a move. Nothing short of a real
// process can prove the fix, because the proof is about the routing — the refusal must reach stderr,
// which a harness keeps, as well as stdout, where it is an `info` line a harness routinely drops —
// and about the engine still playing, with the net it already had, after the refusal.
//
// The in-process cases (engine/uci_inprocess_test.go, engine/uci_coverage_test.go) cover the same
// behaviour through Game and are what the coverage profile sees; this one covers what they cannot.

import (
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// RNNUE1 header offsets this test needs, and the width the engine is built for. They mirror
// engine/nnue/nnue.go's own (unexported) constants and engine/nnue/load_test.go's headerWith helper,
// which is in the nnue package's test binary and so cannot be called from here. Drift is not silent:
// a wrong offset makes the mutated net fail some OTHER check, and the assertion below — which names
// hidden_size and both values — fails loudly rather than passing for the wrong reason.
const (
	rnnue1OffsetHiddenSize = 16   // uint16
	rnnue1OffsetHeaderCrc  = 56   // uint64, over bytes 0..55
	rnnue1HiddenSizeWrong  = 512  // a width this engine is not
	rnnue1HiddenSizeEngine = 1024 // engine/nnue's own hiddenSize, which is unexported
)

// writeIncompatibleNet copies the shipped net, sets hidden_size to a width this build is not, and
// recomputes header_crc over bytes 0..55 so the ARCHITECTURE check is what refuses the file rather
// than the header CRC. Returns the path written.
//
// crc64.ECMA with Go's hash/crc64 already IS CRC-64/XZ — Update complements the running value on
// entry and on return, so the all-ones init and final XOR the catalogue calls for are applied for
// you. Adding them by hand double-complements both (see TestCrc64XZCheckValue in engine/nnue).
func writeIncompatibleNet(t *testing.T) string {
	t.Helper()
	shipped, err := os.ReadFile(filepath.Join("nnue", "pawnstar-v12.bin"))
	if err != nil {
		t.Fatalf("read the shipped net: %v", err)
	}
	net := append([]byte(nil), shipped...)
	binary.LittleEndian.PutUint16(net[rnnue1OffsetHiddenSize:], rnnue1HiddenSizeWrong)
	binary.LittleEndian.PutUint64(net[rnnue1OffsetHeaderCrc:],
		crc64.Checksum(net[:rnnue1OffsetHeaderCrc], crc64.MakeTable(crc64.ECMA)))
	path := filepath.Join(t.TempDir(), "incompatible-hidden-size.bin")
	if err := os.WriteFile(path, net, 0o644); err != nil {
		t.Fatalf("write the incompatible net: %v", err)
	}
	return path
}

// reInfoNodes matches the cumulative node count on an iteration-summary info line.
var reInfoNodes = regexp.MustCompile(`^info depth [0-9]+ seldepth [0-9]+ .* nodes ([0-9]+) `)

// searchFromStartpos runs one fixed-depth search from the start position and returns the bestmove and
// the final cumulative node count.
//
// `Clear Hash` first and `isready` before the `go`: the TT and the eval cache deliberately persist
// across searches in one session (see CLAUDE.md, "ucinewgame does not clear the TT/eval cache"), so
// without clearing them a repeat of the same search returns a smaller, non-comparable node count. And
// the search is read to its `bestmove` rather than abandoned — a `quit` straight after `go` cancels
// the search and still answers `bestmove`, which would prove nothing at all.
func (e *uciEngine) searchFromStartpos(t *testing.T, depth int) (bestmove string, nodes int) {
	t.Helper()
	e.send("setoption name Clear Hash")
	e.send("position startpos")
	e.send("isready")
	if _, ok := e.readUntil(reReadyok, 30*time.Second); !ok {
		t.Fatalf("no readyok before `go depth %d`", depth)
	}
	e.send(fmt.Sprintf("go depth %d", depth))
	// Read to ANY bestmove, then insist it names a move: an engine with no evaluator answers
	// `bestmove 0000`, and waiting for a move-shaped line instead would only time out slowly.
	out, ok := e.readUntil(reBestmove, 120*time.Second)
	if !ok {
		t.Fatalf("go depth %d: no bestmove within the timeout; got:\n%s", depth, strings.Join(out, "\n"))
	}
	for _, line := range out {
		if mm := reInfoNodes.FindStringSubmatch(line); mm != nil {
			nodes, _ = strconv.Atoi(mm[1])
		}
		if reBestmove.MatchString(line) {
			bestmove = strings.Fields(line)[1]
		}
	}
	if !reBestmoveMv.MatchString("bestmove " + bestmove) {
		t.Fatalf("go depth %d answered `bestmove %s` \u2014 not a move, so no search happened; got:\n%s",
			depth, bestmove, strings.Join(out, "\n"))
	}
	if nodes == 0 {
		t.Fatalf("go depth %d produced no info line with a node count, so no search can be shown to "+
			"have happened; got:\n%s", depth, strings.Join(out, "\n"))
	}
	return bestmove, nodes
}

// waitForStderrLine reports whether want appears, exactly, among the engine's stderr lines from index
// from onwards, waiting up to timeout for the stderr pump goroutine to deliver it.
func (e *uciEngine) waitForStderrLine(want string, from int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		for _, line := range e.stderrSoFar()[from:] {
			if line == want {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestUCIRefusedEvalFileKeepsTheNetPlaying drives the built binary as a GUI would and asserts the whole
// decided behaviour of a refused `setoption name EvalFile`:
//
//  1. the loader's refusal, naming the field, the file's value and the engine's value, appears on
//     stdout as an `info string` AND on stderr (the same text, without the prefix);
//  2. the kept-net line appears on both streams and names the net still playing;
//  3. the engine still searches, with that same net — the same bestmove and the identical node count
//     as a search taken before the refusal, which an engine playing different weights could not give.
func TestUCIRefusedEvalFileKeepsTheNetPlaying(t *testing.T) {
	skipWithoutTheShippedNet(t)
	if testing.Short() {
		t.Skip("UCI subprocess test skipped in -short mode")
	}
	// Absolute paths: the engine echoes back what it was given, so the expected message is exact.
	good, err := filepath.Abs(filepath.Join("nnue", "pawnstar-v12.bin"))
	if err != nil {
		t.Fatalf("absolute path of the shipped net: %v", err)
	}
	bad := writeIncompatibleNet(t)

	e := startEngineCapturingStderr(t)
	defer e.close()
	e.send("uci")
	if _, ok := e.readUntil(reUciok, 30*time.Second); !ok {
		t.Fatalf("uci: no uciok within the timeout")
	}
	e.send("setoption name Threads value 1")
	e.send("setoption name OwnBook value false") // a book move would return before any search

	// A good net, loaded from a file and acknowledged.
	e.send("setoption name EvalFile value " + good)
	e.send("isready")
	loaded, ok := e.readUntil(reReadyok, 30*time.Second)
	if !ok {
		t.Fatalf("no readyok after loading %s", good)
	}
	if !anyLineMatches(loaded, regexp.MustCompile(`^info string NNUE net loaded from `)) {
		t.Fatalf("loading the shipped net was not acknowledged; got:\n%s", strings.Join(loaded, "\n"))
	}

	// The search this net plays, before anything is refused.
	wantMove, wantNodes := e.searchFromStartpos(t, 10)
	t.Logf("before the refused EvalFile: bestmove %s over %d nodes", wantMove, wantNodes)

	// Now the incompatible net. readyok is the harness's cue that the option was applied, so every
	// line the refusal prints must be on the wire before it.
	stderrBefore := len(e.stderrSoFar())
	e.send("setoption name EvalFile value " + bad)
	e.send("isready")
	refusal, ok := e.readUntil(reReadyok, 30*time.Second)
	if !ok {
		t.Fatalf("no readyok after the refused EvalFile — the engine must stay responsive")
	}
	wantReason := fmt.Sprintf(`info string EvalFile %q: nnue: net refused: hidden_size is %d in the file but %d in this engine`,
		bad, rnnue1HiddenSizeWrong, rnnue1HiddenSizeEngine)
	wantKept := fmt.Sprintf("info string NNUE: keeping the net already loaded ('%s'); '%s' was refused and is not in play",
		good, bad)
	for _, want := range []string{wantReason, wantKept} {
		if !anyLineMatches(refusal, regexp.MustCompile(`^`+regexp.QuoteMeta(want)+`$`)) {
			t.Errorf("a refused EvalFile did not print on stdout:\n  %s\ngot:\n%s",
				want, strings.Join(refusal, "\n"))
		}
	}
	// Both lines must also be on stderr, without the `info string ` prefix: a match harness keeps
	// stderr and routinely drops info lines, so stdout alone leaves the operator guessing.
	//
	// Polled rather than read once: stdout and stderr are separate pipes drained by separate
	// goroutines, so seeing readyok on stdout does not mean the stderr lines have been scanned yet.
	// Reading once here would be a flake waiting for a loaded machine.
	for _, want := range []string{wantReason, wantKept} {
		bare := strings.TrimPrefix(want, "info string ")
		if !e.waitForStderrLine(bare, stderrBefore, 10*time.Second) {
			t.Errorf("a refused EvalFile did not print on stderr:\n  %s\ngot:\n%s",
				bare, strings.Join(e.stderrSoFar()[stderrBefore:], "\n"))
		}
	}

	// And the engine plays on with the net it already had: same move, same tree.
	gotMove, gotNodes := e.searchFromStartpos(t, 10)
	if gotMove != wantMove || gotNodes != wantNodes {
		t.Errorf("after the refused EvalFile the engine searched differently: bestmove %s over %d nodes, "+
			"want %s over %d — the kept net must be the net that was already playing",
			gotMove, gotNodes, wantMove, wantNodes)
	} else {
		t.Logf("after the refused EvalFile: bestmove %s over %d nodes — unchanged", gotMove, gotNodes)
	}
}
