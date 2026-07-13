# Pawnstar-Go build wrapper.
#
# The NNUE kernels use the experimental simd/archsimd package, which is gated behind
# GOEXPERIMENT=simd. Every build/test MUST set it, so it is centralised here — always
# build via `make`, not a bare `go build`. Requires Go 1.26+ (archsimd landed in 1.26).
#
# GO defaults to whatever `go` is on PATH (the 1.26.4 toolchain in ~/.local/bin/go).

GO ?= go
export GOEXPERIMENT := simd

# The NNUE kernels here are AVX2 (archsimd 256-bit + the hand-written dot in dot_amd64.s), selected by
# BUILD TAG, not by a runtime CPU check — so a simd binary would SIGILL on a pre-Haswell amd64 CPU at the
# first eval. GOAMD64=v3 (Haswell baseline: AVX2/BMI2/FMA) makes that requirement explicit: the Go runtime
# then refuses to start on an unsupported CPU with a clear diagnostic instead of dying on an illegal
# instruction mid-search. Codegen-only — the engine is integer-only, so results are unchanged (the golden
# bench signature is asserted by the UCI test). Applies to this simd build only; a plain `go build` (the
# scalar-fallback path, no GOEXPERIMENT) does not set it and still runs anywhere.
export GOAMD64 := v3

BINARY := pawnstar-go
PKG    := ./...

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

.PHONY: all build test test-short check perft search bk see nnue book lazysmp race timing test-uci bench baseline vet fmt clean

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(VERSIONED) .
	@rm -f $(BINARY) && ln -s $(VERSIONED) $(BINARY)
	@for f in $(BINARY)_[0-9]*_[0-9]*_[0-9]*; do [ "$$f" = "$(VERSIONED)" ] || rm -f "$$f"; done
	@echo "built $(VERSIONED)  (pawnstar-go -> $(VERSIONED), version $(VERSION))"

# Full test suite (all packages; perft capped at depth 5, search to depth 9).
test:
	$(GO) test $(PKG)

# Quick suite (-short: BK at depth 7, smaller NNUE walk).
test-short:
	$(GO) test -short $(PKG)

# Full correctness gate: the WHOLE suite, plus the race-detector pass (the only thing a plain `go test`
# does not cover).
#
# `test` is what makes the gate closed BY CONSTRUCTION. It used to be a union of the focused `-run` regexes
# below, which silently skipped any test matching none of them — and two load-bearing gates were being
# skipped exactly that way: TestNnueBoardLayout (the hard contract guarding the unsafe position->nnue.Board
# reinterpret; a field reorder would corrupt every eval) and TestOutputDotInt8Asm (the asm bit-identity
# gate), because `-run TestNNUE` is case-sensitive and matches neither. Never gate on a regex union.
#
# The focused targets are deliberately NOT prerequisites here. Every one of them is a strict SUBSET of
# `test` (same tests, just verbose), so including them only multiplied the work: CI runs
# `make -j"$(nproc)" check`, which runs prerequisites in PARALLEL, so `test` + 9 focused targets meant ten
# concurrent `go test` processes fighting over the runner's cores — enough for the full-suite run to blow
# go test's default 10-minute per-package timeout. They remain as standalone targets for verbose,
# diagnosable output on a specific invariant (`make perft`, `make bk`, ...).
# Use `make test-short` for a fast pass.
check: test race

perft:
	$(GO) test $(PKG) -run TestPerft -v

search:
	$(GO) test $(PKG) -run TestSearch -short -v

bk:
	$(GO) test $(PKG) -run TestBratkoKopec -v

see:
	$(GO) test $(PKG) -run TestSEE -v

# NNUE gates: int16 reference (0 cp vs C++), int8 search eval (<=40 cp, scalar==SIMD), lazy accumulator,
# the unsafe position->nnue.Board layout contract (TestNnueBoardLayout), and the hand-written AVX2/NEON
# output-dot bit-identity gate (TestOutputDotInt8Asm, in the nnue package).
# The regex is case-sensitive: `TestNNUE` alone matches NEITHER TestNnueBoardLayout NOR TestOutputDotInt8Asm.
nnue:
	$(GO) test $(PKG) -run 'TestNNUE|TestNnue|TestOutputDot' -v

# Opening-book gate (questionable-move filtering).
book:
	$(GO) test $(PKG) -run TestOpeningBook -v

# Lazy SMP smoke test (multi-threaded search sanity).
lazysmp:
	$(GO) test $(PKG) -run TestLazySMP -v

# The concurrency tests under the race detector — the data-race-freedom claims (lockless TT/eval
# cache sharing, the ponder clock atomics) are only actually verified when -race is on.
race:
	$(GO) test $(PKG) -race -run 'TestLazySMP|TestPonderRace|TestFixedTimeDeadlineFires' -v

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
