#include "textflag.h"

// func prefetchAddr(p unsafe.Pointer)
// PREFETCHT2: prefetch into L2/L3 with low temporal locality — matches the C++ engine's
// __builtin_prefetch(addr, 0, 1) (read, locality 1). NOSPLIT leaf, no stack frame.
TEXT ·prefetchAddr(SB), NOSPLIT|NOFRAME, $0-8
	MOVQ p+0(FP), AX
	PREFETCHT2 (AX)
	RET
