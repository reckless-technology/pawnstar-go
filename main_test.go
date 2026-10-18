package main

// The binary's own loop, run in process: main reads UCI commands from stdin until `quit` or the end of
// input. The engine package's UCI test drives the built binary as a subprocess, which proves the same thing
// but leaves this file out of the coverage profile.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runMain runs main with input as its stdin and returns what it printed.
func runMain(t *testing.T, input string) string {
	t.Helper()
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
