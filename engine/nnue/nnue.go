// Package nnue implements the engine's NNUE (Efficiently Updatable Neural Network) evaluation,
// ported from the C++ Pawnstar engine (src/nnue.h): the loader for the shared "RNNUE1" net
// container, the lazily-updatable feature-transformer accumulator, the exact int16 SCReLU reference
// forward pass (EvaluateExact — the correctness oracle), and the search's int8 fast path
// (EvaluateInt8), with SIMD kernels (amd64 archsimd + AVX2 asm, arm64 NEON asm) selected by build tag.
//
// RNNUE1 is the one net container every reckless-technology chess engine shares, so a net trained
// once loads in all of them (see the reckless-nnue skill). It replaced the engine-private 32-byte
// "PSN1" header, which described the architecture but carried neither the king bucket map's contents
// nor any integrity check — the two gaps that let a net load cleanly and then evaluate wrongly.
//
// Architecture (shipped net v12): Chess768 features (2 colours * 6 types * 64 squares) per
// perspective, x kingBuckets king-square weight banks -> an hiddenSize int16 accumulator
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
	"hash/crc64"
	"math/bits"
	"os"
	"sync"
)

// The architecture this build is compiled for. Every one of these is written into the header by Stamp
// and checked against the header by Load, so a net of another shape is refused rather than misread.
const (
	featuresPerBucket = 768                             // features per perspective per bucket: 2 colours * 6 types * 64
	kingBuckets       = 8                               // king-square weight banks
	featureRows       = featuresPerBucket * kingBuckets // 6144 (bucket-major)
	hiddenSize        = 1024                            // feature-transformer / accumulator width per perspective
	outputBuckets     = 1                               // material output heads (1 = a single head, i.e. no output bucketing)
	qa                = 255                             // feature-transformer weight/activation scale (SCReLU clamp)
	qb                = 64                              // output-layer weight scale
	evalScale         = 400                             // maps raw network output to centipawns
	activationSCReLU  = 0                               // the activation code this engine implements: clamp(x, 0, QA), squared by the output dot
	engineFlags       = 0                               // bit 0 = horizontal king mirroring, off in this engine; all other bits reserved zero
	int8Shift         = 9                               // SCReLU activation >> shift for the int8 output dot (C++ kInt8Shift)
	maxEval           = 30000                           // eval clamp magnitude (matches C++ kMaxEvaluation)
	rankFlip          = 0x38                            // XOR mask to flip a square's rank (a1<->a8)
	formatVersion     = 1                               // RNNUE1 container format version
	headerBytes       = 64                              // RNNUE1 header size

	numFeatureWeights = featureRows * hiddenSize       // 6291456
	numOutputWeights  = outputBuckets * 2 * hiddenSize // 2048: own perspective's half first, then the opponent's
	// int16PayloadBytes covers feature_weights, feature_bias and output_weights; output_bias follows as
	// int32[outputBuckets], so the payload is not a whole number of int16 values.
	int16PayloadBytes = (numFeatureWeights + hiddenSize + numOutputWeights) * 2
	payloadBytes      = int16PayloadBytes + outputBuckets*4 // 12589060 at this architecture

	// Piece type codes in Board.Squares / Board.Pieces indices — identical to the engine's piece
	// constants (and the C++ engine's): 0 = empty, then pawn..king.
	pawnCode = 1
	kingCode = 6
)

// magic is the RNNUE1 container's eight-byte signature, the first field of every net header.
var magic = [8]byte{'R', 'N', 'N', 'U', 'E', '1', 0, 0}

// crcTable drives CRC-64/XZ (also called CRC-64/GO-ECMA): polynomial 0x42F0E1EBA9EA3693, reflected
// (table constant 0xC96C5795D7870F42), init all ones, reflect in and out, final XOR all ones.
//
// Go's hash/crc64 with crc64.ECMA *is* CRC-64/XZ, with no adjustment: Update complements the running
// value on entry and again on return, so the all-ones init and all-ones final XOR are already
// applied, and the value it takes and returns is the post-XOR one (which is what lets Checksum and
// Update compose for streaming). Adding an init and a final XOR of one's own double-complements
// both: crc64.Update(^uint64(0), crcTable, []byte("123456789")) ^ ^uint64(0) is 0x2B9C7EE4E2780C8A,
// where CRC-64/XZ's mandatory check value for those nine bytes is 0x995DC9BBDF1939FA.
// TestCrc64XZCheckValue asserts the check value, and is the first thing the suite proves.
var crcTable = crc64.MakeTable(crc64.ECMA)

// crc64XZ returns the CRC-64/XZ of data.
func crc64XZ(data []byte) uint64 { return crc64.Checksum(data, crcTable) }

// kingBucketMap maps a king square (in the perspective's own orientation) to its weight bank.
// File-pair (file/2) selects columns 0-3; board half (own ranks 1-4 vs advanced 5-8) adds 4.
// MUST be byte-identical to the map in the C++ engine, the Rust engine and the bullet trainer.
//
// The map's *contents* are part of the architecture, not just its length: a net trained against a
// different map but the same kingBuckets would load cleanly under a header check and then read the
// wrong weight bank at every evaluation. kingBucketMapCRC closes that, and is stamped into every
// net's header — so changing an entry here is a format change, owed a format_version bump and a
// re-stamp of every net.
var kingBucketMap = [64]uint8{
	0, 0, 1, 1, 2, 2, 3, 3,
	0, 0, 1, 1, 2, 2, 3, 3,
	0, 0, 1, 1, 2, 2, 3, 3,
	0, 0, 1, 1, 2, 2, 3, 3,
	4, 4, 5, 5, 6, 6, 7, 7,
	4, 4, 5, 5, 6, 6, 7, 7,
	4, 4, 5, 5, 6, 6, 7, 7,
	4, 4, 5, 5, 6, 6, 7, 7,
}

// kingBucketMapCRC is the CRC-64/XZ of this engine's own king bucket map — its 64 entries, one
// uint8 each, in square order. It is taken over kingBucketMap itself, so there is no second
// representation of the map that could drift away from the one the evaluator indexes.
func kingBucketMapCRC() uint64 { return crc64XZ(kingBucketMap[:]) }

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
	white [hiddenSize]int16
	black [hiddenSize]int16
}

// DiffLanes returns how many white-perspective and black-perspective lanes differ between two
// accumulators — a test/diagnostic helper for pinpointing incremental-update bugs.
func DiffLanes(a, b *Accumulator) (white, black int) {
	for i := 0; i < hiddenSize; i++ {
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
	featureWeights []int16 // [featureRows * hiddenSize], column-major: row*hidden + i; rows bucket-major
	featureBias    []int16 // [hiddenSize]
	outputWeights  []int16 // [2 * hiddenSize]: first hidden weight stm accumulator, next hidden weight the other
	outputBias     int32   // in QA*QB units (int32 in the container, as the shape with output buckets needs)
	// outputWeightsI8 is outputWeights requantised to int8 range (values in [-127,127], stored int16 for the
	// SIMD kernel): w8 = clamp(round(w / outputWScale), -127, 127). It drives the search's int8 output dot
	// (EvaluateInt8); the int16 outputWeights stay the reference path (EvaluateExact). Mirrors the C++ engine.
	outputWeightsI8 []int16
	// outputWeightsI8Nat is the same int8 weights in NATURAL order as []int8, consumed by the hand-written
	// AVX2 int8 dot (outputDotInt8Asm), which packs activations to uint8 and uses VPMADDUBSW against int8
	// weights (so, unlike the archsimd path, no VPACKSSDW-order regroup — it permutes activations instead).
	outputWeightsI8Nat []int8
	outputWScale       int // divisor mapping the int16 output weights into int8 range (>= 1); 1 when they already fit
}

// The RNNUE1 header's field offsets. Little-endian throughout; bytes 56..63 hold the CRC of 0..55,
// so every field above it is covered by the header's own integrity check.
const (
	offsetMagic             = 0  // 8 bytes
	offsetFormatVersion     = 8  // uint16
	offsetHeaderBytes       = 10 // uint16
	offsetFeaturesPerBucket = 12 // uint16
	offsetKingBuckets       = 14 // uint16
	offsetHiddenSize        = 16 // uint16
	offsetOutputBuckets     = 18 // uint16
	offsetFlags             = 20 // uint8
	offsetActivation        = 21 // uint8
	offsetQa                = 22 // uint16
	offsetQb                = 24 // uint16
	offsetEvalScale         = 26 // uint16
	offsetReserved          = 28 // uint32, zero
	offsetKingBucketMapCrc  = 32 // uint64
	offsetPayloadBytes      = 40 // uint64
	offsetPayloadCrc        = 48 // uint64
	offsetHeaderCrc         = 56 // uint64, over bytes 0..55
	headerCrcCoverage       = offsetHeaderCrc
)

// LoadFile reads a stamped net file from disk and loads it.
func LoadFile(path string) (*Network, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("nnue: cannot read net %q: %w", path, err)
	}
	return Load(data)
}

// Load parses an RNNUE1 net image and makes the two checks every engine's loader must make, in this
// order:
//
//  1. Header. The magic, then header_crc over bytes 0..55, then every architecture field against
//     this build's own compile-time constants, then king_bucket_map_crc against the CRC of this
//     engine's own map.
//  2. Payload. payload_bytes against the bytes actually there, then payload_crc over them.
//
// Any mismatch refuses the net, naming the field with both the file's value and the engine's. A
// refused net is NOT loaded and no other net is installed in its place: the caller gets an error and
// nothing else, because a loader that quietly falls back to an embedded net lets a match report a
// plausible Elo for a net that never played a move.
func Load(data []byte) (*Network, error) {
	if len(data) < headerBytes {
		return nil, fmt.Errorf("nnue: net refused: the file is %d bytes, too short for the %d-byte RNNUE1 header",
			len(data), headerBytes)
	}
	if [8]byte(data[offsetMagic:offsetFormatVersion]) != magic {
		return nil, fmt.Errorf("nnue: net refused: magic is %q in the file but %q in this engine (not an RNNUE1 net)",
			data[offsetMagic:offsetFormatVersion], magic[:])
	}
	if storedHeaderCrc := binary.LittleEndian.Uint64(data[offsetHeaderCrc:headerBytes]); storedHeaderCrc != crc64XZ(data[:headerCrcCoverage]) {
		return nil, fmt.Errorf("nnue: net refused: header_crc is %#016x in the file but %#016x over its own bytes 0..%d (corrupt header)",
			storedHeaderCrc, crc64XZ(data[:headerCrcCoverage]), headerCrcCoverage-1)
	}
	if err := validateArchitecture(data); err != nil {
		return nil, err
	}

	// Check 2: the payload. payload_bytes was validated against this architecture's expected size
	// above, so this also catches a truncated or padded file.
	payload := data[headerBytes:]
	if len(payload) != payloadBytes {
		return nil, fmt.Errorf("nnue: net refused: the file carries %d payload bytes after its header but payload_bytes says %d",
			len(payload), payloadBytes)
	}
	if storedPayloadCrc := binary.LittleEndian.Uint64(data[offsetPayloadCrc:offsetHeaderCrc]); storedPayloadCrc != crc64XZ(payload) {
		return nil, fmt.Errorf("nnue: net refused: payload_crc is %#016x in the file but %#016x over its %d payload bytes (corrupt weights)",
			storedPayloadCrc, crc64XZ(payload), len(payload))
	}

	// Payload in bullet's save order: feature_weights, feature_bias, output_weights, output_bias.
	n := &Network{
		featureWeights: make([]int16, numFeatureWeights),
		featureBias:    make([]int16, hiddenSize),
		outputWeights:  make([]int16, numOutputWeights),
	}
	off := readInt16Slice(payload, 0, n.featureWeights)
	off = readInt16Slice(payload, off, n.featureBias)
	off = readInt16Slice(payload, off, n.outputWeights)
	n.outputBias = int32(binary.LittleEndian.Uint32(payload[off : off+4]))
	n.deriveInt8OutputWeights()
	return n, nil
}

// validateArchitecture checks every architecture field of a verified header against this build's own
// compile-time constants, and the file's king_bucket_map_crc against the CRC of this engine's own
// map. The first mismatch refuses the net, naming the field and both values.
func validateArchitecture(header []byte) error {
	for _, field := range []struct {
		name   string
		file   uint64
		engine uint64
		isHex  bool
	}{
		{name: "format_version", file: uint64(binary.LittleEndian.Uint16(header[offsetFormatVersion:offsetHeaderBytes])), engine: formatVersion},
		{name: "header_bytes", file: uint64(binary.LittleEndian.Uint16(header[offsetHeaderBytes:offsetFeaturesPerBucket])), engine: headerBytes},
		{name: "features_per_bucket", file: uint64(binary.LittleEndian.Uint16(header[offsetFeaturesPerBucket:offsetKingBuckets])), engine: featuresPerBucket},
		{name: "king_buckets", file: uint64(binary.LittleEndian.Uint16(header[offsetKingBuckets:offsetHiddenSize])), engine: kingBuckets},
		{name: "hidden_size", file: uint64(binary.LittleEndian.Uint16(header[offsetHiddenSize:offsetOutputBuckets])), engine: hiddenSize},
		{name: "output_buckets", file: uint64(binary.LittleEndian.Uint16(header[offsetOutputBuckets:offsetFlags])), engine: outputBuckets},
		{name: "flags", file: uint64(header[offsetFlags]), engine: engineFlags},
		{name: "activation", file: uint64(header[offsetActivation]), engine: activationSCReLU},
		{name: "qa", file: uint64(binary.LittleEndian.Uint16(header[offsetQa:offsetQb])), engine: qa},
		{name: "qb", file: uint64(binary.LittleEndian.Uint16(header[offsetQb:offsetEvalScale])), engine: qb},
		{name: "eval_scale", file: uint64(binary.LittleEndian.Uint16(header[offsetEvalScale:offsetReserved])), engine: evalScale},
		{name: "reserved", file: uint64(binary.LittleEndian.Uint32(header[offsetReserved:offsetKingBucketMapCrc])), engine: 0},
		{name: "king_bucket_map_crc", file: binary.LittleEndian.Uint64(header[offsetKingBucketMapCrc:offsetPayloadBytes]), engine: kingBucketMapCRC(), isHex: true},
		{name: "payload_bytes", file: binary.LittleEndian.Uint64(header[offsetPayloadBytes:offsetPayloadCrc]), engine: payloadBytes},
	} {
		if field.file == field.engine {
			continue
		}
		if field.isHex {
			return fmt.Errorf("nnue: net refused: %s is %#016x in the file but %#016x in this engine "+
				"(the net was trained against a different king bucket map)", field.name, field.file, field.engine)
		}
		return fmt.Errorf("nnue: net refused: %s is %d in the file but %d in this engine", field.name, field.file, field.engine)
	}
	return nil
}

// Stamp wraps a weight payload in a fresh RNNUE1 container, writing every architecture field, both
// CRCs and the payload's size from THIS build's own compiled constants and map — never from anything
// the caller claims about the payload. That is why a 512-wide net stamped by a 1024-wide build gets a
// 1024-wide header and is then refused by Load: the stamper cannot describe a net it was not built
// for, so the mismatch surfaces at once instead of after a spent training run.
//
// Stamp is the exact inverse of Load, which is what keeps the writer and the reader from drifting:
// TestStampReproducesEmbeddedNet asserts that stamping the embedded net's own payload reproduces the
// shipped file byte for byte.
func Stamp(payload []byte) ([]byte, error) {
	if len(payload) != payloadBytes {
		return nil, fmt.Errorf("nnue: cannot stamp a %d-byte payload; this architecture's payload is %d bytes",
			len(payload), payloadBytes)
	}
	header := make([]byte, headerBytes, headerBytes+payloadBytes)
	copy(header[offsetMagic:], magic[:])
	binary.LittleEndian.PutUint16(header[offsetFormatVersion:], formatVersion)
	binary.LittleEndian.PutUint16(header[offsetHeaderBytes:], headerBytes)
	binary.LittleEndian.PutUint16(header[offsetFeaturesPerBucket:], featuresPerBucket)
	binary.LittleEndian.PutUint16(header[offsetKingBuckets:], kingBuckets)
	binary.LittleEndian.PutUint16(header[offsetHiddenSize:], hiddenSize)
	binary.LittleEndian.PutUint16(header[offsetOutputBuckets:], outputBuckets)
	header[offsetFlags] = engineFlags
	header[offsetActivation] = activationSCReLU
	binary.LittleEndian.PutUint16(header[offsetQa:], qa)
	binary.LittleEndian.PutUint16(header[offsetQb:], qb)
	binary.LittleEndian.PutUint16(header[offsetEvalScale:], evalScale)
	binary.LittleEndian.PutUint32(header[offsetReserved:], 0)
	binary.LittleEndian.PutUint64(header[offsetKingBucketMapCrc:], kingBucketMapCRC())
	binary.LittleEndian.PutUint64(header[offsetPayloadBytes:], payloadBytes)
	binary.LittleEndian.PutUint64(header[offsetPayloadCrc:], crc64XZ(payload))
	// Last, so header_crc covers every field above it.
	binary.LittleEndian.PutUint64(header[offsetHeaderCrc:], crc64XZ(header[:headerCrcCoverage]))
	return append(header, payload...), nil
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

// featureRow is the feature row for a piece in one perspective's bucket bank.
//
//	white perspective: bucket*768 + colour*384 + (type-1)*64 + square
//	black perspective: bucket*768 + (1-colour)*384 + (type-1)*64 + (square ^ rankFlip)
func featureRow(bucket, colorIdx int, pc uint8, sq int, blackPersp bool) int {
	typeOffset := (int(pc) - pawnCode) * 64
	var base int
	if blackPersp {
		base = (1-colorIdx)*384 + typeOffset + (sq ^ rankFlip)
	} else {
		base = colorIdx*384 + typeOffset + sq
	}
	return bucket*featuresPerBucket + base
}

// column returns the feature-transformer weight column (length hiddenSize) for one piece on
// square s in the given perspective's bucket bank.
func (n *Network) column(bucket, colorIdx int, pc uint8, s int, blackPersp bool) []int16 {
	start := featureRow(bucket, colorIdx, pc, s, blackPersp) * hiddenSize
	return n.featureWeights[start : start+hiddenSize]
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
func (n *Network) refreshSide(side *[hiddenSize]int16, b *Board, bucket int, blackPersp bool) {
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
	n.refreshSide(&acc.white, b, int(kingBucketMap[b.kingSquare(0)]), false)
	n.refreshSide(&acc.black, b, int(kingBucketMap[b.kingSquare(1)^rankFlip]), true)
}

// Update moves acc from matching `from` to matching `to` by applying only the feature deltas for
// squares whose occupancy differs. Reversible — swap from/to to undo — and a no-op when placements
// are identical (a null move). Captures, castling, en passant and promotion are handled uniformly
// via the colour bitboards: for each square a colour vacated, subtract the piece that was there
// (read from `from`); for each it now occupies, add the piece now there (read from `to`). A king
// move that crosses a bucket boundary changes that whole perspective's bank, so it is refreshed.
func (n *Network) Update(acc *Accumulator, from, to *Board) {
	wf := int(kingBucketMap[from.kingSquare(0)])
	wt := int(kingBucketMap[to.kingSquare(0)])
	bf := int(kingBucketMap[from.kingSquare(1)^rankFlip])
	bt := int(kingBucketMap[to.kingSquare(1)^rankFlip])

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
func (n *Network) diffSide(side *[hiddenSize]int16, from, to *Board, bucket int, blackPersp bool) {
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

// dequant reduces the raw int64 dot (in QA*QA*QB units) to clamped centipawns.
func dequant(output int64, bias int32) int {
	output /= qa
	output += int64(bias)
	output *= evalScale
	output /= qa * qb
	if output > maxEval {
		output = maxEval
	} else if output < -maxEval {
		output = -maxEval
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
	return dequant(outputDot(s, o, n.outputWeights), n.outputBias)
}

// EvaluateInt8 is the int8 output-layer forward pass — the search's hot-path evaluator (mirrors the
// C++ Network::Evaluate). Activations are requantised to uint8 (SCReLU >> int8Shift) and the
// output weights to int8; the dot then scales back by outputWScale * 2^shift before dequant. Faster
// than the exact int16 dot at a small (~<=40 cp) deviation from EvaluateExact, which the search
// tolerates. stm is the colour index (0 white / 1 black).
func (n *Network) EvaluateInt8(acc *Accumulator, stm int) int {
	s, o := &acc.white, &acc.black
	if stm == 1 {
		s, o = &acc.black, &acc.white
	}
	dot := n.int8OutputDot(s, o)
	output := dot * int64(n.outputWScale) * (1 << int8Shift)
	return dequant(output, n.outputBias)
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
