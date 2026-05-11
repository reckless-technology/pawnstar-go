# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

Requires Go 1.22 or later.

```bash
go build -o pawnstar-go ./cmd/pawnstar/   # build the engine binary
go test ./engine/                          # run all tests
```

## Running Tests

Move generation correctness is verified by perft tests against known node counts:

```bash
go test ./engine/ -run TestPerft -v
go test ./engine/ -run BenchmarkPerft7 -bench=. -benchtime=10s
```

## Architecture

Pawnstar-Go is a UCI chess engine using bitboard board representation and alpha-beta search. It is a port of the C++ Pawnstar engine.

**Board representation** — `Position` ([engine/position.go](engine/position.go)) is the central state struct, copied by value. `MakeMove` returns a new `Position`. It holds per-piece and per-color bitboards, a square→piece array for O(1) lookup, Zobrist hash, castling rights, and en passant square. `Bitboard` ([engine/bitboard.go](engine/bitboard.go)) provides the 64-bit set operations.

**Move encoding** — `Move` ([engine/move.go](engine/move.go)) packs from/to squares, moving piece, captured piece, move type flags (castling, en passant, promotion, double push), and a sort score into a single `int64`, with an identical bit layout to the C++ version. `MoveList` is a fixed-capacity stack struct with no heap allocation during search.

**Attack generation** — ([engine/tables.go](engine/tables.go)) precomputes ray tables (N/NE/E/SE/S/SW/W/NW) and uses the XOR ray-table trick for sliding piece attacks. No BMI2 PEXT is used; this is the main performance difference vs the C++ version. `Pins` ([engine/pins.go](engine/pins.go)) computes pin rays and absolute pins before move generation.

**Search** — `Game` ([engine/game.go](engine/game.go)) owns the transposition tables, history table, chess clock, and position history stack. `SearchRootNode` in ([engine/search.go](engine/search.go)) drives iterative deepening. The same file contains the alpha-beta implementation (with PVS, null-move pruning, LMR, check extensions) and quiescence search. Search runs on a goroutine so UCI `stop` can interrupt it.

**Transposition table** — Two tables: 64 MB for the main search, 8 MB for quiescence ([engine/transposition.go](engine/transposition.go)). Entries store hash, best move, score, depth, node type (CUT/ALL/PV), and an age flag for replacement policy.

**Evaluation** — ([engine/evaluate.go](engine/evaluate.go)) scores material, piece-square tables, pawn structure (doubled/isolated/passed/backward/defended), mobility, and king safety. SEE ([engine/see.go](engine/see.go)) is implemented but not currently wired into move ordering (the quiescent SEE filter is commented out, matching the C++ version).

**UCI protocol** — [cmd/pawnstar/main.go](cmd/pawnstar/main.go) parses all standard UCI commands: `uci`, `isready`, `ucinewgame`, `position`, `go` (all clock types), `stop`, `quit`, plus `perft`.
