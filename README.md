# Pawnstar-Go

A UCI chess engine written in Go. This is a port of the original C++ Pawnstar engine.

## Build and run

```bash
go build -o pawnstar-go .
./pawnstar-go              # interactive UCI session
```

Requires Go 1.24 or later.

Point any UCI-compatible GUI (Arena, Cute Chess, etc.) at the binary — no configuration needed.

## Testing

```bash
go test ./engine/ -run TestPerft -short -v        # all 131 EPD positions, depth ≤ D5, ~15s
go test ./engine/ -run TestPerft -v               # full suite, up to D6 (slow)
go test ./engine/ -run TestPerft/002 -v           # single position by 1-based index (002 = second position)
go test ./engine/ -run TestPerftExtra -short -v  # verify incremental Zobrist hash vs recomputed hash at every node
go test ./engine/ -run TestSearch -short -v       # search every EPD position to depth 9; exercises TT and search
go test ./engine/ -run TestBratkoKopec -v         # 24 Bratko-Kopec positions at depth 8; regression test for search quality
go test ./engine/ -run TestSEE -v                 # 18 static exchange evaluation cases; unit tests for evaluateStaticExchange
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

Move generation correctness is verified by perft — counting leaf nodes to a given depth against known values. All 131 EPD positions must pass at every depth before any change is considered correct.

The engine runs at approximately **42 Mnps** on the perft suite. The C++ original reaches ~600 Mnps via BMI2 PEXT instructions.

## Architecture

### Board representation

`position` ([engine/position.go](engine/position.go)) is a value type; `makeMove` and `makeNullMove` operate on a caller-owned copy. `Game` maintains a `posStack []position` history stack (capacity 512, heap-allocated); undoing a move is a slice truncation — there is no unmake logic.

The struct holds:
- `pieces [7]bitboard` — indexed by piece type constant (`pawn`=1 … `king`=6; index 0 unused)
- `colors [2]bitboard` — indexed by color constant (`white`=0, `black`=1)
- `squares[64]piece` — O(1) piece lookup by square
- `scores[2]int` — incremental material + PST totals per side (excludes king; updated by `addPiece`/`removePiece`/`movePiece`)
- `colorToMove color` — side to move as a direct field
- Zobrist hash maintained incrementally
- `halfMove uint8` — half-move clock for the 50-move rule

`bitboard` ([engine/bitboard.go](engine/bitboard.go)) is a `uint64` with methods for common set operations (`popCount`, `lsb`, `popLsb`, shifts in all eight directions).

### Move encoding

`move` ([engine/move.go](engine/move.go)) is a packed `int64`:

```
bits  0– 5   to square
bits  6–11   from square
bits 12–14   moving piece
bits 15–17   captured piece (noPiece if non-capture)
bits 18–21   move type (nonCapture/capture/promotionKnight/…/castling)
bit     22   gives-check flag
bits 32–63   sort score (i32, not part of move identity)
```

`move.equal` masks the score field when comparing. `move(0)` is the null sentinel (`moveNone()`). `String()` outputs UCI long-algebraic notation (`e2e4`, `e7e8q`). The sort score field doubles as storage for the search score inside transposition table entries (packed via `move.withScore`).

Move lists are plain `[]move` slices allocated with `make([]move, 0, 64)` — no fixed-capacity struct.

### Attack generation

[engine/tables.go](engine/tables.go) holds all precomputed data initialised once at startup via `init()`:

- **Magic bitboard tables** for sliding pieces (`bishopAttacks`, `rookAttacks`) — magic numbers found offline with seed 1337
- **Ray tables** in all eight compass directions, 64 squares × 8 directions
- **Knight, king, pawn attack tables** indexed by square
- **Pawn structure masks** — passed-pawn, isolated-pawn, adjacent-file masks per square
- **King pawn shelter masks** — three rows in front of the king per square
- **Intervening-square tables** — squares strictly between any two squares on a shared ray
- **Zobrist hashes** — seeded 1968; one per (color, piece, square) triple, plus castling, en-passant, and side-to-move

### Move generation

[engine/movegen.go](engine/movegen.go) implements fully legal move generation. `generateLegalMoves` and `generateLegalCaptures` are the entry points.

#### Pin computation

Before generating non-king moves, `computePins` ([engine/pins.go](engine/pins.go)) identifies absolutely pinned pieces:

1. Find candidate pinners: enemy sliders (bishops/rooks/queens) on a bishop or rook ray from the king.
2. For each candidate pinner, collect the squares strictly between the king and the pinner (`interveningSquares`).
3. If exactly one friendly piece occupies that ray segment, it is pinned. Its legal destination set is restricted to that ray (including the pinner's square, so the pinner can be captured).

The result is a `pins` struct with a `pinned` bitboard and a `[64]bitboard` of allowed destination sets indexed by square. `allowedSquares(sq)` returns the restriction for a pinned piece, or all squares for an unpinned one.

#### Legal move generation algorithm

`generateMoves` proceeds in this order:

1. **Build the king's forbidden-square set** — compute all enemy attacks, temporarily removing the king from the occupancy so that X-ray attacks through it are included. Pawn attacks are generated by shift; all other enemy pieces iterate over their attack sets.

2. **King moves** — generate moves to non-forbidden squares; separate captures and quiet moves.

3. **Double-check short-circuit** — if the king is in double check, only king moves are legal; return immediately.

4. **Single-check restrictions** — restrict the allowed capture set to the checker's square, and the allowed non-capture set to the squares between the king and the checker (`interveningSquares`).

5. **Piece moves (knight, bishop, rook, queen)** — for each piece, generate attacks, mask by `pins.allowedSquares(from)`, then split into captures and quiet moves.

6. **Castling** — only when not in check, with the transit squares unoccupied and unattacked.

7. **Pawn moves** — all pawn move types are generated inline by bulk bitboard shifts:
   - Single and double pushes (masked by empty squares and the allowed non-capture set)
   - Diagonal captures west and east (masked by enemy pieces and the allowed capture set)
   - Promotions (push and capture variants, four piece types each)
   - En-passant: candidates are found via reverse pawn-attack lookup; each candidate is additionally tested for the rare horizontal discovered check — with both pawns removed from the king's rank, check whether a rook or queen on that rank now attacks the king.
   - Pin constraints applied per candidate pawn via `pins.allowedSquares`.

### Search

`Game` ([engine/game.go](engine/game.go)) owns all persistent search state: transposition table, history table, evaluation cache, opening book, chess clock, a `parallelSlots chan struct{}` semaphore (capacity `runtime.NumCPU()`), and the `posStack []position` game-history stack. `isCancelPending` is an `atomic.Bool` set by `stopThinking` or by the time-check logic inside the search.

#### searchState and parallel structures

All recursive search functions ([engine/search.go](engine/search.go)) take `*searchState` rather than `*Game` directly. Each goroutine — the main search and every parallel worker — owns its own `searchState`:

```
searchState
├── posStack  []position        goroutine's copy-make stack; seeds with 1 entry (fork position)
├── hashStack []hashStackEntry  compact (hash, halfMoveClock) record for repetition detection
├── killers   [maxPly][2]move   quiet moves that caused beta cutoffs, indexed by ply
├── nodeCount int               nodes searched by this goroutine
├── game      *Game             pointer to all shared state (TT, history, eval cache, clock, cancel)
└── wasCutoff *atomic.Bool      per-batch cutoff signal; nil for the main search
```

`hashStackEntry` stores only `hash` (8 bytes) and `halfMoveClock` (1 byte + padding = 16 bytes total), compared to 168 bytes for a full `position`. This matters for worker forks: `newWorkerState` copies the parent's entire `hashStack` — O(depth) entries — at 16 bytes each rather than 168, making the fork ~10× cheaper on the repetition history. Each worker seeds its `posStack` with a single entry: the position at the fork point.

There are two constructors:
- `newSearchStateFromGame(game)` — builds the main search state. Converts all entries in `game.posStack` to compact `hashStackEntry` records (preserving the full game history for three-fold repetition detection), then seeds `posStack` with just the current position.
- `newWorkerState(parent, cutoff)` — builds a worker state. Copies the parent's `hashStack` (already compact), seeds `posStack` with the parent's current position, and wires up the per-batch `cutoff` signal.

All shared state — TT, history table, eval cache, time control, cancel flag, semaphore — is accessed only through the `game *Game` pointer; `searchState` carries no redundant copies.

#### Iterative deepening and aspiration windows

`searchRootNode` drives iterative deepening from `startDepth` (3):

1. Return the opening book move if available; return immediately for forced moves (single legal move).
2. Run an initial pass at `startDepth` to score and order moves.
3. For each depth from `startDepth+1` to `maxPly`:
   - Open a ±50 cp aspiration window around the previous depth's score (`aspirationDelta = 50`).
   - Search all root moves within `[alphaAspiration, betaAspiration]`. Break out of the move loop immediately on a fail-high (score ≥ betaAspiration) to avoid searching remaining moves with an inverted window.
   - If the result falls outside the window (fail-low: no move exceeded alphaAspiration; fail-high: a move hit betaAspiration), widen to `[initialAlpha, initialBeta]` and retry once.
   - Apply early-exit heuristics (Standard clock only): stop early if the best move is consistent and/or the score is stable across depths, as a fraction of the allocated time budget.

**Time allocation (Standard clock):** `allocated = timeRemaining / movesRemaining`. Hard stop = `max(100ms, min(allocated × 2, timeRemaining − 1s))`. The clock is checked every 65 536 nodes (`nodeCount & 0xFFFF == 0`).

#### `searchAlphaBeta` highlights

| Technique | Parameters |
|---|---|
| Principal variation search (PVS) | Null-window on moves 2+ when not in check; re-search full window on fail-high |
| Null-move pruning | R = 4; skipped in check, null-move positions, non-null windows, endgames; requires `pstScore > beta + 100` |
| Late move reduction (LMR) | −1 ply from the 2nd quiet non-check non-pawn/king move onward (depth > 2); −2 ply additionally from move index 7 (depth > 3, zero history count); re-search at full depth on fail-high |
| Young Brothers Wait (YBW) | At null-window nodes with depth ≥ 4, the first two moves are searched sequentially; remaining moves are dispatched to parallel workers via `searchMovesParallel` |
| Check extension | In quiescence, an in-check position re-enters `searchAlphaBeta` at the same depth instead of standing pat |
| Transposition table | Cut/All hits return early if score is conclusive; PV hits increment depth by 1 |

#### Young Brothers Wait parallel search

At eligible null-window nodes, `searchMovesParallel` dispatches each remaining move to a goroutine via `runWorker`:

1. A slot is acquired from the `parallelSlots` channel (blocks if all `NumCPU` slots are in use, providing back-pressure).
2. The worker builds its own `searchState` via `newWorkerState`, searching with a null window `[alpha, alpha+1]`.
3. On a fail-high, the worker stores `true` into the per-batch `wasCutoff atomic.Bool`, signalling sibling workers to abort their searches via `isCancelled()`.
4. The worker sends its result (a `move` with score packed into the upper 32 bits) to a buffered channel, then releases its slot.
5. The dispatcher waits for all workers, drains the channel, and returns the best-scoring move.

Workers never spawn sub-workers, preventing unbounded goroutine creation.

#### Move ordering

Within `searchAlphaBeta`, moves are ordered as follows:

1. **TT move** — tried first, before generating the full legal move list; on a beta cutoff, saves generating and scoring all moves.
2. **Remaining legal moves** sorted by `scoreAndSortMovesInner`:
   - *Captures and promotions* — scored by Static Exchange Evaluation (`evaluateStaticExchange`): `seeScore × 1000`. Winning captures (SEE > 0) rank highest; losing captures (SEE < 0) rank lowest.
   - *Killer moves* — quiet moves that caused beta cutoffs at this ply in sibling branches. Each ply stores two killers (`killers[ply][0/1]`). Killer 0 scores +90 000, killer 1 scores +80 000, placing them between winning captures and ordinary quiet moves.
   - *Other quiet moves* — scored by enemy-attack penalty (`−pieceValue × 1000` if the destination is attacked), plus PST delta and history count.

### Transposition table

Single 512 MB table ([engine/transposition.go](engine/transposition.go)). The entry count is rounded up to the next power of 2 so entries are indexed by `hash & mask` (a bitmask) rather than modulo.

Each `transposition` entry is exactly 24 bytes:

```
hash     zobrist   8 bytes   Zobrist hash of the stored position
move     move      8 bytes   best move; search score packed into upper 32 bits
depth    int16     2 bytes   search depth at which this entry was written
nodeType nodeType  1 byte    Cut / All / PV
age      uint8     1 byte    generation counter at write time
lock     uint32    4 bytes   per-entry spinlock (0 = unlocked, 1 = locked)
```

The `lock` field occupies the 4 bytes of natural padding that would otherwise follow `age`, keeping the struct at 24 bytes.

**Concurrency:** each entry carries its own `uint32` spinlock protected by `atomic.CompareAndSwapUint32` / `atomic.StoreUint32`. Workers contend only when they access the exact same slot simultaneously, which is rare given table size.

**Aging:** `transpositionTable.currentAge uint8` is incremented by `age()` — an O(1) operation — at the start of each root search. `record()` stamps `tt.currentAge` onto every entry it writes. An entry is considered stale when `entry.age != tt.currentAge`.

**Replacement policy:** replace if the slot is empty, the entry is from a previous search (`age ≠ currentAge`), the new entry is at least as deep, or the new entry is a PV node.

**`record()` signature:** `record(hash zobrist, m move, depth int16, nt nodeType)` — takes individual fields and builds the entry internally, stamping `currentAge` itself. Callers pass the move with score already packed via `m.withScore(score)`.

### Evaluation

All scores are in centipawns from the perspective of the side to move.

**Piece values:** P = 100, N = 350, B = 350, R = 525, Q = 1000. SEE uses simplified values (P=100, N/B=300, R=500, Q=900) for speed.

**Piece-square tables** ([engine/evaluate.go](engine/evaluate.go)) ported from the C Pawnstar engine. Stored with rank 8 at low indices; White reads via `sq ^ 0x38` (rank flip), Black reads directly. PST scores for all non-king pieces are accumulated into `scores[c]` incrementally by `addPiece`/`removePiece`/`movePiece` in `position.go` — no re-scan on evaluation.

**Bishop pair:** +30 cp when both bishops are present (one on each colour).

**Pawn structure** is computed per side by `determinePawnStructure` and scored as: passed pawns (rank-based PST bonus), defended pawns (+5 cp each), backward pawns (−10 cp each), doubled pawns (−10 cp each), isolated pawns (−20 cp each). A doubled passed pawn is not counted as passed (the rear pawn is obstructed).

**Mobility:** Bishop attacks (excluding squares defended by enemy pawns) and rook attacks are each scored via a lookup table indexed by the number of available squares (bishop: −20 to +20 over 14 buckets; rook: −10 to +20 over 15 buckets).

**King safety:** King PST switches between midgame and endgame tables via `isEndgame` (true if neither side has a queen, or every queen-side has no rooks and at most one minor piece). In the midgame: a three-row pawn shelter scores friendly pawns +15/+10/+5 and enemy pawns −15/−10/−5 across the three ranks in front of the king; open files near the king score −15 (king's file) and −10 (adjacent files); friendly non-pawn pieces score +10 (adjacent ring) and +5 (two-square ring).

An alpha/beta material early-exit skips the full evaluation when the material score alone is more than 200 cp outside the search window. Results are cached in a 64 K-entry `evalHashTable`; each entry carries its own `lock uint32` spinlock (atomic CAS/store), matching the transposition table's locking design.

**Static Exchange Evaluation** ([engine/see.go](engine/see.go)) simulates the sequence of captures on a square using the least-valuable-attacker method, propagating scores backwards so each side can choose not to recapture if it would lose material. Used in move ordering to distinguish winning from losing captures.

### Opening book

`openingBook` ([engine/book.go](engine/book.go)) reads [book.txt](book.txt) (385 lines from TSCP) from the same directory as the binary at startup. `loadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves; an absent file silently produces an empty book. `getMove` returns a random candidate, giving variety across games.

### History heuristic

`historyTable` ([engine/history.go](engine/history.go)) records how often each (ply, from→to) pair raised alpha or caused a beta cutoff. Counts are added to the move sort score via `scoreAndSortMovesInner` and reset at the start of each root search.

### UCI protocol

`Game.HandleCommand` ([engine/game.go](engine/game.go)) processes commands on the calling goroutine. Supported:

| Command | Notes |
|---|---|
| `uci` | Prints engine id and `uciok` |
| `isready` | Prints `readyok` |
| `ucinewgame` | Stops search, resets position and clock |
| `position startpos [moves ...]` | Sets position from start FEN, applies move list |
| `position fen <fen> [moves ...]` | Sets position from FEN, applies move list |
| `go wtime <ms> btime <ms> [movestogo <n>]` | Standard clock |
| `go movetime <ms>` | Fixed time per move |
| `go depth <n>` | Fixed depth |
| `go infinite` | Search until `stop` |
| `stop` | Signals search goroutine to halt |
| `quit` | Stops search and exits |

[main.go](main.go) is a thin entry point: reads stdin line-by-line and calls `game.HandleCommand`.

### Search diagnostics

[engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, null-move/LMR/PVS attempt and fail counts, draw detection, parallel batch/cutoff/move counts, aspiration window failures, killer move beta cutoffs, and TT spinlock contention. Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect search behaviour. Counters accumulate across searches unless explicitly reset.
