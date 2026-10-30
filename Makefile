# Pawnstar-Go build wrapper.
#
# The NNUE kernels use the experimental simd/archsimd package, which is gated behind
# GOEXPERIMENT=simd. Every build/test MUST set it, so it is centralised here — always
# build via `make`, not a bare `go build`. Requires Go 1.26.x, not 1.27 or later: archsimd landed in 1.26, and
# 1.27 changed its API (LoadInt16x16Slice became LoadInt16x16, StoreSlice became Store, the array forms gained an
# Array suffix), so this code does not compile there.
#
# GO defaults to whatever `go` is on PATH (tools/setup-dev.* install the newest 1.26.x).
#
# On Windows run it from Git Bash with mingw32-make (MSYS2's native GNU make; CLAUDE.md says why not MSYS2's own).

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

# The NNUE network SHAPE. Two knobs, so either shape builds from this one tree. The DEFAULTS are the
# shared architecture every reckless-technology chess engine is unified on - horizontal king mirroring
# with eight material output heads - which is what the shipped net (engine/nnue/pawnstar-v20.bin,
# release 2.0.0) was trained for. v12's older shape is still buildable, on purpose:
#
#   make check KING_MIRRORING=0 OUTPUT_BUCKETS=1      # v12's shape: unmirrored, one output head
#
# Both knobs are stamped into every net's header (`flags` bit 0, `output_buckets`, and the bucket map's
# CRC-64) and the loader checks all three, so a net can only load into a build of its own shape - a
# KING_MIRRORING=0 build REFUSES the shipped net, naming format_version, which is the container working
# and not a fault. There is no net in this tree at v12's shape (pawnstar-v12.bin was removed in 2.0.0),
# so a build there runs its gates against the synthetic net instead.
#
# A shape change is also an evaluation change, so it moves the bench signature and breaks bit-exactness
# with the C++ and Rust ports until all three land together: do not flip these defaults for one engine
# alone. They were flipped for 2.0.0 in all three at once.
#
# Go has no preprocessor, so the knobs are BUILD TAGS (engine/nnue/shape_*.go) rather than -D defines,
# and they have to reach every `go build`, `go test` and `go vet` here - which is why GOTAGS is threaded
# through every one of them below. The tags name the NON-default shape (`..._king_mirroring_off`,
# `..._output_buckets_1`), so a build with no tags at all - a plain `go build` - is the shipped shape.
# `go test` also builds the engine BINARY for the UCI subprocess tests; that build reads the shape off
# the nnue package's own constants, so it cannot drift from the tests driving it (engine/uci_test.go,
# buildEngineBinary).
NNUE_TAGS :=

ifeq ($(KING_MIRRORING),0)
  NNUE_TAGS += pawnstar_nnue_king_mirroring_off
else ifneq ($(KING_MIRRORING),)
  ifneq ($(KING_MIRRORING),1)
    $(error KING_MIRRORING must be 0 or 1, not "$(KING_MIRRORING)")
  endif
endif

ifeq ($(OUTPUT_BUCKETS),1)
  NNUE_TAGS += pawnstar_nnue_output_buckets_1
else ifneq ($(OUTPUT_BUCKETS),)
  ifneq ($(OUTPUT_BUCKETS),8)
    $(error OUTPUT_BUCKETS must be 1 or 8, not "$(OUTPUT_BUCKETS)")
  endif
endif

empty :=
space := $(empty) $(empty)
comma := ,
ifneq ($(strip $(NNUE_TAGS)),)
  GOTAGS := -tags $(subst $(space),$(comma),$(strip $(NNUE_TAGS)))
endif
GOTAGS ?=

BINARY := pawnstar-go
PKG    := ./...

# Version, MAJOR.MINOR.BUILD, from git alone (tools/version.sh): the nearest annotated vMAJOR.MINOR tag and the
# commits since it; 0.0.0 with no such tag reachable. Linked into the binary via -ldflags so the UCI `id name` reports
# "Pawnstar-Go MAJOR.MINOR.BUILD" (engine/version.go), and the built binary is named pawnstar-go_MAJOR_MINOR_BUILD with
# a `pawnstar-go` symlink to it (like C++'s pawnstar; a copy on Windows, below). Nothing here is edited to release: tag main and push the tag.
#
# EXE is the target's executable suffix (go env GOEXE: .exe on Windows, empty elsewhere), on the versioned binary and
# the alias alike - so the cleanup loop in `build`, which keeps only the file named exactly $(VERSIONED), keeps the
# binary it just built. On Windows the alias is a copy: Git Bash's `ln -s` silently makes a copy anyway (MSYS's
# default for symlinks), so copy outright and say so.
EXE := $(shell $(GO) env GOEXE)
VERSION := $(shell tools/version.sh)
VERSIONED := $(BINARY)_$(subst .,_,$(VERSION))$(EXE)
ALIAS := $(BINARY)$(EXE)
BASELINE := $(BINARY)-baseline$(EXE)
LDFLAGS := -X pawnstar-go/engine.version=$(VERSION)
ifeq ($(OS),Windows_NT)
ALIAS_CMD := cp
ALIAS_KIND := a copy of
else
ALIAS_CMD := ln -s
ALIAS_KIND := ->
endif

.PHONY: all build test test-short check cover perft search bk see nnue synthetic-net book lazysmp race timing test-uci bench baseline vet fmt clean docs-bundle

all: build

build:
	$(GO) build $(GOTAGS) -ldflags "$(LDFLAGS)" -o $(VERSIONED) .
	@rm -f $(ALIAS) && $(ALIAS_CMD) $(VERSIONED) $(ALIAS)
	@for f in $(BINARY)_[0-9]*_[0-9]*_[0-9]*; do [ "$$f" = "$(VERSIONED)" ] || rm -f "$$f"; done
	@echo "built $(VERSIONED)  ($(ALIAS) $(ALIAS_KIND) $(VERSIONED), version $(VERSION))"

# Full test suite (all packages; perft capped at depth 5, search to depth 9).
test:
	$(GO) test $(GOTAGS) $(PKG)

# Quick suite (-short: BK at depth 7, smaller NNUE walk).
test-short:
	$(GO) test $(GOTAGS) -short $(PKG)

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

# Statement coverage of the whole suite: every package's tests counted against every package (-coverpkg), so code one
# package exercises in another counts. The profile goes to coverage.out (git-ignored) and the last line printed is the
# total (`go tool cover -html=coverage.out` for the annotated source). It keeps the default -covermode=set: atomic,
# which a counting profile of the concurrent tests would need, slows the instrumented suite far more than its answer is
# worth here. The timeout is for the instrumented full suite, which on a slow machine runs past go test's default ten
# minutes (some 80 s on a 32-core desktop).
#
# The total is gated: under COVER_MIN per cent the target fails, here and in CI's coverage job, which runs this target -
# the one place the threshold is written (`make cover COVER_MIN=95` to try another). What is counted is ./... whole:
# the engine, engine/nnue and the binary's main package, every statement a release ships. The repository has no Go
# developer tools to leave out (tools/ holds shell and PowerShell scripts), and go never counts a _test.go file.
COVER_MIN ?= 90

cover:
	$(GO) test $(GOTAGS) $(PKG) -timeout 45m -coverpkg=$(PKG) -coverprofile=coverage.out
	@$(GO) tool cover -func=coverage.out | awk -v min=$(COVER_MIN) ' \
		/^total:/ { found = 1; total = $$NF; sub(/%/, "", total) } \
		END { \
			if (!found) { print "cover: no total line in go tool cover -func"; exit 1 } \
			printf "total statement coverage %s%% (gate %s%%)\n", total, min; \
			if (total + 0 < min + 0) { printf "cover: %s%% is under the %s%% gate\n", total, min; exit 1 } \
		}'

perft:
	$(GO) test $(GOTAGS) $(PKG) -run TestPerft -v

search:
	$(GO) test $(GOTAGS) $(PKG) -run TestSearch -short -v

bk:
	$(GO) test $(GOTAGS) $(PKG) -run TestBratkoKopec -v

see:
	$(GO) test $(GOTAGS) $(PKG) -run TestSEE -v

# NNUE gates: int16 reference (0 cp vs C++), int8 search eval (<=40 cp, scalar==SIMD), lazy accumulator,
# the unsafe position->nnue.Board layout contract (TestNnueBoardLayout), and the hand-written AVX2/NEON
# output-dot bit-identity gate (TestOutputDotInt8Asm, in the nnue package).
# The regex is case-sensitive: `TestNNUE` alone matches NEITHER TestNnueBoardLayout NOR TestOutputDotInt8Asm.
nnue:
	$(GO) test $(GOTAGS) $(PKG) -run 'TestNNUE|TestNnue|TestOutputDot' -v

# Write the synthetic net at THIS build's shape to $(NET), and nothing else. The net comes from a fixed seed
# through fixed integer arithmetic, so its bytes are identical on every platform and in all three bit-exact
# ports - which is half the proof of a cross-port node-count comparison on its own, because two ports
# comparing node counts have to be searching the same net. It is also what gives a shape with no trained
# net a signature at all: a KING_MIRRORING=0 build rightly refuses the shipped net and this tree holds no
# net at v12's shape, so `bench` cannot run there.
#
# A target rather than a bare `go test` in the caller, because the generator lives in a test (this
# repository keeps no developer tools) and so needs GOEXPERIMENT and the shape's build tags, both of which
# this file is the one place that sets:
#
#   make synthetic-net NET=synthetic-net.bin
#   tools/new-shape-signature.sh --hash 64 --net synthetic-net.bin ./pawnstar-go
# NET goes through $(abspath): `go test` runs a test in its own PACKAGE directory, so a relative path
# quietly lands in engine/nnue/ instead of where the caller asked for it. On Windows abspath yields
# C:/..., which is what a native Go binary needs - an MSYS /c/... path it cannot open at all.
NET ?= synthetic-net.bin
synthetic-net:
	$(GO) test $(GOTAGS) ./engine/nnue -run TestWriteSyntheticNet -v -count=1
synthetic-net: export PAWNSTAR_WRITE_SYNTHETIC_NET = $(abspath $(NET))

# Opening-book gate (questionable-move filtering).
book:
	$(GO) test $(GOTAGS) $(PKG) -run TestOpeningBook -v

# Lazy SMP smoke test (multi-threaded search sanity).
lazysmp:
	$(GO) test $(GOTAGS) $(PKG) -run TestLazySMP -v

# The concurrency tests under the race detector — the data-race-freedom claims (lockless TT/eval
# cache sharing, the ponder clock atomics) are only actually verified when -race is on. -race needs cgo, which go turns
# off where it finds no C compiler or the environment says CGO_ENABLED=0, so it is switched on here for this target.
# The C compiler is go's CC: the system's cc on Linux and macOS; on Windows MSYS2's CLANG64 clang, which
# tools/setup-dev.ps1 installs and records with `go env -w CC=...` (the Windows CI job passes CC itself).
race: export CGO_ENABLED := 1
race:
	$(GO) test $(GOTAGS) $(PKG) -race -run 'TestLazySMP|TestPonderRace|TestFixedTimeDeadlineFires' -v

# Chess-clock / time-management tests (budget, deadline firing, defaults, ponder race).
timing:
	$(GO) test $(GOTAGS) $(PKG) -run 'TestTimeControlDefaults|TestComputeBudget|TestPonderRace|TestFixedTimeDeadlineFires' -v

# UCI protocol regression test — drives the built binary as a UCI process (handshake, seldepth,
# searchmoves, currmove, bench determinism, ponder protocol). Lives in the engine package
# ([engine/uci_test.go](engine/uci_test.go)) but builds the binary itself (lazily), so no `build`
# prerequisite is needed; -short skips it, and other engine tests never trigger the build.
test-uci:
	$(GO) test $(GOTAGS) $(PKG) -run 'TestUCIProtocol|TestUCIBenchDeterminism' -v

bench:
	$(GO) test $(GOTAGS) $(PKG) -bench BenchmarkPerft7 -benchtime=10s -run '^$$'

vet:
	$(GO) vet $(GOTAGS) $(PKG) .

fmt:
	$(GO) fmt ./...

# The documentation bundle a release ships, pawnstar-go-<version>-docs.zip: the markdown docs plus `go doc -all` for the
# engine and engine/nnue packages, each twice - *-api.txt (the exported API) and *-internals.txt (-u, every documented
# internal function and type).
docs-bundle:
	rm -rf docs-bundle && mkdir -p docs-bundle
	cp README.md CLAUDE.md THIRD-PARTY-NOTICES.md docs-bundle/
	$(GO) doc $(GOTAGS) -all ./engine > docs-bundle/engine-api.txt
	$(GO) doc $(GOTAGS) -all -u ./engine > docs-bundle/engine-internals.txt
	$(GO) doc $(GOTAGS) -all ./engine/nnue > docs-bundle/nnue-api.txt
	$(GO) doc $(GOTAGS) -all -u ./engine/nnue > docs-bundle/nnue-internals.txt
	rm -f $(BINARY)-$(VERSION)-docs.zip
	cd docs-bundle && zip -rq ../$(BINARY)-$(VERSION)-docs.zip .
	rm -rf docs-bundle

# Snapshot the current build as the SPRT baseline opponent (tools/sprt.sh BIN_B default).
baseline: build
	cp $(ALIAS) $(BASELINE)
	@echo "baseline snapshot -> $(BASELINE)"

clean:
	$(GO) clean
	rm -f $(ALIAS) $(BINARY)_[0-9]*_[0-9]*_[0-9]* $(BASELINE) coverage.out
