package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"
)

// debugX collects per-search diagnostic counters.  To use, call dbgxClear before
// a search and dbgxPrint after to inspect search behaviour.
// Field names are kept in sync with the C++ engine's debug_dictionary keys: dbgxPrint renders each field
// as camelToWords(name), which must equal the corresponding C++ INCREMENT("...") key, so the two engines'
// diagnostic dumps line up counter-for-counter.
type debugX struct {
	allNodes                    uint // fail-low (All) nodes
	alphaBetaCalls              uint // every searchAlphaBeta entry
	bestScoreChanged            uint // a move raised the best score
	betaCutoffs                 uint // fail-high (Cut) nodes
	checkExtensions             uint // a check extension deepened the search by one ply
	checkmates                  uint
	drawsByMaterial2            uint // K vs K
	drawsByMaterial3            uint // K+minor vs K
	drawsByMaterial4            uint // KB vs KB, same-colour bishops
	drawsByRepetition           uint
	epCaptureExtensions         uint // an en-passant capture was searched one ply deeper
	evalCalls                   uint
	evalHashHits                uint
	internalIterativeReductions uint // internal iterative reduction fired (no TT move)
	killerBetaCutoffs           uint // a killer move produced a beta cutoff
	lateMovePrunes              uint // late-move (move-count) pruning skipped a quiet move
	lateMoveReduction1          uint // single-ply LMR applied
	lateMoveReduction2          uint // double-ply LMR applied
	lateMoveReduction3          uint // triple-ply LMR applied (zero history)
	lateMoveReductionFails      uint // an LMR re-search was required (score > alpha at reduced depth)
	maxPlyReached               uint // hit the ply cap in searchAlphaBeta
	nullMoveAttempts            uint // null-move pruning was attempted
	nullMoveFails               uint // the null-move score did not reach beta
	promotionExtensions         uint // a promotion was searched one ply deeper
	pvChanged                   uint // a move raised alpha (new PV)
	pvNodes                     uint // exact (PV) nodes
	pvsAttempts                 uint // a PVS null-window search was attempted
	pvsFails                    uint // PVS failed high and a full-window re-search was needed
	pvsFailsRootPly             uint // of which, at the root (ply 0)
	quiescentAllNodes           uint // quiescence node with no cutoff
	quiescentBetaCutoffs        uint // a capture produced a beta cutoff
	quiescentCalls              uint
	quiescentChecks             uint // in-check node re-entered searchAlphaBeta
	quiescentEvalBetaCutoffs    uint // stand-pat eval ≥ beta
	quiescentEvalRaisesAlpha    uint // stand-pat eval raised alpha
	quiescentMaxPly             uint // hit the ply cap in quiescence
	quiescentPvChanged          uint // a capture raised alpha
	quiescentSeePrunes          uint // stopped on the first losing (negative-SEE) capture
	reverseFutilityPrunes       uint // reverse-futility pruning returned early
	stalemates                  uint
	tableHitAllNode             uint // TT hit on an All-node entry
	tableHitAllNodeCutoffs      uint // that hit also pruned (stored score ≤ alpha)
	tableHitCutNode             uint // TT hit on a Cut-node entry
	tableHitCutNodeCutoffs      uint // that hit also pruned (stored score ≥ beta)
	tableHitPvNode              uint // TT hit on a PV-node entry (search depth incremented by 1)
	tableMove                   uint // a TT move was tried first
	tableMoveBetaCutoffs        uint // the TT move produced a beta cutoff
	tableMoveRaisedAlpha        uint // the TT move raised alpha
}

// dbgx is the global accumulator for search diagnostics.
var dbgx debugX

// dbgxClear resets all diagnostic counters to zero.
func dbgxClear() {
	dbgx = debugX{}
}

// recordMaterialDraw bumps the diagnostic counter for a material-draw category (2/3/4 pieces on the board).
func (ss *searchState) recordMaterialDraw(cat int) {
	switch cat {
	case 2:
		ss.dbg.drawsByMaterial2++
	case 3:
		ss.dbg.drawsByMaterial3++
	case 4:
		ss.dbg.drawsByMaterial4++
	}
}

// camelToWords converts a camelCase identifier to a space-separated lowercase string.
func camelToWords(s string) string {
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (unicode.IsUpper(ch) || unicode.IsNumber(ch)) {
			b.WriteByte(' ')
		}
		b.WriteRune(unicode.ToLower(ch))
	}
	return b.String()
}

// dbgxLine is one formatted counter line used by dbgxPrint.
type dbgxLine struct {
	name string // human-readable counter name derived from the struct field name
	val  uint64 // accumulated count
}

// dbgxPrint prints all diagnostic counters in alphabetical order.
func dbgxPrint() {
	v := reflect.ValueOf(dbgx)
	t := reflect.TypeFor[debugX]()
	lines := make([]dbgxLine, t.NumField())
	for i := range t.NumField() {
		lines[i] = dbgxLine{camelToWords(t.Field(i).Name), v.Field(i).Uint()}
	}
	slices.SortFunc(lines, func(a, b dbgxLine) int { return strings.Compare(a.name, b.name) })
	for _, l := range lines {
		fmt.Printf("%-30s %10d\n", l.name, l.val)
	}
}
