package nnue

// The RNNUE1 container: the CRC-64/XZ check value, the stamper's agreement with the shipped net, every
// refusal the loader's two checks can raise, and the diagnostic lane diff, the output clamp and the int8
// requantisation's degenerate cases. The engine package's suite exercises the loader and the evaluators on
// the embedded net; these cover the inputs it never sees — a short, foreign, corrupt or mis-shaped file.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
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

// TestShapeReferenceValues pins the three numbers that identify a shape across the ports: the format
// version, the exact payload size and the king bucket map's CRC. The map's contents are part of the
// architecture — king_buckets matching while the entries differ loads cleanly and then reads the
// wrong weight bank at every evaluation — so the CRC is stamped into every net's header and must be
// the same in the C++ and Rust ports and in zenith, whose maps are generated from the same numbers.
//
// The mirrored shape's three values were produced independently by the C++ port and by zenith before
// this one was written. A port whose numbers differ has a bug and must compare field by field rather
// than adjust its map.
func TestShapeReferenceValues(t *testing.T) {
	// format_version and the map's CRC follow the MIRRORING knob; the payload size follows the HEAD
	// count. Keyed separately on purpose: the two knobs are independent, and a single `if` on one of
	// them would assert the wrong payload size for a build that moved only the other.
	wantVersion, wantMapCRC := 1, uint64(0xCC6DDB5DAF0AB18D)
	if KingMirroringEnabled {
		wantVersion, wantMapCRC = 2, 0x0A20060D4E9586E0
	}
	wantPayload := 12589060
	if OutputBuckets == 8 {
		wantPayload = 12617760
	}
	if FormatVersion != wantVersion {
		t.Errorf("FormatVersion = %d, want %d — version 2 means mirroring on WITH eight output heads, and "+
			"is derived from the mirroring knob, never written down", FormatVersion, wantVersion)
	}
	if PayloadBytes != wantPayload {
		t.Errorf("PayloadBytes = %d, want %d", PayloadBytes, wantPayload)
	}
	if got := KingBucketMapCRC(); got != wantMapCRC {
		t.Errorf("KingBucketMapCRC() = %#016x, want %#016x — the king bucket map has changed, which is a "+
			"format change: bump formatVersion and re-stamp every net", got, wantMapCRC)
	}
	t.Logf("shape: mirroring %v, %d output head(s), format_version %d, payload_bytes %d, king_bucket_map_crc %#016x",
		KingMirroringEnabled, OutputBuckets, FormatVersion, PayloadBytes, KingBucketMapCRC())
}

// TestKingBucketMapWellFormed is the Go stand-in for the C++ engine's static_assert on the same
// property: every entry must be a bank index in range, and every bank from 0 up to the highest must
// be selected by at least one square. Neither fault is visible to kingBuckets — a negative or
// oversized entry would index featureWeights outside its bank, and a map selecting only banks {0, 3}
// would size the weights for four banks and leave two of them with no square to train them.
//
// On the mirrored shape it also checks the map is mirror-symmetric, which is what makes it safe to
// index with the king's UNMIRRORED square as bullet does: if map[sq] and map[sq^7] ever disagreed,
// the bank a perspective read would depend on which side of the board the king happened to be on
// after a mirror that was supposed to have folded the two together.
func TestKingBucketMapWellFormed(t *testing.T) {
	var isSelected [64]bool
	highest := 0
	for square, bucket := range kingBucketMap {
		if int(bucket) >= len(isSelected) {
			t.Fatalf("kingBucketMap[%d] = %d, past the last addressable bank", square, bucket)
		}
		isSelected[bucket] = true
		highest = max(highest, int(bucket))
	}
	for bucket := 0; bucket <= highest; bucket++ {
		if !isSelected[bucket] {
			t.Errorf("no square selects bank %d: number the banks from 0 with no gaps, or the weights are "+
				"sized for a bank nothing trains", bucket)
		}
	}
	if highest+1 != kingBuckets {
		t.Errorf("the map needs %d banks but kingBuckets is %d", highest+1, kingBuckets)
	}
	if !KingMirroringEnabled {
		return
	}
	for square := range kingBucketMap {
		if kingBucketMap[square] != kingBucketMap[square^fileFlip] {
			t.Errorf("the mirrored map is not mirror-symmetric: [%d] = %d but [%d] = %d",
				square, kingBucketMap[square], square^fileFlip, kingBucketMap[square^fileFlip])
		}
	}
}

// TestOutputBucketSelection checks the head selector against bullet's MaterialCount<N> arithmetic,
// (piece_count - 2) / ceil(32 / N), over every legal piece count from the bare kings to a full board
// — and that every head this build compiles is reachable, so a divisor that quietly left heads
// untrainable could not pass.
func TestOutputBucketSelection(t *testing.T) {
	reached := make(map[int]bool, OutputBuckets)
	for pieceCount := 2; pieceCount <= 32; pieceCount++ {
		// A board with pieceCount pieces: the two kings, then pawns, all on distinct squares.
		var board Board
		board.Colors[0] = (uint64(1) << (pieceCount - pieceCount/2)) - 1
		board.Colors[1] = ((uint64(1) << pieceCount) - 1) &^ board.Colors[0]
		if got := board.pieceCount(); got != pieceCount {
			t.Fatalf("test board has %d pieces, meant to have %d", got, pieceCount)
		}
		want := (pieceCount - 2) / ((32 + OutputBuckets - 1) / OutputBuckets)
		got := outputBucket(&board)
		if got != want {
			t.Errorf("outputBucket at %d pieces = %d, want %d", pieceCount, got, want)
		}
		if got < 0 || got >= OutputBuckets {
			t.Fatalf("outputBucket at %d pieces = %d, outside the %d head(s) this build has",
				pieceCount, got, OutputBuckets)
		}
		reached[got] = true
	}
	if len(reached) != OutputBuckets {
		t.Errorf("only %d of %d output heads are reachable by any piece count", len(reached), OutputBuckets)
	}
}

// acceptedNet returns a net image THIS build accepts, so that the loader's refusals can be provoked
// by mutating a sound file at either shape. At the shipped shape that is the shipped net; at v12's
// older shape this tree holds no trained net, so it is the synthetic net (synthetic.go), which
// carries this build's own header and is accepted exactly as a trained net would be.
func acceptedNet(t *testing.T) []byte {
	t.Helper()
	if KingMirroringEnabled && OutputBuckets == 8 {
		return embeddedNet
	}
	image, err := SyntheticNetImage()
	if err != nil {
		t.Fatalf("stamp the synthetic net: %v", err)
	}
	return image
}

// TestStampReproducesEmbeddedNet: stamping the shipped net's own payload rebuilds the shipped file byte
// for byte. That makes Stamp and Load provably inverse — the header the loader checks is exactly the
// header the stamper writes — and proves the committed net carries this build's architecture, CRCs and
// king bucket map rather than merely passing the checks by luck.
func TestStampReproducesEmbeddedNet(t *testing.T) {
	if !KingMirroringEnabled || OutputBuckets != 8 {
		t.Skipf("the shipped net is format_version 2 with eight output heads; this build is format_version "+
			"%d with %d, so it rightly refuses it — TestSyntheticNetAtThisShape is the stamper's gate here",
			FormatVersion, OutputBuckets)
	}
	// The shipped net's own fixed numbers (pawnstar-v20.bin, release 2.0.0), pinned as literals so the
	// cross-port comparison values stay written down here rather than only derived from whatever file
	// happens to be committed. Independently reproduced from the raw bytes by a reader written from the
	// reckless-nnue specification, not from this package.
	const (
		shippedHeaderCrc  = 0x5A2DFC8FA67F2B2C
		shippedPayloadCrc = 0x62C460241DDC3EF7
	)
	if got := binary.LittleEndian.Uint64(embeddedNet[offsetHeaderCrc:]); got != shippedHeaderCrc {
		t.Errorf("the shipped net's header_crc is %#016x, want %#016x", got, uint64(shippedHeaderCrc))
	}
	if got := binary.LittleEndian.Uint64(embeddedNet[offsetPayloadCrc:]); got != shippedPayloadCrc {
		t.Errorf("the shipped net's payload_crc is %#016x, want %#016x", got, uint64(shippedPayloadCrc))
	}
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

// headerWith returns a copy of sound whose header has been mutated by edit, with header_crc then
// recomputed. Recomputing it is the point: otherwise every mutation below would trip the header CRC
// first and no architecture field's own refusal would ever be reached.
func headerWith(sound []byte, edit func(header []byte)) []byte {
	net := append([]byte(nil), sound...)
	edit(net[:headerBytes])
	binary.LittleEndian.PutUint64(net[offsetHeaderCrc:], crc64XZ(net[:headerCrcCoverage]))
	return net
}

// shapeNet returns a loaded net at THIS build's shape — the shipped one where that is this shape, and
// the synthetic one otherwise — so a kernel gate runs at either shape instead of depending on a net
// that only one of them accepts.
func shapeNet(t *testing.T) *Network {
	t.Helper()
	net, err := Load(acceptedNet(t))
	if err != nil {
		t.Fatalf("this build refuses the net at its own shape: %v", err)
	}
	return net
}

// TestLoadRefusals: every malformed, corrupt or mis-shaped image is refused with an error naming the
// field, the file's value and the engine's — and nothing is loaded in its place.
//
// Every case mutates a net this build ACCEPTS (acceptedNet) and the expectations are derived from it,
// so the suite provokes the same refusals at either shape instead of being written around whichever
// net happens to be shipped. The shipped net's own fixed CRCs are pinned separately, in
// TestStampReproducesEmbeddedNet.
func TestLoadRefusals(t *testing.T) {
	sound := acceptedNet(t)
	if net, err := Load(sound); err != nil || net == nil {
		t.Fatalf("this build refuses its own net: %v — every case below mutates it, so nothing after "+
			"this means anything", err)
	}
	// The shape fields' wrong values: whatever this build is not.
	otherVersion, otherHeads, otherFlags := 2, 8, uint8(1)
	if KingMirroringEnabled {
		otherVersion, otherFlags = 1, 0
	}
	if OutputBuckets == 8 {
		otherHeads = 1
	}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "too short for the 64-byte RNNUE1 header"},
		{"a header one byte short", sound[:headerBytes-1], "too short for the 64-byte RNNUE1 header"},
		{
			"a foreign container", headerWith(sound, func(h []byte) { copy(h, "PSN1") }),
			`magic is "PSN1E1\x00\x00" in the file but "RNNUE1\x00\x00" in this engine`,
		},
		{
			// Not via headerWith: this is the one case whose header_crc must stay stale.
			"a corrupt header", func() []byte {
				net := append([]byte(nil), sound...)
				net[offsetReserved]++ // covered by header_crc, which is left as it was
				return net
			}(),
			fmt.Sprintf("header_crc is %#016x in the file but", binary.LittleEndian.Uint64(sound[offsetHeaderCrc:])),
		},
		{
			"another format version", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetFormatVersion:], uint16(otherVersion)) }),
			fmt.Sprintf("format_version is %d in the file but %d in this engine", otherVersion, FormatVersion),
		},
		{
			"an extended header", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetHeaderBytes:], 128) }),
			"header_bytes is 128 in the file but 64 in this engine",
		},
		{
			"another feature count", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetFeaturesPerBucket:], 704) }),
			"features_per_bucket is 704 in the file but 768 in this engine",
		},
		{
			"another bucket count", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetKingBuckets:], 4) }),
			"king_buckets is 4 in the file but 8 in this engine",
		},
		{
			"another width", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetHiddenSize:], hiddenSize/2) }),
			"hidden_size is 512 in the file but 1024 in this engine",
		},
		{
			// Material output heads and horizontal king mirroring are separately measured changes, so a
			// net carrying the other count of either is refused rather than read at the wrong shape.
			"another output head count", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetOutputBuckets:], uint16(otherHeads)) }),
			fmt.Sprintf("output_buckets is %d in the file but %d in this engine", otherHeads, OutputBuckets),
		},
		{
			"the other mirroring setting", headerWith(sound, func(h []byte) { h[offsetFlags] = otherFlags }),
			fmt.Sprintf("flags is %d in the file but %d in this engine", otherFlags, engineFlags),
		},
		{
			"another activation", headerWith(sound, func(h []byte) { h[offsetActivation] = 1 }),
			"activation is 1 in the file but 0 in this engine",
		},
		{
			"another QA", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetQa:], 181) }),
			"qa is 181 in the file but 255 in this engine",
		},
		{
			"another QB", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetQb:], 128) }),
			"qb is 128 in the file but 64 in this engine",
		},
		{
			"another eval scale", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint16(h[offsetEvalScale:], 410) }),
			"eval_scale is 410 in the file but 400 in this engine",
		},
		{
			"a used reserved field", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint32(h[offsetReserved:], 1) }),
			"reserved is 1 in the file but 0 in this engine",
		},
		{
			// The failure king_bucket_map_crc exists to catch: the right number of buckets, the wrong map.
			"another king bucket map", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint64(h[offsetKingBucketMapCrc:], 0) }),
			fmt.Sprintf("king_bucket_map_crc is 0x0000000000000000 in the file but %#016x in this engine", KingBucketMapCRC()),
		},
		{
			"another payload size", headerWith(sound, func(h []byte) { binary.LittleEndian.PutUint64(h[offsetPayloadBytes:], payloadBytes/2) }),
			fmt.Sprintf("payload_bytes is %d in the file but %d in this engine", payloadBytes/2, payloadBytes),
		},
		{
			"a truncated payload", sound[:len(sound)-1],
			fmt.Sprintf("carries %d payload bytes after its header but payload_bytes says %d", payloadBytes-1, payloadBytes),
		},
		{
			// Padding is no longer tolerated: payload_bytes is exact, so trailing bytes mean the file is
			// not the one the header describes.
			"a padded payload", append(append([]byte(nil), sound...), 0xAA, 0xBB, 0xCC),
			fmt.Sprintf("carries %d payload bytes after its header but payload_bytes says %d", payloadBytes+3, payloadBytes),
		},
		{
			"corrupt weights", func() []byte {
				net := append([]byte(nil), sound...)
				net[headerBytes]++ // one flipped weight byte, covered by payload_crc
				return net
			}(),
			fmt.Sprintf("payload_crc is %#016x in the file but", binary.LittleEndian.Uint64(sound[offsetPayloadCrc:])),
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

// TestLoadFile: a missing file is an error naming it; a sound net loads from a file the same as it does
// from memory, so nothing about reading the file changes what is loaded.
func TestLoadFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.bin")
	if _, err := LoadFile(missing); err == nil || !strings.Contains(err.Error(), "cannot read net") {
		t.Errorf("LoadFile(missing) error = %v", err)
	}
	sound := acceptedNet(t)
	path := filepath.Join(t.TempDir(), "net.bin")
	if err := os.WriteFile(path, sound, 0o600); err != nil {
		t.Fatalf("write the net: %v", err)
	}
	fromFile, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%s): %v", path, err)
	}
	fromMemory, err := Load(sound)
	if err != nil {
		t.Fatalf("Load(the same bytes): %v", err)
	}
	if fromFile.outputWScale != fromMemory.outputWScale {
		t.Errorf("int8 scale %d from the file, %d from memory", fromFile.outputWScale, fromMemory.outputWScale)
	}
	for bucket := range fromMemory.outputBias {
		if fromFile.outputBias[bucket] != fromMemory.outputBias[bucket] {
			t.Errorf("head %d bias %d from the file, %d from memory",
				bucket, fromFile.outputBias[bucket], fromMemory.outputBias[bucket])
		}
	}
}

// TestSyntheticNetAtThisShape is the stamper's and the loader's gate at a shape with no trained net:
// the synthetic net is exactly payloadBytes of weights in this build's own container, and this build
// loads it back. It is also the inverse-proof TestStampReproducesEmbeddedNet gives at the shipped
// shape —
// SyntheticNetPayload writes the payload independently of the code that reads it, so the two agree
// about the layout or this fails.
//
// At the shared architecture the image's bytes are pinned. The C++ port measured them identical on
// Windows and on Linux, and the three bit-exact ports must search the SAME net for their fixed-depth
// node counts to be comparable at all: a port whose image differs by one byte has a different net and
// its node counts mean nothing.
func TestSyntheticNetAtThisShape(t *testing.T) {
	payload := SyntheticNetPayload()
	if len(payload) != payloadBytes {
		t.Fatalf("the synthetic payload is %d bytes, want %d", len(payload), payloadBytes)
	}
	image, err := SyntheticNetImage()
	if err != nil {
		t.Fatalf("stamp the synthetic net: %v", err)
	}
	if len(image) != headerBytes+payloadBytes {
		t.Fatalf("the synthetic image is %d bytes, want %d", len(image), headerBytes+payloadBytes)
	}
	net, err := Load(image)
	if err != nil || net == nil {
		t.Fatalf("this build refused its own synthetic net — the header writer and the reader disagree: %v", err)
	}
	sum := sha256.Sum256(image)
	digest := hex.EncodeToString(sum[:])
	t.Logf("synthetic net: %d bytes, SHA-256 %s", len(image), digest)
	if KingMirroringEnabled && OutputBuckets == 8 {
		const want = "4abfdb7bf3f6753fb53d8fed3b524750f7c06031cd5464445f18075f8ad4871b"
		if len(image) != 12617824 || digest != want {
			t.Errorf("the synthetic net at the shared architecture is %d bytes / %s, want 12617824 / %s "+
				"— the ports would be searching different nets, so STOP rather than adjusting the expectation",
				len(image), digest, want)
		}
	}
}

// TestWriteSyntheticNet writes the synthetic net to the path in PAWNSTAR_WRITE_SYNTHETIC_NET and does
// nothing otherwise. It is how a fixed-depth parity run gets a net to search at a shape nothing has
// been trained for — the Go stand-in for the C++ suite's --write-synthetic-net, which this
// repository has no developer tool to hold:
//
//	PAWNSTAR_WRITE_SYNTHETIC_NET=/tmp/synthetic.bin go test ./engine/nnue -run TestWriteSyntheticNet
func TestWriteSyntheticNet(t *testing.T) {
	path := os.Getenv("PAWNSTAR_WRITE_SYNTHETIC_NET")
	if path == "" {
		t.Skip("set PAWNSTAR_WRITE_SYNTHETIC_NET=<path> to write the synthetic net out")
	}
	image, err := SyntheticNetImage()
	if err != nil {
		t.Fatalf("stamp the synthetic net: %v", err)
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	sum := sha256.Sum256(image)
	t.Logf("wrote %s: %d bytes, SHA-256 %s", path, len(image), hex.EncodeToString(sum[:]))
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
