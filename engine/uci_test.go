package engine

// UCI protocol regression test. Drives the actual engine binary over stdin/stdout with synchronized I/O
// (read the engine's responses before sending the next command, so `quit` never cancels a running search)
// and asserts on the protocol behaviour.
//
// This is a faithful port of the former test/uci_test.sh (now removed); `make test-uci` runs this test.
// Every assertion the script made is reproduced here (see the section comments, which map 1:1 onto the
// numbered checks in the original script). These features (seldepth / currmove / searchmoves, the
// uciok/readyok handshake, bench determinism, the ponder protocol) are about the engine's behaviour *as a
// UCI process*, so the only faithful test drives the built binary the way a GUI does — hence a subprocess.
//
// The build REQUIRES GOEXPERIMENT=simd (the NNUE SIMD kernels), which is set on the `go build` invocation
// in TestMain.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The UCI tests drive the built engine binary as a subprocess. Because this test lives in the engine
// package, the binary is built lazily (once, on first use) rather than eagerly in TestMain — so the many
// other `go test ./engine/` invocations (e.g. make check's per-test sub-targets, quick dev runs) don't
// pay for a binary build they never use, and `-short` skips it entirely. The build targets the root main
// package ("pawnstar-go", CWD-independent) and requires GOEXPERIMENT=simd for the NNUE SIMD kernels.
var (
	uciBinOnce sync.Once
	uciBinPath string
	uciBinDir  string
	uciBinErr  error
)

func buildEngineBinary() {
	uciBinDir, uciBinErr = os.MkdirTemp("", "pawnstar-uci")
	if uciBinErr != nil {
		return
	}
	bin := filepath.Join(uciBinDir, "pawnstar-go-uci-test")
	if runtime.GOOS == "windows" {
		bin += ".exe" // name the output explicitly so the built path and the exec path agree on Windows
	}
	build := exec.Command("go", "build", "-o", bin, "pawnstar-go") // the root main package (this test is in engine/)
	build.Env = append(os.Environ(), "GOEXPERIMENT=simd")
	if out, err := build.CombinedOutput(); err != nil {
		uciBinErr = fmt.Errorf("go build failed: %v\n%s", err, out)
		return
	}
	uciBinPath = bin
}

// engineBinary returns the path to the engine binary, building it once (lazily) on first call.
func engineBinary(t *testing.T) string {
	t.Helper()
	uciBinOnce.Do(buildEngineBinary)
	if uciBinErr != nil {
		t.Fatalf("build engine binary: %v", uciBinErr)
	}
	return uciBinPath
}

// TestMain removes the lazily-built binary's temp dir after the package's tests finish (a no-op when no
// UCI test ran and the binary was never built).
func TestMain(m *testing.M) {
	code := m.Run()
	if uciBinDir != "" {
		_ = os.RemoveAll(uciBinDir)
	}
	os.Exit(code)
}

// uciEngine is a running engine subprocess we can send commands to and read responses from synchronously.
type uciEngine struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string // stdout lines delivered by a background goroutine
}

// startEngine launches the engine pinned to a single core / single thread (so searches are deterministic
// and cheap), mirroring the bash coproc's `GOMAXPROCS=1 PAWNSTAR_THREADS=1`.
func startEngine(t *testing.T) *uciEngine {
	t.Helper()
	cmd := exec.Command(engineBinary(t))
	cmd.Env = append(os.Environ(), "GOMAXPROCS=1", "PAWNSTAR_THREADS=1")
	cmd.Stderr = nil // discard stderr, like the script's 2>/dev/null

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("startEngine: StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("startEngine: StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("startEngine: Start: %v", err)
	}

	e := &uciEngine{
		t:     t,
		cmd:   cmd,
		stdin: stdin,
		lines: make(chan string, 1024),
	}
	// Pump stdout lines into the channel so readUntil can select on a timeout rather than block forever.
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			e.lines <- sc.Text()
		}
		close(e.lines)
	}()
	return e
}

// send writes a UCI command line to the engine.
func (e *uciEngine) send(cmd string) {
	e.t.Helper()
	if _, err := io.WriteString(e.stdin, cmd+"\n"); err != nil {
		e.t.Fatalf("send(%q): %v", cmd, err)
	}
}

// readUntil reads lines until one matches re (returning all lines read, inclusive of the matching one) or
// the timeout elapses (returning what was read so far and matched=false). Equivalent to the script's
// read_until: it accumulates every line and stops at the first match.
func (e *uciEngine) readUntil(re *regexp.Regexp, timeout time.Duration) (lines []string, matched bool) {
	e.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-e.lines:
			if !ok {
				return lines, false // engine exited
			}
			lines = append(lines, line)
			if re.MatchString(line) {
				return lines, true
			}
		case <-deadline:
			return lines, false
		}
	}
}

// close shuts the engine down (quit + drain + wait), tolerating an already-exited or wedged process.
//
// The drain is mandatory, not tidiness. Cmd.StdoutPipe's contract is explicit: "it is incorrect to call
// Wait before all reads from the pipe have completed." The stdout pump goroutine blocks on `e.lines <- ...`
// once that buffered channel fills, which stops draining the engine's stdout, which blocks the ENGINE on a
// full pipe, which means it never exits, which means Wait() blocks forever — a three-way deadlock that
// wedges the whole package until go test's 10-minute timeout kills it. The window is easy to hit: any test
// that stops reading while a search is still emitting `info` lines (a failed assertion, or simply a slow
// CI runner) leaves output in flight at teardown.
//
// So: ask it to quit, close stdin, drain stdout to EOF so the pump can finish and the engine can exit, and
// only then Wait. If the engine ignores `quit` (or is hung), kill it rather than wedge the suite.
// Errors are deliberately ignored — the process may already be gone, and close() runs from a defer, where
// t.Fatalf would fire on the wrong goroutine.
func (e *uciEngine) close() {
	_, _ = io.WriteString(e.stdin, "quit\n")
	_ = e.stdin.Close()

	drained := make(chan struct{})
	go func() {
		for range e.lines { // to EOF: lets the pump goroutine return and the engine flush and exit
		}
		close(drained)
	}()

	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		_ = e.cmd.Process.Kill() // ignored quit / hung: killing closes stdout, so the drain above completes
		<-drained
	}
	_ = e.cmd.Wait()
}

// anyLineMatches reports whether any line satisfies re.
func anyLineMatches(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

var (
	reUciok        = regexp.MustCompile(`^uciok`)
	reReadyok      = regexp.MustCompile(`^readyok`)
	reBestmove     = regexp.MustCompile(`^bestmove`)
	reBestmoveMv   = regexp.MustCompile(`^bestmove [a-h][1-8][a-h][1-8]`)
	reIdName       = regexp.MustCompile(`^id name Pawnstar`)
	reThreadsOpt   = regexp.MustCompile(`option name Threads`)
	rePonderOpt    = regexp.MustCompile(`option name Ponder`)
	reInfoDepth    = regexp.MustCompile(`^info depth`)
	reCurrmove     = regexp.MustCompile(`currmove [a-h][1-8][a-h][1-8].* currmovenumber [0-9]`)
	reBestmoveA2A3 = regexp.MustCompile(`^bestmove a2a3`)
	reSeldepth     = regexp.MustCompile(`.*info depth +([0-9]+).*seldepth +([0-9]+)`)
)

// TestUCIProtocol drives the built engine as a UCI process and asserts on its protocol behaviour, mirroring
// the former test/uci_test.sh check-for-check. The sub-tests run against one shared engine process (the
// original script used a single coproc for checks 1–6) except bench determinism, which spawns fresh processes.
func TestUCIProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("UCI subprocess test skipped in -short mode")
	}
	e := startEngine(t)
	defer e.close()

	// 1) uci handshake: id + option advertisements + uciok.
	t.Run("Handshake", func(t *testing.T) {
		e.send("uci")
		out, ok := e.readUntil(reUciok, 10*time.Second)
		if !ok {
			t.Fatalf("uci: no uciok within timeout; got:\n%s", strings.Join(out, "\n"))
		}
		if !anyLineMatches(out, reIdName) {
			t.Errorf("uci: missing 'id name Pawnstar'; got:\n%s", strings.Join(out, "\n"))
		}
		if !anyLineMatches(out, reUciok) {
			t.Errorf("uci: missing uciok")
		}
		if !anyLineMatches(out, reThreadsOpt) {
			t.Errorf("uci: does not advertise Threads option")
		}
	})

	// 2) isready / readyok handshake.
	t.Run("IsReady", func(t *testing.T) {
		e.send("isready")
		if _, ok := e.readUntil(reReadyok, 10*time.Second); !ok {
			t.Fatalf("isready: no readyok within timeout")
		}
	})

	// Deterministic, real searches: single thread and no opening book (otherwise `go` returns a book move
	// instantly, before any search, and emits no info lines).
	e.send("setoption name Threads value 1")
	e.send("setoption name OwnBook value false")

	// 3) info seldepth: present on a real search and >= the nominal depth (quiescence extends beyond it).
	t.Run("Seldepth", func(t *testing.T) {
		e.send("ucinewgame")
		e.send("position startpos")
		e.send("go depth 12")
		out, ok := e.readUntil(reBestmove, 30*time.Second)
		if !ok {
			t.Fatalf("go depth 12: no bestmove within timeout")
		}
		// Last `info depth ...` line.
		var lastInfo string
		for _, l := range out {
			if reInfoDepth.MatchString(l) {
				lastInfo = l
			}
		}
		if lastInfo == "" {
			t.Fatalf("go depth 12 produced no info lines (book not disabled? search not run?)")
		}
		mm := reSeldepth.FindStringSubmatch(lastInfo)
		if mm == nil {
			t.Fatalf("info seldepth bad/absent: %q", lastInfo)
		}
		d, _ := strconv.Atoi(mm[1])
		s, _ := strconv.Atoi(mm[2])
		if s < d {
			t.Errorf("info seldepth < depth: seldepth %d < depth %d (%q)", s, d, lastInfo)
		}
	})

	// 4) go searchmoves restricts the root. Force a move the engine would never choose (a2a3); if the
	//    filter works the bestmove must be exactly that.
	t.Run("SearchMoves", func(t *testing.T) {
		e.send("position startpos")
		e.send("go searchmoves a2a3 depth 10")
		out, ok := e.readUntil(reBestmove, 30*time.Second)
		if !ok {
			t.Fatalf("go searchmoves: no bestmove within timeout")
		}
		var bm string
		for _, l := range out {
			if reBestmove.MatchString(l) {
				bm = l
			}
		}
		if !reBestmoveA2A3.MatchString(bm) {
			t.Errorf("searchmoves: expected 'bestmove a2a3', got %q", bm)
		}
	})

	// 5) info currmove / currmovenumber: emitted once a search runs past the ~3s reporting gate. Use a
	//    movetime well beyond the gate (6s) so there is a comfortable window of root iterations emitting it.
	t.Run("CurrMove", func(t *testing.T) {
		e.send("position startpos")
		e.send("go movetime 6000")
		out, ok := e.readUntil(reBestmove, 30*time.Second)
		if !ok {
			t.Fatalf("go movetime 6000: no bestmove within timeout")
		}
		if !anyLineMatches(out, reCurrmove) {
			t.Errorf("currmove/currmovenumber not emitted on a long search")
		}
	})

	// 6) pondering: `go ... ponder` searches on the opponent's time and must NOT emit a bestmove until
	//    `ponderhit` (or `stop`). Then ponderhit converts it into a timed search that returns a bestmove.
	t.Run("Ponder", func(t *testing.T) {
		// Re-advertise options and assert the Ponder option is present (script re-sends `uci` here).
		e.send("uci")
		out, ok := e.readUntil(reUciok, 10*time.Second)
		if !ok {
			t.Fatalf("ponder: no uciok on second handshake")
		}
		if !anyLineMatches(out, rePonderOpt) {
			t.Errorf("uci: does not advertise Ponder option")
		}

		e.send("position fen r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1")
		e.send("go wtime 3000 btime 3000 ponder")
		// Let the ponder search run; it must not resolve on its own this fast.
		time.Sleep(300 * time.Millisecond)
		e.send("isready") // handled concurrently with the running ponder search
		out, ok = e.readUntil(reReadyok, 10*time.Second)
		if !ok {
			t.Fatalf("ponder: no readyok while pondering")
		}
		if anyLineMatches(out, reBestmove) {
			t.Fatalf("ponder: bestmove emitted before ponderhit; got:\n%s", strings.Join(out, "\n"))
		}

		e.send("ponderhit") // convert to a timed search (~120ms budget from wtime 3000)
		out, ok = e.readUntil(reBestmove, 30*time.Second)
		if !ok {
			t.Fatalf("ponder: no bestmove within timeout after ponderhit")
		}
		if !anyLineMatches(out, reBestmoveMv) {
			t.Errorf("ponder: no valid 'bestmove <uci>' after ponderhit; got:\n%s", strings.Join(out, "\n"))
		}
	})

	// 7) Ponder on a position whose search RESOLVES ON ITS OWN (a forced mate ends iterative deepening
	// early). This is the case the Ponder sub-test above deliberately avoids ("it must not resolve on its
	// own this fast") — and it is exactly the case that was broken: searchRootNode set isCancelPending to
	// join the Lazy SMP helpers, which made searchEntry's ponder-wait guard (`!isCancelPending`) always
	// false, so the engine emitted bestmove while still pondering. UCI forbids that, and it also advanced
	// the engine's own posStack for a move the GUI never confirmed. Regression test for that bug.
	t.Run("PonderForcedMate", func(t *testing.T) {
		e.send("position fen 6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1") // Ra1-a8# — mate in 1
		e.send("go ponder")
		time.Sleep(500 * time.Millisecond) // the mate is found in ~20ms; a buggy engine answers immediately
		e.send("isready")
		out, ok := e.readUntil(reReadyok, 10*time.Second)
		if !ok {
			t.Fatalf("ponderForcedMate: no readyok while pondering")
		}
		if anyLineMatches(out, reBestmove) {
			t.Fatalf("ponderForcedMate: bestmove emitted while pondering (UCI violation); got:\n%s",
				strings.Join(out, "\n"))
		}
		// It must still answer once the GUI resolves the ponder — a fix that simply never released would hang.
		e.send("ponderhit")
		if _, ok := e.readUntil(reBestmove, 30*time.Second); !ok {
			t.Fatalf("ponderForcedMate: no bestmove within timeout after ponderhit")
		}
	})
}

// TestUCIMalformedInput drives the engine with truncated and garbage UCI lines. A UCI engine that panics on
// a malformed line loses the game on the spot, so the bar is absolute: it must survive every one of these
// and still be responsive afterwards. Each of these used to panic:
//
//	"go wtime" (and every other keyword taking a value) — unguarded args[i+1] index
//	"position fen"                                      — empty FEN, fields[0] on an empty slice
//	"position fen 8/8"                                  — kingless: kingSquare() == 64 indexes past the tables
//	"position fen garbage"                              — board parse walked x/y off the board
func TestUCIMalformedInput(t *testing.T) {
	if testing.Short() {
		t.Skip("UCI subprocess test skipped in -short mode")
	}
	malformed := []string{
		"go wtime", "go btime", "go winc", "go binc", "go movestogo", "go depth", "go movetime", "go nodes",
		"go searchmoves", "go", "go wtime abc", "go depth -5",
		"position", "position fen", "position fen 8/8", "position fen garbage", "position fen ///////",
		"position fen 99999999/99999999 w", "position fen 8/8 w - -", "position startpos moves",
		"position startpos moves zzzz", "position fen rnbq w KQkq e",
		"setoption name", "setoption name Hash value", "setoption", "ponderhit", "stop",
	}
	e := startEngine(t)
	defer e.close()
	e.send("uci")
	if _, ok := e.readUntil(reUciok, 10*time.Second); !ok {
		t.Fatalf("malformed: no uciok on handshake")
	}
	for _, cmd := range malformed {
		e.send(cmd)
		// Several of these legitimately START A SEARCH — a bare `go` is an infinite one. The engine runs
		// pinned to GOMAXPROCS=1 (see startEngine), so a search left running competes with the UCI goroutine
		// for that single P, and on a loaded machine (CI runs `make -j$(nproc) check`, so the whole suite and
		// the race-detector pass are hammering the box) the isready round-trip below can miss its deadline.
		// That is a property of the harness, not a bug in the engine, so stop any search first rather than
		// racing it. `stop` is a no-op when nothing is running.
		e.send("stop")
		e.send("isready")
		// If the engine panicked, its stdout closes and readUntil returns matched=false — which is precisely
		// the failure we are guarding against.
		if _, ok := e.readUntil(reReadyok, 30*time.Second); !ok {
			t.Fatalf("malformed: engine died or hung on %q (no readyok) — it must tolerate any input", cmd)
		}
	}
	// Still fully functional after all that garbage: a real search must still produce a legal bestmove.
	e.send("position startpos")
	e.send("go depth 6")
	out, ok := e.readUntil(reBestmove, 30*time.Second)
	if !ok {
		t.Fatalf("malformed: no bestmove after recovering from malformed input")
	}
	if !anyLineMatches(out, reBestmoveMv) {
		t.Errorf("malformed: expected a legal 'bestmove <uci>' after recovery; got:\n%s", strings.Join(out, "\n"))
	}
}

// benchGoldenNodes is the expected `bench` node total — the cross-engine bit-parity signature (the C++
// engine reports the same count; see CLAUDE.md). Any change to search, move ordering, pruning, the TT, or
// the eval shifts this number: bump it ONLY for a deliberate, SPRT-gated engine change, never to quieten
// the test.
const benchGoldenNodes = "6601454"

// TestUCIBenchDeterminism: single-threaded bench is deterministic, so two runs must agree (guards the
// uninitialised-read class of bug that made node counts wobble) AND must equal the golden bit-parity
// signature benchGoldenNodes. Each run is a fresh `bench\nquit` piped into a new process.
func TestUCIBenchDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("UCI subprocess test skipped in -short mode")
	}
	run := func() string {
		t.Helper()
		cmd := exec.Command(engineBinary(t))
		cmd.Env = append(os.Environ(), "GOMAXPROCS=1")
		cmd.Stdin = strings.NewReader("bench\nquit\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bench run: %v", err)
		}
		re := regexp.MustCompile(`(?m)^([0-9]+) nodes`)
		mm := re.FindStringSubmatch(string(out))
		if mm == nil {
			t.Fatalf("bench run: no '<n> nodes' line in output:\n%s", out)
		}
		return mm[1]
	}
	b1 := run()
	b2 := run()
	if b1 != b2 {
		t.Errorf("bench nondeterministic: run1=%q run2=%q", b1, b2)
	}
	if b1 != benchGoldenNodes {
		t.Errorf("bench signature changed: got %s nodes, want %s (bit-parity gate — see benchGoldenNodes)", b1, benchGoldenNodes)
	} else {
		t.Logf("bench matches the golden signature (%s nodes)", b1)
	}
}
