# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

Requires Go 1.24 or later.

```bash
go build -o pawnstar-go .   # build the engine binary
go test ./engine/            # run all tests
```

Run the engine interactively via UCI: `./pawnstar-go`

## Running Tests

Move generation correctness is verified by perft tests against known node counts:

```bash
go test ./engine/ -run TestPerft -v               # all perft positions (slow — runs up to depth 8)
go test ./engine/ -run TestPerft -short -v        # quick run, caps depth at 5
go test ./engine/ -run TestPerft/002 -v           # single position by index (format: %03d/d%d)
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

The Go port runs at ~35 Mnps; the C++ original reaches ~700 Mnps via BMI2 PEXT. Perft benchmarks are the primary tool for tracking performance regressions.

## Architecture

Pawnstar-Go is a UCI chess engine using bitboard board representation and alpha-beta search. It is a port of the C++ Pawnstar engine.

**Board representation** — `Position` ([engine/position.go](engine/position.go)) is the central state struct, mutated in place. `MakeMove` / `UnmakeMove` traverse the game tree by saving irreversible state (hash, castling rights, en passant, checkers, halfmove clock) into an internal `stateStack` on each `MakeMove` and restoring it on `UnmakeMove`. It holds per-piece and per-color bitboards, a square→piece array for O(1) lookup, Zobrist hash, castling rights, and en passant square. `PositionFromFen` constructs a `Position` from a FEN string. `Bitboard` ([engine/bitboard.go](engine/bitboard.go)) provides the 64-bit set operations.

**Move encoding** — `Move` ([engine/move.go](engine/move.go)) packs from/to squares, moving piece, captured piece, move type flags (castling, en passant, promotion, double push), and a sort score into a single `int64`, with an identical bit layout to the C++ version. `MoveList` is a fixed-capacity stack struct with no heap allocation during search.

**Attack generation** — ([engine/magic.go](engine/magic.go)) implements magic bitboard lookup for sliding piece attacks (`BishopAttacks`, `RookAttacks`, `QueenAttacks`). Magic numbers are precomputed constants (found offline with seed 1337). ([engine/tables.go](engine/tables.go)) precomputes ray tables (N/NE/E/SE/S/SW/W/NW), knight/king/pawn attack tables, pawn structure masks, and Zobrist hashes. Ray tables are used for pin and check detection; magic bitboards handle sliding piece move generation. `Pins` ([engine/pins.go](engine/pins.go)) computes pin rays and absolute pins before move generation.

**Move generation** — ([engine/movegen.go](engine/movegen.go)) implements fully legal move generation. `GenerateLegalMoves` and `GenerateLegalCaptures` are the public entry points. Internally `genMoves` builds the forbidden-square set for the king (including X-ray attacks through the king), handles double-check early returns, applies pin constraints via `Pins.AllowedSquares`, and delegates pawn moves (including en passant horizontal-pin detection) to `genPawnMoves`.

**Search** — `Game` ([engine/game.go](engine/game.go)) owns the transposition tables, history table, chess clock, opening book, and the single `Position` used throughout search. `SearchRootNode` in ([engine/search.go](engine/search.go)) drives iterative deepening. The same file contains the alpha-beta implementation (with PVS, null-move pruning, LMR, check extensions) and quiescence search. Search runs on a goroutine so UCI `stop` can interrupt it.

**Transposition table** — Two tables: 64 MB for the main search, 8 MB for quiescence ([engine/transposition.go](engine/transposition.go)). Entries store hash, best move, score, depth, node type (CUT/ALL/PV), and an age flag for replacement policy.

**Evaluation** — ([engine/evaluate.go](engine/evaluate.go)) scores material, piece-square tables, pawn structure (doubled/isolated/passed/backward/defended), mobility, and king safety. SEE ([engine/see.go](engine/see.go)) is implemented but not currently wired into move ordering (the quiescent SEE filter is commented out, matching the C++ version).

**Opening book** — ([engine/book.go](engine/book.go)) embeds [engine/book.txt](engine/book.txt) (385 lines from TSCP). `LoadOpeningBook` plays through each line from the start position and maps every encountered Zobrist hash to the set of candidate next moves. `GetMove` returns a random candidate, giving variety across games. Called from `NewGame`; wired into `SearchRootNode` before iterative deepening begins.

**UCI protocol** — [main.go](main.go) parses all standard UCI commands: `uci`, `isready`, `ucinewgame`, `position`, `go` (all clock types), `stop`, `quit`.
