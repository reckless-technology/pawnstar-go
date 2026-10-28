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
	"bytes"
	"context"
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

	"pawnstar-go/engine/nnue"
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
	// The NNUE SHAPE tags this test binary was built with, read off the nnue package's own constants so
	// the engine binary cannot be built at a different shape from the tests driving it. Without this a
	// new-shape test run would drive a DEFAULT-shape engine, which loads the shipped net happily and so
	// passes while testing the wrong thing.
	args := []string{"build"}
	if shapeTags := nnueShapeBuildTags(); shapeTags != "" {
		args = append(args, "-tags", shapeTags)
	}
	args = append(args, "-o", bin, "pawnstar-go") // the root main package (this test is in engine/)
	build := exec.Command("go", args...)
	build.Env = append(os.Environ(), "GOEXPERIMENT=simd")
	if out, err := build.CombinedOutput(); err != nil {
		uciBinErr = fmt.Errorf("go build failed: %v\n%s", err, out)
		return
	}
	uciBinPath = bin
}

// nnueShapeBuildTags returns the comma-separated build tags that reproduce this test binary's NNUE
// shape, or "" for the default shape. It mirrors engine/nnue/shape_*.go, and the Makefile's
// KING_MIRRORING / OUTPUT_BUCKETS knobs set the same tags.
func nnueShapeBuildTags() string {
	var tags []string
	if nnue.KingMirroringEnabled {
		tags = append(tags, "pawnstar_nnue_king_mirroring")
	}
	if nnue.OutputBuckets == 8 {
		tags = append(tags, "pawnstar_nnue_output_buckets_8")
	}
	return strings.Join(tags, ",")
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
	// stderrLines holds the engine's stderr, but only for an engine started by
	// startEngineCapturingStderr; startEngine discards stderr as the original script did. Guarded
	// because the pump goroutine appends to it while the test reads it.
	stderrMu    sync.Mutex
	stderrLines []string
}

// startEngine launches the engine pinned to a single core / single thread (so searches are deterministic
// and cheap), mirroring the bash coproc's `GOMAXPROCS=1 PAWNSTAR_THREADS=1`. stderr is discarded.
func startEngine(t *testing.T) *uciEngine {
	t.Helper()
	return startEngineStreams(t, false)
}

// startEngineCapturingStderr is startEngine with the engine's stderr captured as well, for the one
// thing only a real process can show: that a message reaches BOTH streams. A match harness keeps
// stderr and routinely drops `info` lines, so an in-process test of stdout alone cannot prove the
// operator was told anything.
func startEngineCapturingStderr(t *testing.T) *uciEngine {
	t.Helper()
	return startEngineStreams(t, true)
}

// startEngineStreams is the shared body of the two constructors above.
func startEngineStreams(t *testing.T, withStderr bool) *uciEngine {
	t.Helper()
	cmd := exec.Command(engineBinary(t))
	cmd.Env = append(os.Environ(), "GOMAXPROCS=1", "PAWNSTAR_THREADS=1")
	cmd.Stderr = nil // discard stderr, like the script's 2>/dev/null (unless captured below)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("startEngine: StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("startEngine: StdoutPipe: %v", err)
	}
	e := &uciEngine{
		t:     t,
		cmd:   cmd,
		stdin: stdin,
		lines: make(chan string, 1024),
	}
	// The stderr pipe must be created before Start and drained by a goroutine for the same reason
	// close() drains stdout: a full pipe nobody reads blocks the engine, and Wait would then never
	// return.
	var stderr io.ReadCloser
	if withStderr {
		if stderr, err = cmd.StderrPipe(); err != nil {
			t.Fatalf("startEngine: StderrPipe: %v", err)
		}
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("startEngine: Start: %v", err)
	}
	if stderr != nil {
		go func() {
			sc := bufio.NewScanner(stderr)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				e.stderrMu.Lock()
				e.stderrLines = append(e.stderrLines, sc.Text())
				e.stderrMu.Unlock()
			}
		}()
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

// stderrSoFar returns a copy of the engine's stderr lines so far (empty unless the engine was started
// by startEngineCapturingStderr).
func (e *uciEngine) stderrSoFar() []string {
	e.stderrMu.Lock()
	defer e.stderrMu.Unlock()
	return append([]string(nil), e.stderrLines...)
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
	reInfoSeldepth = regexp.MustCompile(`^info depth.* seldepth `)
	reCurrmove     = regexp.MustCompile(`currmove [a-h][1-8][a-h][1-8].* currmovenumber [0-9]`)
	reBestmoveA2A3 = regexp.MustCompile(`^bestmove a2a3`)
	reSeldepth     = regexp.MustCompile(`.*info depth +([0-9]+).*seldepth +([0-9]+)`)
)

// TestUCIProtocol drives the built engine as a UCI process and asserts on its protocol behaviour, mirroring
// the former test/uci_test.sh check-for-check. The sub-tests run against one shared engine process (the
// original script used a single coproc for checks 1–6) except bench determinism, which spawns fresh processes.
func TestUCIProtocol(t *testing.T) {
	skipWithoutTheShippedNet(t)
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
		// The last iteration's summary line: once a search runs past the ~3 s currmove gate (a slow or
		// loaded machine), `info depth N currmove ...` lines follow it and carry no seldepth.
		var lastInfo string
		for _, l := range out {
			if reInfoSeldepth.MatchString(l) {
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

	// 5) info currmove / currmovenumber: emitted as each root move begins, once a search has run past the ~3s
	//    reporting gate. The search runs until the first such line (or the timeout), then stops. No fixed
	//    movetime: a single root move can grow its node count several times over from one iteration to the
	//    next, so on a slow machine (a small shared runner, a loaded one) it can straddle the whole of any fixed
	//    window past the gate and leave nothing to report inside it.
	t.Run("CurrMove", func(t *testing.T) {
		e.send("position startpos")
		e.send("go infinite")
		if _, ok := e.readUntil(reCurrmove, 120*time.Second); !ok {
			t.Errorf("currmove/currmovenumber not emitted on a long search")
		}
		e.send("stop")
		if _, ok := e.readUntil(reBestmove, 30*time.Second); !ok {
			t.Fatalf("currmove: no bestmove after stop")
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
	skipWithoutTheShippedNet(t)
	if testing.Short() {
		t.Skip("UCI subprocess test skipped in -short mode")
	}
	malformed := []string{
		"go wtime", "go btime", "go winc", "go binc", "go movestogo", "go depth", "go movetime", "go nodes",
		"go searchmoves", "go", "go wtime abc", "go depth -5", "go depth abc", "go nodes 12x", "go movetime soon",
		"position", "position fen", "position fen 8/8", "position fen garbage", "position fen ///////",
		"position fen 99999999/99999999 w", "position fen 8/8 w - -", "position startpos moves",
		"position startpos moves zzzz", "position fen rnbq w KQkq e",
		"setoption name", "setoption name Hash value", "setoption", "ponderhit", "stop",
	}
	// Each command gets a FRESH process, with the whole script piped in and stdin then closed.
	//
	// This deliberately avoids an interactive stop/isready handshake, which is what the first version of this
	// test did — and it was a coin flip, not a test. Several of these lines legitimately start a long search
	// (a bare `go` searches on the default five-minute clock; a truncated `go nodes` once meant an unbounded
	// one, before a keyword with no value was ignored with it), and the engine runs pinned to
	// GOMAXPROCS=1. Racing an unbounded search on a loaded CI box (the whole suite plus the race-detector pass
	// run concurrently) has two failure modes I hit: the engine can be blocked WRITING stdout — nothing drains
	// it between sends, so the pipe fills — and a blocked writer cannot read `stop` or `isready`, so the
	// handshake never completes; and stopThinking() blocks the UCI loop until the search actually winds down.
	// Neither is an engine bug. Piping the full script and reading to EOF has neither hazard: `quit` exits in
	// ~20ms even mid-infinite-search, and the process cannot block on stdout because we read it to the end.
	bin := engineBinary(t)
	for _, cmd := range malformed {
		t.Run(cmd, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			proc := exec.CommandContext(ctx, bin)
			proc.Env = append(os.Environ(), "GOMAXPROCS=1", "PAWNSTAR_THREADS=1")
			proc.Stdin = strings.NewReader("uci\nisready\n" + cmd + "\nquit\n")
			out, err := proc.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("engine HUNG on %q (no exit within 60s)", cmd)
			}
			if bytes.Contains(out, []byte("panic:")) || bytes.Contains(out, []byte("index out of range")) {
				t.Fatalf("engine PANICKED on %q — it must tolerate any input:\n%s", cmd, out)
			}
			if err != nil {
				t.Fatalf("engine exited non-zero on %q: %v\n%s", cmd, err, out)
			}
		})
	}

	// Still fully functional after garbage: a real (BOUNDED — no race) search must produce a legal bestmove.
	t.Run("recovers", func(t *testing.T) {
		e := startEngine(t)
		defer e.close()
		e.send("uci")
		if _, ok := e.readUntil(reUciok, 30*time.Second); !ok {
			t.Fatalf("recovers: no uciok")
		}
		for _, cmd := range malformed {
			e.send(cmd)
		}
		e.send("stop")
		e.send("position startpos") // resets the position stack AND the time control (newTimeControl)
		e.send("go depth 6")        // bounded: cannot outlive the deadline the way an inherited clockInfinite can
		out, ok := e.readUntil(reBestmove, 60*time.Second)
		if !ok {
			t.Fatalf("recovers: no bestmove after malformed input")
		}
		if !anyLineMatches(out, reBestmoveMv) {
			t.Errorf("recovers: expected a legal 'bestmove <uci>'; got:\n%s", strings.Join(out, "\n"))
		}
	})
}

// TestUCIStopAndQuitStraightAfterGo drives the binary with a `stop` or `quit` written straight after `go
// infinite`, before the search goroutine has begun the root search. The root search used to clear the
// cancel flag as it began, so such a stop was lost: the engine never answered `go infinite` + `stop` and
// never exited on `go infinite` + `quit` — every attempt hung before the fix.
func TestUCIStopAndQuitStraightAfterGo(t *testing.T) {
	skipWithoutTheShippedNet(t)
	if testing.Short() {
		t.Skip("UCI subprocess test skipped in -short mode")
	}
	const setup = "setoption name OwnBook value false\nsetoption name Threads value 1\n"
	t.Run("stop", func(t *testing.T) {
		e := startEngine(t)
		defer e.close()
		e.send(strings.TrimSuffix(setup, "\n"))
		e.send("isready")
		if _, ok := e.readUntil(reReadyok, 30*time.Second); !ok {
			t.Fatal("no readyok")
		}
		const stops = 300
		for i := range stops {
			e.send("position startpos\ngo infinite\nstop")
			if _, ok := e.readUntil(reBestmove, 30*time.Second); !ok {
				t.Fatalf("go infinite + stop at once: no bestmove on attempt %d of %d (the stop was lost)", i+1, stops)
			}
		}
	})
	t.Run("quit", func(t *testing.T) {
		bin := engineBinary(t)
		const quits = 20
		for i := range quits {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			proc := exec.CommandContext(ctx, bin)
			proc.Env = append(os.Environ(), "GOMAXPROCS=1", "PAWNSTAR_THREADS=1")
			proc.Stdin = strings.NewReader(setup + "position startpos\ngo infinite\nquit\n")
			out, err := proc.CombinedOutput()
			hung := ctx.Err() != nil
			cancel()
			if hung {
				t.Fatalf("go infinite + quit at once: the engine never exited on attempt %d of %d", i+1, quits)
			}
			if err != nil || !bytes.Contains(out, []byte("bestmove ")) {
				t.Fatalf("go infinite + quit at once: exit %v, output:\n%s", err, out)
			}
		}
	})
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
	skipWithoutTheShippedNet(t)
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
