---
name: feedback-full-names-not-abbreviations
description: Prefer full words to abbreviations in variable and function names when writing new code
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 4f58b693-7492-4c16-97a8-ee8793ba8b86
---

The user prefers full names over abbreviations in variable and function names.

**Why:** Readability — descriptive names read better than cryptic contractions.

**How to apply:** For new identifiers, expand invented abbreviations: `histBonus`→`historyBonus`,
`abs32`→`absInt32`, `histMax`→`historyMax`, `quietsBuf`→`quietsBuffer`, `idx`→`index`, `e`→`entry`,
`contHist`→`continuationHistory`. Keep the codebase's pervasive established idioms that also match
surrounding code and avoid footguns — `ss` (searchState), `m` (move), `i`, `c` (color), `pv`, `ml`
(a parameter named `move` would shadow the `move` type). Balances against the standing "read like the
surrounding code" rule; the target is descriptive-but-idiomatic, not maximal verbosity everywhere.
