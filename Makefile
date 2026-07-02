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

# Version, mirroring the C++ engine: MAJOR.MINOR are edited by hand (keep in sync with engine/version.go),
# BUILD is the git commit count (globally reproducible; 0 outside a git checkout). The build number is baked
# into the binary via -ldflags so the UCI `id name` reports "Pawnstar-Go MAJOR.MINOR.BUILD", and the built
# binary is named pawnstar-go_MAJOR_MINOR_BUILD with a `pawnstar-go` symlink to it (like C++'s pawnstar).
MAJOR := 0
MINOR := 1
BUILD := $(shell git rev-list --count HEAD 2>/dev/null || echo 0)
VERSION := $(MAJOR).$(MINOR).$(BUILD)
VERSIONED := $(BINARY)_$(MAJOR)_$(MINOR)_$(BUILD)
LDFLAGS := -X pawnstar-go/engine.versionBuild=$(BUILD)

.PHONY: all build test test-short check perft search bk see nnue timing test-uci bench baseline vet fmt clean

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(VERSIONED) .
	@rm -f $(BINARY) && ln -s $(VERSIONED) $(BINARY)
	@for f in $(BINARY)_[0-9]*_[0-9]*_[0-9]*; do [ "$$f" = "$(VERSIONED)" ] || rm -f "$$f"; done
	@echo "built $(VERSIONED)  (pawnstar-go -> $(VERSIONED), version $(VERSION))"

# Full test suite (perft to depth 6, search to depth 9 — slow).
test:
	$(GO) test $(PKG)

# Quick suite (perft capped at depth 5, BK at depth 7).
test-short:
	$(GO) test -short $(PKG)

# Focused correctness gate: movegen (perft), SEE, Bratko-Kopec, NNUE, chess-clock timing,
# and the UCI protocol regression (drives the built binary). `perft` here is the full
# depth-6 sweep, so this is thorough rather than quick — use `make test-short` for a fast pass.
check: perft see bk nnue timing test-uci

perft:
	$(GO) test $(PKG) -run TestPerft -v

search:
	$(GO) test $(PKG) -run TestSearch -short -v

bk:
	$(GO) test $(PKG) -run TestBratkoKopec -v

see:
	$(GO) test $(PKG) -run TestSEE -v

# NNUE gates: int16 reference (0 cp vs C++), int8 search eval (<=40 cp, scalar==SIMD), lazy accumulator.
nnue:
	$(GO) test $(PKG) -run TestNNUE -v

# Chess-clock / time-management tests (budget, deadline firing, defaults, ponder race).
timing:
	$(GO) test $(PKG) -run 'TestTimeControlDefaults|TestComputeBudget|TestPonderRace|TestFixedTimeDeadlineFires' -v

# UCI protocol regression test — drives the built binary as a UCI process (handshake, seldepth,
# searchmoves, currmove, bench determinism, ponder protocol). Lives in the engine package
# ([engine/uci_test.go](engine/uci_test.go)) but builds the binary itself (lazily), so no `build`
# prerequisite is needed; -short skips it, and other engine tests never trigger the build.
test-uci:
	$(GO) test $(PKG) -run 'TestUCIProtocol|TestUCIBenchDeterminism' -v

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
	rm -f $(BINARY) $(BINARY)_[0-9]*_[0-9]*_[0-9]* pawnstar-go-baseline
