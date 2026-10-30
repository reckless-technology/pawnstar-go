# Third-party notices

Pawnstar-Go is free software under the **GNU General Public License, version 3** (see
[LICENSE](LICENSE), and <https://www.gnu.org/licenses/gpl-3.0.html>), and comes with **no warranty**.
That licence covers the engine's own code. This file records the third-party material distributed with
it, and the notices that material obliges.

The module has **no third-party code dependencies at all**: [go.mod](go.mod) declares none, there is no
`go.sum`, nothing is vendored, `go mod graph` names only this module and the Go toolchain, and
`go list -deps ./...` resolves to this module's own three packages plus the standard library. So
everything below is about **data** shipped in this repository and in its release binaries.

**Why this file differs from the C++ engine's.** `pawnstar`'s notices carry bullet's MIT licence,
because that repository redistributes two trainer files adapted from bullet's own example. Neither file
is here - this port carries no trainer at all, and `tools/` holds only shell and PowerShell scripts.
Where the Go source names bullet, it is a comment describing arithmetic this code must match (the
output-head divisor, the king-bucket map expansion, the payload save order), not bullet's code. So no
MIT notice is owed here, and one must not be copied in from the C++ repository to make the two files
look alike.

## The evaluation network's training data

The network this engine ships and embeds, [engine/nnue/pawnstar-v20.bin](engine/nnue/pawnstar-v20.bin)
(**v20**, the net of release 2.0.0 — the same blob, byte for byte, as
[`pawnstar`](https://github.com/reckless-technology/pawnstar) and
[`pawnstar-rs`](https://github.com/reckless-technology/pawnstar-rs) ship), was trained **entirely on
Leela Chess Zero self-play training data**.

That archive is published under the **Open Database License 1.0**, with its contents under the
**Database Contents License 1.0**:

| | |
|---|---|
| Source | Leela Chess Zero self-play training data |
| Source home | <https://storage.lczero.org/files/training_data/> |
| Database licence | `ODbL-1.0`, <http://opendatacommons.org/licenses/odbl/1.0/> |
| Contents licence | `DbCL-1.0`, <http://opendatacommons.org/licenses/dbcl/1.0/> |
| Runs used for v20 | `test91` (62.83% of the training pool) and `test60` (37.17%) |

A trained network is an ODbL **Produced Work**. Distributing one obliges a notice naming the source,
and this is that notice:

> Position data from the Leela Chess Zero self-play training data
> (https://storage.lczero.org/files/training_data/), run test91, used under the Open Database License
> 1.0 (http://opendatacommons.org/licenses/odbl/1.0/).

> Position data from the Leela Chess Zero self-play training data
> (https://storage.lczero.org/files/training_data/), run test60, used under the Open Database License
> 1.0 (http://opendatacommons.org/licenses/odbl/1.0/).

Those two lines are the attribution the extractor that read the archive wrote into every training
shard's provenance sidecar, one per run, reproduced here verbatim but for the run name.

### What the obligation is, and what it is not

Two things worth stating plainly, because both are easy to get wrong in either direction:

- **The obligation is the notice.** ODbL requires that a Produced Work distributed from the database
  carry a notice naming the source and its licence. It does **not** require the network itself to be
  licensed under ODbL, and `pawnstar-v20.bin` is not offered under ODbL.
- **Training a network with GPLv3 code does not make the network GPLv3.** The engine, the trainer and
  the extraction tools are GPLv3; the network is a set of weights those tools produced from
  third-party data, and a program's licence does not reach the data it processes. The network is
  distributed under this project's own licence, by choice rather than by obligation.

Neither point is a grant or a disclaimer of anything: it is a statement of which obligation this file
discharges. Nothing here enlarges or narrows the Open Database License, whose own text at the URL above
governs.

### Where this notice travels

The network is compiled into every binary this repository distributes
([engine/nnue/embed.go](engine/nnue/embed.go), a `go:embed` directive), so the Produced Work goes
wherever the executable goes — and no release publishes a source tarball, so a binary is all a
downloader has. Four places therefore carry the notice:

- this file, in the source tree;
- the documentation bundle each release ships (`pawnstar-go-<version>-docs.zip`), which includes it;
- the engine itself: the **`license` UCI command** prints a condensed form of everything in this file
  (`engine/licence.go`), so it is reachable from inside any GUI's UCI session. The wording is shared
  with `pawnstar`, `pawnstar-rs` and `zenith`, and `TestLicenceNoticeAgreesWithTheNoticesFile` holds
  the condensed form and this file to each other;
- the command line: `pawnstar-go --license` prints the same text, from the same place.

## The opening book

[engine/book.txt](engine/book.txt) is derived from the public-domain TSCP `book.txt`
(<https://github.com/terredeciles/TSCP>), reaching this repository as a byte-for-byte copy of the C++
engine's `pawnstar.book`.

## Withdrawn training data — PlentyChess

Networks up to and including **v12** were trained on self-play data generated by **PlentyChess**, by
**Yoshie2000**. That data is no longer used: no network in this release was trained on any of it, and
every network that was has been withdrawn — `pawnstar-v12.bin` and every net before it are gone from
the source tree, and every release that shipped them has been deleted. The credit is kept deliberately;
see README.md, *Provenance of earlier networks*.
