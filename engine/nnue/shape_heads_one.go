//go:build !pawnstar_nnue_output_buckets_8

package nnue

// outputBuckets is 1: a single output head, which every position selects. That is v12's shape, the
// one the shipped net was trained for, and it is the default.
//
// Build the shared architecture's eight material output heads with
// `-tags pawnstar_nnue_output_buckets_8` (`make OUTPUT_BUCKETS=8 ...`), which selects
// shape_heads_eight.go instead. The two shape knobs are independent on purpose: mirroring and the
// output heads are separate, separately SPRT-gated measurements, and landing both at once would give
// one number for two changes.
const outputBuckets = 1
