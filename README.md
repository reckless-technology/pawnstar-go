Pawnstar is a chess engine that supports the UCI protocol. This is a Go port of the original C++ implementation.

It uses bitboards for board representation and achieves approximately 35 million moves per second on a standard laptop (the C++ version reaches ~700 Mnps using BMI2 hardware PEXT; the Go port uses a software ray-table fallback instead).

To build the executable run `go build -o pawnstar-go ./cmd/pawnstar/` from this directory.

To run the engine: `./pawnstar-go`

To run the move generation tests: `go test ./engine/`
