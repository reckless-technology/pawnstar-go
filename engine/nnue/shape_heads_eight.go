//go:build pawnstar_nnue_output_buckets_8

package nnue

// outputBuckets is 8: eight material output heads, selected by (piece_count - 2) / 4 — see
// outputBucket in nnue.go, which derives the divisor as ceil(32 / outputBuckets) so that it matches
// bullet's MaterialCount<N> at any N.
//
// This is half of the shared architecture the five reckless-technology engines are unifying on,
// measured in zenith at +14.7 Elo at fixed depth. The other half is horizontal king mirroring
// (shape_mirroring_on.go), a separately SPRT-gated change. More heads do NOT move format_version:
// the king bucket map is unchanged, and output_buckets is its own header field, so the version only
// moves when the map's meaning does.
const outputBuckets = 8
