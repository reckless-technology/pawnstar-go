#include "textflag.h"

// func prefetchAddr(p unsafe.Pointer)
// PRFM PLDL3KEEP: prefetch for load into L3, temporal (retained) — matches the C++ engine's
// __builtin_prefetch(addr, 0, 1) (read, locality 1). NOSPLIT leaf, no stack frame.
TEXT ·prefetchAddr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD p+0(FP), R0
	PRFM (R0), PLDL3KEEP
	RET
