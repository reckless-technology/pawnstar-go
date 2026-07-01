package engine

import "testing"

// midgameFen is a non-trivial position for NNUE micro-benchmarks.
const midgameFen = "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1"

func benchNet(b *testing.B) *nnueNetwork {
	net := loadEmbeddedNet()
	if net == nil {
		b.Fatal("no embedded net")
	}
	return net
}

func BenchmarkNNUEEvaluateExact(b *testing.B) {
	net := benchNet(b)
	pos := positionFromFen(midgameFen)
	var acc nnueAccumulator
	net.refresh(&acc, &pos)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = net.evaluateExact(&acc, pos.colorToMove)
	}
}

func BenchmarkNNUERefresh(b *testing.B) {
	net := benchNet(b)
	pos := positionFromFen(midgameFen)
	var acc nnueAccumulator
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		net.refresh(&acc, &pos)
	}
}

func BenchmarkNNUEUpdate(b *testing.B) {
	net := benchNet(b)
	from := positionFromFen(midgameFen)
	to := from
	to.makeMove(to.generateLegalMoves()[0]) // a quiet-ish first move
	var acc nnueAccumulator
	net.refresh(&acc, &from)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		net.update(&acc, &from, &to)
		net.update(&acc, &to, &from) // and back, so acc is unchanged
	}
}

var sink int
