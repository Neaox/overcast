//go:build !nosqlite

package config

// sqliteBuildSupported reports whether this binary was built with SQLite
// support (i.e. NOT built with -tags nosqlite — see
// internal/state/sqlite_hybrid_nosqlite.go). Used by resolveAutoState: the
// hybrid backend requires SQLite, so OVERCAST_STATE=auto must never resolve
// to hybrid in a nosqlite build — doing so would crash the process at
// startup (NewHybridStore returns a hard error in that build) purely because
// a volume happened to be mounted, which defeats the entire point of "auto
// never surprises you with a crash; worst case is memory".
const sqliteBuildSupported = true

// SQLiteSupported reports whether this binary was built with SQLite support
// (i.e. not built with -tags nosqlite). Exported so callers outside this
// package — e.g. tests that need to skip a hybrid/persistent/wal-specific
// assertion under a nosqlite build — can check it without reaching into
// unexported internals.
func SQLiteSupported() bool { return sqliteBuildSupported }
