//go:build !pawnstar_nnue_output_buckets_1

package nnue

// outputBuckets is 8: eight material output heads, selected by (piece_count - 2) / 4 — see
// outputBucket in nnue.go, which derives the divisor as ceil(32 / outputBuckets) so that it matches
// bullet's MaterialCount<N> at any N.
//
// This is half of the shared architecture the reckless-technology chess engines are unified on, and
// it is the DEFAULT: the shipped net (pawnstar-v20.bin, release 2.0.0) was trained for it. Measured
// in zenith at +14.7 Elo at fixed depth. The other half is horizontal king mirroring
// (shape_mirroring_on.go).
//
// Build v12's single output head with `-tags pawnstar_nnue_output_buckets_1`
// (`make OUTPUT_BUCKETS=1 ...`), which selects shape_heads_one.go instead. The two shape knobs stay
// independent: the head count does NOT move format_version, because the king bucket map is untouched
// and output_buckets is its own header field, so the version only moves when the map's meaning does.
const outputBuckets = 8
