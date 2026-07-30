package main

// trust_target.go — shared plumbing for `overcast trust` and `overcast
// https`: which CA directory a command operates on (the locally-minted CA,
// or the cached copy of a remote daemon's CA when --endpoint was passed),
// and the safety gate on fetching trust roots over the network.

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// trustTarget names the CA a trust/https command operates on.
type trustTarget struct {
	// caDir holds (or will hold) the CA certificate.
	caDir string
	// endpoint is the daemon origin the CA belongs to; empty for the
	// locally-minted CA.
	endpoint string
}

func (tt trustTarget) remote() bool { return tt.endpoint != "" }

// resolveTrustTarget decides local vs remote from the --endpoint flag.
// The flag is registered persistently on the root command with a default
// value, so only an explicitly *passed* --endpoint selects the remote flow —
// `overcast trust install` alone keeps meaning the local CA.
func resolveTrustTarget(cmd *cobra.Command, dataDir string) trustTarget {
	f := cmd.Flag("endpoint")
	if f == nil || !f.Changed {
		return trustTarget{caDir: trust.DirFor(dataDir)}
	}
	ep := strings.TrimRight(f.Value.String(), "/")
	return trustTarget{caDir: trust.RemoteDirFor(dataDir, ep), endpoint: ep}
}

// remoteCAGate is the safety check in front of fetching a trust root:
// loopback endpoints (the Docker case — the daemon is on this machine) pass
// silently; anything else needs the user's explicit --trust-remote
// acknowledgement, because installing a root CA fetched from another host
// hands that host the ability to impersonate any TLS site to this machine.
func remoteCAGate(endpoint string, trustRemote bool) error {
	loop, err := trust.LoopbackEndpoint(endpoint)
	if err != nil {
		return err
	}
	if loop || trustRemote {
		return nil
	}
	return fmt.Errorf(
		"refusing to fetch a trust root from non-loopback endpoint %s: installing a CA from another host is a trust decision that must be explicit — re-run with --trust-remote if you control that daemon",
		endpoint)
}

// fetchAndCacheRemoteCA fetches the daemon's CA certificate, validates it,
// and caches it under the target directory so status/uninstall (and a later
// re-install) find it without another fetch. Gated by remoteCAGate.
func fetchAndCacheRemoteCA(ctx context.Context, tt trustTarget, trustRemote bool) error {
	if err := remoteCAGate(tt.endpoint, trustRemote); err != nil {
		return err
	}
	pemBytes, _, err := trust.FetchRemoteCA(ctx, tt.endpoint)
	if err != nil {
		return err
	}
	return trust.SaveRemoteCA(tt.caDir, pemBytes)
}

// loadDataDir resolves the data dir the same way the daemon does, so the CLI
// and `overcast serve` always operate on the same CA locations.
func loadDataDir() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}
