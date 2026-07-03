// Package nnue implements the engine's NNUE (Efficiently Updatable Neural Network) evaluation,
// ported from the C++ Pawnstar engine (src/nnue.h): the loader for the stamped "PSN1" net format,
// the lazily-updatable feature-transformer accumulator, the exact int16 SCReLU reference forward
// pass (EvaluateExact — the correctness oracle), and the search's int8 fast path (EvaluateInt8),
// with SIMD kernels (amd64 archsimd + AVX2 asm, arm64 NEON asm) selected by build tag.
//
// Architecture (shipped net v12): Chess768 features (2 colours * 6 types * 64 squares) per
// perspective, x nnueKingBuckets king-square weight banks -> an nnueHiddenSize int16 accumulator
// per perspective. Activation is SCReLU (clamp to [0,QA] then square). The output layer dots the
// two perspectives (side-to-move first) and dequantises to centipawns.
//
// The package is decoupled from the engine's board types: positions are passed as a Board — raw
// uint64 occupancy bitboards plus uint8 piece codes — so the engine (which this package must not
// import) converts at the call boundary.
package nnue

import (
	"encoding/binary"
	"fmt"
	"math/bits"
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
	nnueInt8Shift     = 9                               // SCReLU activation >> shift for the int8 output dot (C++ kInt8Shift)
	nnueMaxEval       = 30000                           // eval clamp magnitude (matches C++ kMaxEvaluation)
	nnueRankFlip      = 0x38                            // XOR mask to flip a square's rank (a1<->a8)
	nnueFormatVersion = 1                               // net header format version
	nnueHeaderBytes   = 32                              // stamped NetHeader size

	nnueNumFeatureWeights = nnueFeatureRows * nnueHiddenSize // 6291456
	nnueNumOutputWeights  = 2 * nnueHiddenSize               // 2048
	// Packed payload size: feature_weights + feature_bias + output_weights + output_bias, all int16.
	nnuePayloadInt16 = nnueNumFeatureWeights + nnueHiddenSize + nnueNumOutputWeights + 1
	nnuePayloadBytes = nnuePayloadInt16 * 2

	// Piece type codes in Board.Squares / Board.Pieces indices — identical to the engine's piece
	// constants (and the C++ engine's): 0 = empty, then pawn..king.
	pawnCode = 1
	kingCode = 6
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

// Board is the raw piece-placement view of a chess position that the evaluator consumes. The engine
// builds one at the call boundary from its own position type (a plain value conversion), keeping
// this package free of engine imports. White is colour index 0, black 1; piece codes are 1 (pawn)
// through 6 (king), 0 = empty — matching the engine's constants bit-for-bit.
type Board struct {
	Pieces  [7]uint64 // per-piece-type occupancy; index = piece code (index 0 unused)
	Colors  [2]uint64 // per-colour occupancy
	Squares [64]uint8 // piece code per square, 0 when empty
}

// kingSquare returns the king square of colour c (a legal board always has both kings).
func (b *Board) kingSquare(c int) int {
	return bits.TrailingZeros64(b.Pieces[kingCode] & b.Colors[c])
}

// Accumulator holds both perspective accumulators (each the feature-transformer output for one
// side's king-relative view). Comparable, so tests can check bit-identity with ==.
type Accumulator struct {
	white [nnueHiddenSize]int16
	black [nnueHiddenSize]int16
}

// DiffLanes returns how many white-perspective and black-perspective lanes differ between two
// accumulators — a test/diagnostic helper for pinpointing incremental-update bugs.
func DiffLanes(a, b *Accumulator) (white, black int) {
	for i := 0; i < nnueHiddenSize; i++ {
		if a.white[i] != b.white[i] {
			white++
		}
		if a.black[i] != b.black[i] {
			black++
		}
	}
	return white, black
}

// Network is a loaded, quantised network: read-only after loading and safe to share across threads.
type Network struct {
	featureWeights []int16 // [nnueFeatureRows * nnueHiddenSize], column-major: row*hidden + i; rows bucket-major
	featureBias    []int16 // [nnueHiddenSize]
	outputWeights  []int16 // [2 * nnueHiddenSize]: first hidden weight stm accumulator, next hidden weight the other
	outputBias     int16   // in QA*QB units
	// outputWeightsI8 is outputWeights requantised to int8 range (values in [-127,127], stored int16 for the
	// SIMD kernel): w8 = clamp(round(w / outputWScale), -127, 127). It drives the search's int8 output dot
	// (EvaluateInt8); the int16 outputWeights stay the reference path (EvaluateExact). Mirrors the C++ engine.
	outputWeightsI8 []int16
	// outputWeightsI8Nat is the same int8 weights in NATURAL order as []int8, consumed by the hand-written
	// AVX2 int8 dot (outputDotInt8Asm), which packs activations to uint8 and uses VPMADDUBSW against int8
	// weights (so, unlike the archsimd path, no VPACKSSDW-order regroup — it permutes activations instead).
	outputWeightsI8Nat []int8
	outputWScale       int // divisor mapping the int16 output weights into int8 range (>= 1); 1 when they already fit
	loaded             bool
}

// LoadFile reads a stamped net file from disk and loads it.
func LoadFile(path string) (*Network, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("nnue: cannot read net %q: %w", path, err)
	}
	return Load(data)
}

// Load parses a stamped ("PSN1") net image. Every architecture field is validated against this
// build's constants, so an incompatible or unstamped net is rejected rather than silently misread.
// Trailing bytes after the packed payload (bullet padding) are ignored.
func Load(data []byte) (*Network, error) {
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
	n := &Network{
		featureWeights: make([]int16, nnueNumFeatureWeights),
		featureBias:    make([]int16, nnueHiddenSize),
		outputWeights:  make([]int16, nnueNumOutputWeights),
	}
	off := readInt16Slice(p, 0, n.featureWeights)
	off = readInt16Slice(p, off, n.featureBias)
	off = readInt16Slice(p, off, n.outputWeights)
	n.outputBias = int16(binary.LittleEndian.Uint16(p[off : off+2]))
	n.deriveInt8OutputWeights()
	n.loaded = true
	return n, nil
}

// deriveInt8OutputWeights requantises the int16 output weights to int8 range for the int8 output dot.
// The scale keeps the largest-magnitude weight inside [-127,127]; nets whose weights already fit (the
// shipped one) get scale 1 and a lossless copy. Mirrors the C++ Network::LoadFromMemory tail.
func (n *Network) deriveInt8OutputWeights() {
	maxOW := 0
	for _, v := range n.outputWeights {
		a := int(v)
		if a < 0 {
			a = -a
		}
		if a > maxOW {
			maxOW = a
		}
	}
	n.outputWScale = (maxOW + 126) / 127
	if n.outputWScale < 1 {
		n.outputWScale = 1
	}
	n.outputWeightsI8 = make([]int16, len(n.outputWeights))
	n.outputWeightsI8Nat = make([]int8, len(n.outputWeights))
	for i, v := range n.outputWeights {
		q := divRoundHalfAway(int(v), n.outputWScale) // std::lround(w / scale): ties away from zero
		if q > 127 {
			q = 127
		} else if q < -127 {
			q = -127
		}
		n.outputWeightsI8[i] = int16(q)
		n.outputWeightsI8Nat[i] = int8(q) // natural order for the asm kernel; regrouped below for archsimd
	}
	// Reorder the weights into the lane layout the SIMD output dot consumes (identity for the scalar
	// kernel), so each activation pairs with its own weight without a per-eval permute. See the kernels.
	n.outputWeightsI8 = regroupWeightsForDot(n.outputWeightsI8)
}

// divRoundHalfAway returns round(num/den) with ties rounded away from zero, matching C++ std::lround on
// the exact ratio (num, den are small so there is no floating-point rounding to reproduce). den >= 1.
func divRoundHalfAway(num, den int) int {
	if num >= 0 {
		return (num + den/2) / den
	}
	return -((-num + den/2) / den)
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
func nnueFeatureRow(bucket, colorIdx int, pc uint8, sq int, blackPersp bool) int {
	typeOffset := (int(pc) - pawnCode) * 64
	var base int
	if blackPersp {
		base = (1-colorIdx)*384 + typeOffset + (sq ^ nnueRankFlip)
	} else {
		base = colorIdx*384 + typeOffset + sq
	}
	return bucket*nnueInputSize + base
}

// column returns the feature-transformer weight column (length nnueHiddenSize) for one piece on
// square s in the given perspective's bucket bank.
func (n *Network) column(bucket, colorIdx int, pc uint8, s int, blackPersp bool) []int16 {
	start := nnueFeatureRow(bucket, colorIdx, pc, s, blackPersp) * nnueHiddenSize
	return n.featureWeights[start : start+nnueHiddenSize]
}

// popLsb clears and returns the index of the least-significant set bit of *bb (which must be non-zero).
func popLsb(bb *uint64) int {
	s := bits.TrailingZeros64(*bb)
	*bb &= *bb - 1
	return s
}

// addColumn and subColumn (accumulator column add/sub) and outputDot (the SCReLU output dot) are the
// hot kernels; they have SIMD (archsimd / hand-written asm) and scalar implementations selected by
// build tag in kernels_*.go / dot_*.go. All are bit-identical.

// refreshSide rebuilds one perspective accumulator from scratch: seed the bias, then add the
// feature column of every piece, oriented for that perspective's king bucket.
func (n *Network) refreshSide(side *[nnueHiddenSize]int16, b *Board, bucket int, blackPersp bool) {
	copy(side[:], n.featureBias)
	for colorIdx := 0; colorIdx <= 1; colorIdx++ {
		friendly := b.Colors[colorIdx]
		for pc := uint8(pawnCode); pc <= kingCode; pc++ {
			bb := b.Pieces[pc] & friendly
			for bb != 0 {
				s := popLsb(&bb)
				addColumn(side, n.column(bucket, colorIdx, pc, s, blackPersp))
			}
		}
	}
}

// Refresh rebuilds both perspectives of acc from scratch for board b.
func (n *Network) Refresh(acc *Accumulator, b *Board) {
	n.refreshSide(&acc.white, b, nnueKingBucketMap[b.kingSquare(0)], false)
	n.refreshSide(&acc.black, b, nnueKingBucketMap[b.kingSquare(1)^nnueRankFlip], true)
}

// Update moves acc from matching `from` to matching `to` by applying only the feature deltas for
// squares whose occupancy differs. Reversible — swap from/to to undo — and a no-op when placements
// are identical (a null move). Captures, castling, en passant and promotion are handled uniformly
// via the colour bitboards: for each square a colour vacated, subtract the piece that was there
// (read from `from`); for each it now occupies, add the piece now there (read from `to`). A king
// move that crosses a bucket boundary changes that whole perspective's bank, so it is refreshed.
func (n *Network) Update(acc *Accumulator, from, to *Board) {
	wf := nnueKingBucketMap[from.kingSquare(0)]
	wt := nnueKingBucketMap[to.kingSquare(0)]
	bf := nnueKingBucketMap[from.kingSquare(1)^nnueRankFlip]
	bt := nnueKingBucketMap[to.kingSquare(1)^nnueRankFlip]

	if wf == wt && bf == bt {
		for colorIdx := 0; colorIdx <= 1; colorIdx++ {
			vacated := from.Colors[colorIdx] &^ to.Colors[colorIdx]
			for vacated != 0 {
				s := popLsb(&vacated)
				pc := from.Squares[s]
				subColumn(&acc.white, n.column(wt, colorIdx, pc, s, false))
				subColumn(&acc.black, n.column(bt, colorIdx, pc, s, true))
			}
			occupied := to.Colors[colorIdx] &^ from.Colors[colorIdx]
			for occupied != 0 {
				s := popLsb(&occupied)
				pc := to.Squares[s]
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
func (n *Network) diffSide(side *[nnueHiddenSize]int16, from, to *Board, bucket int, blackPersp bool) {
	for colorIdx := 0; colorIdx <= 1; colorIdx++ {
		vacated := from.Colors[colorIdx] &^ to.Colors[colorIdx]
		for vacated != 0 {
			s := popLsb(&vacated)
			subColumn(side, n.column(bucket, colorIdx, from.Squares[s], s, blackPersp))
		}
		occupied := to.Colors[colorIdx] &^ from.Colors[colorIdx]
		for occupied != 0 {
			s := popLsb(&occupied)
			addColumn(side, n.column(bucket, colorIdx, to.Squares[s], s, blackPersp))
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

// EvaluateExact is the exact int16 SCReLU forward pass — the reference evaluator. Returns the score
// in centipawns from the side-to-move's perspective (stm is the colour index, 0 white / 1 black).
// The side-to-move accumulator feeds the first half of the output layer.
func (n *Network) EvaluateExact(acc *Accumulator, stm int) int {
	s, o := &acc.white, &acc.black
	if stm == 1 {
		s, o = &acc.black, &acc.white
	}
	return nnueDequant(outputDot(s, o, n.outputWeights), n.outputBias)
}

// EvaluateInt8 is the int8 output-layer forward pass — the search's hot-path evaluator (mirrors the
// C++ Network::Evaluate). Activations are requantised to uint8 (SCReLU >> nnueInt8Shift) and the
// output weights to int8; the dot then scales back by outputWScale * 2^shift before dequant. Faster
// than the exact int16 dot at a small (~<=40 cp) deviation from EvaluateExact, which the search
// tolerates. stm is the colour index (0 white / 1 black).
func (n *Network) EvaluateInt8(acc *Accumulator, stm int) int {
	s, o := &acc.white, &acc.black
	if stm == 1 {
		s, o = &acc.black, &acc.white
	}
	dot := n.int8OutputDot(s, o)
	output := dot * int64(n.outputWScale) * (1 << nnueInt8Shift)
	return nnueDequant(output, n.outputBias)
}

var (
	defaultNet     *Network
	defaultNetOnce sync.Once
)

// Embedded parses the embedded net once and caches the read-only result for reuse across games
// (e.g. repeated NewGame calls in tests); returns nil if the embedded net fails to load.
func Embedded() *Network {
	defaultNetOnce.Do(func() {
		net, err := Load(embeddedNet)
		if err != nil {
			fmt.Fprintf(os.Stderr, "info string NNUE: %v\n", err)
			return
		}
		defaultNet = net
	})
	return defaultNet
}
