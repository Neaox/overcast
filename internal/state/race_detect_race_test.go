//go:build race

package state_test

// raceDetectorEnabled is true when the test binary was built with `go test
// -race`. Some pressure-handling regression tests assert a latency bound
// meant to catch a real starvation regression (see
// hybrid_pressure_test.go's TestHybridStore_PacedWriteLoadDoesNotStarveReads)
// — the race detector's per-memory-access instrumentation adds a large,
// fairly constant multiplier to every operation, so a bound tuned for a
// normal build would be flaky under `-race` for reasons that have nothing
// to do with storage pressure. Tests that need to know need a looser bound
// under `-race` check this constant instead of hardcoding two magic
// numbers inline.
const raceDetectorEnabled = true
