package engine

import "pawnstar-go/engine/nnue"

// hashStackEntry packs a position's Zobrist hash (top 56 bits) and its half-move clock (low 8 bits) into a
// single uint64 — the eval-cache packing trick. This halves the entry (8 B vs the 16 B a {hash, uint8}
// struct occupies with padding), so the per-node repetition scan walks half as many cache lines. Repetition
// compares only the top 56 bits, which is ample discrimination for a scan window of at most ~100 plies
// (and safer than the eval cache's 48-bit key); the low byte carries the clock, where a zero marks an
// irreversible-move boundary that stops the scan.
type hashStackEntry zobrist

// packHashStack builds an entry: hash in the top 56 bits, half-move clock in the low byte.
func packHashStack(hash zobrist, clock uint8) hashStackEntry {
	return hashStackEntry((hash &^ 0xFF) | zobrist(clock))
}

// clock returns the packed half-move clock (0 marks an irreversible-move boundary).
func (e hashStackEntry) clock() uint8 { return uint8(e) }

// sameHashAs reports whether two entries hold the same position (top 56 bits of the hash; clock ignored).
func (e hashStackEntry) sameHashAs(o hashStackEntry) bool { return (e^o)>>8 == 0 }

// searchState holds all mutable state for one Lazy SMP search thread. Each thread owns an independent
// searchState searching the whole tree; the threads share only the lockless transposition table and
// eval cache (via game). Everything on searchState — posStack, hashStack, killers, history,
// continuation history, countermoves, the accumulator stack, node count, and diagnostics — is
// per-thread, so no thread ever writes state another reads (which keeps the concurrent search
// data-race free without locks).
//
// posStack is the copy-make stack for the search path.  It starts with exactly one entry (the current
// position) and grows by one entry for each move played.
//
// hashStack is a compact (hash, halfMove) record for every position from the start of the game through
// the current search node.  It is the only structure consulted by isDrawByRepetition.
type searchState struct {
	posStack  []position       // copy-make stack: one entry per move played; seeded with the current position
	hashStack []hashStackEntry // full history from game start through current node, for repetition detection
	killers   [maxPly][2]move  // per-ply quiet moves that recently caused beta cutoffs
	history   historyTable     // per-thread history heuristic (per-thread to avoid Lazy-SMP contention)
	nodeCount int              // nodes searched since the last root iteration reset
	seldepth  int              // deepest ply reached this search (for UCI `info seldepth`)
	game      *Game            // shared: time control, cancel flag, lockless TT + eval cache
	dbg       *debugX          // diagnostic counters: the global dbgx for the main thread, a throwaway per helper

	// Move-ordering follow-up heuristics keyed by the previous move (persist across a whole search).
	// countermoves[prev.pieceTo()] is the best quiet refutation observed to the previous move.
	// continuationHistory[prev.pieceTo()*contKeys + move.pieceTo()] scores playing move after prev.
	countermoves        [contKeys]move
	continuationHistory []int16

	// NNUE lazy accumulator stack, indexed by posStack depth: accStack[i] is the accumulator for
	// posStack[i], valid iff accValid[i]. Maintained deferred — playMove/makeNullMove only mark the
	// new depth invalid; the accumulator is brought current on demand by currentAccumulator (nearest
	// valid ancestor + forward updates) only when an eval actually reads it (on eval-cache miss), so
	// cut-off nodes never pay for it. undoMove is free: ancestors stay valid, and the popped depth is
	// re-invalidated by the next playMove into that slot. Both slices are nil when nnue is disabled.
	accStack []nnue.Accumulator
	accValid []bool
}

// newSearchState is the private constructor used by newSearchStateFromGame.  hs is the caller's
// pre-built hashStack (ownership transfers).  startPos is the single seed entry for posStack.
func newSearchState(hs []hashStackEntry, startPos *position, game *Game) *searchState {
	ps := make([]position, 1, maxPly)
	ps[0] = *startPos
	ss := &searchState{
		posStack:            ps,
		hashStack:           hs,
		game:                game,
		continuationHistory: make([]int16, contKeys*contKeys),
		dbg:                 &dbgx, // main thread / single-threaded default; helper threads get their own
	}
	// Seed the current position's accumulator (depth 0); deeper accumulators are computed on demand.
	// accStack grows lazily to the search depth so construction stays cheap.
	ss.accStack = make([]nnue.Accumulator, 1, 8)
	ss.accValid = make([]bool, 1, maxPly+8)
	rootBoard := ss.posStack[0].nnueBoard()
	game.nnue.Refresh(&ss.accStack[0], &rootBoard)
	ss.accValid[0] = true
	return ss
}

// newSearchStateFromGame creates a searchState for the main (serial) search.
// posStack is seeded with only the current position.  hashStack is built from
// the game's full position history so that draw-by-repetition detection can
// look back through the complete game.
func newSearchStateFromGame(game *Game) *searchState {
	n := len(game.posStack)
	hs := make([]hashStackEntry, n, n+maxPly)
	for i, pos := range game.posStack {
		hs[i] = packHashStack(pos.hash, pos.halfMove)
	}
	return newSearchState(hs, &game.posStack[n-1], game)
}

// currentPosition returns a pointer to the current (deepest) position on the stack.
func (ss *searchState) currentPosition() *position {
	return &ss.posStack[len(ss.posStack)-1]
}

// ensureAccDepth grows the lazy accumulator stack so depth d is indexable. Growth appends zeroed
// (invalid) entries and happens only when the search first reaches a new maximum depth.
func (ss *searchState) ensureAccDepth(d int) {
	for len(ss.accStack) <= d {
		ss.accStack = append(ss.accStack, nnue.Accumulator{})
		ss.accValid = append(ss.accValid, false)
	}
}

// invalidateAcc marks depth d's accumulator stale (its position just changed). O(1) — no computation.
func (ss *searchState) invalidateAcc(d int) {
	ss.ensureAccDepth(d)
	ss.accValid[d] = false
}

// currentAccumulator brings the current (top) position's accumulator up to date and returns it.
// It walks back to the deepest valid ancestor, then applies incremental updates forward one ply at a
// time (copy the parent accumulator, apply the piece delta). Called only on an eval-cache miss, so a
// node that never evaluates never triggers any accumulator work. accValid[0] is seeded true at
// construction, so the backward walk always terminates.
func (ss *searchState) currentAccumulator() *nnue.Accumulator {
	d := len(ss.posStack) - 1
	ss.ensureAccDepth(d)
	k := d
	for k > 0 && !ss.accValid[k] {
		k--
	}
	if k < d {
		// Chain the board views forward so each position converts exactly once per catch-up sweep.
		from := ss.posStack[k].nnueBoard()
		for j := k + 1; j <= d; j++ {
			to := ss.posStack[j].nnueBoard()
			ss.accStack[j] = ss.accStack[j-1]
			ss.game.nnue.Update(&ss.accStack[j], &from, &to)
			ss.accValid[j] = true
			from = to
		}
	}
	return &ss.accStack[d]
}

// playMove appends a copy of the current position to posStack, applies m to it,
// and records the resulting (hash, halfMove) in hashStack.
func (ss *searchState) playMove(m move) {
	n := len(ss.posStack)
	ss.posStack = append(ss.posStack, ss.posStack[n-1])
	ss.posStack[n].makeMove(m)
	pos := &ss.posStack[n]
	ss.hashStack = append(ss.hashStack, packHashStack(pos.hash, pos.halfMove))
	ss.invalidateAcc(n) // depth n's accumulator is now stale; computed on demand by currentAccumulator
}

// makeNullMove appends a copy of the current position to posStack, applies a
// null move to it, and records the resulting (hash, halfMove) in hashStack.
func (ss *searchState) makeNullMove() {
	n := len(ss.posStack)
	ss.posStack = append(ss.posStack, ss.posStack[n-1])
	ss.posStack[n].makeNullMove()
	pos := &ss.posStack[n]
	ss.hashStack = append(ss.hashStack, packHashStack(pos.hash, pos.halfMove))
	// A null move leaves piece placement unchanged, so the deferred update is a no-op copy; still mark
	// the new depth invalid so a deeper eval recomputes through it correctly.
	ss.invalidateAcc(n)
}

// undoMove removes the last position from posStack and the last entry from
// hashStack, reverting to the state before the most recent playMove or makeNullMove.
// The accumulator needs no work: ancestor accumulators stay valid, and the popped depth's slot is
// re-invalidated by the next playMove into it.
func (ss *searchState) undoMove() {
	n := len(ss.posStack)
	ss.posStack = ss.posStack[:n-1]
	ss.hashStack = ss.hashStack[:len(ss.hashStack)-1]
}

// isDrawByFiftyMoves reports whether the 50-move rule draw condition is met.
func (ss *searchState) isDrawByFiftyMoves() bool {
	return ss.currentPosition().halfMove >= 100
}

// isDrawByRepetition reports whether the current position has appeared twice
// before in the combined game + search history (three occurrences total).
// The scan steps backwards in two-ply increments so only positions with the
// same side to move are compared.  It stops early at an irreversible move
// (halfMoveClock == 0) because repetitions cannot cross a pawn move or capture.
// A minimum of five entries is required: positions 0, 2, and 4 would be the
// earliest possible three-fold repetition.
func (ss *searchState) isDrawByRepetition() bool {
	n := len(ss.hashStack)
	if n < 5 {
		return false
	}
	cur := ss.hashStack[n-1]
	repetitions := 2
	for i := n - 5; i >= 0; i -= 2 {
		if ss.hashStack[i].sameHashAs(cur) {
			repetitions--
			if repetitions == 0 {
				ss.dbg.drawsByRepetition++
				return true
			}
		}
		if ss.hashStack[i].clock() == 0 {
			return false
		}
	}
	return false
}

// isCancelled returns true when the search should stop because the engine received a time-expiry or
// stop command.
func (ss *searchState) isCancelled() bool {
	return ss.game.isCancelPending.Load()
}

// variation is a line of play (principal variation).
type variation struct {
	moves []move
}

// clear empties the variation. Called before each searchSingleMove so a child that does not raise
// alpha (a cutoff or all-node) leaves no stale tail behind — otherwise the parent's reconstructed PV
// would splice on a move from a previously searched sibling, producing an "illegal PV move".
func (v *variation) clear() { v.moves = v.moves[:0] }

// copyVariation sets dst to best followed by all moves in src.
func copyVariation(dst *variation, src variation, best move) {
	dst.moves = dst.moves[:0]
	dst.moves = append(dst.moves, best)
	dst.moves = append(dst.moves, src.moves...)
}

// searchSingleMove plays m, calls searchAlphaBeta recursively, undoes the move,
// and returns the score.  When the position is not a PV node and the move is not
// the first tried, a null-window PVS search is attempted first; if it fails high,
// a full-window re-search is performed.
func searchSingleMove(ss *searchState, depth, ply, alpha, beta int, m move, pv *variation, moveIdx int) int {
	wasInCheck := ss.currentPosition().isInCheck()
	childDepth := depth - 1
	// Search extensions: search a promotion or en-passant capture one ply deeper (the per-node check
	// extension is applied in searchAlphaBeta). Mirrors the C++ SearchSingleMove. Like C++, this is
	// unguarded — on promotion-dense positions it can drive the search to maxPly (the clock bounds it in
	// real play; TestSearch caps nodes so its fixed-depth runs stay bounded).
	switch m.kind() {
	case promotionKnight, promotionBishop, promotionRook, promotionQueen:
		childDepth++
		ss.dbg.promotionExtensions++
	case epCapture:
		childDepth++
		ss.dbg.epCaptureExtensions++
	}
	ss.playMove(m)
	isChecking := ss.currentPosition().isInCheck()
	var score int
	if beta > alpha+1 && moveIdx > 0 && !wasInCheck && !isChecking {
		// Try principal variation search.
		ss.dbg.pvsAttempts++
		score = -searchAlphaBeta(ss, childDepth, ply+1, -alpha-1, -alpha, pv, m)
		if score > alpha {
			// PVS failed high — re-search with full window.
			ss.dbg.pvsFails++
			if ply == 0 {
				ss.dbg.pvsFailsRootPly++
			}
			score = -searchAlphaBeta(ss, childDepth, ply+1, -beta, -alpha, pv, m)
		}
	} else {
		score = -searchAlphaBeta(ss, childDepth, ply+1, -beta, -alpha, pv, m)
	}
	ss.undoMove()
	return score
}

// absInt returns the absolute value of x.
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
