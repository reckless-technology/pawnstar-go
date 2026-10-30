package main

// The binary's own loop, run in process: main reads UCI commands from stdin until `quit` or the end of
// input. The engine package's UCI test drives the built binary as a subprocess, which proves the same thing
// but leaves this file out of the coverage profile.

import (
	"io"
	"os"
	"path/filepath"
	"pawnstar-go/engine"
	"strings"
	"testing"
)

// runMain runs main with input as its stdin and returns what it printed. args, if given, replace the
// command line for the call - main otherwise sees the test binary's own arguments, which it ignores.
func runMain(t *testing.T, input string, args ...string) string {
	t.Helper()
	if args != nil {
		old := os.Args
		os.Args = append([]string{"pawnstar-go"}, args...)
		defer func() { os.Args = old }()
	}
	in := filepath.Join(t.TempDir(), "stdin.txt")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	stdin, err := os.Open(in)
	if err != nil {
		t.Fatalf("open stdin: %v", err)
	}
	defer func() { _ = stdin.Close() }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdin, w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	main()
	_ = w.Close()
	os.Stdin, os.Stdout = oldIn, oldOut
	return <-done
}

// TestMainAnswersUntilQuit: the banner, then each command answered in turn, and nothing read after quit.
func TestMainAnswersUntilQuit(t *testing.T) {
	t.Setenv("PAWNSTAR_THREADS", "2")
	out := runMain(t, "uci\nsetoption name Threads value 1\nisready\nquit\nisready\n")
	if !strings.HasPrefix(out, "Pawnstar (Go)") {
		t.Errorf("no banner first; output:\n%s", out)
	}
	if !strings.Contains(out, "option name Threads type spin default 2 ") {
		t.Errorf("PAWNSTAR_THREADS=2 not reported as the Threads default; output:\n%s", out)
	}
	if strings.Count(out, "readyok") != 1 || !strings.Contains(out, "uciok") {
		t.Errorf("want uciok and exactly one readyok (none after quit); output:\n%s", out)
	}
}

// TestMainEndsAtEndOfInput: a GUI that closes the pipe without `quit` ends the loop; a nonsense
// PAWNSTAR_THREADS leaves the hardware default in place.
func TestMainEndsAtEndOfInput(t *testing.T) {
	t.Setenv("PAWNSTAR_THREADS", "many")
	out := runMain(t, "isready\n")
	if !strings.HasSuffix(out, "readyok\n") {
		t.Errorf("want readyok last; output:\n%s", out)
	}
}

// TestMainLicenceFlag: --license prints the licence notice and nothing else - no banner, no engine
// constructed, stdin untouched - under each spelling the flag accepts, and prints exactly the text the
// `license` UCI command prints, since the two must never drift. engine.LicenceNotice is the one copy;
// the engine package's own test is what holds that text to THIRD-PARTY-NOTICES.md.
func TestMainLicenceFlag(t *testing.T) {
	want := strings.Join(engine.LicenceNotice(), "\n") + "\n"
	for _, flag := range []string{"--license", "-license", "--licence", "-licence"} {
		out := runMain(t, "isready\n", flag)
		if out != want {
			t.Errorf("%s did not print the licence notice alone; output:\n%s", flag, out)
		}
		if strings.Contains(out, "UCI compatible chess engine") || strings.Contains(out, "readyok") {
			t.Errorf("%s ran the UCI loop; output:\n%s", flag, out)
		}
	}
}

// TestMainIgnoresOtherArguments: the engine takes its configuration from UCI and the environment, so an
// unrecognised argument is ignored rather than refused - which is also why the in-process tests above can
// call main with the test binary's own command line.
func TestMainIgnoresOtherArguments(t *testing.T) {
	out := runMain(t, "isready\nquit\n", "--not-a-flag", "license")
	if !strings.HasPrefix(out, "Pawnstar (Go)") || !strings.Contains(out, "readyok") {
		t.Errorf("unrecognised arguments did not leave the UCI loop alone; output:\n%s", out)
	}
}
