//go:build !pawnstar_nnue_king_mirroring_off

package nnue

// kingMirroringOn is 1: this build mirrors a perspective whose own king stands on files e-h
// (`square ^ 7`) before feature indexing, and indexes the mirrored king bucket map
// (`half * 4 + min(file, 7 - file)`) instead of v12's file-pair map.
//
// This is half of the shared architecture the reckless-technology chess engines are unified on, and
// it is the DEFAULT: the shipped net (pawnstar-v20.bin, release 2.0.0) was trained for it. Measured
// in zenith at +14.3 Elo at fixed depth and worth more than that — halving the king-placement space
// gives twice the effective data per bucket at the same file size. The other half is the eight
// material output heads (shape_heads_eight.go).
//
// Build v12's unmirrored shape with `-tags pawnstar_nnue_king_mirroring_off`
// (`make KING_MIRRORING=0 ...`), which selects shape_mirroring_off.go instead. The knob is an int
// rather than a bool so that every value derived from it — the mirror mask, the header's `flags` bit
// and `format_version` — is one piece of constant arithmetic off this single number, exactly as the
// C++ engine derives them from PAWNSTAR_NNUE_KING_MIRRORING. See KingMirroringEnabled in nnue.go.
//
// Mirroring changes what a bucket MEANS, not merely how many there are, so by the container's own
// rule it is a format change: format_version is 2 here, and a build at v12's shape refuses this
// build's net (and vice versa), naming format_version. That is the container working, not a fault.
const kingMirroringOn = 1
