//go:build !pawnstar_nnue_king_mirroring

package nnue

// kingMirroringOn is 0: this build does NOT mirror a perspective whose own king stands on files e-h.
// That is v12's shape, the one the shipped net was trained for, and it is the default.
//
// Build the shared architecture's mirrored shape with `-tags pawnstar_nnue_king_mirroring`
// (`make KING_MIRRORING=1 ...`), which selects shape_mirroring_on.go instead. The knob is an int
// rather than a bool so that every value derived from it — the mirror mask, the header's `flags` bit
// and `format_version` — is one piece of constant arithmetic off this single number, exactly as the
// C++ engine derives them from PAWNSTAR_NNUE_KING_MIRRORING. See KingMirroringEnabled in nnue.go.
const kingMirroringOn = 0
