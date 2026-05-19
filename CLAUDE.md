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
go test ./engine/ -run TestPerft/002 -v           # single position by index (format: %03d)
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

The Go port runs at ~35 Mnps; the C++ original reaches ~700 Mnps via BMI2 PEXT. Perft benchmarks are the primary tool for tracking performance regressions.

## Playing test games

[game.py](../pawnstar-rs/game.py) (in the sibling Rust repo) orchestrates a UCI game between pawnstar-go and pawnstar-rs, building both engines automatically. Time control and colours are configured at the top of the file.

## Known Pitfalls

**Promotion notation** — `move.String()` outputs correct UCI notation (`e7e8q`). `move.GoString()` outputs a human-readable debug form (`pe7e8=q`) that is **not** UCI and must never be used for engine communication.

**`noSquare` equals a1** — `noSquare` is defined as `square(0)`, which is the same integer value as `a1`. It is only used for the en-passant target square and castling sentinel contexts where `a1` would never validly appear.

**Pawn structure masks exist but are not used by the evaluator** — `tables.go` precomputes passed-pawn, isolated-pawn, doubled-pawn, and supported-pawn masks. These are not currently read by `evaluate.go`; the evaluator only uses PST scores and king pawn shelter.

## Architecture

Pawnstar-Go is a UCI chess engine using bitboard board representation and alpha-beta search. It is a port of the C++ Pawnstar engine.

**Board representation** — `position` ([engine/position.go](engine/position.go)) is the central state struct, mutated in place throughout search. `makeMove` / `unmakeMove` traverse the game tree by pushing an `irreversibleState` (hash, castling rights, en passant, checkers bitboard, halfmove clock, flags) onto an internal `stateStack` slice, then popping and restoring it on undo. The struct holds per-piece and per-color `bitboard` arrays, a `squares [64]piece` array for O(1) lookup, incremental Zobrist hash, and incremental PST score accumulators (`scoreWhite`, `scoreBlack`). `positionFromFen` constructs a `position` from a FEN string. `bitboard` ([engine/bitboard.go](engine/bitboard.go)) is a `uint64` with methods for all set operations.

**Incremental scoring** — `addPiece`, `removePiece`, and `movePiece` in `position.go` update `scoreWhite`/`scoreBlack` incrementally using `pstValue` from `evaluate.go`. This covers all non-king pieces. The king's PST contribution is computed at evaluation time because it is phase-dependent and cannot be maintained incrementally without knowing the game phase.

**Move encoding** — `move` ([engine/move.go](engine/move.go)) is a packed `int64` with an identical bit layout to the C++ version:

```
bits  0– 5  to square
bits  6–11  from square
bits 12–14  moving piece
bits 15–17  captured piece
bits 18–21  move type
bit     22  gives-check flag
bits 32–63  sort score (not part of move identity)
```

The score is also used to carry the search score inside transposition table entries (packed into a move via `move.withScore`). Move lists are `[]move` slices allocated with `make([]move, 0, 64)`. `move.equal` masks the score field when comparing identity.

**Attack generation** — [engine/magic.go](engine/magic.go) implements magic bitboard lookup for sliding pieces (`bishopAttacks`, `rookAttacks`). Magic numbers are precomputed constants found offline with seed 1337. [engine/tables.go](engine/tables.go) initialises all precomputed data in its `init()` function: ray tables (eight directions × 64 squares), knight/king/pawn attack tables, pawn structure masks, king pawn shelter masks, intervening-square tables, and Zobrist hashes (seeded 1968). `computePins` ([engine/pins.go](engine/pins.go)) identifies absolutely pinned pieces and their allowed ray before move generation.

**Move generation** — [engine/movegen.go](engine/movegen.go) implements fully legal move generation. `generateLegalMoves` and `generateLegalCaptures` are the entry points. Internally, `generateMoves` builds the king's forbidden-square set (with X-ray attacks through the king), handles double-check early return, restricts non-king moves to checker capture or interposition in single check, applies pin constraints via `pins.allowedSquares`, generates castling, and generates all pawn move types inline (including en-passant with a horizontal discovered-check test).

**Search** — `Game` ([engine/game.go](engine/game.go)) owns the transposition table, history table, chess clock, opening book, and the single `position`. Search runs on a goroutine launched by `startThinking`; `stopThinking` sets `isCancelPending = true` then blocks on a `sync.WaitGroup`. `searchRootNode` in [engine/search.go](engine/search.go) drives iterative deepening from `startDepth` (3), with early exit when the best move is stable and/or the score is stable across consecutive depths. `searchAlphaBeta` implements PVS, null-move pruning (R=4), LMR (−1 ply after move index 3; −2 ply after move index 6 at depth > 3), and check extensions via quiescence re-entry. `scoreAndSortMoves` uses MVV-LVA + history count as the sort key.

**Transposition table** — Single 64 MB table ([engine/transposition.go](engine/transposition.go)). Indexed by `hash % tableSize` (table size is kept odd for distribution). Entries store hash, best move (score packed into upper 32 bits), depth, node type (Cut/All/PV), and an `isOld` flag. Replacement policy: prefer empty slots, old entries, then shallower entries; PV nodes always replace. `age()` marks all entries old at the start of each root search.

**Evaluation** — [engine/evaluate.go](engine/evaluate.go) returns the score from the side-to-move's perspective. It reads the incremental `scoreWhite`/`scoreBlack` accumulators, adds the king's PST bonus (midgame vs endgame table selected by `isEndgame`), and adds a king pawn shelter bonus (+10 cp per pawn in the three squares directly in front of the king, midgame only). SEE ([engine/see.go](engine/see.go)) is implemented but not wired into move ordering, matching the C++ version.

**Opening book** — [engine/book.go](engine/book.go) embeds [engine/book.txt](engine/book.txt) (385 lines from TSCP) at compile time. `loadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves. `getMove` returns a random candidate for variety across games.

**UCI protocol** — `Game.HandleCommand` in [engine/uci.go](engine/uci.go) parses all standard UCI commands: `uci`, `isready`, `ucinewgame`, `position`, `go` (all clock types), `stop`, `quit`. [main.go](main.go) is a thin entry point that reads stdin line-by-line and delegates to `HandleCommand`.

**Search diagnostics** — [engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, and null-move/LMR/PVS attempt and fail counts. Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect search behaviour. Counters accumulate across searches unless explicitly reset.
