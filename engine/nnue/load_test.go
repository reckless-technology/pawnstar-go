package nnue

// The RNNUE1 container: the CRC-64/XZ check value, the stamper's agreement with the shipped net, every
// refusal the loader's two checks can raise, and the diagnostic lane diff, the output clamp and the int8
// requantisation's degenerate cases. The engine package's suite exercises the loader and the evaluators on
// the embedded net; these cover the inputs it never sees — a short, foreign, corrupt or mis-shaped file.

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
)

// TestCrc64XZCheckValue asserts CRC-64/XZ's mandatory check value before anything else in this package
// is believed: the CRC of the nine ASCII bytes "123456789" is 0x995DC9BBDF1939FA. Every engine sharing
// the container asserts the same number, so the five implementations cannot disagree about what a net's
// CRC fields mean.
//
// Go's hash/crc64 with crc64.ECMA already applies the all-ones init and all-ones final XOR (see crcTable),
// so crc64.Checksum IS CRC-64/XZ. Applying an init and a final XOR on top double-complements both and
// yields 0x2B9C7EE4E2780C8A — asserted here too, because that wrong number is the one a reader who adds
// them by hand will get, and naming it is faster than re-deriving why.
func TestCrc64XZCheckValue(t *testing.T) {
	const checkValue = 0x995DC9BBDF1939FA
	if got := crc64XZ([]byte("123456789")); got != checkValue {
		t.Fatalf("CRC-64/XZ(%q) = %#016x, want %#016x", "123456789", got, uint64(checkValue))
	}
	// Streaming must compose to the same value, so a CRC taken in pieces matches one taken in one go.
	if got := crc64XZ([]byte("")); got != 0 {
		t.Errorf("CRC-64/XZ of no bytes = %#016x, want 0", got)
	}
}

// TestKingBucketMapCRC pins the CRC of this engine's king bucket map. The map's contents are part of the
// architecture: king_buckets matching while the entries differ loads cleanly and then reads the wrong
// weight bank at every evaluation, so this number is stamped into every net's header and must be the same
// in the C++ and Rust ports, whose maps are byte-identical to this one.
func TestKingBucketMapCRC(t *testing.T) {
	const want = 0xCC6DDB5DAF0AB18D
	if got := kingBucketMapCRC(); got != want {
		t.Errorf("kingBucketMapCRC() = %#016x, want %#016x — the king bucket map has changed, which is a "+
			"format change: bump formatVersion and re-stamp every net", got, uint64(want))
	}
}

// TestStampReproducesEmbeddedNet: stamping the shipped net's own payload rebuilds the shipped file byte
// for byte. That makes Stamp and Load provably inverse — the header the loader checks is exactly the
// header the stamper writes — and proves the committed net carries this build's architecture, CRCs and
// king bucket map rather than merely passing the checks by luck.
func TestStampReproducesEmbeddedNet(t *testing.T) {
	if len(embeddedNet) != headerBytes+payloadBytes {
		t.Fatalf("the embedded net is %d bytes, want %d (a %d-byte header and a %d-byte payload)",
			len(embeddedNet), headerBytes+payloadBytes, headerBytes, payloadBytes)
	}
	stamped, err := Stamp(embeddedNet[headerBytes:])
	if err != nil {
		t.Fatalf("Stamp(the embedded net's payload): %v", err)
	}
	if string(stamped) != string(embeddedNet) {
		for i := range stamped {
			if stamped[i] != embeddedNet[i] {
				t.Fatalf("the stamped net differs from the embedded one at byte %d: %#02x vs %#02x",
					i, stamped[i], embeddedNet[i])
			}
		}
		t.Fatalf("the stamped net is %d bytes, the embedded one %d", len(stamped), len(embeddedNet))
	}
}

// TestStampRefusesAMisShapedPayload: the stamper writes the header from its own constants, so it must
// refuse a payload that is not this architecture's rather than describe a net it is not.
func TestStampRefusesAMisShapedPayload(t *testing.T) {
	for _, size := range []int{0, payloadBytes - 1, payloadBytes + 1} {
		if out, err := Stamp(make([]byte, size)); err == nil || out != nil {
			t.Errorf("Stamp(%d bytes) = (%d bytes, %v), want a refusal", size, len(out), err)
		}
	}
}

// headerWith returns a copy of the embedded net whose header has been mutated by edit, with header_crc
// then recomputed. Recomputing it is the point: otherwise every mutation below would trip the header CRC
// first and no architecture field's own refusal would ever be reached.
func headerWith(edit func(header []byte)) []byte {
	net := append([]byte(nil), embeddedNet...)
	edit(net[:headerBytes])
	binary.LittleEndian.PutUint64(net[offsetHeaderCrc:], crc64XZ(net[:headerCrcCoverage]))
	return net
}

// TestLoadRefusals: every malformed, corrupt or mis-shaped image is refused with an error naming the
// field, the file's value and the engine's — and nothing is loaded in its place.
func TestLoadRefusals(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "too short for the 64-byte RNNUE1 header"},
		{"a header one byte short", embeddedNet[:headerBytes-1], "too short for the 64-byte RNNUE1 header"},
		{
			"a foreign container", headerWith(func(h []byte) { copy(h, "PSN1") }),
			`magic is "PSN1E1\x00\x00" in the file but "RNNUE1\x00\x00" in this engine`,
		},
		{
			// Not via headerWith: this is the one case whose header_crc must stay stale.
			"a corrupt header", func() []byte {
				net := append([]byte(nil), embeddedNet...)
				net[offsetReserved]++ // covered by header_crc, which is left as it was
				return net
			}(),
			"header_crc is 0x19ee0dc79a04735d in the file but",
		},
		{
			"another format version", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetFormatVersion:], 2) }),
			"format_version is 2 in the file but 1 in this engine",
		},
		{
			"an extended header", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetHeaderBytes:], 128) }),
			"header_bytes is 128 in the file but 64 in this engine",
		},
		{
			"another feature count", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetFeaturesPerBucket:], 704) }),
			"features_per_bucket is 704 in the file but 768 in this engine",
		},
		{
			"another bucket count", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetKingBuckets:], 4) }),
			"king_buckets is 4 in the file but 8 in this engine",
		},
		{
			"another width", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetHiddenSize:], hiddenSize/2) }),
			"hidden_size is 512 in the file but 1024 in this engine",
		},
		{
			// Material output buckets are a later, separately measured change; a net carrying them is
			// refused by this build rather than read as if it had one output head.
			"material output buckets", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetOutputBuckets:], 8) }),
			"output_buckets is 8 in the file but 1 in this engine",
		},
		{
			// Likewise horizontal king mirroring, flags bit 0.
			"horizontal king mirroring", headerWith(func(h []byte) { h[offsetFlags] = 1 }),
			"flags is 1 in the file but 0 in this engine",
		},
		{
			"another activation", headerWith(func(h []byte) { h[offsetActivation] = 1 }),
			"activation is 1 in the file but 0 in this engine",
		},
		{
			"another QA", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetQa:], 181) }),
			"qa is 181 in the file but 255 in this engine",
		},
		{
			"another QB", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetQb:], 128) }),
			"qb is 128 in the file but 64 in this engine",
		},
		{
			"another eval scale", headerWith(func(h []byte) { binary.LittleEndian.PutUint16(h[offsetEvalScale:], 410) }),
			"eval_scale is 410 in the file but 400 in this engine",
		},
		{
			"a used reserved field", headerWith(func(h []byte) { binary.LittleEndian.PutUint32(h[offsetReserved:], 1) }),
			"reserved is 1 in the file but 0 in this engine",
		},
		{
			// The failure king_bucket_map_crc exists to catch: the right number of buckets, the wrong map.
			"another king bucket map", headerWith(func(h []byte) { binary.LittleEndian.PutUint64(h[offsetKingBucketMapCrc:], 0) }),
			"king_bucket_map_crc is 0x0000000000000000 in the file but 0xcc6ddb5daf0ab18d in this engine",
		},
		{
			"another payload size", headerWith(func(h []byte) { binary.LittleEndian.PutUint64(h[offsetPayloadBytes:], payloadBytes/2) }),
			"payload_bytes is 6294530 in the file but 12589060 in this engine",
		},
		{
			"a truncated payload", embeddedNet[:len(embeddedNet)-1],
			"carries 12589059 payload bytes after its header but payload_bytes says 12589060",
		},
		{
			// Padding is no longer tolerated: payload_bytes is exact, so trailing bytes mean the file is
			// not the one the header describes.
			"a padded payload", append(append([]byte(nil), embeddedNet...), 0xAA, 0xBB, 0xCC),
			"carries 12589063 payload bytes after its header but payload_bytes says 12589060",
		},
		{
			"corrupt weights", func() []byte {
				net := append([]byte(nil), embeddedNet...)
				net[headerBytes]++ // one flipped weight byte, covered by payload_crc
				return net
			}(),
			"payload_crc is 0x8a91ec366e6b450b in the file but",
		},
	}
	for _, tc := range cases {
		net, err := Load(tc.data)
		if err == nil || net != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Load = (loaded %v, %v), want no net and an error containing %q",
				tc.name, net != nil, err, tc.want)
		}
	}
}

// TestLoadFile: a missing file is an error naming it; the shipped net loads from its file the same as the
// embedded copy.
func TestLoadFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.bin")
	if _, err := LoadFile(missing); err == nil || !strings.Contains(err.Error(), "cannot read net") {
		t.Errorf("LoadFile(missing) error = %v", err)
	}
	net, err := LoadFile("pawnstar-v12.bin")
	if err != nil {
		t.Fatalf("LoadFile(pawnstar-v12.bin): %v", err)
	}
	if want := Embedded(); net.outputBias != want.outputBias || net.outputWScale != want.outputWScale {
		t.Error("the net loaded from its file differs from the embedded one")
	}
}

// TestDiffLanes counts the differing lanes of each perspective independently.
func TestDiffLanes(t *testing.T) {
	var a, b Accumulator
	if w, bl := DiffLanes(&a, &b); w != 0 || bl != 0 {
		t.Errorf("identical accumulators differ in (%d, %d) lanes", w, bl)
	}
	b.white[0], b.white[hiddenSize-1], b.black[7] = 1, -1, 300
	if w, bl := DiffLanes(&a, &b); w != 2 || bl != 1 {
		t.Errorf("DiffLanes = (%d, %d), want (2, 1)", w, bl)
	}
}

// TestDequantClamps: the network's raw output is clamped to +/-maxEval centipawns.
func TestDequantClamps(t *testing.T) {
	const huge = int64(1) << 50
	if got := dequant(huge, 0); got != maxEval {
		t.Errorf("dequant(+huge) = %d, want %d", got, maxEval)
	}
	if got := dequant(-huge, 0); got != -maxEval {
		t.Errorf("dequant(-huge) = %d, want %d", got, -maxEval)
	}
	if got := dequant(0, 0); got != 0 {
		t.Errorf("dequant(0) = %d", got)
	}
}

// TestDeriveInt8OutputWeightsDegenerate: all-zero weights still get a scale of one (never a division by
// zero), and a length that is not a multiple of sixteen keeps its tail in the regrouped layout.
func TestDeriveInt8OutputWeightsDegenerate(t *testing.T) {
	n := &Network{outputWeights: make([]int16, 17)}
	n.deriveInt8OutputWeights()
	if n.outputWScale != 1 {
		t.Errorf("all-zero weights: scale %d, want 1", n.outputWScale)
	}
	n.outputWeights[16] = 381 // scale (381 + 126) / 127 = 3, so 127 after requantising
	n.deriveInt8OutputWeights()
	if n.outputWScale != 3 || n.outputWeightsI8[16] != 127 || n.outputWeightsI8Nat[16] != 127 {
		t.Errorf("scale %d, tail %d / %d; want 3, 127 / 127", n.outputWScale, n.outputWeightsI8[16], n.outputWeightsI8Nat[16])
	}
}
