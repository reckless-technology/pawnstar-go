---
name: feedback-tee-test-output
description: "Always pipe/tee test output to a tmp file so it can be revisited"
metadata:
  type: feedback
---

The user's rule: **always pipe or tee the output of a test run to a tmp file** so it can be revisited later without re-running.

**Why:** grepping a test's stdout inline discards everything the filter missed — when the filter is wrong (e.g. `t.Logf` output needs `-v`, or the pattern doesn't match), the evidence is gone and the run must be repeated; worse, an empty grep can silently masquerade as "no output" (this bit a BKGEN regeneration: `go test` without `-v` printed nothing, the pipeline wrote an empty file, and a downstream edit failed confusingly).

**How to apply:** `go test ... 2>&1 | tee /tmp/<name>.txt | <filter>` — filter for the summary, keep the full transcript on disk. Same for benches, SPRT logs, engine debug dumps.
