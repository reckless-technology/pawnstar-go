---
name: reference-rnnue1-container-go
description: The RNNUE1 net container in pawnstar-go — Go's hash/crc64 already IS CRC-64/XZ, the fixed CRCs and header bytes of the shipped v12 net, and the refusal rule (loud on both streams, keeping the net already playing)
metadata:
  node_type: memory
  type: reference
---

# The `RNNUE1` container in pawnstar-go

Landed 2026-10-27 on branch `rnnue1-container`. The specification is the **`reckless-nnue` skill** — it
is the authority, and this note only records what is Go-specific or is a fixed number worth not
re-deriving. Implemented at this engine's current shape: `flags` = 0 (horizontal king mirroring
**off**) and `output_buckets` = 1 (**no** material output heads). Those two are later, separately
SPRT-gated changes owed to all three ports at once.

## Go's `hash/crc64` already *is* CRC-64/XZ — do not add an init or a final XOR

This cost the only real time in the whole job, and the skill's own Go hint was wrong about it.

`crc64.MakeTable(crc64.ECMA)` is the reflected table `0xC96C5795D7870F42`, and Go's `update`
complements the running value **on entry and again on return**. So the all-ones init and the all-ones
final XOR CRC-64/XZ calls for are already applied, and the value `Checksum`/`Update` take and return
is the **post-XOR** one — which is exactly what lets a CRC be taken in pieces and compose.

```go
var crcTable = crc64.MakeTable(crc64.ECMA)
func crc64XZ(data []byte) uint64 { return crc64.Checksum(data, crcTable) } // correct
```

| Expression over `"123456789"` | Value |
|---|---|
| `crc64.Checksum(d, t)` — and `crc64.Update(0, t, d)` | `0x995DC9BBDF1939FA` ✅ the catalogue check value |
| `crc64.Update(^uint64(0), t, d) ^ ^uint64(0)` | `0x2B9C7EE4E2780C8A` ❌ double-complemented |
| `crc64.Update(^uint64(0), t, d)` | `0xD463811B1D87F375` ❌ |

`TestCrc64XZCheckValue` asserts the check value and names the wrong number, so nobody has to work out
why their CRC disagrees with the C++ and Rust ports again.

## Fixed numbers for the nets

The three ports must stamp byte-identical files, so these are the cross-port comparison values.

**The shipped net since release 2.0.0 is `pawnstar-v20.bin`** at the mirrored eight-head shape, and
`pawnstar-v12.bin` has been deleted from the tree (history untouched). v20's values:

| | |
|---|---|
| `format_version` / `flags` / `output_buckets` | 2 / 1 / 8 |
| `king_bucket_map_crc` | `0x0A20060D4E9586E0` — the mirrored `min(file, 7 - file)` map |
| `payload_bytes` | `12617760` = 12,582,912 + 2,048 + 32,768 + **32** |
| `payload_crc` | `0x62C460241DDC3EF7` |
| `header_crc` | `0x5A2DFC8FA67F2B2C` |
| file size | `12617824` (64 + payload) |
| SHA-256 | `c8a29b328849cd1ea60c65f5d3dea84f585f404037ec026c44a94e1174b1cc34` |

Independently decoded from the raw bytes by a Python reader written from the `reckless-nnue`
specification, which also reproduced the v12 values below, so the two sets are on one footing.

The retired **v12** net's values, kept because another port or an old file may still be compared
against them:

| | |
|---|---|
| `king_bucket_map_crc` | `0xCC6DDB5DAF0AB18D` — the `file/2` map, byte-identical in C++ (`kKingBucketMap`) and Rust (`KING_BUCKET_MAP`) |
| `payload_bytes` | `12589060` = 12,582,912 + 2,048 + 4,096 + **4** |
| `payload_crc` | `0x8A91EC366E6B450B` |
| `header_crc` | `0x19EE0DC79A04735D` |
| file size | `12589124` (64 + payload) — was 12,589,152 under `PSN1`, which had 62 bytes of bullet padding |
| `output_bias` | 1208, and it is now **`int32`**, not `int16` — the one payload change from `PSN1` |

Header bytes, for a direct comparison against another port's output:

```
52 4e 4e 55 45 31 00 00  01 00 40 00 00 03 08 00
00 04 01 00 00 00 ff 00  40 00 90 01 00 00 00 00
8d b1 0a af 5d db 6d cc  04 18 c0 00 00 00 00 00
0b 45 6b 6e 36 ec 91 8a  5d 73 04 9a c7 0d ee 19
```

`Stamp` is `Load`'s exact inverse and writes every field from its **own** compiled constants and map,
so `TestStampReproducesEmbeddedNet` rebuilds the committed file byte for byte — which is the proof
that the shipped net really carries this build's architecture rather than merely passing the checks.
There is no separate stamping tool in this repo (and no Go developer tool at all, by design — the
coverage gate is `./...` whole); `Stamp` plus that test is the whole of it.

## A refused net is never substituted for — and this rule has now moved twice

**The rule as it stands (2026-10-28, branch `evalfile-refusal-is-loud`, all four engines together).**
`LoadNNUEFile` changes **nothing** on a refusal: the net already installed stays installed and goes on
playing, and `networkOrigin` still names it. `handleUciSetOption` prints **two** lines on **both**
streams — the loader's refusal, then

```
info string NNUE: keeping the net already loaded ('<kept net origin>'); '<refused path>' was refused and is not in play
```

(bare, without the `info string ` prefix, on stderr). The wording is **fixed and shared with
`pawnstar`, `pawnstar-rs` and `zenith`**; do not reword it in one port. `networkOrigin` on `Game` is
what makes naming the kept net possible at all: the path, or `embedded`.

**Why it moved.** Installing `nil` closed trap 1 (a silent fallback) but opened the mirror of it: the
engine then had **no defined play**, answering `bestmove 0000` to every later `go`. An operator who
missed one message got a thousand games of nothing rather than a thousand games of the previous net,
and "which net actually played?" was still unanswered. Keeping the net already playing **and saying
so, loudly, on a stream no harness drops** satisfies the skill's rule — a refused net is never put
into play — while leaving the engine in a state that has a defined meaning.

**`searchRootNode`'s no-net guard stays**, as a *defensive* path: it is reachable only when even the
embedded net is unusable, i.e. a corrupt build, which is `EnableNNUE`'s failure and the one case that
really does leave no evaluator. Its message is now
`info string NNUE: no net installed (not even the embedded one) — refusing to search`; it no longer
blames a refused `EvalFile`, which cannot cause it.

**The tests, each time.** `TestLoadNNUEFileErrors` once asserted a bad `EvalFile` "must leave the
current evaluator intact"; 2026-10-27 reversed that into
`TestLoadNNUEFileRefusesWithoutFallingBack` (expecting `game.network == nil`); 2026-10-28 made it
`TestLoadNNUEFileRefusesWithoutSubstituting`, which asserts the **stronger** property rather than
simply reverting: the net and its origin are unchanged, `keptNetMessage` names that net, the engine
still searches with it, **and** the no-net guard still refuses loudly when that state is forced
directly. `engine/uci_inprocess_test.go` checks the same through `HandleCommand` on both streams
(`captureStderr` / `runUCIStreams`), and `engine/uci_evalfile_test.go` drives the **real binary** —
the only way to show the routing, and that the search afterwards is real (same bestmove, identical
node count: 136,277 at depth 10 from the start position, book off, hash cleared).

**pawnstar-go has no startup `EvalFile` path at all**, unlike the other three: `main()` installs the
embedded net via `NewGame` and reads no `PAWNSTAR_EVALFILE`, so there is no startup fallback and no
`FATAL: ... was read and refused` here to leave alone. (The C++ port's `src/main.cpp` does have that
startup path, and as of 2026-10-27 still had trap 1 in the open — "falling back to the embedded net"
on a load failure.)

## The gate

Nothing here changes evaluation, so `bench` **must not move**: **6,601,454** before and after, and
identical bestmove and node count on all sixteen positions
(`PAWNSTAR_BENCH_DEBUG=1`). A moved bench means the loader is wrong. See
[[project-cross-engine-bit-parity]].
