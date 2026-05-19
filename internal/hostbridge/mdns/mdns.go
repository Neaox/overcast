// Package mdns defines the Publisher contract used by the overcast host CLI
// to advertise custom domain names through the operating system's multicast
// DNS responder (Bonjour on macOS, Avahi on Linux, DNS-SD on Windows).
//
// The interface is intentionally small: real backends live behind it in
// build-tagged files and are selected at compile time via New. Callers — in
// practice the hostbridge package — depend only on the interface and are
// therefore platform-agnostic. Adding a new backend is an additive change
// that does not touch any consumer.
package mdns

import (
	"context"
	"errors"
	"net"

	"go.uber.org/zap"
)

// ErrUnsupported is returned by New on platforms that do not yet have an
// mDNS backend wired up. Callers should treat it as a soft failure and fall
// back to a hosts-file synchroniser or similar.
var ErrUnsupported = errors.New("mdns: no backend available on this platform")

// Record is a single mDNS A-record advertisement — a hostname under the
// .local TLD paired with the address it should resolve to.
type Record struct {
	// Hostname is the fully-qualified name to advertise, e.g. "api.myapp.local".
	Hostname string
	// IP is the address the hostname should resolve to. For overcast this is
	// almost always 127.0.0.1 (the forwarded container port on the host).
	IP net.IP
}

// Key returns the deduplication key for a Record. Two records with the same
// key refer to the same logical advertisement; publishing a record whose key
// already exists in the active set is treated as a replace.
func (r Record) Key() string { return r.Hostname }

// Publisher advertises Records via the host's mDNS responder.
//
// Implementations must be safe for concurrent use. Publish and Unpublish are
// expected to be idempotent: publishing an already-published record, or
// unpublishing one that was never published, must not return an error.
// Close releases any resources held by the publisher and unpublishes any
// records that are still active.
type Publisher interface {
	Publish(ctx context.Context, r Record) error
	Unpublish(ctx context.Context, r Record) error
	Close() error
}

// New returns the mDNS Publisher for the current platform. If no backend is
// available it returns ErrUnsupported; callers decide whether to fall back
// to another mechanism or fail hard.
//
// The concrete implementation is selected at build time via build tags in
// the per-platform files (darwin.go, linux.go, windows.go).
func New(log *zap.Logger) (Publisher, error) { return newPublisher(log) }
