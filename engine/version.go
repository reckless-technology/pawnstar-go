package engine

import "fmt"

// Engine version, reported to the UCI host as "id name Pawnstar-Go <major>.<minor>.<build>" (mirrors the C++
// engine's scheme). Major and minor are edited by hand here — keep them in sync with MAJOR/MINOR in the
// Makefile, which names the built binary pawnstar-go_<major>_<minor>_<build>. The build number is the git
// commit count (`git rev-list --count HEAD`), a globally-reproducible number identical for every clone at a
// given commit; it is injected at link time via `-ldflags "-X pawnstar-go/engine.versionBuild=<n>"` (the
// Makefile does this). It defaults to "0" for a plain `go build`/`go test` with no ldflags, matching the C++
// PAWNSTAR_BUILD_NUMBER default.
const (
	versionMajor = 0
	versionMinor = 1
)

var versionBuild = "0"

// versionString returns the engine version as "major.minor.build" (e.g. "0.1.141").
func versionString() string {
	return fmt.Sprintf("%d.%d.%s", versionMajor, versionMinor, versionBuild)
}
