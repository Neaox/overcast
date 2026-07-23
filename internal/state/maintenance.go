//go:build !nosqlite

package state

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"
)

// defaultMaintenanceInterval is how often the background maintenance loop
// (3.5 in docs/storage-plan.md) runs routine SQLite housekeeping — a passive
// WAL checkpoint plus a conditional incremental vacuum. Shared by
// HybridStore (overridable via HybridOptions.MaintenanceInterval /
// OVERCAST_HYBRID_MAINTENANCE_INTERVAL) and SQLiteStore (fixed — see
// SQLiteStore.runMaintenance for why it doesn't get its own config knob).
const defaultMaintenanceInterval = 5 * time.Minute

// maintenanceVacuumFreelistRatio is the fraction of a database's total pages
// that must be free (per PRAGMA freelist_count / PRAGMA page_count) before
// the maintenance loop bothers running PRAGMA incremental_vacuum. Named
// rather than inlined so the threshold's meaning is documented at its
// definition instead of being a bare literal at the call site. 15% balances
// reclaiming meaningfully bloated databases against not doing needless I/O
// on a healthy one every cycle.
const maintenanceVacuumFreelistRatio = 0.15

// runSQLitePragmaMaintenance runs one routine background-maintenance pass
// against db: a passive WAL checkpoint (never blocks concurrent writers,
// unlike the TRUNCATE-mode checkpoint migrate.go's backupBeforeMigration
// uses once, before any writers exist yet — see that function's comment),
// then a conditional PRAGMA incremental_vacuum when the freelist ratio
// warrants it (see shouldVacuum). Shared by HybridStore.runMaintenance and
// SQLiteStore.runMaintenance so the pragma sequence and vacuum-gating logic
// live in exactly one place. Must only ever be called from a background
// goroutine — never from the request path.
func runSQLitePragmaMaintenance(ctx context.Context, db *sql.DB, log *zap.Logger, logSource string) {
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		logMaintenanceWarn(log, logSource, "wal_checkpoint(PASSIVE) failed", err)
		return
	}

	var freelistCount, pageCount int64
	if err := db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freelistCount); err != nil {
		logMaintenanceWarn(log, logSource, "freelist_count failed", err)
		return
	}
	if err := db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		logMaintenanceWarn(log, logSource, "page_count failed", err)
		return
	}
	if !shouldVacuum(freelistCount, pageCount) {
		return
	}

	start := time.Now()
	if _, err := db.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
		logMaintenanceWarn(log, logSource, "incremental_vacuum failed", err)
		return
	}
	if log != nil {
		log.Info(logSource+": incremental_vacuum complete",
			zap.Int64("freelist_count", freelistCount),
			zap.Int64("page_count", pageCount),
			zap.Duration("elapsed", time.Since(start)))
	}
}

// shouldVacuum reports whether a database's free-page ratio is high enough
// to justify running PRAGMA incremental_vacuum, per
// maintenanceVacuumFreelistRatio. A database with no pages yet (fresh/empty)
// never needs vacuuming. Extracted as a pure function so the gating logic is
// unit-testable without a real *sql.DB.
func shouldVacuum(freelistCount, pageCount int64) bool {
	if pageCount <= 0 {
		return false
	}
	return float64(freelistCount)/float64(pageCount) > maintenanceVacuumFreelistRatio
}

func logMaintenanceWarn(log *zap.Logger, source, msg string, err error) {
	if log != nil {
		log.Warn(source+": "+msg, zap.Error(err))
	}
}
