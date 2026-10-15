package engine

// version is the engine's version, MAJOR.MINOR.BUILD, reported to the UCI host as "id name Pawnstar-Go <version>".
// It comes from git alone, the rule every reckless-technology project follows (tools/version.sh): MAJOR.MINOR is the
// nearest annotated tag vMAJOR.MINOR reachable from the commit built, BUILD the number of commits since it. The
// Makefile and the workflows inject it at link time, -ldflags "-X pawnstar-go/engine.version=<version>"; a plain
// `go build` or `go test`, with no ldflags, reports 0.0.0. Nothing here is edited to release: tag main and push the tag.
var version = "0.0.0"

// versionString returns the engine version as "major.minor.build" (e.g. "1.0.5").
func versionString() string {
	return version
}
