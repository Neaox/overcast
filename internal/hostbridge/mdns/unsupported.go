//go:build !darwin && !linux && !windows

package mdns

import "go.uber.org/zap"

// newPublisher is the fallback for platforms without a wired-up backend.
// It always returns ErrUnsupported so New can report a clean soft failure.
func newPublisher(_ *zap.Logger) (Publisher, error) { return nil, ErrUnsupported }
