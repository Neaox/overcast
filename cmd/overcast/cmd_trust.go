package main

// cmd_trust.go — `overcast trust install|uninstall|status`. Thin wrappers
// around internal/hostbridge/trust so the user never has to touch the
// underlying API; `overcast https` builds on the same pieces. Passing
// --endpoint targets a running daemon's CA (fetched from /_overcast/ca.pem
// and cached locally — the Docker flow) instead of the locally-minted one.
// On platforms without a trust backend the commands report ErrUnsupported
// and exit cleanly — there's nothing we can do from here.

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

func newTrustCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Manage the overcast certificate authority in the system trust store",
	}
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the overcast CA into the system trust store (creates it if missing; --endpoint fetches a daemon's instead)",
		RunE: runTrust(func(s trust.Store, cmd *cobra.Command, tt trustTarget) error {
			if tt.remote() {
				trustRemote, _ := cmd.Flags().GetBool("trust-remote")
				if err := fetchAndCacheRemoteCA(cmd.Context(), tt, trustRemote); err != nil {
					return err
				}
			} else if _, err := trust.LoadOrCreateCA(tt.caDir); err != nil {
				return err
			}
			return s.Install(cmd.Context())
		}),
	}
	install.Flags().Bool("trust-remote", false,
		"acknowledge installing a CA fetched from a NON-loopback --endpoint (a trust decision: that host could then impersonate any TLS site to this machine)")
	cmd.AddCommand(
		install,
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove the overcast CA from the system trust store",
			RunE: runTrust(func(s trust.Store, cmd *cobra.Command, _ trustTarget) error {
				return s.Uninstall(cmd.Context())
			}),
		},
		&cobra.Command{
			Use:   "status",
			Short: "Report whether the overcast CA is installed",
			RunE: runTrust(func(s trust.Store, cmd *cobra.Command, tt trustTarget) error {
				ok, err := s.Installed(cmd.Context())
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if tt.remote() {
					fmt.Fprintln(out, "overcast CA source:", tt.endpoint, "(fetched)")
				}
				fmt.Fprintln(out, "overcast CA directory:", tt.caDir)
				if ok {
					fmt.Fprintln(out, "overcast CA: installed")
				} else {
					fmt.Fprintln(out, "overcast CA: not installed")
				}
				return nil
			}),
		},
	)
	return cmd
}

func runTrust(fn func(trust.Store, *cobra.Command, trustTarget) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		log, err := zap.NewDevelopment()
		if err != nil {
			return err
		}
		defer func() { _ = log.Sync() }()

		// The CLI and `overcast serve` must resolve the same data dir so
		// they always operate on the same CA locations.
		cfg, err := loadDataDir()
		if err != nil {
			return err
		}
		tt := resolveTrustTarget(cmd, cfg.DataDir)
		store, err := trust.New(log, tt.caDir)
		if err != nil {
			if errors.Is(err, trust.ErrUnsupported) {
				return fmt.Errorf("no trust-store backend available on this platform yet")
			}
			return err
		}
		return fn(store, cmd, tt)
	}
}
