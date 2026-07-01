# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

Requires Go 1.26 or later (the NNUE SIMD kernels use the `simd/archsimd` package, gated behind `GOEXPERIMENT=simd`).

```bash
make build                  # build with GOEXPERIMENT=simd (recommended) -> ./pawnstar-go
make test                   # run all tests with SIMD enabled
make test-short             # -short variant
make baseline               # snapshot the current binary as ./pawnstar-go-baseline for SPRT
```

The `Makefile` sets `GOEXPERIMENT=simd` for you. Building directly still works — `GOEXPERIMENT=simd go build -o pawnstar-go .` — and a plain `go build` compiles too, falling back to the scalar NNUE kernels (`engine/nnue_kernels_generic.go`) selected by build tag. The SIMD and scalar kernels are bit-identical; SIMD is ~3–5× faster on the hot paths.

Run the engine interactively via UCI: `./pawnstar-go`. NNUE is the default evaluator; set env `PAWNSTAR_NNUE=0` to start on the handcrafted eval instead.

## Running Tests

Move generation correctness is verified by perft tests against known node counts:

```bash
go test ./engine/ -run TestPerft -v               # all perft positions (slow — runs up to depth 6)
go test ./engine/ -run TestPerft -short -v        # quick run, caps depth at 5
go test ./engine/ -run TestPerft/002 -v           # single position by 1-based index (002 = second position)
go test ./engine/ -run TestPerftExtra -short -v  # verify incremental Zobrist hash vs recomputed hash at every node
go test ./engine/ -run TestSearch -short -v       # run search on every EPD position to depth 9
go test ./engine/ -run TestBratkoKopec -v         # 24 Bratko-Kopec positions at depth 8; search quality regression
go test ./engine/ -run TestSEE -v                 # 18 static exchange evaluation unit tests
go test ./engine/ -run TestNNUEReference -v       # NNUE EvaluateExact vs 250-FEN C++ reference (gate: 0 cp)
go test ./engine/ -run TestNNUEIncremental -v     # lazy accumulator == full refresh, bit-identical
go test ./engine/ -bench BenchmarkNNUE -benchmem   # NNUE evaluate/refresh/update micro-benchmarks
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

Run these with `GOEXPERIMENT=simd` (or via `make test`) so the SIMD kernels are exercised. Always run `TestPerft` and `TestSearch` together when verifying engine changes. `TestBratkoKopec` is the search quality gate; it runs with **NNUE enabled** and its accepted move set was generated from multiple NNUE runs at depths 7–8 (regenerate with `GEN_BK=1 go test -run TestGenerateBratkoKopecNNUE` after swapping the embedded net). `TestNNUEReference` (exact-eval correctness) and `TestNNUEIncremental` (lazy accumulator correctness) are the NNUE gates — both must stay green after any accumulator or kernel change.

The Go port runs at ~42 Mnps with the handcrafted eval; NNUE is slower per node but far stronger (SPRT: +572 Elo vs the handcrafted baseline at fixed depth). The C++ original reaches ~700 Mnps via BMI2 PEXT. Perft benchmarks are the primary tool for tracking move-generation performance regressions.

## SPRT self-play harness

`tools/sprt.sh <candidate-bin> <baseline-bin> [rounds]` runs a fastchess binary-vs-binary SPRT (single-core engines via `GOMAXPROCS=1`, fixed depth via `DEPTH=`, openings from `~/pawnstar_nnue/openings.epd`). Snapshot a baseline with `make baseline`. Every eval or search change that could affect strength is gated this way — run it in the background, they are long. The NNUE port itself passed [0, 10] with LLR 2.96 (146W/3L/5D over 154 games).

## Playing test games

[game.py](../pawnstar-rs/game.py) (in the sibling Rust repo) orchestrates a UCI game between pawnstar-go and pawnstar-rs, building both engines automatically. Time control and colours are configured at the top of the file.

## Known Pitfalls

**Promotion notation** — `move.String()` outputs correct UCI notation (`e7e8q`). `move.GoString()` outputs a human-readable debug form (`pe7e8=q`) that is **not** UCI and must never be used for engine communication.

**`noSquare` equals a1** — `noSquare` is defined as `square(0)`, which is the same integer value as `a1`. It is only used for the en-passant target square and castling sentinel contexts where `a1` would never validly appear.

**Aspiration window fail-high** — when a root move scores ≥ `betaAspiration`, the inner move loop must break immediately (`hasFailedHigh = true; break`). Continuing to search remaining moves with `alpha ≥ betaAspiration` produces an inverted window and garbage scores that corrupt move ordering for the retry.

## Architecture

Pawnstar-Go is a UCI chess engine using bitboard board representation and alpha-beta search. It is a port of the C++ Pawnstar engine.

**Board representation** — `position` ([engine/position.go](engine/position.go)) is a value type used with copy-make traversal: `makeMove` and `makeNullMove` operate on a caller-owned copy of the struct. `Game.posStack []position` (heap-allocated, capacity 512) maintains the history stack; undoing a move is a slice truncation. The struct holds `pieces [7]bitboard` (indexed by piece type constant, `pawn`=1…`king`=6, index 0 unused) and `colors [2]bitboard` (indexed by color constant, `white`=0/`black`=1) instead of named per-piece fields, plus a `squares [64]piece` array for O(1) lookup, an incremental Zobrist hash, incremental PST score accumulators (`scores [2]int`), `colorToMove color`, and `halfMove uint8` (50-move clock). The king square is not stored; `kingSquare(c color) square` computes it on demand as `(pieces[king] & colors[c]).lsb()`. `positionFromFen` constructs a `position` from a FEN string and treats `'0'` as a no-op digit (advance by 0 squares) to handle non-standard FEN notation. `bitboard` ([engine/bitboard.go](engine/bitboard.go)) is a `uint64` with methods for all set operations.

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

The score field also carries the search score inside transposition table entries (packed via `move.withScore`). Move lists are `[]move` slices allocated with `make([]move, 0, 64)`. `move.equal` masks the score field when comparing identity.

**Attack generation** — [engine/tables.go](engine/tables.go) initialises all precomputed data in its `init()` function: magic bitboard tables for sliding pieces (`bishopAttacks`, `rookAttacks` — magic numbers found offline with seed 1337), ray tables (eight directions × 64 squares), knight/king/pawn attack tables, pawn structure masks, king pawn shelter masks, intervening-square tables, and Zobrist hashes (seeded 1968). `computePins` ([engine/pins.go](engine/pins.go)) identifies absolutely pinned pieces and their allowed ray before move generation.

**Move generation** — [engine/movegen.go](engine/movegen.go) implements fully legal move generation. `generateLegalMoves` and `generateLegalCaptures` are the entry points. Internally, `generateMoves` builds the king's forbidden-square set (with X-ray attacks through the king), handles double-check early return, restricts non-king moves to checker capture or interposition in single check, applies pin constraints via `pins.allowedSquares`, generates castling, and generates all pawn move types inline (including en-passant with a horizontal discovered-check test).

**Search** — `Game` ([engine/game.go](engine/game.go)) owns the transposition table, history table, eval table, chess clock, opening book, a `parallelSlots chan struct{}` semaphore (capacity `NumCPU`), and the position history stack. `isCancelPending` is an `atomic.Bool`. Search runs on a goroutine launched by `startThinking`; `stopThinking` stores `true` into `isCancelPending` then blocks on a `sync.WaitGroup`.

All recursive search functions ([engine/search.go](engine/search.go)) take `*searchState` rather than `*Game`. `searchState` holds: `posStack []position` (the goroutine's own copy-make stack, one entry per move played during search), `hashStack []hashStackEntry` (a compact `{hash, halfMoveClock}` record for every position from game start through the current node, used exclusively by `isDrawByRepetition`), and `killers [maxPly][2]move` (quiet moves that caused beta cutoffs at each ply). `hashStackEntry` is 16 bytes vs 168 bytes for a full `position`, making worker forks ~10× cheaper on the repetition history. `newSearchStateFromGame` builds `hashStack` from the full game history and seeds `posStack` with only the current position; `newWorkerState` copies the parent's `hashStack` (compact, 16 bytes × n) and seeds `posStack` with a single position — the fork point. All shared state (TT, history table, eval table, time control, cancel flag, semaphore) is accessed via the `game *Game` pointer — `searchState` carries no redundant pointer fields.

`searchRootNode` drives iterative deepening from `startDepth` (3). Each depth iteration uses an aspiration window of ±`aspirationDelta` (50 cp) around `prevScore` (the previous depth's result); the window is `[alphaAspiration, betaAspiration]`. On fail-low (`hasRaisedAlpha` is false after all moves) or fail-high (a move scored ≥ `betaAspiration`, detected mid-loop), the attempt loop retries once with the full `[initialAlpha, initialBeta]` window. `prevScore` is updated from `alpha` after each completed depth. Early-exit heuristics apply on the Standard clock when the best move and/or score are stable across depths.

`searchAlphaBeta` implements PVS, null-move pruning (R=4), LMR (−1 ply from the 2nd move onward; −2 ply additionally from move index 7 at depth > 3 with zero history count), check extensions via quiescence re-entry, and **Young Brothers Wait** parallel search: at null-window nodes with `depth ≥ 4`, the first two moves are searched sequentially on the main goroutine; remaining moves are dispatched to parallel workers via `searchMovesParallel`. Each worker runs `runWorker` (a standalone function), acquires a slot from `parallelSlots`, searches with a null window, signals the per-batch `atomic.Bool` cutoff on fail-high, and sends its result (a `move` with score packed in) to a channel. Workers never spawn sub-workers.

Move ordering: (1) TT move tried first; (2) remaining moves scored by `scoreAndSortMovesInner`: captures and promotions use Static Exchange Evaluation (`seeScore × 1000`), killer moves get a fixed bonus (`killerScore0 = 90 000`, `killerScore1 = 80 000`), and other quiet moves use an enemy-attack penalty plus PST delta and history count. Killer moves are updated via `ss.updateKillers(ply, m)` on every beta cutoff for quiet moves.

**Transposition table** — Single 512 MB table ([engine/transposition.go](engine/transposition.go)). Indexed by `hash % tableSize` (table size is kept odd for distribution). Each 24-byte entry stores hash, best move (score packed into upper 32 bits), depth, node type (Cut/All/PV), `age uint8` (generation counter), and `lock uint32` (per-entry spinlock occupying the 4 bytes of natural padding after `age`). Concurrent access from parallel workers uses `atomic.CompareAndSwapUint32`/`atomic.StoreUint32` — workers contend only when they hit the exact same slot. Replacement policy: prefer empty slots, stale entries (`entry.age != tt.currentAge`), then shallower entries; PV nodes always replace. `age()` increments `currentAge` (O(1)) at the start of each root search. `record(hash, move, depth, nodeType)` takes individual fields and stamps `currentAge` internally.

**Evaluation** — [engine/evaluate.go](engine/evaluate.go) returns the score from the side-to-move's perspective. Search piece values (used by `pstValue`): P=100, N=350, B=350, R=525, Q=1000 cp. SEE uses simplified values (P=100, N/B=300, R=500, Q=900) for speed. PST tables are stored with rank 8 at low indices; White reads via `sq ^ 0x38` (rank flip), Black reads directly. PST scores for all non-king pieces are accumulated into `scores[c]` incrementally. Evaluation adds: bishop-pair bonus (+30 cp), pawn structure scores (passed-pawn PST, defended +5, backward −10, doubled −10, isolated −20 cp), bishop and rook mobility (lookup tables by available-square count), and king safety (phase-dependent PST, three-row pawn shelter with friendly/enemy weights of ±15/±10/±5, open-file penalties, adjacent-piece bonuses). An alpha/beta material early-exit skips the full evaluation when material alone is more than 200 cp outside the search window. Results are cached in a 64 K-entry `evalHashTable`; each entry carries its own `lock uint32` CAS spinlock, matching the transposition table's locking design. SEE ([engine/see.go](engine/see.go)) is used in move ordering for captures.

**NNUE evaluation** — [engine/nnue.go](engine/nnue.go) is the default evaluator (a `*nnueNetwork` on `Game`; `nil` selects the handcrafted eval above). It is a direct port of the C++ Pawnstar NNUE: a Chess768 → 1024 → 1 network with 8 king buckets and SCReLU activation, quantised (QA=255, QB=64, scale=400). The net is loaded from a 32-byte `PSN1`-stamped `.bin` (little-endian int16 arrays: feature weights, feature bias, output weights, output bias) — the exact C++ `pawnstar-*.bin` format, read directly with no conversion. The v12 net is embedded via `go:embed` ([engine/nnue_embed.go](engine/nnue_embed.go), `engine/nnue/pawnstar-v12.bin`).

- **Feature indexing** (`nnueFeatureRow`): per perspective, `bucket*768 + colour*384 + (type−1)*64 + square`; the black perspective flips the colour bit and mirrors the square (`sq ^ 0x38`). King bucket per perspective from `nnueKingBucketMap[kingSquare]` (black king mirrored first).
- **Accumulator** (`nnueAccumulator`: two 1024-int16 perspectives) is maintained **lazily** on `searchState`. `playMove`/`makeNullMove` only mark the new ply's accumulator invalid; `undoMove` is free; `currentAccumulator` brings the current node up to date from the nearest valid ancestor (copy + incremental `update`) **only on an eval-cache miss**, so cut-off nodes and eval-cache hits never touch it. A king move crossing a bucket boundary triggers a full `refreshSide` of that perspective. Captures/EP/castling/promotion are handled uniformly via colour-bitboard diff + the `squares[]` array.
- **Kernels** are build-tag split: SIMD (`nnue_kernels_amd64.go`, `amd64 && goexperiment.simd`, 256-bit archsimd) and scalar (`nnue_kernels_generic.go`); bit-identical. `outputDot` is the SCReLU output dot; `addColumn`/`subColumn` are the accumulator column updates.
- **Score band**: `evaluateExact` keeps the faithful ±30000 clamp for the reference test; the search path (`searchEvalAcc`) clamps into ±(`winThreshold`−1000)=±8000 so a large eval can never collide with mate scores.
- **Gates**: `TestNNUEReference` (== C++ 250-FEN reference at 0 cp), `TestNNUEIncremental` (lazy accumulator == full refresh, bit-identical). Enable per-game with `game.EnableNNUE()` (embedded) or `game.LoadNNUEFile(path)` (external).

**History heuristic** — [engine/history.go](engine/history.go) records how often each (ply, from→to) pair raised alpha or caused a beta cutoff. Counts are added to the move sort score via `scoreAndSortMovesInner` and reset at the start of each root search via `historyTable.clear()`.

**Opening book** — [engine/book.go](engine/book.go) reads [book.txt](book.txt) (385 lines from TSCP) from the same directory as the binary at startup via `os.Executable()`. `loadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves; an absent file silently produces an empty book. `getMove` returns a random candidate for variety across games.

**UCI protocol** — `Game.HandleCommand` in [engine/game.go](engine/game.go) parses all standard UCI commands: `uci`, `isready`, `ucinewgame`, `position`, `go` (all clock types), `stop`, `quit`, and `setoption`. Two options are advertised: `UseNNUE` (check, default true — false selects the handcrafted eval) and `EvalFile` (string, default `<embedded>` — a path to a PSN1 net, or `<embedded>`/`default`/empty to reload the embedded net). `handleUciSetOption`/`parseSetOption` span multi-word option names and values. [main.go](main.go) is a thin entry point that reads stdin line-by-line and delegates to `HandleCommand`; it enables NNUE at startup unless `PAWNSTAR_NNUE=0`.

**Search diagnostics** — [engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, null-move/LMR/PVS attempt and fail counts, draw detection (`drawsByRepetition`), parallel search counts (`parallelSearches`, `parallelCutoffs`, `parallelMovesSearched`), aspiration window failures (`aspirationFails`), killer move beta cutoffs (`killerBetaCutoffs`), and TT spinlock contention (`spinlocksFind`, `spinlocksRecord`). Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect search behaviour. Counters accumulate across searches unless explicitly reset.
