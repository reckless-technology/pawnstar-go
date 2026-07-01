package main

import (
	"bufio"
	"fmt"
	"os"
	"pawnstar-go/engine"
)

func main() {
	fmt.Println("Pawnstar (Go) — UCI compatible chess engine")
	game := engine.NewGame()
	if os.Getenv("PAWNSTAR_NNUE") != "0" {
		if err := game.EnableNNUE(); err != nil {
			fmt.Fprintf(os.Stderr, "info string %v; using handcrafted eval\n", err)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if game.HandleCommand(scanner.Text()) {
			return
		}
	}
}
