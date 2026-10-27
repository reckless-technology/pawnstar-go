package engine

// Bridge between the engine and the nnue evaluation package (engine/nnue): the position -> nnue.Board
// view conversion, the search-band clamp applied to raw NNUE scores, and the Game methods that install
// a network. The nnue package is deliberately free of engine imports (it consumes raw bitboards and
// piece codes), so this file is the only place the two vocabularies meet.

import (
	"errors"
	"unsafe"

	"pawnstar-go/engine/nnue"
)

// nnueBoard returns the position as the raw piece-placement view the nnue package consumes — a
// zero-copy reinterpret, not a conversion. position's first three fields (pieces [7]bitboard,
// colors [2]bitboard, squares [64]piece) are laid out byte-identically to nnue.Board (Pieces
// [7]uint64, Colors [2]uint64, Squares [64]uint8) because bitboard is uint64 and piece is uint8 and
// the field order matches — so the position's leading 136 bytes ARE an nnue.Board. Passing the
// pointer (rather than materialising a value) is what C++ gets for free by handing Update a
// `Position const&`; profiling showed the old per-eval materialisation was ~10% of runtime, almost
// all of it redundant rebuilds of the parent board across a node's siblings.
//
// The layout equivalence is a contract, not a coincidence: it mirrors the C++ Position. It is asserted
// bit-for-bit by TestNnueBoardLayout (offsets + a random-position round-trip against
// nnueBoardExplicit), and any reorder of position's leading fields fails that test loudly. The nnue
// package stays free of engine imports — this file remains the only place the two vocabularies meet.
func (pos *position) nnueBoard() *nnue.Board {
	return (*nnue.Board)(unsafe.Pointer(pos))
}

// nnueBoardExplicit builds the nnue.Board by explicit field copy — the independent oracle that
// TestNnueBoardLayout checks the zero-copy nnueBoard() against, so the unsafe reinterpret can never
// silently diverge from a plain, obviously-correct construction. Not used on any hot path.
func (pos *position) nnueBoardExplicit() nnue.Board {
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
	return min(max(score, -nnueSearchClamp), nnueSearchClamp)
}

// EnableNNUE (re)installs the embedded NNUE network as this game's evaluator, replacing any net loaded
// via EvalFile. NewGame already installs it, so this is only needed to revert to the embedded net.
//
// Returns an error if the embedded net is unusable — which would mean the build itself is corrupt,
// since the net is compiled in and its container's checks are made on it like any other. As in
// LoadNNUEFile, that leaves the game with no evaluator rather than keeping whatever net was
// installed before: a net asked for and not delivered is never silently substituted.
func (game *Game) EnableNNUE() error {
	net := nnue.Embedded()
	game.network = net     // nil when even the embedded net is unusable: no substitution, by design
	game.evalTable.clear() // cached scores are net-dependent
	if net == nil {
		return errors.New("nnue: embedded net unavailable")
	}
	return nil
}

// LoadNNUEFile loads an external RNNUE1 net from disk and installs it as this game's evaluator,
// replacing the net installed before it.
//
// A refused net leaves the game with NO evaluator at all — not the net that was installed before,
// and not the embedded one. That is deliberate and is the whole point of the container's checks: a
// loader that keeps the previous net plays on with weights nobody asked for, and a match then
// reports a plausible Elo for a net that never played a move. searchRootNode refuses to search
// while no net is installed, so the failure is loud at every later `go` rather than silent. Call
// EnableNNUE to come back to the embedded net deliberately.
func (game *Game) LoadNNUEFile(path string) error {
	net, err := nnue.LoadFile(path)
	game.network = net     // nil on a refusal: no fallback, by design
	game.evalTable.clear() // cached scores are net-dependent
	return err
}
