---
name: reference-rnnue1-container-go
description: The RNNUE1 net container in pawnstar-go — Go's hash/crc64 already IS CRC-64/XZ, the fixed CRCs and header bytes of the shipped v12 net, and the no-fallback refusal rule
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

## Fixed numbers for the shipped v12 net

The three ports must stamp byte-identical files, so these are the cross-port comparison values.

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

## A refused net does not fall back — and that changed two tests

`LoadNNUEFile` installs `nil` on a refusal: not the net loaded before it, and **not** the embedded
one. `searchRootNode` then refuses to search and returns no move, so the caller emits
`bestmove 0000`. The refusal is printed as an `info string` **and** on stderr, because a match log
keeps stderr even when the harness drops info lines. `EnableNNUE` is the deliberate way back.

This reversed the old, explicitly tested behaviour — `TestLoadNNUEFileErrors` asserted that a bad
`EvalFile` "must leave the current evaluator intact" — so it was rewritten as
`TestLoadNNUEFileRefusesWithoutFallingBack`, and the in-process `EvalFile` case now expects
`game.network == nil`. Both traps the skill names are closed at once: a silent fallback (a match
reporting a plausible Elo for a net that never played a move) and leaving the net merely unloaded
(`readyok`, then a crash on the first `go`).

Note the C++ port's `src/main.cpp` still has the trap-1 fallback in the open — on a
`PAWNSTAR_EVALFILE` load failure it prints "falling back to the embedded net" and plays on.

## The gate

Nothing here changes evaluation, so `bench` **must not move**: **6,601,454** before and after, and
identical bestmove and node count on all sixteen positions
(`PAWNSTAR_BENCH_DEBUG=1`). A moved bench means the loader is wrong. See
[[project-cross-engine-bit-parity]].
