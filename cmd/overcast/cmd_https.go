package main

// cmd_https.go — `overcast https enable|disable|status`. The one-command
// HTTPS setup: creates the local CA if missing, installs it into the system
// trust store (the OS asks the user to approve — that's expected), mints or
// refreshes the server certificate, and tells the user the one line of
// configuration to add. Overcast is configured entirely through environment
// variables (there is no persisted config file), so the command cannot flip
// the mode on for future `overcast serve` runs itself — echoing the exact
// line is the whole remaining step.
//
// `overcast trust install|uninstall|status` remain the lower-level pieces;
// disable/status here delegate to the same backend.

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

func newHTTPSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "https",
		Short: "Set up browser-trusted HTTPS (and HTTP/2) for the API and web UI",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Create the local CA, install it into the system trust store, and mint the server certificate",
			Long: `Performs the full HTTPS setup in one shot:

  1. Creates the overcast local CA if it does not exist yet.
  2. Installs the CA certificate into the system trust store — the OS will
     ask you to approve this; approving is the only manual step.
  3. Mints (or refreshes) the server certificate covering localhost,
     localhost.overcast.sh, *.localhost.overcast.sh, and every other name
     overcast advertises.

Safe to re-run: every step is idempotent.

Overcast is configured via environment variables only, so finish by setting
OVERCAST_TLS=auto for the daemon (the command prints the exact line).`,
			RunE: runHTTPSEnable,
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Remove the overcast CA from the system trust store",
			Long: `Removes the overcast CA certificate from the system trust store. The CA
key material on disk is kept, so a later enable re-uses it (and previously
minted certificates stay valid once the CA is re-installed).

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

// httpsStore builds the trust store and resolves the CA dir + config once.
func httpsStore() (trust.Store, *config.Config, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, "", fmt.Errorf("load config: %w", err)
	}
	log, err := zap.NewDevelopment()
	if err != nil {
		return nil, nil, "", err
	}
	caDir := trust.DirFor(cfg.DataDir)
	store, err := trust.New(log, caDir)
	if err != nil {
		if errors.Is(err, trust.ErrUnsupported) {
			return nil, nil, "", fmt.Errorf("no trust-store backend available on this platform yet")
		}
		return nil, nil, "", err
	}
	return store, cfg, caDir, nil
}

func runHTTPSEnable(cmd *cobra.Command, _ []string) error {
	store, cfg, caDir, err := httpsStore()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	// 1+2. Create the CA if missing and install it into the trust store.
	// Install is idempotent and reports "already installed" through its log.
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

	// 3. Mint or refresh the server certificate for every advertised name.
	if _, _, err := trust.ServerCertificate(caDir, cfg.TLSAutoSANs()); err != nil {
		return err
	}
	fmt.Fprintln(out, "✓ server certificate ready (covers localhost, localhost.overcast.sh, *.localhost.overcast.sh, ...)")
	fmt.Fprintln(out, "  CA directory:", caDir)

	// 4. Overcast has no persisted config file — everything is environment
	// variables — so the one remaining step is the user's.
	uiPort := defaultUIPort
	fmt.Fprintf(out, `
HTTPS is set up. Start (or restart) the daemon with TLS enabled:

  OVERCAST_TLS=auto overcast serve

or add OVERCAST_TLS=auto to your environment / docker run -e flags, then open

  https://localhost.overcast.sh:%d

`, uiPort)
	return nil
}

func runHTTPSDisable(cmd *cobra.Command, _ []string) error {
	store, _, _, err := httpsStore()
	if err != nil {
		return err
	}
	if err := store.Uninstall(cmd.Context()); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "✓ overcast CA removed from the system trust store (key material on disk is kept)")
	fmt.Fprintln(out, "  Unset OVERCAST_TLS to serve plain HTTP again.")
	return nil
}

func runHTTPSStatus(cmd *cobra.Command, _ []string) error {
	store, cfg, caDir, err := httpsStore()
	if err != nil {
		return err
	}
	installed, err := store.Installed(cmd.Context())
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "CA directory:      ", caDir)
	if installed {
		fmt.Fprintln(out, "system trust store: installed")
	} else {
		fmt.Fprintln(out, "system trust store: not installed (run `overcast https enable`)")
	}
	switch {
	case cfg.TLSAuto():
		fmt.Fprintln(out, "OVERCAST_TLS:       auto (daemon serves HTTPS)")
	case cfg.TLSEnabled():
		fmt.Fprintln(out, "OVERCAST_TLS:       explicit cert/key via OVERCAST_TLS_CERT/OVERCAST_TLS_KEY")
	default:
		fmt.Fprintln(out, "OVERCAST_TLS:       not set (daemon serves plain HTTP)")
	}
	return nil
}
