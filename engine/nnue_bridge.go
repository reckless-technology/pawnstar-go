package engine

// Bridge between the engine and the nnue evaluation package (engine/nnue): the position -> nnue.Board
// view conversion, the search-band clamp applied to raw NNUE scores, and the Game methods that install
// a network. The nnue package is deliberately free of engine imports (it consumes raw bitboards and
// piece codes), so this file is the only place the two vocabularies meet.

import (
	"fmt"

	"pawnstar-go/engine/nnue"
)

// nnueBoard builds the raw piece-placement view the nnue package consumes. Pure value conversion —
// the engine's bitboard/piece codes already match the nnue package's (uint64 occupancy, piece codes
// pawn=1..king=6), so this is a plain widening copy with no re-encoding.
func (pos *position) nnueBoard() nnue.Board {
	var b nnue.Board
	for i, bb := range pos.pieces {
		b.Pieces[i] = uint64(bb)
	}
	b.Colors[white] = uint64(pos.colors[white])
	b.Colors[black] = uint64(pos.colors[black])
	for s, pc := range pos.squares {
		b.Squares[s] = uint8(pc)
	}
	return b
}

// nnueSearchClamp keeps a returned NNUE evaluation strictly inside the search's non-mate band, so a
// large eval can never be mistaken for a forced win (winThreshold) or collide with mate scores.
const nnueSearchClamp = winThreshold - 1000

// clampEvalToBand clamps a raw NNUE score into the search's non-mate band.
func clampEvalToBand(score int) int {
	if score > nnueSearchClamp {
		return nnueSearchClamp
	}
	if score < -nnueSearchClamp {
		return -nnueSearchClamp
	}
	return score
}

// EnableNNUE (re)installs the embedded NNUE network as this game's evaluator, replacing any net loaded
// via EvalFile. NewGame already installs it, so this is only needed to revert to the embedded net.
// Returns an error if the embedded net is unusable.
func (game *Game) EnableNNUE() error {
	net := nnue.Embedded()
	if net == nil {
		return fmt.Errorf("nnue: embedded net unavailable")
	}
	game.nnue = net
	game.evalTable.clear() // cached scores are net-dependent
	return nil
}

// LoadNNUEFile loads an external PSN1 net from disk and installs it as this game's evaluator,
// replacing the embedded net. Returns an error (leaving the current evaluator unchanged) if the file
// is missing or incompatible with this build.
func (game *Game) LoadNNUEFile(path string) error {
	net, err := nnue.LoadFile(path)
	if err != nil {
		return err
	}
	game.nnue = net
	game.evalTable.clear() // cached scores are net-dependent
	return nil
}
