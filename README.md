# Pawnstar-Go

A UCI chess engine written in Go. This is a port of the original C++ Pawnstar engine, preserving the same algorithms and identical move encoding so the two implementations can be compared directly.

A Rust port also exists at [../pawnstar-rs](../pawnstar-rs), which runs at roughly 1.8× the speed of this version.

## Build and run

```bash
go build -o pawnstar-go .
./pawnstar-go              # interactive UCI session
```

Requires Go 1.24 or later.

Point any UCI-compatible GUI (Arena, Cute Chess, etc.) at the binary — no configuration needed.

## Testing

Move generation correctness is verified by perft — counting leaf nodes to a given depth and comparing against known values. All 21 EPD positions must pass before any change is considered correct.

```bash
go test ./engine/ -run TestPerft -short -v        # all positions, depth ≤ D5, ~2s
go test ./engine/ -run TestPerft -v               # full suite, up to D8 (slow)
go test ./engine/ -run TestPerft/002 -v           # single position by 0-based index
go test ./engine/ -bench BenchmarkPerft7 -benchtime=10s
```

The engine runs at approximately **35 Mnps** on the perft suite. The C++ original reaches ~700 Mnps via BMI2 PEXT instructions.

## Playing a game

[game.py](../pawnstar-rs/game.py) in the Rust repo orchestrates a UCI game between pawnstar-go (White) and pawnstar-rs (Black), building both engines automatically:

```bash
python3 ../pawnstar-rs/game.py
```

## Architecture

### Board representation

`position` ([engine/position.go](engine/position.go)) is a value type; `makeMove` and `makeNullMove` operate on a caller-owned copy. `Game` maintains a `posStack []position` history stack; undoing a move is a slice truncation — there is no unmake logic.

The struct holds:
- Named bitboard fields: `pawns`, `knights`, `bishops`, `rooks`, `queens`, `kings`, `whitePieces`, `blackPieces`
- `squares[64]piece` — O(1) piece lookup by square
- `scores[2]int` — incremental material + PST totals per side (excludes king; updated by `addPiece`/`removePiece`/`movePiece`)
- `kingLocn[2]square` — king locations (updated separately because king PST is phase-dependent)
- Zobrist hash maintained incrementally

`Bitboard` ([engine/bitboard.go](engine/bitboard.go)) is a `uint64` with methods for common set operations (`popCount`, `lsb`, `clearLsb`, shifts in all eight directions).

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

`move.equal` masks the score field when comparing. `move(0)` is the null sentinel (`moveNone()`). `String()` outputs UCI long-algebraic notation (`e2e4`, `e7e8q`).

Move lists are plain `[]move` slices allocated with `make([]move, 0, 64)` per call site — no fixed-capacity struct.

### Attack generation

`MagicTables` ([engine/magic.go](engine/magic.go)) implements magic bitboard lookup for sliding pieces. Magic numbers are precomputed constants found offline with seed 1337 and are identical to the C++ version. `BishopAttacks(occ, sq)` and `RookAttacks(occ, sq)` are the hot paths; `QueenAttacks` ORs both.

`Tables` ([engine/tables.go](engine/tables.go)) holds all precomputed non-magic data initialised once at startup: ray tables in all eight directions (64 squares × 8 directions), knight/king/pawn attack tables, pawn structure masks (passed, isolated, adjacent-file), king pawn shelter masks, intervening-square tables, and Zobrist hashes for pieces/castling/en-passant/side-to-move.

### Move generation

`GenerateLegalMoves` and `GenerateLegalCaptures` ([engine/movegen.go](engine/movegen.go)) are the public entry points, returning `[]move`. Internally:

1. Compute checkers bitboard and pin rays (`Pins` in [engine/pins.go](engine/pins.go)).
2. If in double check, only king moves are legal — return early.
3. Build the forbidden-square set for the king (attacked squares, including X-ray attacks through the king via temporarily clearing the king from occupancy).
4. For single check, restrict non-king moves to interpositions and captures of the checker.
5. Apply pin constraints via `Pins.AllowedSquares(from)` — returns the ray the piece may move along, or all squares if unpinned.
6. Delegate pawn moves to `genPawnMoves`, which handles single/double pushes, captures, promotions, and en passant (with horizontal-pin detection for en passant).

### Search

`Game` ([engine/game.go](engine/game.go)) owns all search state. `startThinking` launches a goroutine that calls `searchEntry`; `stopThinking` sets `isCancelPending = true` and blocks on a `sync.WaitGroup`.

`searchRootNode` ([engine/search.go](engine/search.go)) drives iterative deepening:

1. Return opening book move if available.
2. Return immediately for forced moves (single legal move).
3. Run an initial shallow pass at depth 3 (`startDepth`) to score and order moves before deepening begins.
4. For each depth from 4 to `maxPly` (64):
   - Sort moves by score from the previous iteration.
   - Search each move with a full-width window at the root; update best move and print a UCI `info` line when alpha improves.
   - Stop if a forced win or loss is found (`|alpha| > 9000`).
   - Apply early-exit heuristics (Standard clock only):
     - Best move consistent across all depths so far AND score stable (within ±50 cp): stop if elapsed > ¼ of budget.
     - Either condition alone: stop if elapsed > ½ of budget.
     - Hard stop if elapsed > budget.

**Time allocation (Standard clock):** `allocated = timeRemaining / movesRemaining`. Hard stop = `max(100ms, min(allocated × 2, timeRemaining − 1s))`. The clock is checked every 65 536 nodes (`nodeCount & 0xFFFF == 0`).

**`searchAlphaBeta` highlights:**

| Technique | Parameters |
|---|---|
| Principal variation search (PVS) | Null-window on moves 2+ when not in check; re-search full window on fail-high |
| Null-move pruning | R = 4; skipped if in check, position is already null-move, window is not null (`beta ≠ alpha + 1`), or side has ≤ 3 pieces |
| Late move reduction (LMR) | Reduce by 1 for moves 4+ (non-capture, non-check, non-pawn, depth > 2); reduce by 2 additionally for moves 7+ at depth > 3; re-search at full depth if score exceeds alpha |
| Check extension | In quiescence, an in-check position re-enters `searchAlphaBeta` at the same depth instead of standing pat |
| Transposition table | Hit on PV node increments depth by 1; Cut/All hits return early if score is conclusive |

**Move ordering within `searchAlphaBeta`:**

1. TT move (tried first, before generating all legal moves).
2. Remaining legal moves scored by `captured_value × 10000 − moving_value × 1000 + history_count`, sorted descending.

### Transposition table

Single 64 MB table ([engine/transposition.go](engine/transposition.go)). Indexed by `hash % tableSize` (table size is odd to improve distribution). Each entry stores hash, best move (with score packed into the upper 32 bits), depth, node type (Cut/All/PV), and an `isOld` flag.

**Replacement policy:** replace if the slot is empty, the existing entry is old (from a previous root search), the new entry is at least as deep, or the new entry is a PV node. `age()` marks all entries old at the start of each root search.

### Evaluation

All scores are in centipawns from the perspective of the side to move.

**Piece values:** P = 100, N = 300, B = 300, R = 500, Q = 900.

**Piece-square tables** ([engine/evaluate.go](engine/evaluate.go)) ported from the C Pawnstar engine. Stored with rank 8 at low indices; White reads via `sq ^ 0x38` (rank flip), Black reads directly. PST scores for all non-king pieces are accumulated into `scores[c]` incrementally by `addPiece`/`removePiece`/`movePiece` in `position.go` — no re-scan on evaluation.

**Bishop pair:** +30 cp when both bishops are present (one on each colour).

**Pawn structure** is computed per side by `determinePawnStructure` and scored as: passed pawns (rank-based PST bonus), defended pawns (+5 cp each), backward pawns (−10 cp each), doubled pawns (−10 cp each), isolated pawns (−20 cp each). A doubled passed pawn is not counted as passed (the rear pawn is obstructed).

**Mobility:** Bishop attacks (excluding squares defended by enemy pawns) and rook attacks are each scored via a lookup table indexed by the number of available squares (bishop: −20 to +20 over 14 buckets; rook: −10 to +20 over 15 buckets).

**King safety:** King PST switches between midgame and endgame tables via `isEndgame` (true if neither side has a queen, or every queen-side has no rooks and at most one minor piece). In the midgame: a three-row pawn shelter scores friendly pawns +15/+10/+5 and enemy pawns −15/−10/−5 across the three ranks in front of the king; open files near the king score −15 (king's file) and −10 (adjacent files); friendly non-pawn pieces score +10 (adjacent ring) and +5 (two-square ring).

An alpha/beta material early-exit skips the full evaluation when the material score alone is more than 200 cp outside the search window.

**SEE** ([engine/see.go](engine/see.go)) is implemented but not wired into move ordering — matching the C++ version.

### Opening book

`OpeningBook` ([engine/book.go](engine/book.go)) embeds [engine/book.txt](engine/book.txt) (385 lines from TSCP) at compile time via `//go:embed`. `LoadOpeningBook` replays each line from the start position and maps Zobrist hashes to candidate next moves. `GetMove` returns a random candidate, giving variety across games.

### History heuristic

`historyTable` ([engine/history.go](engine/history.go)) records how often each (ply, from→to) pair raised alpha or caused a beta cutoff. Counts are added to the move sort score and reset at the start of each root search.

### UCI protocol

`Game.HandleCommand` ([engine/uci.go](engine/uci.go)) processes commands on the calling goroutine. Supported:

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

[engine/debug.go](engine/debug.go) defines a global `dbgx debugX` struct with counters for beta cutoffs, TT hits by node type, and null-move/LMR/PVS attempt and fail counts. Call `dbgxClear()` before a search and `dbgxPrint()` after to inspect search behaviour.
