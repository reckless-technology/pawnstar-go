// Pawnstar-Go is a UCI chess engine: a Go port of the C++ Pawnstar, kept bit-for-bit
// identical to it at fixed search depth. All engine logic lives in the engine package;
// this binary just wires stdin/stdout to the UCI command handler.
package main

import (
	"bufio"
	"fmt"
	"os"
	"pawnstar-go/engine"
	"runtime"
	"strconv"
)

// wantsLicence reports whether the command line asks for the licence notice - the same text the
// `license` UCI command prints (engine/licence.go), which is where it lives: the net is embedded, so a
// release binary carries an ODbL Produced Work while carrying no file from the source tree, and the
// notice has to be reachable both from a GUI's UCI session and from a bare command line.
//
// Anything else on the command line is ignored, as it always has been - the engine takes its
// configuration from UCI and the environment, and the in-process tests call main with the test
// binary's own arguments.
func wantsLicence(args []string) bool {
	for _, arg := range args {
		if arg == "--license" || arg == "-license" || arg == "--licence" || arg == "-licence" {
			return true
		}
	}
	return false
}

// main runs the UCI loop: construct the engine (NNUE installed, thread count from the environment),
// then read stdin line by line and delegate each command to Game.HandleCommand until it reports quit.
// `--license` short-circuits all of that and prints the third-party notice instead - the same text
// the `license` UCI command prints, taken from the same place.
func main() {
	if wantsLicence(os.Args[1:]) {
		for _, line := range engine.LicenceNotice() {
			fmt.Println(line)
		}
		return
	}
	fmt.Println("Pawnstar (Go) — UCI compatible chess engine")
	game := engine.NewGame()
	// Lazy SMP thread count: PAWNSTAR_THREADS if set (and valid), otherwise all logical CPUs. A GUI can
	// still override this at any time via `setoption name Threads`.
	threads := runtime.NumCPU()
	if n, err := strconv.Atoi(os.Getenv("PAWNSTAR_THREADS")); err == nil && n > 0 {
		threads = n
	}
	game.SetThreads(threads)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if game.HandleCommand(scanner.Text()) {
			return
		}
	}
}
