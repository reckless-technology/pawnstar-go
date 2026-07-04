package engine

import (
	"testing"

	"pawnstar-go/engine/nnue"
)

// midgameFen is a non-trivial position for NNUE micro-benchmarks.
const midgameFen = "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1"

func benchNet(b *testing.B) *nnue.Network {
	net := nnue.Embedded()
	if net == nil {
		b.Fatal("no embedded net")
	}
	return net
}

func BenchmarkNNUEEvaluateExact(b *testing.B) {
	net := benchNet(b)
	pos := positionFromFen(midgameFen)
	board := pos.nnueBoard()
	var acc nnue.Accumulator
	net.Refresh(&acc, board)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = net.EvaluateExact(&acc, int(pos.colorToMove))
	}
}

// BenchmarkNNUEEvaluateInt8 exercises the search's int8 hot-path evaluator (the int8 output dot), the
// kernel the NEON/AVX2 asm accelerates.
func BenchmarkNNUEEvaluateInt8(b *testing.B) {
	net := benchNet(b)
	pos := positionFromFen(midgameFen)
	board := pos.nnueBoard()
	var acc nnue.Accumulator
	net.Refresh(&acc, board)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = net.EvaluateInt8(&acc, int(pos.colorToMove))
	}
}

func BenchmarkNNUERefresh(b *testing.B) {
	net := benchNet(b)
	pos := positionFromFen(midgameFen)
	board := pos.nnueBoard()
	var acc nnue.Accumulator
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		net.Refresh(&acc, board)
	}
}

func BenchmarkNNUEUpdate(b *testing.B) {
	net := benchNet(b)
	from := positionFromFen(midgameFen)
	to := from
	to.makeMove(to.generateLegalMoves()[0]) // a quiet-ish first move
	fromBoard, toBoard := from.nnueBoard(), to.nnueBoard()
	var acc nnue.Accumulator
	net.Refresh(&acc, fromBoard)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		net.Update(&acc, fromBoard, toBoard)
		net.Update(&acc, toBoard, fromBoard) // and back, so acc is unchanged
	}
}

var sink int
