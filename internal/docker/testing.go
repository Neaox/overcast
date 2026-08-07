package docker

import "testing"

// SkipWithoutDocker calls t.Skip if Docker is not reachable on the default
// daemon socket. Use at the top of tests that need a running Docker daemon.
func SkipWithoutDocker(t *testing.T) {
	t.Helper()
	dc := NewClient("", nil)
	if err := dc.Ping(t.Context()); err != nil {
		t.Skipf("skipping: Docker is not available (%v)", err)
	}
}
