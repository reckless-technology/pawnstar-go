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
type debugX struct {
	historyTableMax      uint
	checkmates           uint
	stalemates           uint
	quiescentCalls       uint
	quiescentEvalCutoffs uint
	quiescentBetaCutoffs uint
	quiescentChecks      uint
	evalCalls            uint
	evalCutoffBeta       uint
	evalCutoffAlpha      uint
	evalHashHits         uint
	nodesCut             uint // beta cutoffs (fail-high nodes)
	nodesPvChanged       uint
	nodesPv              uint
	nodesAll             uint
	tableHitAlpha        uint // TT hits that pruned: stored score ≤ alpha (All node)
	tableHitBeta         uint // TT hits that pruned: stored score ≥ beta (Cut node)
	tableHitPv           uint // TT hits on PV nodes (search depth incremented by 1)
	tableMove            uint
	tableMoveBetaCutoff  uint
	tableMoveRaiseAlpha  uint
	nullMoveAttempts     uint // times null-move pruning was attempted
	nullMoveFails        uint // times the null-move score did not reach beta
	lmrAttempts1         uint // times single-ply late-move reduction was applied
	lmrAttempts2         uint // times double-ply LMR was applied (deep node, late move)
	lmrFails             uint // times LMR re-search was required (score > alpha at reduced depth)
	pvsAttempts          uint // times a PVS null-window search was attempted
	pvsFails             uint // times PVS failed high and a full-window re-search was needed
}

var dbgx debugX

func dbgxClear() {
	dbgx = debugX{}
}

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

type dbgxLine struct {
	name string
	val  uint64
}

func dbgxPrint() {
	v := reflect.ValueOf(dbgx)
	t := reflect.TypeFor[debugX]()
	lines := make([]dbgxLine, t.NumField())
	for i := range t.NumField() {
		lines[i] = dbgxLine{camelToWords(t.Field(i).Name), v.Field(i).Uint()}
	}
	slices.SortFunc(lines, func(a, b dbgxLine) int { return strings.Compare(a.name, b.name) })
	for _, l := range lines {
		fmt.Printf("%-25s %10d\n", l.name, l.val)
	}
}
