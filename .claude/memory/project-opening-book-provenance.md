# The opening book's provenance: TSCP out, CC0 Lichess in (2026-10-30)

## What was wrong

`engine/book.txt` was a 385-line book derived from **TSCP's `book.txt`**, and was asserted to be
**"public domain"** in about twenty places across the three ports — documentation, code comments, the
notice the released engine prints, and the tests that enforced the claim. **The claim was false.**
TSCP is **Copyright 1997 Tom Kerrigan**; the only permission ever located is an informal 2002 email
allowing use *"as long as you're not making any money off of it"*. That is a **non-commercial**
permission, which **GPLv3 cannot take**, and it is not a dedication at all — so re-wording could never
have fixed it, and the material had to go. The `terredeciles/TSCP` repository the old docs linked has
no `LICENSE` file whatsoever.

This port mattered as much as the C++ one for a specific reason: the book is **`go:embed`ed**
(`engine/book_embed.go`), so **every Go binary ever shipped from this repository contained those
bytes**.

## What replaced it

The book `pawnstar` built with its `tools/make_book.cpp` and ships as `pawnstar.book` (its PR #63,
commit `1162592` on `main`). Taken **byte for byte**, because the three ports' books are identical by
design and must stay so.

**Superseded on 2026-10-31 by the screened book** - the same book put through `pawnstar`'s
`tools/filter_book`, which marks with a trailing `?` every move that engine's own NNUE scores more than
100 cp below its position's best, and appends the engine's own choice as an extra unmarked line at the 84
positions where every database move is marked. Taken byte for byte from `pawnstar`'s `a7a9198`. The table
below is the screened file; the unscreened one it replaced was 3,244 lines, 155,503 bytes, SHA-256
`154228ffc843dc8068b6d0da2ff7df9daa39442e923d1eb6a82fc93ca448bcae`.

| | |
|---|---|
| Lines | 3,328 opening lines - 3,244 the database's and 84 the engine's own (3,386 including the `#` header) |
| Bytes | 162,286, LF |
| SHA-256 | `5f42b523bb68b011be8989218888c4ddb767c9f89c35100cc68b9115c09e9a19` |
| Source | Lichess open database, standard rated games, export **2017-01** |
| Filters | both players Elo ≥ 2200, time-control base ≥ 180 s, standard, finished, ≥ 16 plies |
| Games | **73,128 used of 10,680,708 seen** |
| Keep rule | a move kept where **≥ 8** games played it from the position **and** it was **≥ 1 %** of games reaching it |
| First moves | `e2e4` (1,610 lines), `d2d4` (1,199), `g1f3` (341), `c2c4` (178), nothing else |
| Screening | `tools/filter_book`, `pawnstar-v20.bin` at depth 12, 100 cp margin; 922 lines carry a `?` |
| Dedication | Creative Commons **CC0 1.0** (`CC0-1.0`) |

## Rules that are easy to get wrong

- **Copy the bytes, never regenerate.** The `#` header carries a **generation timestamp**, so a second
  run of the generator produces a different file and breaks the cross-port identity. There is **no
  generator in this repository and none should be added**. The way to get it:
  `git -C ../pawnstar show origin/main:pawnstar.book > engine/book.txt`, then check the SHA-256 — a
  plain checkout could apply a filter.
- **`.gitattributes` marks `engine/book.txt` `-text`.** Without that, a CRLF checkout would change the
  bytes and silently change what `go:embed` compiles in — a byte-different binary for the same commit.
- **CC0 obliges no attribution.** Wherever Lichess is credited, the text must say plainly that the
  credit is **owed to nobody** and is given because saying where the lines came from is right.
  Overstating an obligation is its own kind of false provenance claim — the mirror image of the defect
  this replaced — so the printed notice is pinned on the words **"CC0 waives attribution"**.
- **A move's weight is the number of book *lines* through it, not a game count.** The generator pruned
  each line where its thresholds stopped it, so no line is a prefix of another and each appears exactly
  once; candidates are stored with repeats, so a uniform pick is frequency-weighted by lines.
- **Format and parser unchanged**: one line per pruned tree leaf in coordinate notation, `#` to end of
  line a comment. The screened book **does** carry `?` moves - 922 of its 3,328 lines - and
  `loadOpeningBook` honours the marker: such a move is replayed so the rest of the line parses but is
  never offered, which is why the 84 engine-chosen lines exist (a position whose every move is marked
  would drop the engine out of book, silently).
- The header names **C++ identifiers and paths** (`OpeningBook::ParseLineOfPlay`, `tools/make_book.cpp`,
  `nnue/openings.epd`). That is an accepted consequence of byte-identity — **do not "fix" it here**, or
  the three books diverge.

## What is gated, and where

- `TestShippedBookProvenance` (`engine/book_test.go`) asserts against the **embedded** bytes, which is
  what ships: a correct `engine/book.txt` beside a stale `go:embed` would pass a file check and still
  carry the old claim. It requires `Lichess open database`, `https://database.lichess.org/`, `CC0 1.0`,
  the dedication URL and `asks for no attribution`; **forbids** `TSCP` and `Kerrigan`; requires **more
  than 385 opening lines**, because a book truncated to its header would load fine and play nothing;
  and checks the start position still has a move. Since the screened book landed it also pins the
  screening: that the book carries `?` marks at all (regenerating with `make_book` and forgetting
  `filter_book` would silently ship every move the database offered), that the file distinguishes the
  engine's own moves from the database's (the header's CC0 claim covers the database's lines, not those),
  and that every position a line continues from still offers a selectable move.
- `TestLicenceCommandPrintsTheNotice` and `TestLicenceNoticeAgreesWithTheNoticesFile`
  (`engine/licence_test.go`) hold the same claims to the printed notice and to
  `THIRD-PARTY-NOTICES.md`. The printed notice must contain no `TSCP`/`Kerrigan`; the markdown file
  deliberately *does*, in the paragraph recording why the book was replaced.
- The `Makefile`'s `book` target names both tests. A focused `-run` union **silently skips what it does
  not match**, which is the standing trap in this repo (see CLAUDE.md, `make check`).

## Where the prose lives

`engine/licence.go` (the five-line replacement, shared word for word with the other ports),
`THIRD-PARTY-NOTICES.md` (its own section, written as the courtesy it is, matching the ODbL section's
tone), `README.md` (*Opening book* and the licence paragraph), `CLAUDE.md` (*Opening book*),
`engine/book_embed.go` and `engine/book.go` comments.

## Evidence (Legion, Windows 11, 2026-10-30)

`go test ./...` green; `golangci-lint` 2.12.2 — 0 issues; **bench 5,617,906 nodes** at depth 13
single-threaded on **both** the `GOEXPERIMENT=simd` (AVX2) and the scalar builds. The bench cannot move:
`RunBench` forces `OwnBook` off, so the three ports' bit-exactness never depended on the book. **A
changed bench after a book change means something unrelated broke.**
