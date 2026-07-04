package engine

import "unsafe"

// prefetchAddr issues a hardware prefetch (PREFETCHT2 — read, low temporal locality, the x86
// equivalent of C++ __builtin_prefetch(addr, 0, 1)) for the cache line containing p. Implemented in
// prefetch_amd64.s. A pure hint: it changes no architectural state and cannot affect results.
func prefetchAddr(p unsafe.Pointer)
