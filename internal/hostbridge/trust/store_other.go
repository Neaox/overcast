//go:build !windows && !darwin && !linux

package trust

import "go.uber.org/zap"

// No trust-store backend on this platform. Certificate minting (ca.go) still
// works everywhere; only the system trust-store integration is missing.
func newStore(_ *zap.Logger, _ string) (Store, error) { return nil, ErrUnsupported }
