// Package trust defines the Store contract for managing a local Certificate
// Authority in the operating system's trust store. The overcast host CLI
// uses it to install a CA once, mint short-lived leaf certificates for
// registered custom domains, and serve HTTPS that the user's browser and
// HTTP clients accept without the usual self-signed-cert dance.
//
// The interface mirrors the shape of smallstep/truststore so that the
// concrete backend — added in step 3 — is a thin wrapper over it. Keeping
// the interface small and platform-agnostic means the host CLI never
// imports the backend directly.
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

// New returns the trust Store for the current platform. If no backend is
// available it returns ErrUnsupported.
func New(log *zap.Logger) (Store, error) { return newStore(log) }
