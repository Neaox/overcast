package boottime

import "time"

// GoStart is the earliest best-effort timestamp captured by Overcast Go code.
// It is approximate: packages that do not import this dependency-free package
// may initialize first, but the remaining pre-main Go init window is small.
var GoStart = time.Now()
