# Third-party notices

Pawnstar-Go is distributed under the GNU General Public License, version 3 (see [LICENSE](LICENSE)).
This file carries the notices that accompanying material obliges, and nothing else. The module has **no
third-party Go dependencies** — [go.mod](go.mod) declares none, there is no `go.sum` and nothing is
vendored — so every notice below is about *data*, not code.

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
  third-party data, and neither licence reaches through the other to relicense it.

Neither point is a grant or a disclaimer of anything: it is a statement of which obligation this file
discharges. Nothing here enlarges or narrows the Open Database License, whose own text at the URL above
governs.

### Where this notice travels

The network is compiled into every binary this repository distributes
([engine/nnue/embed.go](engine/nnue/embed.go), a `go:embed` directive), so the Produced Work goes
wherever the executable goes. Three places therefore carry the notice:

- this file, in the source tree;
- the documentation bundle each release ships (`pawnstar-go-<version>-docs.zip`), which includes it;
- the binary itself — `pawnstar-go --license` prints it.
