---
name: reference-go-simd-archsimd
description: "How to enable Go's simd/archsimd SIMD-intrinsics package for the NNUE kernels"
metadata: 
  node_type: memory
  type: reference
  originSessionId: 6ae67a0a-eca5-4c78-b247-b3c77c1452d5
---

Go's `simd/archsimd` package (real AVX2/AVX-512 intrinsics: `Int8x32`/`Int16x16`/`Int32x8`, `DotProductPairs`, `AddSaturated`, `SaturateToInt8`, `Min`/`Max`, `Load*Slice`/`StoreSlice`) is what the Go NNUE evaluator uses instead of Plan9 assembly. See [[project-go-nnue-port]].

- The machine's `go` on PATH is **already Go 1.26.4** (`~/.local/bin/go/bin/go`, GOROOT `~/.local/bin/go`). No toolchain install needed.
- The package ships in GOROOT at `src/simd/archsimd` but is **gated behind `GOEXPERIMENT=simd`** — a plain `go build` fails with "build constraints exclude all Go files". Every build/test command must be `GOEXPERIMENT=simd go build/test ...` (wrap it in the Makefile/build script so it isn't forgotten).
- Verified working: `GOEXPERIMENT=simd go build` compiles and runs `archsimd.LoadInt16x16Slice(...).Add(...)` correctly on this CPU (has AVX2; `go env GOAMD64` = v1).
- archsimd is amd64-only and emits the vector instructions directly (no visible runtime CPU dispatch), so a binary using AVX2 types will SIGILL on a pre-AVX2 CPU — keep a build-tagged pure-Go scalar fallback for portability (arm64 / non-AVX2), bit-identical to the SIMD path.
