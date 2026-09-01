package main

// cmd_https.go — `overcast https enable|disable|status`. The one-command
// HTTPS setup, in two flavours:
//
//   - Local (no --endpoint): create the local CA if missing, install it into
//     the system trust store (the OS asks the user to approve — that's
//     expected), mint or refresh the server certificate, and tell the user
//     the one line of configuration to add. Overcast is configured entirely
//     through environment variables (there is no persisted config file), so
//     the command cannot flip the mode on for future `overcast serve` runs
//     itself — echoing the exact line is the whole remaining step.
//
//   - Remote (--endpoint https://localhost:4566): the Docker flow. The daemon
//     minted its CA inside the container; fetch its *certificate* from
//     /_overcast/ca.pem, cache it locally, and install it into the trust
//     store — no shared volume, no per-OS certutil incantation. Loopback
//     endpoints only, unless --trust-remote acknowledges the trust decision.
//
// `overcast trust install|uninstall|status` remain the lower-level pieces;
// disable/status here delegate to the same backend.

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/hostbridge/trust"
)

func newHTTPSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "https",
		Short: "Set up browser-trusted HTTPS (and HTTP/2) for the API and web UI",
	}
	enable := &cobra.Command{
		Use:   "enable",
		Short: "Create (or fetch) the overcast CA, install it into the system trust store, and mint the server certificate",
		Long: `Performs the full HTTPS setup in one shot:

  1. Creates the overcast local CA if it does not exist yet — or, with
     --endpoint, fetches the CA certificate from a running daemon (a Docker
     container, typically) so no shared volume is needed.
  2. Installs the CA certificate into the system trust store — the OS will
     ask you to approve this; approving is the only manual step.
  3. Mints (or refreshes) the server certificate covering localhost,
     localhost.overcast.sh, *.localhost.overcast.sh, and every other name
     overcast advertises. (Skipped with --endpoint: the daemon mints its own.)

Safe to re-run: every step is idempotent.

Docker golden path — create the CA here, then let the container borrow it, so
recreating the container never costs you another trust prompt:

  overcast https enable
  docker run -e OVERCAST_TLS=auto -e OVERCAST_CA_DIR=/ca \
    -v ~/.overcast/data/ca:/ca:ro -p 4566:4566 -p 4567:4567 ghcr.io/overcast-sh/overcast:alpha

No overcast CLI on the host? The daemon can mint its own CA and serve the
certificate for --endpoint to install instead:

  overcast https enable --endpoint https://localhost:4566

Overcast is configured via environment variables only, so for a local daemon
finish by setting OVERCAST_TLS=auto (the command prints the exact line).`,
		RunE: runHTTPSEnable,
	}
	enable.Flags().Bool("trust-remote", false,
		"acknowledge installing a CA fetched from a NON-loopback --endpoint (a trust decision: that host could then impersonate any TLS site to this machine)")
	cmd.AddCommand(
		enable,
		&cobra.Command{
			Use:   "disable",
			Short: "Remove the overcast CA from the system trust store",
			Long: `Removes the overcast CA certificate from the system trust store — the local
one by default, or a fetched daemon CA when --endpoint names the same daemon
it was fetched from. Key material and cached certificates on disk are kept,
so a later enable re-uses them.

Also unset OVERCAST_TLS for the daemon to go back to plain HTTP.`,
			RunE: runHTTPSDisable,
		},
		&cobra.Command{
			Use:   "status",
			Short: "Report the HTTPS setup state",
			RunE:  runHTTPSStatus,
		},
	)
	return cmd
}

// httpsStore builds the trust store for the CA the command targets (local,
// or a remote daemon's when --endpoint was passed) and resolves config once.
func httpsStore(cmd *cobra.Command) (trust.Store, *config.Config, trustTarget, error) {
	cfg, err := loadDataDir()
	if err != nil {
		return nil, nil, trustTarget{}, err
	}
	log, err := zap.NewDevelopment()
	if err != nil {
		return nil, nil, trustTarget{}, err
	}
	tt := resolveTrustTarget(cmd, cfg)
	store, err := trust.New(log, tt.caDir)
	if err != nil {
		if errors.Is(err, trust.ErrUnsupported) {
			return nil, nil, trustTarget{}, fmt.Errorf("no trust-store backend available on this platform yet")
		}
		return nil, nil, trustTarget{}, err
	}
	return store, cfg, tt, nil
}

func runHTTPSEnable(cmd *cobra.Command, _ []string) error {
	store, cfg, tt, err := httpsStore(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	trustRemote, _ := cmd.Flags().GetBool("trust-remote")

	// 1. Make the CA certificate exist locally: mint it (local flow) or
	// fetch it from the daemon (remote flow).
	if tt.remote() {
		if err := fetchAndCacheRemoteCA(cmd.Context(), tt, trustRemote); err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ fetched the CA certificate from %s\n", tt.endpoint)
	} else if _, err := trust.LoadOrCreateCA(tt.caDir); err != nil {
		return err
	}

	// 2. Install it into the system trust store (idempotent).
	alreadyInstalled, err := store.Installed(cmd.Context())
	if err != nil {
		return err
	}
	if err := store.Install(cmd.Context()); err != nil {
		return err
	}
	if alreadyInstalled {
		fmt.Fprintln(out, "✓ overcast CA already installed in the system trust store")
	} else {
		fmt.Fprintln(out, "✓ overcast CA installed into the system trust store")
	}

	if tt.remote() {
		fmt.Fprintf(out, `
This machine now trusts the daemon at %s.
Open the web console at

  https://localhost.overcast.sh:%d

(If the daemon was started without OVERCAST_TLS=auto, set it and restart.)
`, tt.endpoint, defaultUIPort)
		return nil
	}

	// 3. Local flow only: mint or refresh the server certificate for every
	// advertised name (a remote daemon mints its own at startup).
	if _, _, err := trust.ServerCertificate(tt.caDir, cfg.TLSAutoSANs()); err != nil {
		return err
	}
	fmt.Fprintln(out, "✓ server certificate ready (covers localhost, localhost.overcast.sh, *.localhost.overcast.sh, ...)")
	fmt.Fprintln(out, "  CA directory:", tt.caDir)

	// 4. Overcast has no persisted config file — everything is environment
	// variables — so the one remaining step is the user's.
	//
	// The Docker line mounts this CA read-only rather than letting the
	// container mint its own. A container's CA dies with the container, and
	// re-installing a trust root on every recreation is not a workflow; the
	// machine already owns a CA at this point, so containers should borrow it
	// and mint leaves. Read-only because a trust anchor is the host's.
	fmt.Fprintf(out, `
HTTPS is set up. Start (or restart) the daemon with TLS enabled:

  OVERCAST_TLS=auto overcast serve

In Docker, share this CA so container recreation never invalidates the trust
install you just approved:

  docker run -e OVERCAST_TLS=auto \
    -e OVERCAST_CA_DIR=/ca -v %s:/ca:ro \
    -p 4566:4566 -p %d:%d ghcr.io/overcast-sh/overcast:alpha

Then open

  https://localhost.overcast.sh:%d

`, tt.caDir, defaultUIPort, defaultUIPort, defaultUIPort)
	return nil
}

func runHTTPSDisable(cmd *cobra.Command, _ []string) error {
	store, _, tt, err := httpsStore(cmd)
	if err != nil {
		return err
	}
	if err := store.Uninstall(cmd.Context()); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if tt.remote() {
		fmt.Fprintf(out, "✓ CA for %s removed from the system trust store (the cached certificate is kept)\n", tt.endpoint)
		return nil
	}
	fmt.Fprintln(out, "✓ overcast CA removed from the system trust store (key material on disk is kept)")
	fmt.Fprintln(out, "  Unset OVERCAST_TLS to serve plain HTTP again.")
	return nil
}

func runHTTPSStatus(cmd *cobra.Command, _ []string) error {
	store, cfg, tt, err := httpsStore(cmd)
	if err != nil {
		return err
	}
	installed, err := store.Installed(cmd.Context())
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if tt.remote() {
		fmt.Fprintln(out, "CA source:          ", tt.endpoint, "(fetched)")
	} else {
		fmt.Fprintln(out, "CA source:           local")
	}
	fmt.Fprintln(out, "CA directory:       ", tt.caDir)
	if installed {
		fmt.Fprintln(out, "system trust store:  installed")
	} else if tt.remote() {
		fmt.Fprintf(out, "system trust store:  not installed (run `overcast https enable --endpoint %s`)\n", tt.endpoint)
	} else {
		fmt.Fprintln(out, "system trust store:  not installed (run `overcast https enable`)")
	}
	switch {
	case cfg.TLSAuto():
		fmt.Fprintln(out, "OVERCAST_TLS:        auto (daemon serves HTTPS)")
	case cfg.TLSEnabled():
		fmt.Fprintln(out, "OVERCAST_TLS:        explicit cert/key via OVERCAST_TLS_CERT/OVERCAST_TLS_KEY")
	default:
		fmt.Fprintln(out, "OVERCAST_TLS:        not set (daemon serves plain HTTP)")
	}
	return nil
}
