---
name: reference-profile-ryzen-2026-07
description: "perf profile Go vs C++ pawnstar on Ryzen 4500U (2026-07-03): Go 1.53x slower, nnueBoard() conversion ~10% is the standout Go-only tax"
metadata:
  type: reference
---

Fresh machine: **AMD Ryzen 5 4500U (Zen 2), 6 cores** — NOT the i9-13900HX of [[reference-pawnstar-perf-measurement]], so absolute nps differ. This machine's `go` needed a PATH fix (go 1.26 at /usr/local/go/bin ahead of distro go-1.22); C++ needs cmake+ninja (apt) and `perf` needs `sudo sysctl kernel.perf_event_paranoid=1`.

**Method:** both engines `bench` (single-thread, book-off, identical **6,888,983-node** tree — C++ build 610 has butterfly history too, so trees match bit-for-bit). `perf record -F 1999 -g --call-graph fp` on each native binary (same tool both sides = apples-to-apples, unlike pprof-vs-callgrind). C++ built `make RELEASE=1` (-O3 -g, DEBUGX off).

**Speed:** C++ ~1.19M nps vs Go ~0.78M nps = **Go 1.53× slower** (stable across 3 interleaved rounds; cf. ~1.66× on the old i9). Eval-dominated as always.

**Profile shapes are very similar** (eval ~55-60% both sides). Near-parity relative cost: move ordering (C++ 7.2% / Go 7.1%), SEE (~5.8% both), alpha-beta (4.3/5.0). The output-layer forward pass is at parity-or-better for Go — the hand-written AVX2 `outputDotInt8Asm` (18.2% self) does its job; C++ inlines its dot into EvaluatePosition (28.3% self incl. dot).

**Two Go-specific costs stand out:**
1. **`nnueBoard()` conversion ≈ 8-12% of total runtime — the standout lever, no C++ equivalent. FIXED 2026-07-03, +~10% nps, via zero-copy reinterpret.** Was: Go's `currentAccumulator` 20.2% self (vs C++ CurrentAccumulator 1.3% + memmove 15%). Only ~21% of that was the 4KB `accStack[j]=accStack[j-1]` copy (`rep movsq`, *cheaper* than C++'s `__memmove_avx_unaligned_erms`); the rest was two full `nnueBoard()` builds (from+to) per eval — a 64-iteration `squares` type-converting byte-loop + 9 bitboards materialized into `nnue.Board`. C++ pays nothing: it hands `Update`/`DiffSide` a `Position const&` and reads it in place. **Root cause was the *materialisation*, NOT the package boundary.** The winning fix (user chose this over an intermediate `boardStack` cache and over merging the packages): `position`'s first three fields (`pieces [7]bitboard`, `colors [2]bitboard`, `squares [64]piece`) are already byte-identical in layout to `nnue.Board` (`bitboard`=uint64, `piece`=uint8, same order — mirrors C++ `Position`), so `nnueBoard()` became `(*nnue.Board)(unsafe.Pointer(pos))` — a zero-copy pointer view. `currentAccumulator` passes posStack entries straight in; **`boardStack`/`deriveNnueBoard` deleted** (less code than before). Package stays import-free (the unsafe cast lives in nnue_bridge.go). Guard: **`TestNnueBoardLayout`** (offsets + random-position round-trip vs the retained explicit builder `nnueBoardExplicit`) fails loudly on any field reorder. Net +~10% interleaved (778k→863k nps), bench bit-identical (6,888,983), all gates + `-race` green. No SPRT needed (search unchanged); would only confirm ~+5-8 Elo. **Waypoints (rejected en route):** derive-alone (incremental child board, keep boundary) gave only +2% — the per-child *parent* rebuild was the real cost, not the child build; a `boardStack` cache of converted boards got +9% but the reinterpret is simpler and fully C++-parity. Files: engine/nnue_bridge.go, engine/search.go, engine/nnue_test.go (guard), CLAUDE.md.
2. Move generation codegen gap: Go movegen+pins ~7.3% vs C++ 4 GenMoves templates 5.2% — the known, largely-unrecoverable compiler gap (bounds checks/inlining), not algorithmic ([[reference-pawnstar-perf-measurement]] PEXT-is-dead).

Profiles saved under the session scratchpad (cpp.perf / go.perf) — regenerate with the method above.
