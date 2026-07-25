//go:build nosqlite

package config

// sqliteBuildSupported is false in a -tags nosqlite build — see
// sqlite_support.go's doc comment for why resolveAutoState needs this.
const sqliteBuildSupported = false

// SQLiteSupported reports whether this binary was built with SQLite support.
// See sqlite_support.go's copy of this function for the full doc comment —
// both files define it identically; build tags make them mutually exclusive.
func SQLiteSupported() bool { return sqliteBuildSupported }
