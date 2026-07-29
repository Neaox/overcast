// Package trust owns the Overcast local Certificate Authority: minting it,
// signing leaf server certificates from it (see ca.go), and installing the
// CA certificate into the operating system's trust store so browsers and
// HTTP clients accept Overcast's HTTPS without the usual self-signed-cert
// dance. `overcast trust install|uninstall|status` and `OVERCAST_TLS=auto`
// both hang off this package, and off the same key material.
//
// The Store interface mirrors the shape of smallstep/truststore; the
// concrete backends shell out to each platform's own tooling (certutil,
// security, update-ca-certificates) so no extra dependency is needed.
package trust

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

// ErrUnsupported is returned by New on platforms or build configurations
// that do not yet have a trust-store backend wired up.
var ErrUnsupported = errors.New("trust: no backend available on this platform")

// Store manages a local Certificate Authority in the system trust store.
//
// Implementations must be safe for concurrent use. All methods must be
// idempotent: installing an already-installed CA, or uninstalling one that
// was never installed, must succeed without error.
type Store interface {
	// Install adds the local CA to the system trust store. On first run it
	// generates the CA key pair; subsequent calls are a no-op.
	Install(ctx context.Context) error

	// Uninstall removes the local CA from the system trust store. The CA
	// key material on disk is preserved so that a later Install can reuse
	// it without invalidating previously-minted leaf certificates.
	Uninstall(ctx context.Context) error

	// Installed reports whether the local CA is currently present in the
	// system trust store.
	Installed(ctx context.Context) (bool, error)
}

// New returns the trust Store for the current platform, managing the CA
// persisted under caDir (see DirFor). If no backend is available it returns
// ErrUnsupported.
func New(log *zap.Logger, caDir string) (Store, error) { return newStore(log, caDir) }
