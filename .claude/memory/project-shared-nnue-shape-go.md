---
name: project-shared-nnue-shape-go
description: The shared NNUE shape (king mirroring + 8 output heads) in pawnstar-go — the build-tag knobs, the synthetic net, the six-position cross-port signature and the CI job that gates it, and the Hash-default divergence that made that signature look like a search bug
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

## 2026-10-30: the shared shape is now the DEFAULT, and v20 is the shipped net (release 2.0.0)

The build tags were **inverted**. They now name the NON-default shape - `pawnstar_nnue_king_mirroring_off`
and `pawnstar_nnue_output_buckets_1` - so a build with no tags at all, including a plain `go build`, is the
mirrored eight-head shape. `make KING_MIRRORING=0 OUTPUT_BUCKETS=1` is the way to v12's shape, and the
Makefile's knob names and accepted values (0/1 and 1/8) are unchanged, which is what let
`tools/new-shape-signature.sh` stay byte-identical with `pawnstar` and `pawnstar-rs`.

`engine/nnue/pawnstar-v20.bin` replaces `pawnstar-v12.bin`, which was **deleted** (not rewritten out of
history): 12,617,824 bytes, SHA-256 `c8a29b32...1174b1cc34`, `header_crc` `0x5A2DFC8FA67F2B2C`,
`payload_crc` `0x62C460241DDC3EF7`. Trained entirely on Leela Chess Zero self-play data; +9.99 Elo at
8+0.08 (8,000 games) and +1.13 at 12+0.12 (12,000 games) against v12. **There is now no net in this tree
at v12's shape**, so `isShippedNetShape` inverted too (`FormatVersion == 2 && OutputBuckets == 8`) and the
retained shape's gates run on the synthetic net alone.

**The new cross-port bench signature is 5,617,906** at depth 13, single-threaded, `benchHashMB` 64 -
identical on the archsimd/AVX2 and the scalar paths, and deterministic over three runs. Measured on the
Legion (Windows, Go 1.26.x), 2026-10-30. The C++ and Rust ports must report the same number.

**What had to be remeasured, and why each one is not a weakening.** Three gates' expected values belong to
the shipped net, so swapping the net moved all three:

- `benchGoldenNodes` 6,601,454 -> **5,617,906**, and the same literal in CI's scalar job.
- `bkCases`, the Bratko-Kopec accepted moves: 3 of 24 changed, regenerated by the documented
  `GEN_BK=1 go test -run TestGenerateBratkoKopecNNUE` path.
- `engine/testdata/nnue_reference.txt`, the 250 int16 reference evaluations. These had been bullet's own
  values for v12 and there is no trainer-side file for v20, so they were **recomputed by an independent
  Python reader and forward pass written from the `reckless-nnue` specification** rather than from this
  package. That reader was validated first by reproducing all 250 of bullet's **v12** values to **0 cp** -
  the check the specification demands of any new implementation of the metric - and then agreed with this
  package on v20 to **0 cp** as well. So the oracle is still a second opinion and not a recording of the
  engine. Keep that discipline if the file is ever regenerated again: validate the generator against a
  known-good net first.

**`TestNNUEInt8`'s deviation now reaches exactly its bound.** Over the 250 reference positions the int8
search evaluator differs from the int16 reference by at most **40 cp** with v20, where v12 measured 28.
The bound is 40 (the C++ `kInt8Bound`), so it passes with no margin. Not loosened, and worth knowing
before anyone reads a future failure as a kernel bug: the net is the same blob in all three ports, so the
C++ and Rust suites sit at the same place.

**CI.** The old `new-shape` job is gone, replaced by two: `legacy-shape`
(`make check KING_MIRRORING=0 OUTPUT_BUCKETS=1`, so the retained shape is still compiled and its
accumulator gates still run) and `cross-port-signature` (the six-position script at the default shape,
over the synthetic net, `--hash 64`). `TestShapeReferenceValues` was also tightened while passing: it had
keyed the expected `payload_bytes` on the MIRRORING knob, which is the wrong one - the payload size
follows the HEAD count - so a build that moved only one knob would have asserted the wrong size.

## RESOLVED: the "pre-existing search divergence" was the Hash default, and the gate is green

**Superseded, same day.** Everything below this section was written before the cause was found, and its
conclusion — that this port could not meet the six-position gate until a search difference was tracked
down — is **wrong**. It is kept because the evidence it gathered is what ruled search and evaluation
out, which is how the real cause was reached.

The cause was `hashTableMB` in `engine/constants.go`: **512** here against **64** in the C++ and Rust
ports, arrived at incidentally through `cec14a4`, a TT *locking* change, rather than decided. Table size
changes the collision pattern and so the node count at a fixed depth. With the table pinned equal all
three ports are bit-exact, and at `Hash 512` the C++ and Rust ports reproduce *this* port's numbers
exactly — including the one-node 88,172 that started the hunt. 64 is now the agreed value in all three
(`0808763` here), and each port pins its advertised `option name Hash` line in its own suite, this one in
`TestUCIHandshakeInProcess`.

Measured again on 2026-10-27 after that fix: **six of six** positions match the C++ and Rust ports
exactly at `Hash 64`, depth 8, over the same synthetic net — 2,709 `g1f3` / 1,100,203 `f3d3` /
117,442 `b4f4` / 183,574 `a1b1` / 125,721 `e2e4` / 134,448 `e2e3`. The gate is
`tools/new-shape-signature.sh`, run by the `new-shape` CI job, and the same file with the same six values
is committed in `pawnstar` and `pawnstar-rs`.

**The lesson is the one the skill now states as a rule.** The only cross-port gate that had ever run was
`bench`, and `bench` pins the table to 64 MB in all three ports — `engine/bench.go` says so in as many
words, "a stable signature independent of the `Hash` option". *A gate that controls for a difference
cannot detect it*, so the one check guarding three-way parity was the reason the break stayed invisible
for five months. The six-position harness that first produced the numbers below set no `Hash` either,
which is why the divergence survived it too. Hence the two rules the script enforces: `--hash` is
mandatory and never defaulted, and its value is printed beside every node count. A node count whose
table size is not stated is not a number anybody can compare.

**And the general one, which the evidence below earned:** a node-count difference between the ports is a
configuration question before it is a code one. Asking the cheap question first — do the three engines
even agree on their table size — would have cost a second, against instrumenting 488,396 evaluations.

## The finding as first written: a “pre-existing Go/C++ search divergence” — superseded above

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
