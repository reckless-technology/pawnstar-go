// Package engine implements a UCI chess engine using bitboard board representation
// and alpha-beta search.  Game is the top-level engine state; HandleCommand is
// the UCI entry point.
package engine

// Search and evaluation tuning constants.
const (
	// initialAlpha and initialBeta bracket every score that can arise during search.
	initialAlpha = -11000
	initialBeta  = 11000

	// checkmatedScore is the base score for checkmate.  It is adjusted by ply so
	// that shorter mates score higher than longer ones.
	checkmatedScore = -10000
	winThreshold    = 9000  // score above which the side to move has a forced win
	loseThreshold   = -9000 // score below which the side to move has a forced loss
	drawScore       = 0

	// maxPly caps search depth and is also the first dimension of the history table.
	maxPly = 64

	// rankFlip is XORed with a square index to flip rank perspective (a1↔a8).
	// Used when indexing piece-square tables for White, whose tables are stored
	// from Black's point of view (rank 8 at low indices).
	rankFlip = 0x38

	// searchCancelledScore is a sentinel returned by a search iteration that was
	// interrupted by the time limit, allowing the caller to detect cancellation.
	searchCancelledScore = -12345678

	// hashNotFoundScore is returned by evalHashTable.find when there is no cached
	// entry for the queried hash; it is outside the range of any real score.
	hashNotFoundScore = -2345678

	// scoreInstabilityThresh is the centipawn tolerance for the early-exit heuristic:
	// if the best-move score varies by more than this across consecutive depths the
	// search is not considered "stable".
	scoreInstabilityThresh = 50

	// startDepth is the depth of the initial ordering pass performed before
	// iterative deepening begins.
	startDepth = 3

	// hashTableMB is the default transposition table size in megabytes.
	hashTableMB = 512

	// evalHashTableSize is the number of slots in the evaluation cache. At 8 bytes per lockless entry
	// this is ~8 MB, the size the C++ engine found optimal (larger regressed slightly).
	evalHashTableSize = 1 << 20

	// aspirationDelta is the initial aspiration window half-width in centipawns.
	aspirationDelta = 50

	// Move-ordering sort-score bands (mirrors the C++ ScoreAndSortMoves). Winning captures sit on top,
	// then the two killers, then the countermove, then quiet moves by history + continuation history.
	winningCaptureBase = 1 << 20 // winning captures/promotions score winningCaptureBase + SEE
	killerScore0       = 1<<19 + 1 // first killer, just below winning captures
	killerScore1       = 1 << 19 // second killer
	countermoveScore   = 1<<19 - 1 // countermove: just below the killers
	maxQuietScore      = 1<<19 - 2 // history + continuation history clamps just below the countermove

	// contKeys is the size of the [piece, to] index space (7 piece types × 64 squares) used to key the
	// countermove table and, squared, the continuation-history table.
	contKeys = 7 * 64

	// Pruning / reduction tuning (ported from C++ Pawnstar; centipawn margins carry over because the
	// Go NNUE evaluation is bit-identical in scale to the C++ evaluator).
	rfpMaxDepth        = 7 // reverse-futility pruning: only applied at depth ≤ this
	rfpMargin          = 80 // reverse-futility pruning: centipawn margin per ply
	lmpMaxDepth        = 8 // late-move (move-count) pruning: only applied at depth ≤ this
	lmpBase            = 3 // a quiet move is skipped once its index ≥ lmpBase + depth*depth
	noTTReduceMinDepth = 4 // internal iterative reduction: min depth at which a node lacking a TT move is reduced
)

// direction is one of the eight compass directions used to index ray tables.
type direction uint8

const (
	north     direction = iota
	northeast direction = iota
	east      direction = iota
	southeast direction = iota
	south     direction = iota
	southwest direction = iota
	west      direction = iota
	northwest direction = iota
)

// color is the side to move: white (0) or black (1).
// The numeric values are used as array indices throughout the engine.
type color uint8

const (
	white color = iota
	black color = iota
)

// enemyOf returns the opposing color.
func enemyOf(c color) color {
	return c ^ 1
}

// piece identifies a piece type.
// The numeric value doubles as an index into piece-square and SEE value tables,
// so the ordering must not change.
type piece uint8

const (
	noPiece piece = iota
	pawn    piece = iota
	knight  piece = iota
	bishop  piece = iota
	rook    piece = iota
	queen   piece = iota
	king    piece = iota
)

func (p piece) toString() string {
	return string(" pnbrqk"[p])
}

// clockType controls how the engine manages its time budget during search.
type clockType uint8

const (
	clockStandard   clockType = iota // wtime/btime/movestogo: allocate a fraction of remaining time.
	clockFixedDepth                  // Search to a fixed depth regardless of elapsed time.
	clockFixedTime                   // Search for a fixed wall-clock duration (movetime).
	clockInfinite                    // Search indefinitely until a stop command is received.
)
