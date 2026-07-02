---
name: feedback-direct-push-main-pawnstar-go
description: User authorized committing and pushing directly to main in the pawnstar-go repo (no branch-first / no ask)
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

The user authorized me to work on and push **directly to `main`** in the pawnstar-go repo (`/home/jonny/work/pawnstar-go`, origin `git@github.com:jonny-reckless/pawnstar-go.git`). No need to branch first or ask before committing/pushing to `main` here.

**Why:** It's the user's personal engine repo; the default-branch "branch first" guardrail just adds friction they don't want on this repo.

**How to apply:** In pawnstar-go, commit and push to `main` directly once work is ready. Still run the relevant gates before pushing (build with `GOEXPERIMENT=simd`, `TestPerft`+`TestSearch`, `TestBratkoKopec`, `bench` determinism; SPRT for strength-affecting changes) — the permission removes the branching ceremony, not the verification. This does **not** extend to the sibling C++ reference repo `/home/jonny/work/pawnstar`, where I branched and asked before touching `main` (e.g. the zobrist commit) — keep doing that there unless told otherwise. See [[project-go-nnue-port]].
