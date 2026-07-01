# Pawnstar-Go

A UCI chess engine written in Go. This is a port of the original C++ Pawnstar engine.

## Build and run

```bash
make build                # build with GOEXPERIMENT=simd -> ./pawnstar-go
./pawnstar-go             # interactive UCI session
```

Requires Go 1.26 or later. The engine evaluates with an embedded **NNUE** network (the only evaluator); its SIMD kernels use `simd/archsimd`, which is gated behind `GOEXPERIMENT=simd` (the `Makefile` sets this). A plain `go build` also works, falling back to bit-identical scalar kernels.

Point any UCI-compatible GUI (Arena, Cute Chess, etc.) at the binary — no configuration needed. UCI option `EvalFile` (path to a PSN1 net, or `<embedded>` for the built-in one) loads an alternative network.

## Testing

```bash
go test ./engine/ -run TestPerft -short -v        # all 131 EPD positions, depth ≤ D5, ~15s
go test ./engine/ -run TestPerft -v               # full suite, up to D6 (slow)
go test ./engine/ -run TestPerft/002 -v           # single position by 1-based index (002 = second position)
go test ./engine/ -run TestPerftExtra -short -v  # verify incremental Zobrist hash vs recomputed hash at every node
go test ./engine/ -run TestSearch -short -v       # search every EPD position to depth 9; exercises TT and search
go test ./engine/ -run TestBratkoKopec -v         # 24 Bratko-Kopec positions at depth 8; regression test for search quality
go test ./engine/ -run TestSEE -v                 # 24 SEE cases + TestSEECrossCheck (fast == make-move reference over ~1M captures)
go test ./engine/ -run TestOpeningBook -v         # opening-book '?' (questionable) move handling
make test-uci                                     # UCI protocol as a process (handshake, seldepth, searchmoves, currmove, bench)
go test ./engine/ -run TestNNUEReference -v       # NNUE exact eval == C++ 250-FEN reference (0 cp)
go test ./engine/ -run TestNNUEIncremental -v     # lazy accumulator == full refresh (bit-identical)
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

Run with `GOEXPERIMENT=simd` (or `make test`) to exercise the SIMD kernels. Move generation correctness is verified by perft — counting leaf nodes to a given depth against known values. All 131 EPD positions must pass at every depth before any change is considered correct.

Perft (move generation only) runs at approximately **42 Mnps**. The NNUE evaluator used in real search is slower per node but far stronger — when first ported it measured **+572 Elo** over the engine's former handcrafted evaluation in a fixed-depth self-play SPRT (146W/3L/5D); that handcrafted eval has since been removed. The C++ original reaches ~600 Mnps via BMI2 PEXT instructions.

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
bits 23–24   transposition node type (nodeType)
bits 25–32   transposition depth searched (int8)
bits 33–40   transposition age / generation (uint8)
bits 41–63   score (signed 23-bit)
```

`move.equal`/`isNone` mask to the identity bits (0–21), so the score and TT metadata never affect identity. `move(0)` is the null sentinel (`moveNone()`). `String()` outputs UCI long-algebraic notation (`e2e4`, `e7e8q`). Bits 23–63 are dual-purpose: a move-ordering sort score (`withScore`/`score`, bits 41–63) during search, or packed `{score, depth, nodeType, age}` metadata inside a transposition entry (`withTTData`/`setTTAge`). A move is only ever one or the other, so the spare bits are shared — which lets a TT cell be exactly two 64-bit words for the lockless table.

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

`Game` ([engine/game.go](engine/game.go)) owns all persistent search state: transposition table, history table, evaluation cache, opening book, chess clock, and the `posStack []position` game-history stack. `isCancelPending` is an `atomic.Bool` set by `stopThinking` or by the time-check logic inside the search.

The search uses **Lazy SMP** (as in the C++ engine): `searchRootNode` runs `threadCount` threads, each an independent iterative-deepening search of the whole root sharing only the lockless transposition table and eval cache. The main thread (id 0) is authoritative — it prints `info`, manages time, and returns the move — while helper threads deepen the shared TT, desynchronised by a Stockfish-style iteration-skip schedule so they explore diverse lines. Thread count defaults to 1 (raised by `main()` from `PAWNSTAR_THREADS`/`NumCPU`, or the UCI `Threads` option). All per-thread state (history, killers, continuation history, countermoves, accumulator, diagnostics) lives on each thread's `searchState`, so nothing is shared except the two lockless tables — the concurrent search is data-race free with no locks.

#### searchState

All recursive search functions ([engine/search.go](engine/search.go)) take `*searchState` rather than `*Game` directly:

```
searchState
├── posStack  []position        copy-make stack; seeds with 1 entry (current position)
├── hashStack []hashStackEntry  compact (hash, halfMoveClock) record for repetition detection
├── killers   [maxPly][2]move   quiet moves that caused beta cutoffs, indexed by ply
├── accStack  []nnueAccumulator lazy NNUE accumulator per ply (+ accValid []bool)
├── nodeCount int               nodes searched this iteration
└── game      *Game             pointer to all shared state (TT, history, eval cache, clock, cancel)
```

`hashStackEntry` stores only `hash` (8 bytes) and `halfMoveClock` (1 byte + padding = 16 bytes total), compared to 168 bytes for a full `position`, keeping the repetition scan cache-friendly. `newSearchStateFromGame(game)` builds the search state: it converts all entries in `game.posStack` to compact `hashStackEntry` records (preserving the full game history for three-fold repetition detection), then seeds `posStack` with just the current position. All shared state — TT, history table, eval cache, time control, cancel flag — is accessed only through the `game *Game` pointer.

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
| Check extension | In quiescence, an in-check position re-enters `searchAlphaBeta` at the same depth instead of standing pat |
| Transposition table | Cut/All hits return early if score is conclusive; PV hits increment depth by 1 |

Under Lazy SMP each thread walks the move list independently; parallelism comes from many whole-tree searches sharing the lockless transposition table, not from splitting a node.

#### Move ordering

Within `searchAlphaBeta`, moves are ordered by `scoreAndSortMoves`, a banded scheme ported from the C++ engine (keyed by `prevMove`, the move played at the parent):

1. **TT move** — tried first, before generating the full legal move list; on a beta cutoff, saves generating and scoring all moves.
2. **Remaining legal moves** sorted into disjoint score bands:
   - *Winning captures/promotions* — `winningCaptureBase (1<<20) + SEE`, on top.
   - *Killers* — the two quiet moves that caused beta cutoffs at this ply (`killers[ply][0/1]`), scoring `1<<19 + 1` and `1<<19`.
   - *Countermove* — the best quiet refutation recorded for `prevMove` (`1<<19 − 1`).
   - *Other quiet moves* — `history + continuationHistory`, clamped just below the countermove.
   - *Losing captures* — their negative SEE, sinking below the quiet moves (this sign is what quiescence SEE pruning keys on).

On a beta cutoff or alpha raise, `recordGood` credits the move to the history and continuation-history tables; a quiet cutoff additionally records it as a killer and as the countermove to `prevMove`. The countermove table and 1-ply continuation history live on the `searchState` and persist across a search.

### Transposition table

Single table ([engine/transposition.go](engine/transposition.go)) sized from `hashTableMB`. The entry count is rounded up to the next power of 2 so entries are indexed by `hash & mask` (a bitmask) rather than modulo.

**Lockless — Hyatt's XOR trick** (ported from the C++ engine). All of score, depth, node type and age are packed into the move's spare bits (see *Move encoding*), so a cell holds just two 64-bit words:

```
data  atomic.Uint64   the packed move (move identity + score/depth/nodeType/age)
key   atomic.Uint64   hash ^ data
```

`find` loads both words with relaxed atomics and accepts the cell only when `key ^ data == hash`. This one check does two jobs:

- **Torn-pair detection.** If two threads store to the same cell concurrently and a reader catches `key` from one store and `data` from the other, `key ^ data` no longer equals any real hash, so the read is rejected as a **miss** — never returned as a wrong entry. A data race on the pair is therefore harmless *by construction*: the worst outcome is a spurious miss and a re-search, never a bad move or a corrupt score. No per-entry lock is needed — this is what lets the Lazy SMP threads share the table.
- **Collision guard.** Reconstructing the full 64-bit hash also rejects entries stored for a different position that happens to index to the same slot.

Each individual word is an `atomic.Uint64` (relaxed load/store), so no *single* word is ever torn and Go's race detector stays quiet; the XOR key supplies the cross-word consistency that per-word atomicity alone cannot. Racing writers resolve to a benign last-writer-wins.

**Replacement policy** (`record`): overwrite when the slot is empty, the existing entry is from an older generation (`cur.ttAge() != currentAge`), the new depth is ≥ the stored depth, or the new node is PV.

**Aging:** `currentAge uint8` is bumped by `age()` (O(1), wraps at 256) at the start of each root search; `record` stamps it onto each entry via `setTTAge`.

**API:** `find(hash) → (transposition, bool)` returns `{hash, move}` with `score()`/`depth()`/`nodeType()`/`age()` reading straight out of the packed move; `record(hash, m, depth, nt)` takes a move whose search score is already packed via `m.withScore(score)`.

### Evaluation

The engine evaluates exclusively with the embedded **NNUE** network (see below); scores are in centipawns from the perspective of the side to move. Two pieces of shared, evaluator-independent machinery live in [engine/evaluate.go](engine/evaluate.go):

**Eval cache:** results are cached in the **lockless** `evalHashTable` (~8 MB, ported from the C++ `EvalCache`): each slot is a single `atomic.Uint64` packing the top 48 bits of the hash and a 16-bit score, so a load/store is atomic with no lock and a foreign/evicted entry fails the key check and reads as a miss. The cached value is the raw, clock-independent eval; `clear()` is called whenever the loaded net changes (`EvalFile`).

**Fifty-move scaling:** `scaleByRule50` ramps the raw eval toward draw as the halfmove clock climbs past 20 plies (`rule50ScaleThreshold`), applied on the way out of the cache (never stored), so the search has a gradient to make progress rather than shuffle a won-but-unconverted position (fixes leaks like KBN-vs-K).

**Static Exchange Evaluation** ([engine/see.go](engine/see.go)) simulates the sequence of captures on a square using the least-valuable-attacker method, propagating scores backwards so each side can choose not to recapture if it would lose material. `evaluateStaticExchange` is MakeMove-free (it applies the capture to a bitboard `seeBoard` and detects check with `seeAttackersTo`) and is used in move ordering and quiescence pruning to distinguish winning from losing captures. Piece values here are the simplified P=100, N/B=300, R=500, Q=900.

### NNUE evaluation

[engine/nnue.go](engine/nnue.go) is the engine's only evaluator, a direct Go port of the C++ Pawnstar NNUE — a Chess768 → 1024 → 1 network with 8 king buckets and SCReLU activation, quantised (QA=255, QB=64, scale=400). It reads the C++ `pawnstar-*.bin` net format directly (32-byte `PSN1` header + little-endian int16 weight arrays); the v12 net is embedded in the binary via `go:embed`. When first ported it measured **+572 Elo** over the engine's former handcrafted evaluation (fixed-depth self-play SPRT); that handcrafted eval has since been removed.

- **Feature transformer / accumulator:** two 1024-int16 perspective accumulators. Feature row per perspective is `bucket*768 + colour*384 + (type−1)*64 + square`, with the black perspective mirroring colour and square (`sq ^ 0x38`); the king bucket comes from `nnueKingBucketMap`.
- **Lazy accumulator:** maintained on `searchState` and computed on demand. `playMove`/`makeNullMove` only invalidate the new ply; `undoMove` is free; `currentAccumulator` catches up from the nearest valid ancestor (copy + incremental `update`) **only when an eval reads it on an eval-cache miss**. A king move that crosses a bucket boundary refreshes that perspective from scratch. Captures, en passant, castling and promotion are handled uniformly through the colour bitboards plus the `squares[]` array.
- **Output layer:** two forward passes (mirroring the C++ engine). `evaluateExact` is the int16 SCReLU reference. `evaluateInt8` is the **search's hot-path evaluator**: activations requantised to uint8 (`(clamp(x,0,QA)²+256) >> 9`) and the output weights to int8 range, scaled back before dequant — faster, at a bounded ≤40 cp (measured 28) deviation from the int16 reference. Exactly the C++ int8 layer.
- **Kernels** are build-tag split into SIMD (`amd64 && goexperiment.simd`, 256-bit `simd/archsimd`) and scalar fallbacks, and are bit-identical (the int8 dot is the exact integer sum `Σ u8·w8`, so scalar == SIMD). SIMD is ~3–5× faster on the output dot and column updates.
- **Correctness:** `TestNNUEReference` matches the C++ exact int16 evaluator on 250 FENs to 0 cp; `TestNNUEInt8` bounds the search's int8 eval within 40 cp of that reference (and confirms scalar == SIMD); `TestNNUEIncremental` proves the lazy accumulator equals a full refresh at every node. Load a different net at runtime with the `EvalFile` UCI option.

### Opening book

`openingBook` ([engine/book.go](engine/book.go)) parses [engine/book.txt](engine/book.txt) (385 lines from TSCP), embedded in the binary via `go:embed` ([engine/book_embed.go](engine/book_embed.go)) so no external file is needed. `loadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves. `getMove` returns a random candidate, giving variety across games.

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
| `go wtime <ms> btime <ms> [winc <ms> binc <ms>] [movestogo <n>]` | Standard clock; without `movestogo`, moves-to-go is a decreasing horizon `max(40−move, 5)`. Increment is parsed but not folded into the budget (SPRT-neutral) |
| `go movetime <ms>` | Fixed time per move |
| `go depth <n>` | Fixed depth |
| `go infinite` | Search until `stop` |
| `go nodes <n>` | Search until the node limit (checked per thread) |
| `go searchmoves <m…>` | Restrict the root search to the given moves |
| `bench` | Fixed-position node-count benchmark (deterministic; single-threaded, book off) |
| `stop` | Signals search goroutines to halt |
| `quit` | Stops search and exits |
| `setoption name OwnBook value <true\|false>` | Consult the opening book (false forces a real search) |
| `setoption name EvalFile value <path\|<embedded>>` | Load an external PSN1 net, or reload the embedded one |
| `setoption name Threads value <n>` | Lazy SMP search-thread count |
| `setoption name Hash value <mb>` | Resize the transposition table |
| `setoption name Clear Hash` | Clear the TT and eval cache |
| `setoption name Move Overhead value <ms>` | Time reserved from every deadline for GUI/network lag |

`info` lines carry `depth`, `seldepth`, `score cp`/`score mate N`, `time`, `nodes`, `nps`, `hashfull`, and `pv`. [main.go](main.go) reads stdin line-by-line and calls `game.HandleCommand` and sets the thread count from `PAWNSTAR_THREADS`/`NumCPU` (NNUE is installed by `NewGame`). (Pondering is the one UCI feature not implemented.)

### Search diagnostics

[engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, null-move/LMR/PVS attempt and fail counts, draw detection, aspiration window failures, and killer move beta cutoffs. Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect search behaviour. Counters accumulate across searches unless explicitly reset.
