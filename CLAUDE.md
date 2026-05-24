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

## Architecture

Pawnstar-Go is a UCI chess engine using bitboard board representation and alpha-beta search. It is a port of the C++ Pawnstar engine.

**Board representation** — `position` ([engine/position.go](engine/position.go)) is a value type used with copy-make traversal: `makeMove` and `makeNullMove` operate on a caller-owned copy of the struct. `Game.posStack []position` (backed by the embedded `posStackData [512]position` array) maintains the history stack; undoing a move is a slice truncation. The struct holds named bitboard fields (`pawns`, `knights`, `bishops`, `rooks`, `queens`, `kings`, `whitePieces`, `blackPieces`), a `squares [64]piece` array for O(1) lookup, an incremental Zobrist hash, incremental PST score accumulators (`scores [2]int`), `kingLocation [2]square` for king positions, and `colorToMove color` as a direct field. `positionFromFen` constructs a `position` from a FEN string. `bitboard` ([engine/bitboard.go](engine/bitboard.go)) is a `uint64` with methods for all set operations.

**Incremental scoring** — `addPiece`, `removePiece`, and `movePiece` in `position.go` update `scores[c]` incrementally using `pstValue` from `evaluate.go`. This covers all non-king pieces. The king's PST contribution is computed at evaluation time because it is phase-dependent and cannot be maintained incrementally without knowing the game phase.

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

**Search** — `Game` ([engine/game.go](engine/game.go)) owns the transposition table, history table, chess clock, opening book, and the single `position`. Search runs on a goroutine launched by `startThinking`; `stopThinking` sets `isCancelPending = true` then blocks on a `sync.WaitGroup`. `searchRootNode` in [engine/search.go](engine/search.go) drives iterative deepening from `startDepth` (3), with early exit when the best move is stable and/or the score is stable across consecutive depths. `searchAlphaBeta` implements PVS, null-move pruning (R=4), LMR (−1 ply from move index 1; −2 ply additionally from move index 7 at depth > 3 with zero history count), and check extensions via quiescence re-entry. `scoreAndSortMoves` uses MVV-LVA + history count as the sort key.

**Transposition table** — Single 64 MB table ([engine/transposition.go](engine/transposition.go)). Indexed by `hash % tableSize` (table size is kept odd for distribution). Entries store hash, best move (score packed into upper 32 bits), depth, node type (Cut/All/PV), and an `isOld` flag. Replacement policy: prefer empty slots, old entries, then shallower entries; PV nodes always replace. `age()` marks all entries old at the start of each root search.

**Evaluation** — [engine/evaluate.go](engine/evaluate.go) returns the score from the side-to-move's perspective. It reads the incremental `scores[c]` accumulators and adds: bishop-pair bonus (+30 cp), pawn structure scores (passed-pawn PST, defended +5, backward −10, doubled −10, isolated −20 cp), bishop and rook mobility (lookup tables by available-square count), and king safety (phase-dependent PST, three-row pawn shelter with friendly/enemy weights of ±15/±10/±5, open-file penalties, adjacent-piece bonuses). The king PST switches between midgame and endgame tables via `isEndgame`. An alpha/beta material early-exit skips the full evaluation when material alone is more than 200 cp outside the search window. SEE ([engine/see.go](engine/see.go)) is implemented but not wired into move ordering, matching the C++ version.

**Opening book** — [engine/book.go](engine/book.go) reads [book.txt](book.txt) (385 lines from TSCP) from the same directory as the binary at startup via `os.Executable()`. `loadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves; an absent file silently produces an empty book. `getMove` returns a random candidate for variety across games.

**UCI protocol** — `Game.HandleCommand` in [engine/game.go](engine/game.go) parses all standard UCI commands: `uci`, `isready`, `ucinewgame`, `position`, `go` (all clock types), `stop`, `quit`. [main.go](main.go) is a thin entry point that reads stdin line-by-line and delegates to `HandleCommand`.

**Search diagnostics** — [engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, and null-move/LMR/PVS attempt and fail counts. Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect search behaviour. Counters accumulate across searches unless explicitly reset.
