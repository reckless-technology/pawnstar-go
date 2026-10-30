//go:build pawnstar_nnue_output_buckets_1

package nnue

// outputBuckets is 1: a single output head, which every position selects. That is v12's shape, which
// is no longer the default and which no net in this tree is trained for — the shipped net is the
// mirrored, eight-head one (shape_heads_eight.go, the default since release 2.0.0). It is kept
// buildable so the shape itself stays exercised and so a v12-era net can still be loaded by a build
// made on purpose for it.
//
// Selected by `-tags pawnstar_nnue_output_buckets_1` (`make OUTPUT_BUCKETS=1 ...`). The head count
// does not move format_version — the king bucket map is untouched — so output_buckets alone is what
// refuses a mismatched net here, naming that field.
const outputBuckets = 1
