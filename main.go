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

// licenceNotice is the attribution the embedded network's training data obliges, printed by
// `pawnstar-go --license`. The network is compiled into the binary (engine/nnue/embed.go), so a
// release binary *contains* an ODbL Produced Work while carrying no file from the source tree: this
// flag is how the notice reaches someone who downloaded only the executable. It is the same text as
// THIRD-PARTY-NOTICES.md, which is the full version.
//
// It prints and exits before anything else happens, touching neither the engine nor the UCI handler,
// so it cannot affect search or the bench signature.
const licenceNotice = `Pawnstar-Go is free software under the GNU General Public License, version 3.

The embedded evaluation network (pawnstar-v20.bin, release 2.0.0) was trained entirely on Leela Chess
Zero self-play training data, published under the Open Database License 1.0 with its contents under the
Database Contents License 1.0. A trained network is an ODbL Produced Work, and distributing one obliges
this notice:

  Position data from the Leela Chess Zero self-play training data
  (https://storage.lczero.org/files/training_data/), run test91, used under the Open Database License
  1.0 (http://opendatacommons.org/licenses/odbl/1.0/).

  Position data from the Leela Chess Zero self-play training data
  (https://storage.lczero.org/files/training_data/), run test60, used under the Open Database License
  1.0 (http://opendatacommons.org/licenses/odbl/1.0/).

The obligation is the notice. ODbL does not require the network itself to be ODbL-licensed, and it is
not offered under ODbL; nor does the engine's GPLv3 licence make the network GPLv3 merely because GPLv3
code trained it.

Full notices: THIRD-PARTY-NOTICES.md in the source tree, and in each release's documentation bundle.`

// wantsLicence reports whether the command line asks for the licence notice. Anything else on the
// command line is ignored, as it always has been - the engine takes its configuration from UCI and the
// environment, and the in-process tests call main with the test binary's own arguments.
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
// `--license` short-circuits all of that and prints the third-party notice instead.
func main() {
	if wantsLicence(os.Args[1:]) {
		fmt.Println(licenceNotice)
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
