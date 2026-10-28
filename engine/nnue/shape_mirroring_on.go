//go:build pawnstar_nnue_king_mirroring

package nnue

// kingMirroringOn is 1: this build mirrors a perspective whose own king stands on files e-h
// (`square ^ 7`) before feature indexing, and indexes the mirrored king bucket map
// (`half * 4 + min(file, 7 - file)`) instead of v12's file-pair map.
//
// This is half of the shared architecture the five reckless-technology engines are unifying on,
// measured in zenith at +14.3 Elo at fixed depth and worth more than that: halving the
// king-placement space gives twice the effective data per bucket at the same file size. The other
// half is the eight material output heads (shape_heads_eight.go), a separately SPRT-gated change.
//
// Mirroring changes what a bucket MEANS, not merely how many there are, so by the container's own
// rule it is a format change: format_version becomes 2 and this build therefore refuses the shipped
// v12 net, naming format_version. That is the container working, not a fault.
const kingMirroringOn = 1
