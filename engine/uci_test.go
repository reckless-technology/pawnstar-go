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
		os.RemoveAll(uciBinDir)
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

// close shuts the engine down (quit + wait), tolerating an already-exited process.
func (e *uciEngine) close() {
	e.send("quit")
	_ = e.stdin.Close()
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
}

// TestUCIBenchDeterminism: single-threaded bench is deterministic, so two runs must agree (guards the
// uninitialised-read class of bug that made node counts wobble). Each run is a fresh `bench\nquit` piped
// into a new process, exactly like the bash script.
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
	} else {
		t.Logf("bench is deterministic across runs (%s nodes)", b1)
	}
}
