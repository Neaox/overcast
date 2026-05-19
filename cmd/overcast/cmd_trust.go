package main

// cmd_trust.go — `overcast trust install|uninstall|status`. Thin wrappers
// around internal/hostbridge/trust so the user never has to touch the
// underlying API. On platforms without a trust backend the commands report
// ErrUnsupported and exit cleanly — there's nothing we can do from here.

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
		Short: "Manage the local overcast certificate authority in the system trust store",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Install the overcast CA into the system trust store",
			RunE:  runTrust(func(s trust.Store, cmd *cobra.Command) error { return s.Install(cmd.Context()) }),
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove the overcast CA from the system trust store",
			RunE:  runTrust(func(s trust.Store, cmd *cobra.Command) error { return s.Uninstall(cmd.Context()) }),
		},
		&cobra.Command{
			Use:   "status",
			Short: "Report whether the overcast CA is installed",
			RunE: runTrust(func(s trust.Store, cmd *cobra.Command) error {
				ok, err := s.Installed(cmd.Context())
				if err != nil {
					return err
				}
				if ok {
					fmt.Fprintln(cmd.OutOrStdout(), "overcast CA: installed")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "overcast CA: not installed")
				}
				return nil
			}),
		},
	)
	return cmd
}

func runTrust(fn func(trust.Store, *cobra.Command) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		log, err := zap.NewDevelopment()
		if err != nil {
			return err
		}
		defer func() { _ = log.Sync() }()

		store, err := trust.New(log)
		if err != nil {
			if errors.Is(err, trust.ErrUnsupported) {
				return fmt.Errorf("no trust-store backend available on this platform yet")
			}
			return err
		}
		return fn(store, cmd)
	}
}
