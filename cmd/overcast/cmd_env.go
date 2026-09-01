package main

// cmd_env.go — `overcast env`. Prints AWS_* environment-variable
// assignments for the shell to eval, so a user can point any AWS SDK or the
// AWS CLI at a running overcast without hand-copying the endpoint and dummy
// credentials every time:
//
//	eval $(overcast env)                 # sh / bash / zsh
//	overcast env --shell powershell | iex
//	overcast env --shell fish | source
//
// scripts/awslocal.sh and scripts/awslocal.ps1 do the same job for a single
// invocation of the AWS CLI, restoring the shell afterwards. This command is
// the other end of that spectrum: it changes nothing itself and leaves the
// caller's shell holding the exports for as long as they want them.

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print AWS environment exports for pointing tools at overcast",
		Args:  cobra.NoArgs,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			region, _ := cmd.Flags().GetString("region")
			shell, _ := cmd.Flags().GetString("shell")

			region = resolveEnvRegion(region)
			format, err := resolveEnvShell(shell)
			if err != nil {
				return err
			}

			vars := []envVar{
				{"AWS_ENDPOINT_URL", endpoint},
				{"AWS_ACCESS_KEY_ID", "test"},
				{"AWS_SECRET_ACCESS_KEY", "test"},
				{"AWS_DEFAULT_REGION", region},
				{"AWS_REGION", region},
				{"AWS_EC2_METADATA_DISABLED", "true"},
			}

			out := cmd.OutOrStdout()
			for _, v := range vars {
				fmt.Fprintln(out, formatEnvVar(format, v))
			}
			return nil
		},
	}
	cmd.Flags().String("region", "", "AWS region to export (default: $OVERCAST_REGION, else us-east-1)")
	cmd.Flags().String("shell", "auto", "output format: auto, sh, powershell, or fish")
	_ = cmd.RegisterFlagCompletionFunc("shell", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"sh", "powershell", "fish"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// envVar is one KEY=value assignment awaiting shell-specific formatting.
type envVar struct {
	key   string
	value string
}

// resolveEnvRegion applies the --region > $OVERCAST_REGION > us-east-1
// precedence shared with scripts/awslocal.sh and scripts/awslocal.ps1.
func resolveEnvRegion(flagRegion string) string {
	if flagRegion != "" {
		return flagRegion
	}
	if envRegion := os.Getenv("OVERCAST_REGION"); envRegion != "" {
		return envRegion
	}
	return "us-east-1"
}

// envShellFormat is the resolved (non-"auto") output format.
type envShellFormat string

const (
	envShellSh         envShellFormat = "sh"
	envShellPowerShell envShellFormat = "powershell"
	envShellFish       envShellFormat = "fish"
)

// resolveEnvShell turns the --shell flag value into a concrete format,
// resolving "auto" by runtime.GOOS the way a user's default shell would
// split along that same line.
func resolveEnvShell(shell string) (envShellFormat, error) {
	if shell == "auto" {
		if runtime.GOOS == "windows" {
			return envShellPowerShell, nil
		}
		return envShellSh, nil
	}
	switch envShellFormat(shell) {
	case envShellSh, envShellPowerShell, envShellFish:
		return envShellFormat(shell), nil
	default:
		return "", fmt.Errorf("unknown --shell %q: want auto, sh, powershell, or fish", shell)
	}
}

// formatEnvVar renders one assignment in the target shell's syntax.
func formatEnvVar(format envShellFormat, v envVar) string {
	switch format {
	case envShellPowerShell:
		return fmt.Sprintf(`$env:%s = "%s"`, v.key, escapePowerShellDouble(v.value))
	case envShellFish:
		return fmt.Sprintf("set -gx %s '%s'", v.key, escapeSingleQuotedPOSIX(v.value))
	default: // envShellSh
		return fmt.Sprintf("export %s='%s'", v.key, escapeSingleQuotedPOSIX(v.value))
	}
}

// escapeSingleQuotedPOSIX escapes a value for embedding inside single quotes
// in sh/fish: end the quote, emit an escaped literal quote, reopen it.
func escapeSingleQuotedPOSIX(value string) string {
	return strings.ReplaceAll(value, `'`, `'\''`)
}

// escapePowerShellDouble escapes a value for embedding inside a
// double-quoted PowerShell string: backtick is the escape character, and it
// must be escaped before the characters it introduces.
func escapePowerShellDouble(value string) string {
	value = strings.ReplaceAll(value, "`", "``")
	value = strings.ReplaceAll(value, `"`, "`\"")
	return value
}
