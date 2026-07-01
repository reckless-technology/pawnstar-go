package engine

import _ "embed"

// embeddedNet is the default NNUE network compiled into the binary, so the engine is self-contained
// and needs no external net file. Loaded on demand by EnableNNUE via loadEmbeddedNet.
//
//go:embed nnue/pawnstar-v12.bin
var embeddedNet []byte
