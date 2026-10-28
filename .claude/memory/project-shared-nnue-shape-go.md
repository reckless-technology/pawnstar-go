---
name: project-shared-nnue-shape-go
description: The shared NNUE shape (king mirroring + 8 output heads) in pawnstar-go — the build-tag knobs, the synthetic net, and the PRE-EXISTING Go/C++ search divergence that the synthetic net's wild evaluations expose
metadata:
  node_type: memory
  type: project
---

# The shared NNUE shape in pawnstar-go

2026-10-27, branch `nnue-new-shape`. The shared architecture of the `reckless-nnue` skill — horizontal
king mirroring and eight material output heads — built here behind build tags alongside v12's shape.
The specification is that skill; the reference implementation is `../pawnstar` (its PR #38 and its
`.claude/memory/project_shared_nnue_shape.md`). This note is only what is specific to this port, and
the finding at the end, which is owed to all three ports.

## The knobs are build tags, because Go has no preprocessor

`pawnstar_nnue_king_mirroring` and `pawnstar_nnue_output_buckets_8`, selecting
`engine/nnue/shape_mirroring_{off,on}.go` and `engine/nnue/shape_heads_{one,eight}.go`, standing in for
the C++'s `PAWNSTAR_NNUE_KING_MIRRORING` / `PAWNSTAR_NNUE_OUTPUT_BUCKETS` and the Rust port's Cargo
features. The Makefile exposes them as `make … KING_MIRRORING=1 OUTPUT_BUCKETS=8` and threads the
resulting `-tags` through **every** `go build`, `go test`, `go vet` and `go doc` (`GOTAGS`), because a
tag that reaches only some of them builds half the tree at the wrong shape in silence.

Each shape file defines **one integer**, `kingMirroringOn` (0 or 1), and everything else is constant
arithmetic off it: `kingMirrorMask = kingMirroringOn * fileFlip`, `engineFlags`, and
`FormatVersion = 1 + kingMirroringOn`. Go cannot convert a `bool` const to an int, so an int knob is
what lets the version be **derived rather than written down** — the rule the skill is emphatic about.
Version 2 means mirroring on *and* eight heads together; more heads alone do not move it, because the
map is untouched.

`engine/uci_test.go`'s `buildEngineBinary` reads the shape back off `nnue.KingMirroringEnabled` /
`nnue.OutputBuckets` and passes the matching `-tags` to the `go build` it runs for the UCI subprocess
tests. Without that, a new-shape test run drove a **default-shape** engine binary, which loads the
shipped net happily and so passed while testing the wrong thing.

## The refresh rule, and breaking it to prove the test

A perspective may be diffed only while its bank, its square flip and its colour flip all stay put.
The three live together in the comparable `perspective` struct and are compared whole
(`nnue.NeedsRefresh`), because on the mirrored map a king stepping **d1 → e1 keeps bucket 3** — files 3
and 4 both mirror to file 3 — while every feature row that perspective reads moves.

Proved by breaking it. Narrowing the comparison to `wf.bank == wt.bank` makes
`TestNNUEBoundaryCrossings` fail on **ply 1 of its first line**, naming
`white(bank=2304 flip=0) -> white(bank=2304 flip=7)` with 1020 white lanes wrong, while the
board-half line and castling still pass — the right discrimination, since those change the bank, which
the broken comparison can still see. Identical failure on the archsimd/AVX2 build and on the scalar
build (`GOEXPERIMENT` cleared). `nnue.DescribePerspective` exists so the failure reads as those three
values rather than as a lane count.

## Testing a shape nothing has been trained for

`nnue.SyntheticNetPayload` / `SyntheticNetImage` (`engine/nnue/synthetic.go`): a fixed-seed xorshift64*
payload wrapped by the engine's own `Stamp`. It is in the package rather than in a test because the
`engine` and `engine/nnue` test packages both need the same bytes and Go cannot share test code across
packages — the same conclusion the Rust port reached for the same reason. Writing the payload out by
hand is also a second opinion on the layout `Load` reads.

**Its bytes are identical to the C++'s**: 12,617,824 bytes,
SHA-256 `4abfdb7bf3f6753fb53d8fed3b524750f7c06031cd5464445f18075f8ad4871b`. That is half the proof on
its own, because two ports comparing node counts have to be searching the same net.
`PAWNSTAR_WRITE_SYNTHETIC_NET=<path> go test ./engine/nnue -run TestWriteSyntheticNet` writes it out —
the Go stand-in for the C++ suite's `--write-synthetic-net`, since this repository keeps no developer
tools.

The net-dependent engine gates skip at a shape whose net does not exist
(`skipWithoutTheShippedNet`), as the Rust and C++ ports' do. `TestNNUEIncremental`,
`TestNNUEBoundaryCrossings` and `TestNNUEOutputHeadsAreAllVisited` do **not** skip: they install the
shape's own net and so run at both shapes, which is the point of having a synthetic one.

## Values

| | v12's shape | the shared shape |
|---|---|---|
| `format_version` | 1 | 2 |
| `flags` | 0 | 1 (bit 0, mirroring) |
| `output_buckets` | 1 | 8 |
| `king_bucket_map_crc` | `0xCC6DDB5DAF0AB18D` | `0x0A20060D4E9586E0` |
| `payload_bytes` | 12,589,060 | 12,617,760 |
| synthetic net | 12,589,124 bytes, SHA-256 `af66fbe8…4696` | 12,617,824 bytes, SHA-256 `4abfdb7b…871b` |

`bench` = **6,601,454** remains the gate for the **default shape only**, and is unmoved by this change
on both the archsimd/AVX2 and the scalar kernel paths. At the new shape there is no bench: the engine
rightly refuses v12's net, and over random weights a depth-13 search does not finish usefully.

## The finding: a PRE-EXISTING Go/C++ search divergence, and it is not this change

The new shape's gate was to be six fixed-depth-8 searches over the synthetic net, matching the C++
exactly. **This port reproduces only one of the six** (startpos, 2,709 nodes, `g1f3`). The others
differ, by 69 nodes out of 183,574 at the closest and by a different best move at the two
mirror-boundary positions.

It is **not** the shape work, and the evidence is specific:

- the int8 evaluation is identical to the C++'s on **488,396 positions** — a 9,219-position random
  walk from all six FENs, then the *entire* 4-ply subtree below the divergent node (44,847), then its
  whole 5-ply subtree plus three capture-only plies, which is what quiescence sees (434,330). Not one
  position disagrees;
- the incremental accumulator equals a from-scratch rebuild everywhere tested, at both shapes and on
  both kernel paths;
- both engines search the same net (the SHA-256 above);
- descending the divergence by root move reaches a node where **every** restricted single-root-move
  search agrees and only the full search differs, by **one** node — the signature of a rare condition,
  not of a wrong evaluation;
- and it reproduces at the **default shape**, with the Go build at this branch's base commit
  `2240492`, i.e. **before any of this change**:

  | default shape, `synthetic-default.bin`, `3k4/pppppppp/8/8/8/8/PPPPPPPP/3K4 w - - 0 1` | Go | C++ |
  |---|---|---|
  | depth 9 | 33,504 | 33,504 |
  | depth 10 | **75,481** | **75,337** |
  | depth 11 | **181,591** | **169,200** |

  Smallest reproducer: the same position and net, `go searchmoves e2e4 depth 10` → Go 88,172, C++
  88,173.

So the two engines are bit-exact over the **shipped** net (bench 6,601,454 over 6.6M nodes at depth
13) and diverge over the synthetic net once a search is deep enough. What the synthetic net changes is
the eval *magnitude*: random weights produce scores in the ±20,000 range, which drives search paths —
aspiration re-searches, pruning margins, the win/lose threshold bands — that a trained net's ±500
scores never reach. Something in that region of the search differs between this port and the C++ one,
and it was there before today.

**Consequence for the work:** the six-position synthetic-net gate cannot be met by this port until
that difference is found, and it is a change owed to all three ports rather than to this one. The
container and shape values, the accumulator, the evaluation and the default-shape bench are all
exactly as specified; the gate is measuring a pre-existing search difference on top of them. The Rust
port reported matching all six, which would make **Go the odd one out** of the three — worth checking
against that port rather than assumed.
