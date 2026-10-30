//go:build pawnstar_nnue_king_mirroring_off

package nnue

// kingMirroringOn is 0: this build does NOT mirror a perspective whose own king stands on files e-h.
// That is v12's shape, which is no longer the default and which no net in this tree is trained for —
// the shipped net is the mirrored, eight-head one (shape_mirroring_on.go, the default since release
// 2.0.0). It is kept buildable so the shape itself stays exercised and so a v12-era net can still be
// loaded by a build made on purpose for it.
//
// Selected by `-tags pawnstar_nnue_king_mirroring_off` (`make KING_MIRRORING=0 ...`). A build here is
// format_version 1 and therefore refuses the shipped net, naming format_version; its gates run
// against the synthetic net instead (synthetic.go), since the expected values of the net-specific
// gates belong to the shipped net.
const kingMirroringOn = 0
