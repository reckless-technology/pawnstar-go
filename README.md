# Pawnstar-Go

A UCI chess engine written in Go. This is a port of the original C++ Pawnstar engine.

## Build and run

```bash
make build                # build with GOEXPERIMENT=simd -> ./pawnstar-go
./pawnstar-go             # interactive UCI session
```

Requires Go 1.26 or later. The engine evaluates with an embedded **NNUE** network (the only evaluator). Its hot kernels are build-tag split, all bit-identical: on **amd64** they use `simd/archsimd` plus hand-written AVX2 assembly, gated behind `GOEXPERIMENT=simd` (the `Makefile` sets this); on **arm64** they use hand-written NEON assembly (no experiment needed). A plain `go build` on any other target falls back to bit-identical scalar kernels.

Point any UCI-compatible GUI (Arena, Cute Chess, etc.) at the binary — no configuration needed. UCI option `EvalFile` (path to a PSN1 net, or `<embedded>` for the built-in one) loads an alternative network.

## Testing

```bash
go test ./engine/ -run TestPerft -v               # all 128 EPD positions, capped at depth 5 (~12s)
go test ./engine/ -run TestPerft -short -v        # same depth-5 cap (short no longer differs)
go test ./engine/ -run TestPerft/002 -v           # single position by 1-based index (002 = second position)
go test ./engine/ -run TestPerftExtra -short -v  # verify incremental Zobrist hash vs recomputed hash at every node
go test ./engine/ -run TestSearch -short -v       # search every EPD position to depth 9; exercises TT and search
go test ./engine/ -run TestBratkoKopec -v         # 24 Bratko-Kopec positions at depth 8 (NNUE on); search-quality gate, bit-identical to C++
go test ./engine/ -run TestSEE -v                 # 24 SEE cases + TestSEECrossCheck (fast == make-move reference over ~1M captures)
go test ./engine/ -run TestOpeningBook -v         # opening-book '?' (questionable) move handling
make test-uci                                     # UCI protocol as a process (handshake, seldepth, searchmoves, currmove, bench)
go test ./engine/ -run TestNNUEReference -v       # NNUE exact int16 eval == C++ 250-FEN reference (0 cp)
go test ./engine/ -run TestNNUEInt8 -v            # search's int8 eval within 40 cp of int16 ref (also scalar == SIMD)
go test ./engine/ -run TestNNUEIncremental -v     # lazy accumulator == full refresh (bit-identical)
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

Run with `GOEXPERIMENT=simd` (or `make test`) to exercise the SIMD kernels. Move generation correctness is verified by perft — counting leaf nodes to a given depth against known values. All 128 EPD positions must pass at every depth before any change is considered correct.

Perft (move generation only) runs at approximately **200 Mnps** single-threaded (start position, amd64 dev machine) and is core-count-independent now that move generation is allocation-free. The NNUE evaluator used in real search is slower per node but far stronger — when first ported it measured **+572 Elo** over the engine's former handcrafted evaluation in a fixed-depth self-play SPRT (146W/3L/5D); that handcrafted eval has since been removed. The C++ original reaches ~540 Mnps on the same machine via BMI2 PEXT sliding attacks — a raw move-gen gap of ~2.7×. In real NNUE search the Go↔C++ speed gap is much smaller than that raw move-gen gap — search nps is dominated by evaluation, where the SIMD kernels are close to the C++ hot path. Over the 24 Bratko-Kopec positions at depth 10 (single-threaded, bit-identical search trees) the Go engine runs ~1.06 Mnps versus the C++ engine's ~1.77 Mnps — **~1.66× slower** on the amd64 machine. On **arm64 (Apple Silicon)**, after the hand-written NEON kernels, the gap is essentially closed: the 16-position `bench` (identical 7,007,401-node trees, interleaved) runs at **~1.14 Mnps vs C++ ~1.15–1.19 Mnps — parity (~0.98×)**, up 6.0× from ~190k nps when arm64 was fully scalar.

## Architecture

### Board representation

`position` ([engine/position.go](engine/position.go)) is a value type; `makeMove` and `makeNullMove` operate on a caller-owned copy. `Game` maintains a `posStack []position` history stack (capacity 512, heap-allocated); undoing a move is a slice truncation — there is no unmake logic.

The struct holds:
- `pieces [7]bitboard` — indexed by piece type constant (`pawn`=1 … `king`=6; index 0 = the occupied-squares bitboard, maintained incrementally so `occupied()` is a plain load)
- `colors [2]bitboard` — indexed by color constant (`white`=0, `black`=1)
- `squares [64]piece` — O(1) piece lookup by square
- `checkers bitboard` — pieces currently giving check to the side to move (refreshed by `makeMove`)
- `hash zobrist` — Zobrist hash, maintained incrementally (recomputed from scratch only in `positionFromFen`)
- `epSquare square` — en-passant target square, or `noSquare`
- `halfMove uint8` — half-move clock for the 50-move rule; `fullMove uint8` — full-move number (0-based)
- `flags uint8` — transient move flags (currently only `flagNullMove`); `castling castlingRights` — remaining castling rights
- `colorToMove color` — side to move as a direct field

There is no per-side score accumulator: the former handcrafted evaluator (PST/material `scores[2]int`) has been removed — NNUE is the only evaluator (see below).

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

Move lists use a **stack-resident `moveList`** (a fixed `[256]move` array + count; [engine/movegen.go](engine/movegen.go)): the hot paths (`perft`, `searchAlphaBeta`, `searchQuiescent`) declare a local `var ml moveList` and call `generateLegalMovesInto`/`generateLegalCapturesInto`, so move generation is **allocation-free** (escape analysis keeps `ml` on the stack). Cold callers (UCI setup, opening book, tests) use the slice-returning `generateLegalMoves`/`generateLegalCaptures` wrappers, which copy into a right-sized heap slice. This replaced a per-node `make([]move, 0, 64)` that heap-allocated on every node.

### Attack generation

[engine/tables.go](engine/tables.go) holds all precomputed data initialised once at startup via `init()`:

- **Magic bitboard tables** for sliding pieces (`bishopAttacks`, `rookAttacks`) — magic numbers found offline with seed 1337
- **Ray tables** in all eight compass directions, 64 squares × 8 directions
- **Knight, king, pawn attack tables** indexed by square, plus empty-board bishop/rook attack tables (`bishopAttacksEmpty`/`rookAttacksEmpty`, used by pin detection)
- **Intervening-square tables** — squares strictly between any two squares on a shared ray
- **Zobrist hashes** — filled from a fixed-seed `xorshift*` PRNG (seed `0xAA55AA55AA55AA55`, shared bit-for-bit with the C++ engine); one per (color, piece, square) triple, plus castling, en-passant, and side-to-move

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

`Game` ([engine/game.go](engine/game.go)) owns only the state shared across Lazy SMP threads: the lockless transposition table and eval cache, the opening book, the chess clock, the `threadCount`, and the `posStack []position` game-history stack. Everything else — history, killers, continuation history, countermoves, the NNUE accumulator stack, node counts — is **per-thread**, living on each thread's `searchState` so the concurrent search never contends. `isCancelPending` is an `atomic.Bool` set by `stopThinking` or by the time-check logic inside the search.

The search uses **Lazy SMP** (as in the C++ engine): `searchRootNode` runs `threadCount` threads, each an independent iterative-deepening search of the whole root sharing only the lockless transposition table and eval cache. The main thread (id 0) is authoritative — it prints `info`, manages time, and returns the move — while helper threads deepen the shared TT, desynchronised by a Stockfish-style iteration-skip schedule so they explore diverse lines. Thread count defaults to 1 (raised by `main()` from `PAWNSTAR_THREADS`/`NumCPU`, or the UCI `Threads` option). All per-thread state (history, killers, continuation history, countermoves, accumulator, diagnostics) lives on each thread's `searchState`, so nothing is shared except the two lockless tables — the concurrent search is data-race free with no locks.

#### searchState

All recursive search functions ([engine/search.go](engine/search.go)) take `*searchState` rather than `*Game` directly:

```
searchState
├── posStack            []position        copy-make stack; seeds with 1 entry (current position)
├── hashStack           []hashStackEntry  compact (hash, halfMoveClock) record for repetition detection
├── killers             [maxPly][2]move   quiet moves that caused beta cutoffs, indexed by ply
├── history             historyTable      per-thread history heuristic (per-thread to avoid contention)
├── countermoves        [contKeys]move    best quiet refutation keyed by the previous move
├── continuationHistory []int16           1-ply continuation history, keyed by [prev.pieceTo()][move.pieceTo()]
├── accStack            []nnueAccumulator lazy NNUE accumulator per ply (+ accValid []bool)
├── nodeCount           int               nodes searched this iteration
├── seldepth            int               deepest ply reached (UCI info seldepth)
├── dbg                 *debugX           diagnostics: global dbgx for the main thread, a throwaway per helper
└── game                *Game             pointer to shared state (TT + eval cache, clock, cancel)
```

`hashStackEntry` packs the Zobrist hash (top 56 bits) and the half-move clock (low 8 bits) into a **single `uint64`** — 8 bytes, versus 160 bytes for a full `position` — keeping the repetition scan cache-friendly. Repetition detection compares only the top 56 hash bits (ample for a scan window of at most ~100 plies); a zero clock byte marks an irreversible-move boundary that stops the scan. `newSearchStateFromGame(game)` builds the search state: it converts all entries in `game.posStack` to compact `hashStackEntry` records (preserving the full game history for three-fold repetition detection), then seeds `posStack` with just the current position. Only truly shared state — the lockless TT + eval cache, the read-only time control, and the atomic cancel flag — is reached through the `game *Game` pointer; the history/killers/countermove/continuation tables and the accumulator stack are all local to this `searchState`.

#### Iterative deepening

`searchRootNode` drives iterative deepening from `startDepth` (3):

1. Return the opening book move if available; return immediately for forced moves (single legal move).
2. Run an initial pass at `startDepth` to score and order moves.
3. For each depth from `startDepth+1` to `maxPly`:
   - Search all root moves in a single **full-window** pass `[initialAlpha, initialBeta]`: `alpha` starts at the minimum and is raised as moves improve, so PVS null-windows every move after the first. There is no aspiration window (the C++ engine tested and rejected aspiration; a fixed-depth cross-engine BK diff confirmed it was the largest Go↔C++ divergence, so Go dropped it). Combined with shared-`xorshift64star` Zobrist hashing, matched score scale, a per-move null-window check, and matched PV capture, **all 24 Bratko-Kopec positions now match the C++ reference bit-for-bit** — identical bestmove, score, node count, and PV at fixed depth (verified at depths 8, 10 and 12, single-threaded, book off; at depth 12 the per-position diagnostic counters — cutoffs, TT hits, prunes, reductions, extensions — are identical too).
   - Apply early-exit heuristics (Standard clock only): stop early if the best move is consistent and/or the score is stable across depths, as a fraction of the allocated time budget.

**Time allocation (Standard clock):** `allocated = timeRemaining / movesRemaining`. Hard stop = `max(100ms, min(allocated × 2, timeRemaining − 1s))`. The hard deadline (and any `go nodes` limit) is polled every 2048 nodes (`nodeCount & 0x7FF == 0`) — a coarser interval let a node-heavy batch overrun the deadline; 2048 keeps the overrun sub-millisecond. During a ponder search `hardDeadlineNanos` holds a no-deadline sentinel, so the poll never fires until `ponderhit` swaps in a real deadline.

#### `searchAlphaBeta` highlights

The margins carry over unchanged from the C++ engine (the NNUE eval is the same centipawn scale). At non-PV, non-check nodes a static eval is computed **once** and drives both reverse futility pruning and the null-move gate. The null-window predicate `beta == alpha+1` is recomputed **per move** against the live alpha (not precomputed once before the loop) — at a PV node, once an earlier move raises alpha to `beta−1` the remaining moves are effectively null-window and LMP/LMR must engage; this is required for bit-identical parity with C++.

| Technique | Parameters |
|---|---|
| Check extension | A node in check is searched one ply deeper; quiescence re-enters `searchAlphaBeta` (rather than standing pat) when in check |
| Internal iterative reduction | −1 ply at depth ≥ `noTTReduceMinDepth` when there is no TT move |
| Reverse futility pruning | non-PV non-check: return `eval` when depth ≤ `rfpMaxDepth` and `eval − rfpMargin·depth ≥ beta` |
| Null-move pruning | R = 4; only when `eval ≥ beta` and there is enough material (no zugzwang); skipped in check / null-move positions |
| Late move pruning | skip quiet moves at depth ≤ `lmpMaxDepth` once `moveIdx ≥ lmpBase + depth²` |
| Principal variation search (PVS) | first move full window; later moves null-window; re-search full window on fail-high |
| Late move reduction (LMR) | null-window non-check: −1 from move index > 3; −2 from index > 6 (depth > 3); −3 additionally when history count is zero; re-search at full depth on fail-high |
| Quiescence SEE pruning | stop at the first losing capture (keys on the negative-SEE sign of losing captures in move ordering) |
| Transposition table | Cut/All hits return early if score is conclusive; PV hits extend depth by 1 |

The pruning/reduction stack was worth +183 Elo (SPRT, 10+0.1); the check extension plus the countermove/continuation-history move ordering a further +160 Elo. Under Lazy SMP each thread walks the move list independently; parallelism comes from many whole-tree searches sharing the lockless transposition table, not from splitting a node.

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

The [engine/nnue/](engine/nnue/) package is the engine's only evaluator, a direct Go port of the C++ Pawnstar NNUE — a Chess768 → 1024 → 1 network with 8 king buckets and SCReLU activation, quantised (QA=255, QB=64, scale=400). It reads the C++ `pawnstar-*.bin` net format directly (32-byte `PSN1` header + little-endian int16 weight arrays); the v12 net is embedded in the binary via `go:embed`. The package has no engine imports — positions cross the boundary as an `nnue.Board` (raw `uint64` bitboards + `uint8` piece codes), built by the small bridge in [engine/nnue_bridge.go](engine/nnue_bridge.go), which also hosts `EnableNNUE`/`LoadNNUEFile` and the search-band clamp. When first ported it measured **+572 Elo** over the engine's former handcrafted evaluation (fixed-depth self-play SPRT); that handcrafted eval has since been removed.

- **Feature transformer / accumulator:** two 1024-int16 perspective accumulators. Feature row per perspective is `bucket*768 + colour*384 + (type−1)*64 + square`, with the black perspective mirroring colour and square (`sq ^ 0x38`); the king bucket comes from `nnueKingBucketMap`.
- **Lazy accumulator:** maintained on `searchState` and computed on demand. `playMove`/`makeNullMove` only invalidate the new ply; `undoMove` is free; `currentAccumulator` catches up from the nearest valid ancestor (copy + incremental `update`) **only when an eval reads it on an eval-cache miss**. A king move that crosses a bucket boundary refreshes that perspective from scratch. Captures, en passant, castling and promotion are handled uniformly through the colour bitboards plus the `squares[]` array.
- **Output layer:** two forward passes (mirroring the C++ engine). `EvaluateExact` is the int16 SCReLU reference. `EvaluateInt8` is the **search's hot-path evaluator**: activations requantised to uint8 (`(clamp(x,0,QA)²+256) >> 9`) and the output weights to int8 range, scaled back before dequant — faster, at a bounded ≤40 cp (measured 28) deviation from the int16 reference. Exactly the C++ int8 layer.
- **Kernels** are build-tag split three ways, all bit-identical (the int8 dot is the exact integer sum `Σ u8·w8`, so scalar == SIMD == NEON): **amd64 + `goexperiment.simd`** (256-bit `simd/archsimd` plus a hand-written AVX2 int8 dot), **arm64** (hand-written NEON — `VADD`/`VSUB` column updates and a `SQXTUN`/`UMULL`/`SMLAL` int8 dot, with the four ops Go's assembler lacks emitted as clang-verified `WORD` encodings), and **scalar** on other targets. The vectorised dot is roughly an order of magnitude faster than scalar (int8 output dot ~9.9× on Apple Silicon; column updates ~7×).
- **Correctness:** `TestNNUEReference` matches the C++ exact int16 evaluator on 250 FENs to 0 cp; `TestNNUEInt8` bounds the search's int8 eval within 40 cp of that reference (and confirms scalar == SIMD); `TestNNUEIncremental` proves the lazy accumulator equals a full refresh at every node. Load a different net at runtime with the `EvalFile` UCI option.

### Opening book

`openingBook` ([engine/book.go](engine/book.go)) parses [engine/book.txt](engine/book.txt) (385 lines from TSCP — a byte-for-byte copy of the C++ engine's `pawnstar.book`), embedded in the binary via `go:embed` ([engine/book_embed.go](engine/book_embed.go)) so no external file is needed. `loadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves. A move written with a trailing `?` is *questionable*: it is replayed so the rest of the line still parses, but is never offered by `getMove` — matching the C++ book (109 lines carry `?`, filtering 122 candidate entries). `getMove` returns a random candidate, giving variety across games.

### History heuristic

`historyTable` ([engine/history.go](engine/history.go)) records how often each (ply, from→to) pair raised alpha or caused a beta cutoff. Counts are added to the quiet-move sort score by `scoreAndSortMoves` (alongside continuation history). The table lives on each `searchState` (per-thread, so Lazy SMP threads don't contend) and starts fresh each search because a new `searchState` is built per search.

### UCI protocol

`Game.HandleCommand` ([engine/game.go](engine/game.go)) processes commands on the calling goroutine. Supported:

| Command | Notes |
|---|---|
| `uci` | Prints engine id and `uciok` |
| `isready` | Prints `readyok` |
| `ucinewgame` | Stops search, resets position and clock. Does **not** clear the TT/eval cache (use `Clear Hash` for that) — so repeating a position in one session reuses the TT and node counts shrink |
| `position startpos [moves ...]` | Sets position from start FEN, applies move list |
| `position fen <fen> [moves ...]` | Sets position from FEN, applies move list |
| `go wtime <ms> btime <ms> [winc <ms> binc <ms>] [movestogo <n>]` | Standard clock; without `movestogo`, moves-to-go is a decreasing horizon `max(40−move, 5)`. Increment is parsed but not folded into the budget (SPRT-neutral) |
| `go movetime <ms>` | Fixed time per move |
| `go depth <n>` | Fixed depth |
| `go infinite` | Search until `stop` |
| `go nodes <n>` | Search until the node limit (checked per thread) |
| `go searchmoves <m…>` | Restrict the root search to the given moves |
| `go ... ponder` | Search on the opponent's time; no `bestmove` until `ponderhit`/`stop` |
| `ponderhit` | Predicted move was played — convert the ponder search to a timed one (budget from now) |
| `bench` / `bench <depth>` | Node-count benchmark over the 16 C++-reference (Bratko-Kopec) positions at depth `benchDepth`=13 (or an explicit `<depth>`); deterministic, single-threaded, book off, on a pinned 64 MB TT (signature independent of the `Hash` option). Current signature: 7,007,401 nodes |
| `stop` | Signals search goroutines to halt |
| `quit` | Stops search and exits |
| `setoption name OwnBook value <true\|false>` | Consult the opening book (false forces a real search) |
| `setoption name EvalFile value <path\|<embedded>>` | Load an external PSN1 net, or reload the embedded one |
| `setoption name Threads value <n>` | Lazy SMP search-thread count |
| `setoption name Hash value <mb>` | Resize the transposition table |
| `setoption name Clear Hash` | Clear the TT and eval cache |
| `setoption name Move Overhead value <ms>` | Time reserved from every deadline for GUI/network lag |
| `setoption name Ponder value <true\|false>` | Advertises pondering support (the GUI drives it via `go ponder`/`ponderhit`) |

`info` lines carry `depth`, `seldepth`, `score cp`/`score mate N`, `time`, `nodes`, `nps`, `hashfull`, and `pv`. [main.go](main.go) sets the thread count from `PAWNSTAR_THREADS`/`NumCPU`, then reads stdin line-by-line and delegates each command to `game.HandleCommand` (NNUE is installed by `NewGame`).

**Pondering** (`go ponder` / `ponderhit` / `Ponder` option) searches on the opponent's time until the predicted move is played (`ponderhit`, which converts it to a timed search budgeted from that moment) or `stop`. The live clock is two `atomic.Int64`s on `Game` — armed on the UCI goroutine before the search starts, mutated only by `ponderhit` — so the concurrent read/write is data-race free (verified under `-race` by `TestPonderRace`). `bestmove` is deferred while pondering and emitted as `bestmove <m> ponder <reply>`.

### Search diagnostics

[engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, null-move/LMR/PVS attempt and fail counts, the pruning/reduction techniques (check extensions, IIR, RFP, LMP, qsearch SEE prunes, the three LMR tiers), draw detection, and killer move beta cutoffs. The search reaches them through `searchState.dbg`: the main thread points at the global `dbgx`, each helper thread at a throwaway, so the counters neither race nor contend under Lazy SMP (helper counts are discarded). Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect single-threaded search behaviour. Counters accumulate across searches unless explicitly reset.
