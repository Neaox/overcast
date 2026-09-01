package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newTestEnvRoot wires newEnvCmd() under a root carrying the persistent
// --endpoint flag the way main.go's real root does, since cmd_env.go reads
// it via cmd.Flags().GetString("endpoint") rather than owning it.
func newTestEnvRoot() *cobra.Command {
	root := &cobra.Command{Use: "overcast"}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newEnvCmd())
	return root
}

// stubEnviron pins the envEnviron seam for one test, so ambient AWS_*
// variables on the machine running the suite can't leak into asserted
// output.
func stubEnviron(t *testing.T, environ []string) {
	t.Helper()
	prev := envEnviron
	envEnviron = func() []string { return environ }
	t.Cleanup(func() { envEnviron = prev })
}

func runEnvCmd(t *testing.T, args ...string) string {
	t.Helper()
	root := newTestEnvRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"env"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("overcast env %v: %v", args, err)
	}
	return buf.String()
}

// TestEnvCmd_ShellFormats pins the exact output syntax for each explicit
// --shell value: the whole point of this command is that the caller can
// eval/iex/source it verbatim. sh and fish statements are ';'-terminated so
// even an unquoted `eval $(overcast env)` — where command substitution
// collapses newlines to spaces — evaluates each statement separately.
func TestEnvCmd_ShellFormats(t *testing.T) {
	stubEnviron(t, nil)
	cases := []struct {
		name  string
		shell string
		want  []string
	}{
		{
			name:  "sh",
			shell: "sh",
			want: []string{
				"export AWS_ENDPOINT_URL='http://localhost:4566';",
				"export AWS_ACCESS_KEY_ID='test';",
				"export AWS_SECRET_ACCESS_KEY='test';",
				"export AWS_DEFAULT_REGION='us-east-1';",
				"export AWS_REGION='us-east-1';",
				"export AWS_EC2_METADATA_DISABLED='true';",
			},
		},
		{
			name:  "powershell",
			shell: "powershell",
			want: []string{
				`$env:AWS_ENDPOINT_URL = "http://localhost:4566"`,
				`$env:AWS_ACCESS_KEY_ID = "test"`,
				`$env:AWS_SECRET_ACCESS_KEY = "test"`,
				`$env:AWS_DEFAULT_REGION = "us-east-1"`,
				`$env:AWS_REGION = "us-east-1"`,
				`$env:AWS_EC2_METADATA_DISABLED = "true"`,
			},
		},
		{
			name:  "fish",
			shell: "fish",
			want: []string{
				"set -gx AWS_ENDPOINT_URL 'http://localhost:4566';",
				"set -gx AWS_ACCESS_KEY_ID 'test';",
				"set -gx AWS_SECRET_ACCESS_KEY 'test';",
				"set -gx AWS_DEFAULT_REGION 'us-east-1';",
				"set -gx AWS_REGION 'us-east-1';",
				"set -gx AWS_EC2_METADATA_DISABLED 'true';",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runEnvCmd(t, "--shell", tc.shell)
			gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
			if len(gotLines) != len(tc.want) {
				t.Fatalf("got %d lines, want %d:\n%s", len(gotLines), len(tc.want), got)
			}
			for i, line := range gotLines {
				if line != tc.want[i] {
					t.Errorf("line %d: got %q, want %q", i, line, tc.want[i])
				}
			}
		})
	}
}

// TestEnvCmd_UnsetsConflictingVars verifies every AWS_* variable exported in
// the calling shell that the command does not manage gets an unset line,
// emitted before the exports and sorted — while managed variables are
// overwritten rather than unset, empty-valued exports are skipped as inert,
// and non-AWS variables are ignored entirely.
func TestEnvCmd_UnsetsConflictingVars(t *testing.T) {
	stubEnviron(t, []string{
		"AWS_SESSION_TOKEN=FwoGZXIvYXdzE...",
		"AWS_PROFILE=prod",
		"AWS_ENDPOINT_URL_DYNAMODB=https://dynamodb.real-aws.example",
		"AWS_REGION=ap-southeast-2", // managed: overwritten, must NOT be unset
		"AWS_EMPTY=",                // inert: no unset line
		"HOME=/home/dev",            // not AWS_*: ignored
	})

	cases := []struct {
		shell string
		want  []string
	}{
		{"sh", []string{
			"unset AWS_ENDPOINT_URL_DYNAMODB;",
			"unset AWS_PROFILE;",
			"unset AWS_SESSION_TOKEN;",
		}},
		{"powershell", []string{
			`Remove-Item Env:\AWS_ENDPOINT_URL_DYNAMODB -ErrorAction SilentlyContinue`,
			`Remove-Item Env:\AWS_PROFILE -ErrorAction SilentlyContinue`,
			`Remove-Item Env:\AWS_SESSION_TOKEN -ErrorAction SilentlyContinue`,
		}},
		{"fish", []string{
			"set -e AWS_ENDPOINT_URL_DYNAMODB;",
			"set -e AWS_PROFILE;",
			"set -e AWS_SESSION_TOKEN;",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			got := runEnvCmd(t, "--shell", tc.shell)
			gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
			if len(gotLines) != len(tc.want)+6 {
				t.Fatalf("got %d lines, want %d unsets + 6 exports:\n%s", len(gotLines), len(tc.want), got)
			}
			for i, want := range tc.want {
				if gotLines[i] != want {
					t.Errorf("unset line %d: got %q, want %q", i, gotLines[i], want)
				}
			}
			for _, line := range gotLines {
				if strings.Contains(line, "AWS_EMPTY") || strings.Contains(line, "HOME") {
					t.Errorf("unexpected line for inert/non-AWS variable: %q", line)
				}
			}
			joined := strings.Join(gotLines[len(tc.want):], "\n")
			if strings.Contains(joined, "unset AWS_REGION") || strings.Contains(joined, `Env:\AWS_REGION `) || strings.Contains(joined, "set -e AWS_REGION") {
				t.Errorf("managed variable AWS_REGION must be overwritten, not unset:\n%s", joined)
			}
		})
	}
}

// TestConflictingAWSVars_Dedup covers the pure collector directly: duplicate
// names (possible in a raw environ) appear once.
func TestConflictingAWSVars_Dedup(t *testing.T) {
	managed := []envVar{{"AWS_REGION", "us-east-1"}}
	got := conflictingAWSVars([]string{
		"AWS_PROFILE=a",
		"AWS_PROFILE=b",
		"AWS_CA_BUNDLE=/tmp/ca.pem",
	}, managed)
	want := []string{"AWS_CA_BUNDLE", "AWS_PROFILE"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestEnvCmd_AutoShell verifies --shell auto (the default) resolves per
// runtime.GOOS; this suite runs on Windows, so auto must match powershell.
func TestEnvCmd_AutoShell(t *testing.T) {
	stubEnviron(t, nil)
	auto := runEnvCmd(t)
	powershell := runEnvCmd(t, "--shell", "powershell")
	if auto != powershell {
		t.Errorf("auto output = %q, want it to match explicit --shell powershell = %q", auto, powershell)
	}
	if !strings.Contains(auto, `$env:AWS_ACCESS_KEY_ID = "test"`) {
		t.Errorf("auto output does not look like powershell format:\n%s", auto)
	}
}

// TestEnvCmd_UnknownShell verifies an unrecognized --shell value is
// rejected rather than silently falling back to a default.
func TestEnvCmd_UnknownShell(t *testing.T) {
	stubEnviron(t, nil)
	root := newTestEnvRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"env", "--shell", "cmd"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for --shell cmd, got nil")
	}
}

// TestEnvCmd_RegionPrecedence pins --region > $OVERCAST_REGION > us-east-1,
// the same precedence scripts/awslocal.sh and scripts/awslocal.ps1 apply to
// their own OVERCAST_REGION handling.
func TestEnvCmd_RegionPrecedence(t *testing.T) {
	stubEnviron(t, nil)
	t.Run("default when nothing set", func(t *testing.T) {
		got := runEnvCmd(t, "--shell", "sh")
		if !strings.Contains(got, "export AWS_REGION='us-east-1';") {
			t.Errorf("expected default region us-east-1, got:\n%s", got)
		}
	})

	t.Run("OVERCAST_REGION env var wins over default", func(t *testing.T) {
		t.Setenv("OVERCAST_REGION", "eu-west-1")
		got := runEnvCmd(t, "--shell", "sh")
		if !strings.Contains(got, "export AWS_REGION='eu-west-1';") {
			t.Errorf("expected region eu-west-1 from OVERCAST_REGION, got:\n%s", got)
		}
		if !strings.Contains(got, "export AWS_DEFAULT_REGION='eu-west-1';") {
			t.Errorf("expected AWS_DEFAULT_REGION eu-west-1 from OVERCAST_REGION, got:\n%s", got)
		}
	})

	t.Run("--region flag wins over OVERCAST_REGION", func(t *testing.T) {
		t.Setenv("OVERCAST_REGION", "eu-west-1")
		got := runEnvCmd(t, "--shell", "sh", "--region", "ap-southeast-2")
		if !strings.Contains(got, "export AWS_REGION='ap-southeast-2';") {
			t.Errorf("expected region ap-southeast-2 from --region flag, got:\n%s", got)
		}
	})
}

// TestEnvCmd_EndpointFlag confirms the persistent --endpoint flag (read the
// same way cmd_status.go reads it) reaches the AWS_ENDPOINT_URL line.
func TestEnvCmd_EndpointFlag(t *testing.T) {
	stubEnviron(t, nil)
	root := newTestEnvRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--endpoint", "http://localhost:4580", "env", "--shell", "sh"})
	if err := root.Execute(); err != nil {
		t.Fatalf("overcast --endpoint ... env: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "export AWS_ENDPOINT_URL='http://localhost:4580';") {
		t.Errorf("expected AWS_ENDPOINT_URL to reflect --endpoint, got:\n%s", got)
	}
}

// TestEnvCmd_NoArgs verifies a stray positional argument is rejected rather
// than silently ignored.
func TestEnvCmd_NoArgs(t *testing.T) {
	root := newTestEnvRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"env", "extra"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for a positional argument, got nil")
	}
}

// TestEscapeSingleQuotedPOSIX and TestEscapePowerShellDouble cover the
// escaping helpers directly: a value containing the shell's own quote
// character is unrealistic for these particular AWS_* variables (their
// values come from --endpoint/--region, not free text), but the escaping
// code path still has to be correct if a user ever passes one.
func TestEscapeSingleQuotedPOSIX(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"http://localhost:4566", "http://localhost:4566"},
		{"it's-a-test", `it'\''s-a-test`},
		{"'leading", `'\''leading`},
		{"trailing'", `trailing'\''`},
		{"''", `'\'''\''`},
	}
	for _, tc := range cases {
		if got := escapeSingleQuotedPOSIX(tc.in); got != tc.want {
			t.Errorf("escapeSingleQuotedPOSIX(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Each embedded quote must become close-quote, escaped-quote,
	// reopen-quote: the standard POSIX trick for a literal ' inside a
	// single-quoted string. Confirm the full wrapped form matches exactly,
	// rather than a weaker property like quote-count parity (odd counts are
	// normal here, since \' is an escape sequence, not a quote delimiter).
	if got, want := "'"+escapeSingleQuotedPOSIX("it's")+"'", `'it'\''s'`; got != want {
		t.Errorf("wrapped escaped form = %q, want %q", got, want)
	}
}

func TestEscapePowerShellDouble(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"http://localhost:4566", "http://localhost:4566"},
		{`has "quotes"`, "has `\"quotes`\""},
		{"has`backtick", "has``backtick"},
		{"`\"", "```\""},
	}
	for _, tc := range cases {
		if got := escapePowerShellDouble(tc.in); got != tc.want {
			t.Errorf("escapePowerShellDouble(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatEnvVar_EndpointEscaping exercises quoting end-to-end through the
// command for an endpoint value carrying the target shell's quote
// character, confirming the escaping helpers are actually wired in.
func TestFormatEnvVar_EndpointEscaping(t *testing.T) {
	stubEnviron(t, nil)
	root := newTestEnvRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--endpoint", "http://it's.example:4566", "env", "--shell", "sh"})
	if err := root.Execute(); err != nil {
		t.Fatalf("overcast env: %v", err)
	}
	got := buf.String()
	want := `export AWS_ENDPOINT_URL='http://it'\''s.example:4566';`
	if !strings.Contains(got, want) {
		t.Errorf("expected escaped endpoint line %q, got:\n%s", want, got)
	}
}
