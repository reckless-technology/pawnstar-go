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
go test ./engine/ -run TestPerft -v               # all perft positions (slow — runs up to depth 6)
go test ./engine/ -run TestPerft -short -v        # quick run, caps depth at 5
go test ./engine/ -run TestPerft/002 -v           # single position by 1-based index (002 = second position)
go test ./engine/ -run TestPerftExtra -short -v  # verify incremental Zobrist hash vs recomputed hash at every node
go test ./engine/ -run TestSearch -short -v       # run search on every EPD position to depth 9
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

The Go port runs at ~42 Mnps; the C++ original reaches ~700 Mnps via BMI2 PEXT. Perft benchmarks are the primary tool for tracking performance regressions.

## Playing test games

[game.py](../pawnstar-rs/game.py) (in the sibling Rust repo) orchestrates a UCI game between pawnstar-go and pawnstar-rs, building both engines automatically. Time control and colours are configured at the top of the file.

## Known Pitfalls

**Promotion notation** — `move.String()` outputs correct UCI notation (`e7e8q`). `move.GoString()` outputs a human-readable debug form (`pe7e8=q`) that is **not** UCI and must never be used for engine communication.

**`noSquare` equals a1** — `noSquare` is defined as `square(0)`, which is the same integer value as `a1`. It is only used for the en-passant target square and castling sentinel contexts where `a1` would never validly appear.

## Architecture

Pawnstar-Go is a UCI chess engine using bitboard board representation and alpha-beta search. It is a port of the C++ Pawnstar engine.

**Board representation** — `position` ([engine/position.go](engine/position.go)) is a value type used with copy-make traversal: `makeMove` and `makeNullMove` operate on a caller-owned copy of the struct. `Game.posStack []position` (heap-allocated, capacity 512) maintains the history stack; undoing a move is a slice truncation. The struct holds named bitboard fields (`pawns`, `knights`, `bishops`, `rooks`, `queens`, `kings`, `whitePieces`, `blackPieces`), a `squares [64]piece` array for O(1) lookup, an incremental Zobrist hash, incremental PST score accumulators (`scores [2]int`), `kingLocation [2]square` for king positions, and `colorToMove color` as a direct field. `positionFromFen` constructs a `position` from a FEN string. `bitboard` ([engine/bitboard.go](engine/bitboard.go)) is a `uint64` with methods for all set operations.

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

**Attack generation** — [engine/tables.go](engine/tables.go) initialises all precomputed data in its `init()` function: magic bitboard tables for sliding pieces (`bishopAttacks`, `rookAttacks` — magic numbers found offline with seed 1337), ray tables (eight directions × 64 squares), knight/king/pawn attack tables, pawn structure masks, king pawn shelter masks, intervening-square tables, and Zobrist hashes (seeded 1968). `computePins` ([engine/pins.go](engine/pins.go)) identifies absolutely pinned pieces and their allowed ray before move generation.

**Move generation** — [engine/movegen.go](engine/movegen.go) implements fully legal move generation. `generateLegalMoves` and `generateLegalCaptures` are the entry points. Internally, `generateMoves` builds the king's forbidden-square set (with X-ray attacks through the king), handles double-check early return, restricts non-king moves to checker capture or interposition in single check, applies pin constraints via `pins.allowedSquares`, generates castling, and generates all pawn move types inline (including en-passant with a horizontal discovered-check test).

**Search** — `Game` ([engine/game.go](engine/game.go)) owns the transposition table, history table, eval table, chess clock, opening book, a `parallelSlots chan struct{}` semaphore (capacity `NumCPU`), and the position history stack. `isCancelPending` is an `atomic.Bool`. Search runs on a goroutine launched by `startThinking`; `stopThinking` stores `true` into `isCancelPending` then blocks on a `sync.WaitGroup`.

All recursive search functions ([engine/search.go](engine/search.go)) take `*searchState` ([engine/parallel.go](engine/parallel.go)) rather than `*Game`. `searchState` holds two stacks: `posStack []position` (the goroutine's own copy-make stack, one entry per move played during search) and `hashStack []hashStackEntry` (a compact `{hash, halfMoveClock}` record for every position from the start of the game through the current node, used exclusively by `isDrawByRepetition`). `hashStackEntry` is 16 bytes vs 168 bytes for a full `position`, making worker forks ~10× cheaper on the repetition history. `newSearchStateFromGame` builds `hashStack` from the full game history and seeds `posStack` with only the current position; `newWorkerState` copies the parent's `hashStack` (compact, 16 bytes × n) and seeds `posStack` with a single position — the fork point. All shared state (TT, history table, eval table, time control, cancel flag, semaphore) is accessed via the `game *Game` pointer — `searchState` carries no redundant pointer fields.

`searchRootNode` drives iterative deepening from `startDepth` (3), with early exit when the best move is stable and/or the score is stable across consecutive depths. `searchAlphaBeta` implements PVS, null-move pruning (R=4), LMR (−1 ply from the 2nd move onward; −2 ply additionally from move index 7 at depth > 3 with zero history count), check extensions via quiescence re-entry, and **Young Brothers Wait** parallel search: at null-window nodes with `depth ≥ 4`, the first three moves are searched sequentially on the main goroutine; remaining moves are dispatched to parallel workers via `searchMovesParallel`. Each worker runs `runWorker` (a standalone function), acquires a slot from `parallelSlots`, searches with a null window, signals the per-batch `atomic.Bool` cutoff on fail-high, and sends its result (a `move` with score packed in) to a channel. Workers never spawn sub-workers. `scoreAndSortMovesInner` (standalone function called by both `Game.scoreAndSortMoves` and `searchState.scoreAndSortMoves`) scores moves by MVV-LVA adjusted for enemy pawn attacks, plus PST delta and history count.

**Transposition table** — Single 64 MB table ([engine/transposition.go](engine/transposition.go)). Indexed by `hash % tableSize` (table size is kept odd for distribution). Entries store hash, best move (score packed into upper 32 bits), depth, node type (Cut/All/PV), and an `isOld` flag. Concurrent access from parallel workers is protected by 256 striped `sync.Mutex` locks (slot maps to `locks[slotIndex % 256]`). Replacement policy: prefer empty slots, old entries, then shallower entries; PV nodes always replace. `age()` marks all entries old at the start of each root search (called before any workers are spawned — no locking needed).

**Evaluation** — [engine/evaluate.go](engine/evaluate.go) returns the score from the side-to-move's perspective. Piece values: P=100, N=300, B=300, R=500, Q=900 cp. PST tables are stored with rank 8 at low indices; White reads via `sq ^ 0x38` (rank flip), Black reads directly. PST scores for all non-king pieces are accumulated into `scores[c]` incrementally. It reads those accumulators and adds: bishop-pair bonus (+30 cp), pawn structure scores (passed-pawn PST, defended +5, backward −10, doubled −10, isolated −20 cp), bishop and rook mobility (lookup tables by available-square count), and king safety (phase-dependent PST, three-row pawn shelter with friendly/enemy weights of ±15/±10/±5, open-file penalties, adjacent-piece bonuses). The king PST switches between midgame and endgame tables via `isEndgame`. An alpha/beta material early-exit skips the full evaluation when material alone is more than 200 cp outside the search window. The evaluation result is cached in a 64 K-entry `evalHashTable` (indexed by `hash % tableSize`) with 256 striped `sync.Mutex` locks for concurrent access; a `hashNotFoundScore` sentinel distinguishes a miss from a real score. SEE ([engine/see.go](engine/see.go)) is implemented but not wired into move ordering, matching the C++ version.

**History heuristic** — [engine/history.go](engine/history.go) records how often each (ply, from→to) pair raised alpha or caused a beta cutoff. Counts are added to the move sort score via `scoreAndSortMovesInner` and reset at the start of each root search via `historyTable.clear()`.

**Opening book** — [engine/book.go](engine/book.go) reads [book.txt](book.txt) (385 lines from TSCP) from the same directory as the binary at startup via `os.Executable()`. `loadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves; an absent file silently produces an empty book. `getMove` returns a random candidate for variety across games.

**UCI protocol** — `Game.HandleCommand` in [engine/game.go](engine/game.go) parses all standard UCI commands: `uci`, `isready`, `ucinewgame`, `position`, `go` (all clock types), `stop`, `quit`. [main.go](main.go) is a thin entry point that reads stdin line-by-line and delegates to `HandleCommand`.

**Search diagnostics** — [engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, null-move/LMR/PVS attempt and fail counts, draw detection (`drawsByRepetition`), and parallel search counts (`parallelSearches`, `parallelCutoffs`, `parallelMovesSearched`). Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect search behaviour. Counters accumulate across searches unless explicitly reset.
