# Pawnstar-Go build wrapper.
#
# The NNUE kernels use the experimental simd/archsimd package, which is gated behind
# GOEXPERIMENT=simd. Every build/test MUST set it, so it is centralised here — always
# build via `make`, not a bare `go build`. Requires Go 1.26+ (archsimd landed in 1.26).
#
# GO defaults to whatever `go` is on PATH (the 1.26.4 toolchain in ~/.local/bin/go).

GO ?= go
export GOEXPERIMENT := simd

BINARY := pawnstar-go
PKG    := ./engine/

.PHONY: all build test test-short perft search bk see test-uci bench baseline vet fmt clean

all: build

build:
	$(GO) build -o $(BINARY) .

# Full test suite (perft to depth 6, search to depth 9 — slow).
test:
	$(GO) test $(PKG)

# Quick suite (perft capped at depth 5, BK at depth 7).
test-short:
	$(GO) test -short $(PKG)

perft:
	$(GO) test $(PKG) -run TestPerft -v

search:
	$(GO) test $(PKG) -run TestSearch -short -v

bk:
	$(GO) test $(PKG) -run TestBratkoKopec -v

see:
	$(GO) test $(PKG) -run TestSEE -v

# UCI protocol regression test — drives the built binary as a UCI process (handshake, seldepth,
# searchmoves, currmove, bench determinism).
test-uci: build
	./test/uci_test.sh $(BINARY)

bench:
	$(GO) test $(PKG) -bench BenchmarkPerft7 -benchtime=10s -run '^$$'

vet:
	$(GO) vet $(PKG) .

fmt:
	$(GO) fmt ./...

# Snapshot the current build as the SPRT baseline opponent (tools/sprt.sh BIN_B default).
baseline: build
	cp $(BINARY) pawnstar-go-baseline
	@echo "baseline snapshot -> pawnstar-go-baseline"

clean:
	$(GO) clean
	rm -f $(BINARY) pawnstar-go-baseline
