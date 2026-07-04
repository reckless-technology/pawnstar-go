package engine

import "unsafe"

// prefetchAddr issues a hardware prefetch (PRFM PLDL3KEEP — read, L3, retained; the arm64 equivalent
// of C++ __builtin_prefetch(addr, 0, 1)) for the cache line containing p. Implemented in
// prefetch_arm64.s. A pure hint: it changes no architectural state and cannot affect results.
func prefetchAddr(p unsafe.Pointer)
