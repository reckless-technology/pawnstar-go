---
name: feedback-root-cause-not-benign
description: "When a supposedly-deterministic system diverges, hunt the real cause — don't excuse it as \"benign\" noise"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

The user rejects hand-wavy explanations for discrepancies. When the Go and C++ engines' `bench` node counts differed, I called it "benign independent-TT-replacement noise"; the user pushed back — *"single thread fixed depth search should be deterministic, including transposition table"* — and was right. The real cause was a 50-move-clock bug, found by isolating the eval path (int16 vs int8), diffing per-node search traces to the first divergent node, then dumping full state there.

**Why:** A single-threaded fixed-depth search IS fully deterministic; any divergence has a concrete, findable cause. Reaching for "benign" is a tell that the investigation stopped too early.

**How to apply:** When two supposedly-equivalent things differ, treat it as a bug to root-cause, not noise to excuse. Bisect empirically — isolate the variable, trace to the first divergence, dump concrete state, and prove the mechanism before declaring done. The user also asked for the state dump explicitly, so lean toward showing evidence over asserting. See [[project-cross-engine-bit-parity]].
