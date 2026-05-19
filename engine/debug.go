package engine

import "fmt"

// debugX collects per-search diagnostic counters.  To use, call dbgxClear before
// a search and dbgxPrint after to inspect search behaviour.
type debugX struct {
	betaCutoffs      uint // beta cutoffs (fail-high nodes)
	tableHitAlpha    uint // TT hits that pruned: stored score ≤ alpha (All node)
	tableHitBeta     uint // TT hits that pruned: stored score ≥ beta (Cut node)
	tableHitPV       uint // TT hits on PV nodes (search depth incremented by 1)
	nullMoveAttempts uint // times null-move pruning was attempted
	nullMoveFails    uint // times the null-move score did not reach beta
	lmrAttempts      uint // times single-ply late-move reduction was applied
	lmr2Attempts     uint // times double-ply LMR was applied (deep node, late move)
	lmrFails         uint // times LMR re-search was required (score > alpha at reduced depth)
	pvsAttempts      uint // times a PVS null-window search was attempted
	pvsFails         uint // times PVS failed high and a full-window re-search was needed
}

var dbgx debugX

func dbgxClear() {
	dbgx = debugX{}
}

func dbgxPrint() {
	fmt.Printf("%+v\n", dbgx)
}
