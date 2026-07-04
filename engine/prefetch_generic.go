//go:build !amd64 && !arm64

package engine

import "unsafe"

// prefetchAddr is a no-op on architectures without a wired-up prefetch instruction: the TT probe just
// pays its cache miss, exactly as before. Correctness is identical everywhere; only speed differs.
func prefetchAddr(p unsafe.Pointer) { _ = p }
