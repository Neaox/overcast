package state

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Diagnostic helper: prints per-phase SQLite open timings to stderr when
// OVERCAST_PROFILE_STARTUP=1. Zero cost when disabled. Not a public API.
var (
	sqlitePhaseEnabled = os.Getenv("OVERCAST_PROFILE_STARTUP") == "1"
	sqlitePhaseMu      sync.Mutex
	sqlitePhasePrev    = time.Now()
)

func markSQLitePhase(phase string) {
	if !sqlitePhaseEnabled {
		return
	}
	sqlitePhaseMu.Lock()
	defer sqlitePhaseMu.Unlock()
	now := time.Now()
	delta := now.Sub(sqlitePhasePrev)
	fmt.Fprintf(os.Stderr, "startup-profile\t  sqlite: %-24s\t+%6.2fms\n",
		phase, float64(delta.Microseconds())/1000)
	sqlitePhasePrev = now
}
