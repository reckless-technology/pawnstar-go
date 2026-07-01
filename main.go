package main

import (
	"bufio"
	"fmt"
	"os"
	"pawnstar-go/engine"
	"runtime"
	"strconv"
)

func main() {
	fmt.Println("Pawnstar (Go) — UCI compatible chess engine")
	game := engine.NewGame()
	if os.Getenv("PAWNSTAR_NNUE") != "0" {
		if err := game.EnableNNUE(); err != nil {
			fmt.Fprintf(os.Stderr, "info string %v; using handcrafted eval\n", err)
		}
	}
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
