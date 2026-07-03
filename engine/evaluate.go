package engine

// rule50ScaleThreshold is the halfmove-clock ply below which the static eval is untouched, so normal
// play (where the clock is reset often by pawn moves/captures) is unaffected.
const rule50ScaleThreshold = 20

// scaleByRule50 scales a static eval toward draw as the fifty-move clock climbs, giving the search a
// standing incentive to make progress (reset the clock) rather than shuffle in a won-but-unconverted
// position — without it a placement-only NNUE eval is identical at clock 0 and clock 99, and leaks won
// endgames (notably KBN-vs-K) to the rule. Identity for rule50 ≤ threshold, then a linear ramp to 0 as
// the clock approaches 100 (the hard draw handled by isDrawByFiftyMoves). Mirrors the C++ ScaleByRule50.
func scaleByRule50(score, rule50 int) int {
	if rule50 <= rule50ScaleThreshold {
		return score
	}
	return int(int64(score) * int64(100-rule50) / int64(100-rule50ScaleThreshold))
}

// evaluatePosition returns the NNUE score from the side-to-move's perspective, drawScore for positions
// with insufficient material. The result is cached raw (clock-independent) in the eval table; the
// fifty-move scaling (scaleByRule50) is applied on the way out, never stored — the hash excludes the
// clock, so the same position can be probed at different clock values.
func evaluatePosition(ss *searchState) int {
	ss.dbg.evalCalls++
	pos := ss.currentPosition()
	if cat := pos.materialDrawCategory(); cat != 0 {
		ss.recordMaterialDraw(cat)
		return drawScore
	}

	hashScore := ss.game.evalTable.find(pos.hash)
	if hashScore != hashNotFoundScore {
		ss.dbg.evalHashHits++
		return scaleByRule50(hashScore, int(pos.halfMove))
	}

	score := clampEvalToBand(ss.game.nnue.EvaluateInt8(ss.currentAccumulator(), int(pos.colorToMove)))
	ss.game.evalTable.record(pos.hash, score)
	return scaleByRule50(score, int(pos.halfMove))
}
