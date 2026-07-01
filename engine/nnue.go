package engine

// NNUE (Efficiently Updatable Neural Network) evaluation, ported from the C++ Pawnstar engine
// (src/nnue.h). This file implements the loader for the stamped "PSN1" net format and the exact
// int16 SCReLU reference forward pass (evaluateExact) — the correctness oracle that must reproduce
// the trainer's evaluation within quantisation rounding. The int8 fast path and archsimd kernels
// are added in a later phase; this scalar reference defines the ground truth they must match.
//
// Architecture (shipped net v12): Chess768 features (2 colours * 6 types * 64 squares) per
// perspective, x kNumKingBuckets king-square weight banks -> a kHiddenSize int16 accumulator per
// perspective. Activation is SCReLU (clamp to [0,QA] then square). The output layer dots the two
// perspectives (side-to-move first) and dequantises to centipawns.

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
)

const (
	nnueInputSize     = 768                             // features per perspective per bucket: 2 colours * 6 types * 64
	nnueKingBuckets   = 8                               // king-square weight banks
	nnueFeatureRows   = nnueInputSize * nnueKingBuckets // 6144 (bucket-major)
	nnueHiddenSize    = 1024                            // feature-transformer / accumulator width per perspective
	nnueQA            = 255                             // feature-transformer weight/activation scale (SCReLU clamp)
	nnueQB            = 64                              // output-layer weight scale
	nnueScale         = 400                             // maps raw network output to centipawns
	nnueMaxEval       = 30000                           // eval clamp magnitude (matches C++ kMaxEvaluation)
	nnueRankFlip      = 0x38                            // XOR mask to flip a square's rank (a1<->a8)
	nnueFormatVersion = 1                               // net header format version
	nnueHeaderBytes   = 32                              // stamped NetHeader size

	nnueNumFeatureWeights = nnueFeatureRows * nnueHiddenSize // 6291456
	nnueNumOutputWeights  = 2 * nnueHiddenSize               // 2048
	// Packed payload size: feature_weights + feature_bias + output_weights + output_bias, all int16.
	nnuePayloadInt16 = nnueNumFeatureWeights + nnueHiddenSize + nnueNumOutputWeights + 1
	nnuePayloadBytes = nnuePayloadInt16 * 2
)

var nnueMagic = [4]byte{'P', 'S', 'N', '1'}

// nnueKingBucketMap maps a king square (in the perspective's own orientation) to its weight bank.
// File-pair (file/2) selects columns 0-3; board half (own ranks 1-4 vs advanced 5-8) adds 4.
// MUST be byte-identical to the map in the C++ engine and the bullet trainer.
var nnueKingBucketMap = [64]int{
	0, 0, 1, 1, 2, 2, 3, 3,
	0, 0, 1, 1, 2, 2, 3, 3,
	0, 0, 1, 1, 2, 2, 3, 3,
	0, 0, 1, 1, 2, 2, 3, 3,
	4, 4, 5, 5, 6, 6, 7, 7,
	4, 4, 5, 5, 6, 6, 7, 7,
	4, 4, 5, 5, 6, 6, 7, 7,
	4, 4, 5, 5, 6, 6, 7, 7,
}

// nnueAccumulator holds both perspective accumulators (each the feature-transformer output).
type nnueAccumulator struct {
	white [nnueHiddenSize]int16
	black [nnueHiddenSize]int16
}

// nnueNetwork is a loaded, quantised network: read-only after loading and safe to share.
type nnueNetwork struct {
	featureWeights []int16 // [nnueFeatureRows * nnueHiddenSize], column-major: row*hidden + i; rows bucket-major
	featureBias    []int16 // [nnueHiddenSize]
	outputWeights  []int16 // [2 * nnueHiddenSize]: first hidden weight stm accumulator, next hidden weight the other
	outputBias     int16   // in QA*QB units
	loaded         bool
}

// loadNNUENetworkFile reads a stamped net file from disk and loads it.
func loadNNUENetworkFile(path string) (*nnueNetwork, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("nnue: cannot read net %q: %w", path, err)
	}
	return loadNNUENetwork(data)
}

// loadNNUENetwork parses a stamped ("PSN1") net image. Every architecture field is validated
// against this build's constants, so an incompatible or unstamped net is rejected rather than
// silently misread. Trailing bytes after the packed payload (bullet padding) are ignored.
func loadNNUENetwork(data []byte) (*nnueNetwork, error) {
	if len(data) < nnueHeaderBytes {
		return nil, fmt.Errorf("nnue: net too small (%d bytes) for a 32-byte header", len(data))
	}
	if [4]byte(data[0:4]) != nnueMagic {
		return nil, fmt.Errorf("nnue: not a stamped Pawnstar net (missing %q header)", string(nnueMagic[:]))
	}
	// Header fields are little-endian; validate each against this build.
	got := struct{ version, inputSize, kingBuckets, hiddenSize, qa, qb, scale int }{
		version:     int(binary.LittleEndian.Uint16(data[4:6])),
		inputSize:   int(binary.LittleEndian.Uint16(data[6:8])),
		kingBuckets: int(binary.LittleEndian.Uint16(data[8:10])),
		hiddenSize:  int(binary.LittleEndian.Uint16(data[10:12])),
		qa:          int(int16(binary.LittleEndian.Uint16(data[12:14]))),
		qb:          int(int16(binary.LittleEndian.Uint16(data[14:16]))),
		scale:       int(int16(binary.LittleEndian.Uint16(data[16:18]))),
	}
	if got.version != nnueFormatVersion || got.inputSize != nnueInputSize || got.kingBuckets != nnueKingBuckets ||
		got.hiddenSize != nnueHiddenSize || got.qa != nnueQA || got.qb != nnueQB || got.scale != nnueScale {
		return nil, fmt.Errorf("nnue: incompatible net (file v%d/in%d/buckets%d/hidden%d/qa%d/qb%d/scale%d; "+
			"engine expects v%d/in%d/buckets%d/hidden%d/qa%d/qb%d/scale%d)",
			got.version, got.inputSize, got.kingBuckets, got.hiddenSize, got.qa, got.qb, got.scale,
			nnueFormatVersion, nnueInputSize, nnueKingBuckets, nnueHiddenSize, nnueQA, nnueQB, nnueScale)
	}
	if len(data) < nnueHeaderBytes+nnuePayloadBytes {
		return nil, fmt.Errorf("nnue: net truncated (%d bytes, need at least %d)", len(data), nnueHeaderBytes+nnuePayloadBytes)
	}

	// Payload in bullet's save order: feature_weights, feature_bias, output_weights, output_bias.
	p := data[nnueHeaderBytes:]
	n := &nnueNetwork{
		featureWeights: make([]int16, nnueNumFeatureWeights),
		featureBias:    make([]int16, nnueHiddenSize),
		outputWeights:  make([]int16, nnueNumOutputWeights),
	}
	off := readInt16Slice(p, 0, n.featureWeights)
	off = readInt16Slice(p, off, n.featureBias)
	off = readInt16Slice(p, off, n.outputWeights)
	n.outputBias = int16(binary.LittleEndian.Uint16(p[off : off+2]))
	n.loaded = true
	return n, nil
}

// readInt16Slice fills dst from little-endian int16 values in b starting at byte offset off,
// returning the offset just past the values read.
func readInt16Slice(b []byte, off int, dst []int16) int {
	for i := range dst {
		dst[i] = int16(binary.LittleEndian.Uint16(b[off : off+2]))
		off += 2
	}
	return off
}

// nnueFeatureRow is the feature row for a piece in one perspective's bucket bank.
//
//	white perspective: bucket*768 + colour*384 + (type-1)*64 + square
//	black perspective: bucket*768 + (1-colour)*384 + (type-1)*64 + (square ^ rankFlip)
func nnueFeatureRow(bucket, colorIdx int, pc piece, sq square, blackPersp bool) int {
	typeOffset := (int(pc) - int(pawn)) * 64
	var base int
	if blackPersp {
		base = (1-colorIdx)*384 + typeOffset + int(sq^nnueRankFlip)
	} else {
		base = colorIdx*384 + typeOffset + int(sq)
	}
	return bucket*nnueInputSize + base
}

// column returns the feature-transformer weight column (length nnueHiddenSize) for one piece on
// square s in the given perspective's bucket bank.
func (n *nnueNetwork) column(bucket, colorIdx int, pc piece, s square, blackPersp bool) []int16 {
	start := nnueFeatureRow(bucket, colorIdx, pc, s, blackPersp) * nnueHiddenSize
	return n.featureWeights[start : start+nnueHiddenSize]
}

// addColumn and subColumn (accumulator column add/sub) and outputDot (the SCReLU output dot) are the
// hot kernels; they have SIMD (archsimd) and scalar implementations selected by build tag in
// nnue_kernels_amd64.go / nnue_kernels_generic.go. All are bit-identical.

// refreshSide rebuilds one perspective accumulator from scratch: seed the bias, then add the
// feature column of every piece, oriented for that perspective's king bucket.
func (n *nnueNetwork) refreshSide(side *[nnueHiddenSize]int16, pos *position, bucket int, blackPersp bool) {
	copy(side[:], n.featureBias)
	for colorIdx := int(white); colorIdx <= int(black); colorIdx++ {
		friendly := pos.colors[colorIdx]
		for pc := pawn; pc <= king; pc++ {
			bb := pos.pieces[pc] & friendly
			for bb != 0 {
				s := bb.popLsb()
				addColumn(side, n.column(bucket, colorIdx, pc, s, blackPersp))
			}
		}
	}
}

// refresh rebuilds both perspectives of acc from scratch for pos.
func (n *nnueNetwork) refresh(acc *nnueAccumulator, pos *position) {
	whiteKing := (pos.pieces[king] & pos.colors[white]).lsb()
	blackKing := (pos.pieces[king] & pos.colors[black]).lsb()
	n.refreshSide(&acc.white, pos, nnueKingBucketMap[whiteKing], false)
	n.refreshSide(&acc.black, pos, nnueKingBucketMap[blackKing^nnueRankFlip], true)
}

// update moves acc from matching `from` to matching `to` by applying only the feature deltas for
// squares whose occupancy differs. Reversible — swap from/to to undo — and a no-op when placements
// are identical (a null move). Captures, castling, en passant and promotion are handled uniformly
// via the colour bitboards: for each square a colour vacated, subtract the piece that was there
// (read from `from`); for each it now occupies, add the piece now there (read from `to`). A king
// move that crosses a bucket boundary changes that whole perspective's bank, so it is refreshed.
func (n *nnueNetwork) update(acc *nnueAccumulator, from, to *position) {
	wf := nnueKingBucketMap[(from.pieces[king]&from.colors[white]).lsb()]
	wt := nnueKingBucketMap[(to.pieces[king]&to.colors[white]).lsb()]
	bf := nnueKingBucketMap[(from.pieces[king]&from.colors[black]).lsb()^nnueRankFlip]
	bt := nnueKingBucketMap[(to.pieces[king]&to.colors[black]).lsb()^nnueRankFlip]

	if wf == wt && bf == bt {
		for colorIdx := int(white); colorIdx <= int(black); colorIdx++ {
			vacated := from.colors[colorIdx] &^ to.colors[colorIdx]
			for vacated != 0 {
				s := vacated.popLsb()
				pc := from.squares[s]
				subColumn(&acc.white, n.column(wt, colorIdx, pc, s, false))
				subColumn(&acc.black, n.column(bt, colorIdx, pc, s, true))
			}
			occupied := to.colors[colorIdx] &^ from.colors[colorIdx]
			for occupied != 0 {
				s := occupied.popLsb()
				pc := to.squares[s]
				addColumn(&acc.white, n.column(wt, colorIdx, pc, s, false))
				addColumn(&acc.black, n.column(bt, colorIdx, pc, s, true))
			}
		}
		return
	}
	// Rare: one king crossed a bucket boundary. Refresh that perspective; diff the other.
	if wf != wt {
		n.refreshSide(&acc.white, to, wt, false)
	} else {
		n.diffSide(&acc.white, from, to, wt, false)
	}
	if bf != bt {
		n.refreshSide(&acc.black, to, bt, true)
	} else {
		n.diffSide(&acc.black, from, to, bt, true)
	}
}

// diffSide applies the piece-placement delta from `from` to `to` to a single perspective within one
// unchanged king bucket (used for the non-crossing perspective when the other king crossed a bucket).
func (n *nnueNetwork) diffSide(side *[nnueHiddenSize]int16, from, to *position, bucket int, blackPersp bool) {
	for colorIdx := int(white); colorIdx <= int(black); colorIdx++ {
		vacated := from.colors[colorIdx] &^ to.colors[colorIdx]
		for vacated != 0 {
			s := vacated.popLsb()
			subColumn(side, n.column(bucket, colorIdx, from.squares[s], s, blackPersp))
		}
		occupied := to.colors[colorIdx] &^ from.colors[colorIdx]
		for occupied != 0 {
			s := occupied.popLsb()
			addColumn(side, n.column(bucket, colorIdx, to.squares[s], s, blackPersp))
		}
	}
}

// nnueDequant reduces the raw int64 dot (in QA*QA*QB units) to clamped centipawns.
func nnueDequant(output int64, bias int16) int {
	output /= nnueQA
	output += int64(bias)
	output *= nnueScale
	output /= nnueQA * nnueQB
	if output > nnueMaxEval {
		output = nnueMaxEval
	} else if output < -nnueMaxEval {
		output = -nnueMaxEval
	}
	return int(output)
}

// evaluateExact is the exact int16 SCReLU forward pass — the reference evaluator. Returns the
// score in centipawns from the side-to-move's perspective. The side-to-move accumulator feeds the
// first half of the output layer.
func (n *nnueNetwork) evaluateExact(acc *nnueAccumulator, sideToMove color) int {
	stm, ntm := &acc.white, &acc.black
	if sideToMove == black {
		stm, ntm = &acc.black, &acc.white
	}
	return nnueDequant(outputDot(stm, ntm, n.outputWeights), n.outputBias)
}

// evaluateExactPosition refreshes an accumulator for pos and returns the exact evaluation.
// Diagnostic/reference helper; the search maintains an accumulator incrementally instead.
func (n *nnueNetwork) evaluateExactPosition(pos *position) int {
	var acc nnueAccumulator
	n.refresh(&acc, pos)
	return n.evaluateExact(&acc, pos.colorToMove)
}

// nnueSearchClamp keeps a returned NNUE evaluation strictly inside the search's non-mate band, so a
// large eval can never be mistaken for a forced win (winThreshold) or collide with mate scores.
const nnueSearchClamp = winThreshold - 1000

// clampEvalToBand clamps a raw NNUE score into the search's non-mate band.
func clampEvalToBand(score int) int {
	if score > nnueSearchClamp {
		return nnueSearchClamp
	}
	if score < -nnueSearchClamp {
		return -nnueSearchClamp
	}
	return score
}

// searchEvalAcc evaluates from a maintained accumulator and clamps into the search's eval band.
// This is the hot-path evaluator used by the search.
func (n *nnueNetwork) searchEvalAcc(acc *nnueAccumulator, stm color) int {
	return clampEvalToBand(n.evaluateExact(acc, stm))
}

// searchEval evaluates pos with a full accumulator refresh (used where no maintained accumulator is
// available); the search itself uses searchEvalAcc with the incrementally-maintained accumulator.
func (n *nnueNetwork) searchEval(pos *position) int {
	return clampEvalToBand(n.evaluateExactPosition(pos))
}

var (
	defaultNet     *nnueNetwork
	defaultNetOnce sync.Once
)

// loadEmbeddedNet parses the embedded net once and caches the read-only result for reuse across
// games (e.g. repeated NewGame calls in tests); returns nil if the embedded net fails to load.
func loadEmbeddedNet() *nnueNetwork {
	defaultNetOnce.Do(func() {
		net, err := loadNNUENetwork(embeddedNet)
		if err != nil {
			fmt.Fprintf(os.Stderr, "info string NNUE: %v\n", err)
			return
		}
		defaultNet = net
	})
	return defaultNet
}

// EnableNNUE (re)installs the embedded NNUE network as this game's evaluator, replacing any net loaded
// via EvalFile. NewGame already installs it, so this is only needed to revert to the embedded net.
// Returns an error if the embedded net is unusable.
func (game *Game) EnableNNUE() error {
	net := loadEmbeddedNet()
	if net == nil {
		return fmt.Errorf("nnue: embedded net unavailable")
	}
	game.nnue = net
	game.evalTable.clear() // cached scores are net-dependent
	return nil
}

// LoadNNUEFile loads an external PSN1 net from disk and installs it as this game's evaluator,
// replacing the embedded net. Returns an error (leaving the current evaluator unchanged) if the file
// is missing or incompatible with this build.
func (game *Game) LoadNNUEFile(path string) error {
	net, err := loadNNUENetworkFile(path)
	if err != nil {
		return err
	}
	game.nnue = net
	game.evalTable.clear() // cached scores are net-dependent
	return nil
}
