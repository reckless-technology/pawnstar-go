package engine

// Bridge between the engine and the nnue evaluation package (engine/nnue): the position -> nnue.Board
// view conversion, the search-band clamp applied to raw NNUE scores, and the Game methods that install
// a network. The nnue package is deliberately free of engine imports (it consumes raw bitboards and
// piece codes), so this file is the only place the two vocabularies meet.

import (
	"errors"
	"fmt"
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

// embeddedNetOrigin is the networkOrigin value standing for the net compiled into the binary. Every
// other origin is the path EvalFile was given, so the kept-net message names either a real file or
// this word and never an empty string.
const embeddedNetOrigin = "embedded"

// EnableNNUE (re)installs the embedded NNUE network as this game's evaluator, replacing any net loaded
// via EvalFile. NewGame already installs it, so this is only needed to revert to the embedded net.
//
// Returns an error if the embedded net is unusable — which would mean the build itself is corrupt,
// since the net is compiled in and its container's checks are made on it like any other. That one case
// does leave the game with no evaluator, and deliberately: the embedded net is the last resort, so
// there is nothing left to keep, and the only honest state is none installed with a search that
// refuses loudly. It is also the single path that keeps searchRootNode's no-net guard reachable. A net
// read from a file and REFUSED is a different case entirely — see LoadNNUEFile, which keeps the net
// already playing and says which one it is.
func (game *Game) EnableNNUE() error {
	net := nnue.Embedded()
	game.network = net                     // nil only when the build itself is corrupt (see above)
	game.networkOrigin = embeddedNetOrigin // and "" below when there is no net to name
	game.evalTable.clear()                 // cached scores are net-dependent
	if net == nil {
		game.networkOrigin = ""
		return errors.New("nnue: embedded net unavailable")
	}
	return nil
}

// LoadNNUEFile loads an external RNNUE1 net from disk and installs it as this game's evaluator,
// replacing the net installed before it.
//
// A refused net changes nothing: the net already playing stays installed and keeps playing, and the
// caller reports the refusal on stdout as an `info string` AND on stderr, naming which net is now in
// play (keptNetMessage, printed by handleUciSetOption). The rule the container's checks exist to
// enforce is that a refused net is never SUBSTITUTED FOR — a match must never report a plausible Elo
// for a net that never played a move — and keeping the net already playing satisfies it, because the
// operator is told on a stream no harness drops which net the games were actually played with.
//
// Uninstalling the evaluator instead (what this used to do) breaks the same rule from the other side:
// it leaves the engine in a state with no defined play, where every later `go` answers
// `bestmove 0000`. An operator who misses one message then has a thousand games of nothing instead of
// a thousand games of the net that was already loaded. searchRootNode's no-net guard therefore stays
// as a defensive path — reachable when even the embedded net is unusable, i.e. a corrupt build (see
// EnableNNUE) — rather than as this function's ordinary outcome.
//
// Call EnableNNUE to come back to the embedded net deliberately.
func (game *Game) LoadNNUEFile(path string) error {
	net, err := nnue.LoadFile(path)
	if err != nil {
		return err // the net already installed keeps playing, and networkOrigin still names it
	}
	game.network = net
	game.networkOrigin = path
	game.evalTable.clear() // cached scores are net-dependent
	return nil
}

// keptNetMessage is the one line that tells an operator which net is actually playing after
// refusedPath was refused. The wording is fixed and shared by all four engines, so a match log reads
// the same whichever engine wrote it; handleUciSetOption prints it on stdout as an `info string` and
// on stderr, because a harness keeps stderr and routinely drops info lines.
//
// The no-net branch is no longer reachable from a refused EvalFile — only from a build whose own
// embedded net is unusable — but the message must never claim to keep a net the engine does not have.
func (game *Game) keptNetMessage(refusedPath string) string {
	if game.network == nil {
		return fmt.Sprintf("NNUE: no net is installed at all; '%s' was refused and is not in play — "+
			"the next `go` will refuse to search", refusedPath)
	}
	return fmt.Sprintf("NNUE: keeping the net already loaded ('%s'); '%s' was refused and is not in play",
		game.networkOrigin, refusedPath)
}
