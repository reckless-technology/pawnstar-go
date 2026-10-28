package nnue

import "encoding/binary"

// The synthetic net: a complete RNNUE1 image at whatever shape THIS build was compiled for, built
// from a fixed seed through fixed integer arithmetic so that its bytes are identical on every
// platform and in every run.
//
// It exists because a shape with no trained net otherwise has nothing to test with and no node count
// to compare. Nothing has been trained at the shared architecture yet, and a build at that shape
// rightly refuses the shipped v12 net (its format_version is 1 where this build's is 2), so the
// accumulator gates and the search would both have had to be skipped exactly where the new code is.
// Weight *quality* is irrelevant to what they check: that an incrementally-updated accumulator
// equals a from-scratch rebuild holds for any weights at all, and a fixed-depth node count is a
// parity signature whatever the weights are, as long as every port uses the same ones.
//
// The engine's own Stamp writes the header, so the image carries this build's architecture fields,
// bucket-map CRC and both checksums, and Load accepts it exactly as it would a trained net. Writing
// the payload here by hand is also a second opinion on the payload layout Load reads — an
// independent writer against the reader.
//
// The generator is byte-for-byte the C++ reference's SyntheticNetImage (test/nnue_incremental_test.cpp,
// `--write-synthetic-net`), because the three bit-exact ports have to search the SAME net to compare
// node counts. At the shared architecture the image is 12,617,824 bytes with
// SHA-256 4abfdb7bf3f6753fb53d8fed3b524750f7c06031cd5464445f18075f8ad4871b, verified identical
// across Windows and Linux by the C++ port; TestSyntheticNetIsStableAcrossPorts pins it here.
const (
	// syntheticSeed is the xorshift64* state the generator starts from (the golden-ratio constant).
	syntheticSeed = 0x9E3779B97F4A7C15
	// syntheticMultiplier is xorshift64*'s output multiplier.
	syntheticMultiplier = 0x2545F4914F6CDD1D
)

// syntheticSource is the xorshift64* generator the synthetic net's weights come from. It is written
// out here rather than taken from math/rand so that the sequence is fixed by this source alone and
// cannot move with a standard-library change — the C++, Rust and Go ports must produce the same
// bytes, so the generator is part of the agreement.
type syntheticSource struct {
	state uint64
}

// next advances the state and returns the next value.
func (s *syntheticSource) next() uint64 {
	s.state ^= s.state >> 12
	s.state ^= s.state << 25
	s.state ^= s.state >> 27
	return s.state * syntheticMultiplier
}

// SyntheticNetPayload builds the synthetic net's weight payload: exactly payloadBytes of
// feature_weights, feature_bias, output_weights and output_bias in the container's order, at this
// build's shape.
func SyntheticNetPayload() []byte {
	payload := make([]byte, payloadBytes)
	source := &syntheticSource{state: syntheticSeed}
	offset := 0
	writeInt16 := func(value int16) {
		binary.LittleEndian.PutUint16(payload[offset:], uint16(value))
		offset += 2
	}

	// feature_weights, then feature_bias: small enough that an accumulator of up to 32 columns stays
	// well inside int16, so the synthetic net exercises the same arithmetic a trained one does.
	for i := 0; i < numFeatureWeights+hiddenSize; i++ {
		writeInt16(int16(int(source.next()>>56) - 128))
	}
	// output_weights: shifted one up from the feature weights' range so that -128, which has no
	// positive int8 counterpart, never appears and the int8 requantisation stays essentially lossless
	// — which keeps the accumulator gates about the accumulator and not about requantisation.
	for i := 0; i < numOutputWeights; i++ {
		writeInt16(int16(int(source.next()>>56) - 128 + 1))
	}
	// output_bias: int32 per output head, in QA*QB units.
	for i := 0; i < outputBuckets; i++ {
		binary.LittleEndian.PutUint32(payload[offset:], uint32(int32(source.next()>>50)-4096))
		offset += 4
	}
	return payload
}

// SyntheticNetImage builds the synthetic net's payload and stamps it into a complete RNNUE1 image at
// this build's shape — the bytes a parity run writes to disk and loads through EvalFile.
func SyntheticNetImage() ([]byte, error) {
	return Stamp(SyntheticNetPayload())
}
