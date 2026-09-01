package main

// cmd_env.go — `overcast env`. Prints AWS_* environment-variable
// assignments for the shell to eval, so a user can point any AWS SDK or the
// AWS CLI at a running overcast without hand-copying the endpoint and dummy
// credentials every time:
//
//	eval "$(overcast env)"               # sh / bash / zsh
//	overcast env --shell powershell | iex
//	overcast env --shell fish | source
//
// Alongside the exports, the output starts with an unset line for every
// AWS_* variable currently exported in the calling shell that this command
// does not itself manage — AWS_PROFILE, AWS_SESSION_TOKEN, a per-service
// AWS_ENDPOINT_URL_<SERVICE> (which would outrank the global AWS_ENDPOINT_URL
// in the SDK precedence chain), and the like. The list is exact rather than
// guessed: this process inherits the caller's exported environment, so it
// sees precisely what needs clearing. Same safety property as `overcast aws`
// and scripts/awslocal.sh — after eval'ing this, nothing left over in the
// shell can redirect or re-sign an AWS call — delivered as visible,
// targeted statements rather than a blanket in-process scrub.
//
// sh and fish lines end with ';' so the output also survives an unquoted
// `eval $(overcast env)`, where command substitution collapses newlines to
// spaces and the statements would otherwise run together.

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// envEnviron is the source of the calling shell's exported environment,
// swappable in tests so ambient AWS_* variables on the machine running the
// suite cannot leak into asserted output.
var envEnviron = os.Environ

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
			for _, name := range conflictingAWSVars(envEnviron(), vars) {
				fmt.Fprintln(out, formatUnsetVar(format, name))
			}
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

// conflictingAWSVars returns, sorted, the names of every AWS_* variable in
// environ that managed does not overwrite — the ones an eval of this
// command's output must unset so they cannot keep influencing AWS calls.
// A variable exported empty (KEY=) is skipped: it is already inert, and
// skipping it keeps the common case's output free of noise.
func conflictingAWSVars(environ []string, managed []envVar) []string {
	managedNames := make(map[string]bool, len(managed))
	for _, v := range managed {
		managedNames[v.key] = true
	}
	seen := make(map[string]bool)
	var names []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" || !strings.HasPrefix(name, "AWS_") {
			continue
		}
		if managedNames[name] || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// formatEnvVar renders one assignment in the target shell's syntax. sh and
// fish statements are ';'-terminated — see the file comment.
func formatEnvVar(format envShellFormat, v envVar) string {
	switch format {
	case envShellPowerShell:
		return fmt.Sprintf(`$env:%s = "%s"`, v.key, escapePowerShellDouble(v.value))
	case envShellFish:
		return fmt.Sprintf("set -gx %s '%s';", v.key, escapeSingleQuotedPOSIX(v.value))
	default: // envShellSh
		return fmt.Sprintf("export %s='%s';", v.key, escapeSingleQuotedPOSIX(v.value))
	}
}

// formatUnsetVar renders one variable removal in the target shell's syntax.
// The PowerShell form tolerates the variable not existing (it always exists
// when emitted, but iex'ing stale output must not error).
func formatUnsetVar(format envShellFormat, name string) string {
	switch format {
	case envShellPowerShell:
		return fmt.Sprintf(`Remove-Item Env:\%s -ErrorAction SilentlyContinue`, name)
	case envShellFish:
		return fmt.Sprintf("set -e %s;", name)
	default: // envShellSh
		return fmt.Sprintf("unset %s;", name)
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
