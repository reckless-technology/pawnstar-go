package engine

import "fmt"

type debugX struct {
	betaCutoffs      uint
	tableHitAlpha    uint
	tableHitBeta     uint
	tableHitPV       uint
	nullMoveAttempts uint
	nullMoveFails    uint
	lmrAttempts      uint
	lmr2Attempts     uint
	lmrFails         uint
	pvsAttempts      uint
	pvsFails         uint
}

var dbgx debugX

func dbgxClear() {
	dbgx = debugX{}
}

func dbgxPrint() {
	fmt.Printf("%+v\n", dbgx)
}
