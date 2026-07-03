package engine

// The root of the search: searchRootNode (book probe, forced-move shortcut, Lazy SMP thread launch,
// time management) and iterativeDeepen (one thread's full-window iterative-deepening loop, the
// Stockfish-style helper skip schedule, info-line printing and the soft-time early exits). The
// recursion itself lives in searchalphabeta.go / searchquiescent.go.

import (
	"fmt"
	"sync"
	"time"
)

// searchRootNode performs iterative-deepening search and returns the best move.
//
// If the opening book contains a move for the current position, it is returned
// immediately.  Otherwise, an initial pass at startDepth (3) scores and orders
// the legal moves, then deepening continues until the time limit, a forced
// mate/loss, or an early-exit heuristic triggers.
//
// Early-exit conditions (Standard clock only):
//   - Best move consistent AND score stable across all completed depths: exit if elapsed > ¼ of budget.
//   - Either condition alone: exit if elapsed > ½ of budget.
//   - Hard stop if elapsed > budget.
func searchRootNode(game *Game) move {
	game.ponderMove = moveNone()      // set below by the main thread's iterativeDeepen if the PV has a reply
	game.isCancelPending.Store(false) // clear before the book/single-move returns so a ponder-wait sees no stale cancel
	searchMoves := game.timeControl.searchMoves
	// Opening book — skipped when OwnBook is off, or when `go searchmoves` restricts the root.
	if game.useOwnBook && len(searchMoves) == 0 {
		if bookMove := game.openingBook.getMove(game.currentPosition().hash); bookMove != moveNone() {
			return bookMove
		}
	}

	moves := game.currentPosition().generateLegalMoves()
	// `go searchmoves`: restrict the root move list to the requested moves (ignored if none are legal
	// here, so a malformed list cannot wedge the search).
	if len(searchMoves) > 0 {
		var filtered []move
		for _, m := range moves {
			for _, s := range searchMoves {
				if m.String() == s {
					filtered = append(filtered, m)
					break
				}
			}
		}
		if len(filtered) > 0 {
			moves = filtered
		}
	}
	if len(moves) == 0 {
		return moveNone()
	}
	if len(moves) == 1 && len(searchMoves) == 0 {
		return moves[0]
	}

	// The clock was already armed by armClock — on the UCI goroutine, before this search goroutine started
	// — so the budgets/deadline are set (and only ponderhit mutates them). Bump the TT generation for the
	// new search.
	game.transpositionTable.age()

	ss := newSearchStateFromGame(game)

	// Initial shallow pass to get move ordering before deepening.
	var initPV variation
	for i := range moves {
		m := moves[i]
		score := searchSingleMove(ss, startDepth, 0, initialAlpha, initialBeta, m, &initPV, i)
		moves[i] = m.withScore(score)
	}
	sortMoves(moves)
	bestMove := moves[0]

	// Lazy SMP: each thread runs an independent iterative-deepening search of the same root, sharing
	// only the lockless transposition table and eval cache (history/killers/continuation/countermoves
	// are per-thread on each searchState). The main thread (id 0) is authoritative — it prints info,
	// applies time management, and returns the move — while helper threads deepen the shared TT to
	// accelerate it, desynchronised by the per-thread iteration-skip schedule. Each helper gets its own
	// searchState, a private throwaway diagnostics struct, and its own copy of the move list.
	nThreads := game.threadCount
	if nThreads < 1 {
		nThreads = 1
	} else if nThreads > maxSearchThreads {
		nThreads = maxSearchThreads
	}
	var helpers sync.WaitGroup
	for i := 1; i < nThreads; i++ {
		helpers.Add(1)
		// The move-list clone and seed move are passed as arguments so they are evaluated here, in the
		// launching goroutine, before the main thread's iterativeDeepen begins mutating moves/bestMove.
		go func(threadID int, helperMoves []move, seed move) {
			defer helpers.Done()
			helper := newSearchStateFromGame(game)
			helper.dbg = &debugX{} // helper diagnostics are discarded; keep them off the global dbgx
			iterativeDeepen(helper, helperMoves, seed, threadID)
		}(i, append([]move(nil), moves...), bestMove)
	}

	bestMove = iterativeDeepen(ss, moves, bestMove, 0)
	game.nodeCount = ss.nodeCount // main-thread node total (used by `bench`)

	game.isCancelPending.Store(true) // main thread is done; signal helpers to stop
	helpers.Wait()
	return bestMove
}

// lazySmpSkipSize / lazySmpSkipPhase drive the Stockfish-style per-helper iteration-skip schedule:
// helper thread t (1-based via (t-1) mod len) skips iteration depth d when ((d + phase) / size) is odd,
// so helpers desynchronise from the main thread and each other and prefill the shared TT with diverse
// entries instead of recomputing the same tree in lockstep. The main thread (id 0) never skips.
var (
	lazySmpSkipSize  = [20]int{1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4}
	lazySmpSkipPhase = [20]int{0, 1, 0, 1, 2, 3, 0, 1, 2, 3, 4, 5, 0, 1, 2, 3, 4, 5, 6, 7}
)

// iterativeDeepen runs one thread's iterative-deepening search over its own move list and returns its
// best move. threadID 0 is the authoritative main thread (prints info, does soft time management);
// helper threads (id ≥ 1) follow the iteration-skip schedule and stay silent. All threads share the
// lockless TT and eval cache via ss.game; the hard deadline (checked inside searchAlphaBeta) and the
// game cancel flag stop every thread together.
func iterativeDeepen(ss *searchState, moves []move, bestMove move, threadID int) move {
	isMain := threadID == 0
	tc := &ss.game.timeControl
	var principalVariation variation
	var bestMoves []move

	for depth := startDepth + 1; depth < maxPly; depth++ {
		if tc.clockType == clockFixedDepth && depth > tc.depth {
			break
		}
		if !isMain { // helper iteration-skip schedule
			s := (threadID - 1) % len(lazySmpSkipSize)
			if ((depth+lazySmpSkipPhase[s])/lazySmpSkipSize[s])%2 != 0 {
				continue
			}
		}
		sortMoves(moves)
		bestMoves = append(bestMoves, bestMove)

		// Full-window root search — no aspiration window (mirrors the C++ IterativeDeepen, which tested
		// and rejected aspiration). alpha starts at the full-window minimum and is raised as root moves
		// improve; beta stays at initialBeta, so PVS null-windows every move after the first improvement.
		var pv variation
		alpha := initialAlpha
		for i, move := range moves {
			// Report the move currently being searched at the root, but only once a search is slow
			// enough that a GUI benefits — avoids flooding info lines on the fast shallow iterations.
			if isMain && ss.game.elapsedSinceSearchStart() > 3*time.Second {
				fmt.Printf("info depth %d currmove %s currmovenumber %d\n", depth, move.String(), i+1)
			}
			pv.clear()
			score := searchSingleMove(ss, depth, 0, alpha, initialBeta, move, &pv, i)
			moves[i] = move.withScore(score)
			if ss.isCancelled() {
				return bestMove
			}
			if score > alpha {
				alpha = score
				bestMove = move
				if isMain {
					copyVariation(&principalVariation, pv, bestMove)
					// Publish the predicted reply (PV[1]) as it improves, so `bestmove <m> ponder <reply>`
					// has it on every exit path — including a hard-timeout / stop cancellation.
					if len(principalVariation.moves) >= 2 {
						ss.game.ponderMove = principalVariation.moves[1]
					} else {
						ss.game.ponderMove = moveNone()
					}
					elapsedMs := ss.game.elapsedSinceSearchStart().Milliseconds()
					nps := int64(ss.nodeCount) * 1000 / max(1, elapsedMs)
					fmt.Printf("info depth %d seldepth %d score %s time %d nodes %d nps %d hashfull %d pv %s\n",
						depth, ss.seldepth, formatScore(alpha), elapsedMs, ss.nodeCount, nps,
						ss.game.transpositionTable.hashfull(), pvString(principalVariation))
				}
			}
		}

		if ss.isCancelled() {
			return bestMove
		}

		if alpha > winThreshold || alpha < loseThreshold {
			break
		}

		// Soft-time management (main thread, standard clock) — skipped while pondering: a ponder search
		// runs until ponderhit/stop, and onPonderhit resets the search-start instant so elapsed is then
		// measured from the ponderhit moment. Mirrors the C++ IterativeDeepen guard.
		if isMain && tc.clockType == clockStandard && !ss.game.isPondering.Load() {
			elapsedTime := ss.game.elapsedSinceSearchStart()
			allocatedTime := ss.game.allocatedTime
			isBestMoveConsistent := allSame(bestMoves, bestMove)
			isScoreStable := allScoreStable(bestMoves, bestMove)
			if isBestMoveConsistent && isScoreStable && elapsedTime*4 > allocatedTime {
				fmt.Println("info string Best move consistent and score stable - search terminated.")
				break
			}
			if (isBestMoveConsistent || isScoreStable) && elapsedTime*2 > allocatedTime {
				if isBestMoveConsistent {
					fmt.Println("info string Best move consistent - search terminated.")
				} else {
					fmt.Println("info string Score stable - search terminated.")
				}
				break
			}
			if elapsedTime > allocatedTime {
				break
			}
		}
	}
	return bestMove
}

// allSame returns true if every move in moves has the same identity as best.
func allSame(moves []move, best move) bool {
	for _, move := range moves {
		if !move.equal(best) {
			return false
		}
	}
	return true
}

// allScoreStable returns true if every move in moves has a score within
// scoreInstabilityThresh centipawns of best's score.
func allScoreStable(moves []move, best move) bool {
	for _, move := range moves {
		if absInt(move.score()-best.score()) > scoreInstabilityThresh {
			return false
		}
	}
	return true
}
