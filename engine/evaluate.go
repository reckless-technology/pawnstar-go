package engine

import "sync/atomic"

// PST tables ported from the C Pawnstar engine.
// Stored with rank 8 at low indices, rank 1 at high indices.
// Black reads the table directly; White reads via sq^rankFlip to mirror ranks.

// evalHashEntry is a single slot in the evaluation hash table.
type evalHashEntry struct {
	hash  zobrist // Zobrist hash of the position
	score int     // cached evaluation score (centipawns, side-to-move perspective)
	lock  uint32  // per-entry spinlock: 0 = unlocked, 1 = locked
}

// evalHashTable is a fixed-size cache for evaluated positions.
// Each entry carries its own spinlock so concurrent workers contend only when
// they happen to evaluate the same position simultaneously.
type evalHashTable struct {
	entries []evalHashEntry // flat array indexed by hash % len(entries)
	mask    zobrist
}

// newEvalHashTable allocates an evaluation hash table with count slots.
func newEvalHashTable(count int) evalHashTable {
	var i int
	// Make the size a power of 2 for efficient circular addressing.
	for i = 1; i < count; i <<= 1 {
	}
	return evalHashTable{entries: make([]evalHashEntry, i), mask: zobrist(i) - 1}
}

// find returns the cached score for hash, or hashNotFoundScore on a miss.
func (t *evalHashTable) find(hash zobrist) int {
	idx := hash & t.mask
	entry := &t.entries[idx]
	for !atomic.CompareAndSwapUint32(&entry.lock, 0, 1) {
	}
	e := *entry
	atomic.StoreUint32(&entry.lock, 0)
	if e.hash == hash {
		return e.score
	}
	return hashNotFoundScore
}

// record stores score for hash, always overwriting the existing entry.
func (t *evalHashTable) record(hash zobrist, score int) {
	idx := hash & t.mask
	entry := &t.entries[idx]
	for !atomic.CompareAndSwapUint32(&entry.lock, 0, 1) {
	}
	entry.hash = hash
	entry.score = score
	atomic.StoreUint32(&entry.lock, 0)
}

// pawnSquare is the piece-square bonus for pawns. Rank 8 at low indices; White flips via ^rankFlip.
var pawnSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	25, 25, 25, 25, 25, 25, 25, 25,
	15, 15, 15, 20, 20, 15, 15, 15,
	10, 10, 10, 15, 15, 10, 10, 10,
	0, 0, 0, 10, 10, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, -40, -40, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
}

// knightSquare is the piece-square bonus for knights.
var knightSquare = [64]int{
	-20, -10, -10, -10, -10, -10, -10, -20,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-20, -20, -10, -10, -10, -10, -20, -20,
}

// bishopSquare is the piece-square bonus for bishops.
var bishopSquare = [64]int{
	-10, -10, -10, -10, -10, -10, -10, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 10, 10, 5, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, -10, -20, -10, -10, -20, -10, -10,
}

// rookSquare is the piece-square bonus for rooks.
var rookSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	5, 10, 10, 10, 10, 10, 10, 5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 0, 0, 0, 0, 0, -5,
	-5, 0, 5, 5, 5, 5, 0, -5,
}

// queenSquare is the piece-square bonus for queens.
var queenSquare = [64]int{
	-10, -10, -10, -5, -5, -10, -10, -10,
	-10, 0, 0, 0, 0, 0, 0, -10,
	-10, 0, 5, 5, 5, 5, 0, -10,
	-5, 0, 5, 5, 5, 5, 0, -5,
	0, 0, 5, 5, 5, 5, 0, -5,
	-10, 5, 5, 5, 5, 5, 0, -10,
	-10, 0, 5, 0, 0, 0, 0, -10,
	-10, -10, -10, -5, -5, -10, -10, -10,
}

// King tables are phase-dependent and not maintained incrementally.
var kingSqMidgame = [64]int{
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-40, -40, -40, -40, -40, -40, -40, -40,
	-20, -20, -20, -20, -20, -20, -20, -20,
	20, 40, 40, -20, 0, -20, 40, 20,
}

// kingSqEndgame is the king piece-square table used once neither side has heavy material.
var kingSqEndgame = [64]int{
	0, 10, 20, 30, 30, 20, 10, 0,
	10, 20, 30, 40, 40, 30, 20, 10,
	20, 30, 40, 50, 50, 40, 30, 20,
	30, 40, 50, 60, 60, 50, 40, 30,
	30, 40, 50, 60, 60, 50, 40, 30,
	20, 30, 40, 50, 50, 40, 30, 20,
	10, 20, 30, 40, 40, 30, 20, 10,
	0, 10, 20, 30, 30, 20, 10, 0,
}

// passedPawnSquare rewards passed pawns based on how advanced they are.
// Indexed from Black's perspective (rank 8 at low indices); White flips via ^rankFlip.
var passedPawnSquare = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	30, 30, 30, 30, 30, 30, 30, 30,
	30, 30, 30, 30, 30, 30, 30, 30,
	20, 20, 20, 20, 20, 20, 20, 20,
	15, 15, 15, 15, 15, 15, 15, 15,
	10, 10, 10, 10, 10, 10, 10, 10,
	5, 5, 5, 5, 5, 5, 5, 5,
	0, 0, 0, 0, 0, 0, 0, 0,
}

// Mobility scores indexed by number of available squares (after exclusions).
var bishopAttackScores = [14]int{-20, -10, -5, -5, 0, 5, 10, 15, 20, 20, 20, 20, 20, 20}
var rookAttackScores = [15]int{-10, -10, -5, -5, 0, 5, 10, 15, 20, 20, 20, 20, 20, 20, 20}

// fileBBs maps file index (0=a … 7=h) to the full-file bitboard.
var fileBBs = [8]bitboard{fileA, fileB, fileC, fileD, fileE, fileF, fileG, fileH}

// flips maps a color to the XOR mask applied to a square index when reading PST tables.
// White XORs with rankFlip (0x38) to mirror ranks; Black reads tables directly (XOR 0).
var flips = [2]square{rankFlip, 0}

// pstValue returns material value plus piece-square bonus for piece p of color c
// on sq.  Returns 0 for the king; the king's PST is phase-dependent and applied
// separately in evaluatePosition.
func pstValue(c color, p piece, sq square) int {
	flip := flips[c]
	switch p {
	case pawn:
		return 100 + pawnSquare[sq^flip]
	case knight:
		return 300 + knightSquare[sq^flip]
	case bishop:
		return 300 + bishopSquare[sq^flip]
	case rook:
		return 500 + rookSquare[sq^flip]
	case queen:
		return 900 + queenSquare[sq^flip]
	}
	return 0
}

// isEndgame returns true when neither side has a queen, or every side that has a
// queen also has no rooks and at most one minor piece.
func isEndgame(pos *position) bool {
	for c := white; c <= black; c++ {
		friendly := pos.colors[c]
		if (pos.pieces[queen] & friendly).isEmpty() {
			continue
		}
		if (pos.pieces[rook]&friendly).popCount() > 0 || ((pos.pieces[knight]|pos.pieces[bishop])&friendly).popCount() > 1 {
			return false
		}
	}
	return true
}

// pawnStructure holds pawn classification for one side.
type pawnStructure struct {
	passed      bitboard // pawns with no enemy pawns ahead on the same or adjacent files
	isolated    bitboard // pawns with no friendly pawns on adjacent files
	unsupported bitboard // pawns not defended by another friendly pawn
	doubled     bitboard // pawns with another friendly pawn ahead on the same file
	defended    bitboard // pawns defended by at least one other friendly pawn
}

// maskPtrs bundles pointers to the four color-specific pawn structure mask tables.
type maskPtrs struct {
	passedMask    *[64]bitboard // squares ahead and on adjacent files (no enemy pawn ⇒ passed)
	doubledMask   *[64]bitboard // squares ahead on the same file (friendly pawn ⇒ doubled)
	isolatedMask  *[64]bitboard // adjacent files at all ranks (no friendly pawn ⇒ isolated)
	supportedMask *[64]bitboard // squares from which the pawn could be defended by a friendly pawn
}

var pawnMasks = [2]maskPtrs{
	{&passedPawnMaskWhite, &doubledPawnMaskWhite, &isolatedPawnMask, &supportedPawnMaskWhite},
	{&passedPawnMaskBlack, &doubledPawnMaskBlack, &isolatedPawnMask, &supportedPawnMaskBlack},
}

// determinePawnStructure classifies all pawns of color c into structural categories.
func determinePawnStructure(pos *position, c color) pawnStructure {
	friendlyPawns := pos.pieces[pawn] & pos.colors[c]
	enemyPawns := pos.pieces[pawn] ^ friendlyPawns
	var ps pawnStructure

	if c == white {
		ps.defended = friendlyPawns & (friendlyPawns.shiftNorthwest() | friendlyPawns.shiftNortheast())
	} else {
		ps.defended = friendlyPawns & (friendlyPawns.shiftSouthwest() | friendlyPawns.shiftSoutheast())
	}

	for f := friendlyPawns; f.isNotEmpty(); {
		sq := f.popLsb()
		b := squareBB(sq)
		masks := &pawnMasks[c]
		if (masks.passedMask[sq] & enemyPawns).isEmpty() {
			ps.passed |= b
		}
		if (masks.doubledMask[sq] & friendlyPawns).isNotEmpty() {
			ps.doubled |= b
		}
		if (masks.isolatedMask[sq] & friendlyPawns).isEmpty() {
			ps.isolated |= b
		}
		if (masks.supportedMask[sq] & friendlyPawns).isEmpty() {
			ps.unsupported |= b
		}
	}
	// A doubled passed pawn doesn't score as passed; the back pawn is obstructed.
	ps.passed &= ^ps.doubled
	return ps
}

// evalMaterialExtra returns a bishop-pair bonus (+30 cp) when both color bishops are present.
func evalMaterialExtra(pos *position, c color) int {
	bishops := pos.pieces[bishop] & pos.colors[c]
	if (bishops&whiteSqs) != 0 && (bishops&blackSqs) != 0 {
		return 30
	}
	return 0
}

// evalPawnStructure scores pawn structure for color c.
func evalPawnStructure(ps *pawnStructure, c color) int {
	flip := flips[c]
	score := 0
	for p := ps.passed; p.isNotEmpty(); {
		sq := p.popLsb()
		score += passedPawnSquare[sq^flip]
	}
	score += ps.defended.popCount() * 5
	score -= ps.unsupported.popCount() * 10
	score -= ps.doubled.popCount() * 10
	score -= ps.isolated.popCount() * 20
	return score
}

// evalMobility scores bishop and rook mobility for color c.
// Bishop squares defended by enemy pawns are excluded from the count.
func evalMobility(pos *position, c color, ps *[2]pawnStructure) int {
	friendly := pos.colors[c]
	occ := pos.occupied()
	enemyDefended := ps[enemyOf(c)].defended
	score := 0
	for b := pos.pieces[bishop] & friendly; b.isNotEmpty(); {
		sq := b.popLsb()
		attacks := bishopAttacks(occ, sq) & ^friendly & ^enemyDefended
		score += bishopAttackScores[attacks.popCount()]
	}
	for r := pos.pieces[rook] & friendly; r.isNotEmpty(); {
		sq := r.popLsb()
		attacks := rookAttacks(occ, sq) & ^friendly
		score += rookAttackScores[attacks.popCount()]
	}
	return score
}

// evalKing scores king safety: PST bonus, three-row pawn shelter, open-file penalties,
// and a bonus for friendly non-pawn pieces near the king.
func evalKing(pos *position, color color, endgame bool) int {
	flip := flips[color]
	friendly := pos.colors[color]
	enemyColor := enemyOf(color)
	friendlyPawns := friendly & pos.pieces[pawn]
	enemyPawns := pos.colors[enemyColor] & pos.pieces[pawn]
	kingLocation := pos.kingLocation[color]

	var pstScore int
	if endgame {
		pstScore = kingSqEndgame[kingLocation^flip]
	} else {
		pstScore = kingSqMidgame[kingLocation^flip]
	}

	safetyScore := 0
	if !endgame {
		// King pawn shelter
		var shelter1, shelter2, shelter3 bitboard
		if color == white {
			shelter1 = kingPawnShelterWhite[kingLocation]
			shelter2 = shelter1.shiftNorth()
			shelter3 = shelter2.shiftNorth()
		} else {
			shelter1 = kingPawnShelterBlack[kingLocation]
			shelter2 = shelter1.shiftSouth()
			shelter3 = shelter2.shiftSouth()
		}
		safetyScore = 15*(friendlyPawns&shelter1).popCount() +
			10*(friendlyPawns&shelter2).popCount() +
			5*(friendlyPawns&shelter3).popCount() -
			15*(enemyPawns&shelter1).popCount() -
			10*(enemyPawns&shelter2).popCount() -
			5*(enemyPawns&shelter3).popCount()

		// Penalise open files near the king.
		kingFile := fileBBs[kingLocation.file()]
		if (kingFile & friendlyPawns).isEmpty() {
			safetyScore -= 15
		}
		if adjW := kingFile.shiftWest(); adjW.isNotEmpty() && (adjW & friendlyPawns).isEmpty() {
			safetyScore -= 10
		}
		if adjE := kingFile.shiftEast(); adjE.isNotEmpty() && (adjE & friendlyPawns).isEmpty() {
			safetyScore -= 10
		}
	}

	// Friendly non-pawn pieces near the king provide protection.
	nonPawns := friendly ^ friendlyPawns
	adj1 := kingAttacks[kingLocation]
	adj2 := kingAttacks2[kingLocation]
	safetyScore += 5*(adj1&nonPawns).popCount() + 5*(adj2&nonPawns).popCount()

	return pstScore + safetyScore
}

// evaluatePosition returns the score from the side-to-move's perspective.
// Returns drawScore for positions with insufficient material.
// If the material score alone is more than 200 cp outside [alpha, beta], it returns early.
func evaluatePosition(ss *searchState, alpha, beta int) int {
	dbgx.evalCalls++
	pos := ss.currentPosition()
	if pos.isDrawByMaterial() {
		return drawScore
	}

	hashScore := ss.game.evalTable.find(pos.hash)
	if hashScore != hashNotFoundScore {
		dbgx.evalHashHits++
		return hashScore
	}

	wScore := pos.scores[white] + evalMaterialExtra(pos, white)
	bScore := pos.scores[black] + evalMaterialExtra(pos, black)

	// Early exit: if material alone is far outside the search window, skip full eval.
	matScore := wScore - bScore
	if pos.colorToMove == black {
		matScore = -matScore
	}
	if matScore > (beta + 200) {
		dbgx.evalCutoffBeta++
		return matScore
	}
	if matScore < (alpha - 200) {
		dbgx.evalCutoffAlpha++
		return matScore
	}

	endgame := isEndgame(pos)
	ps := [2]pawnStructure{determinePawnStructure(pos, white), determinePawnStructure(pos, black)}
	wScore += evalPawnStructure(&ps[white], white)
	bScore += evalPawnStructure(&ps[black], black)
	wScore += evalMobility(pos, white, &ps)
	bScore += evalMobility(pos, black, &ps)
	wScore += evalKing(pos, white, endgame)
	bScore += evalKing(pos, black, endgame)

	if pos.colorToMove == white {
		ss.game.evalTable.record(pos.hash, wScore-bScore)
		return wScore - bScore
	}
	ss.game.evalTable.record(pos.hash, bScore-wScore)
	return bScore - wScore
}
