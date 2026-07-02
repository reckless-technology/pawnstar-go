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

// main runs the UCI loop: construct the engine (NNUE installed, thread count from the environment),
// then read stdin line by line and delegate each command to Game.HandleCommand until it reports quit.
func main() {
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
