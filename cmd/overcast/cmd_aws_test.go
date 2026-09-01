package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// unreachableEndpoint returns an https:// endpoint that refuses connections
// immediately: it binds an ephemeral loopback port, then closes it before
// returning, so nothing is listening and the OS resets the connection
// straight away rather than the fetch having to wait out a timeout.
func unreachableEndpoint(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving an ephemeral port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("closing the reserved port: %v", err)
	}
	return "https://" + addr
}

// newTestAWSRoot wires newAWSCmd() under a root carrying the persistent
// --endpoint flag the way main.go's real root does, mirroring
// newTestEnvRoot in cmd_env_test.go.
func newTestAWSRoot() *cobra.Command {
	root := &cobra.Command{Use: "overcast"}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newAWSCmd())
	return root
}

// awsEnvSet turns a []string of KEY=value pairs into a map for easy
// assertions, and fails the test on a malformed entry.
func awsEnvSet(t *testing.T, env []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(env))
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", kv)
		}
		if _, dup := out[name]; dup {
			t.Fatalf("duplicate env entry for %q", name)
		}
		out[name] = value
	}
	return out
}

func TestBuildAWSEnv_ScrubsAllAWSVarsAndInjectsExactSet(t *testing.T) {
	fakeEnviron := []string{
		"HOME=/home/dev",
		"PATH=/usr/bin",
		"AWS_PROFILE=work",
		"AWS_REGION=eu-west-1",
		"AWS_VAULT=work",
		"AWS_SESSION_TOKEN=abc123",
		"AWS_SDK_LOAD_CONFIG=1",
		"AWS_ENDPOINT_URL=https://stale.example.com",
		"NOT_AWS_RELATED=keep-me",
	}

	env, err := buildAWSEnv(fakeEnviron, "http://localhost:4566", "us-west-2", "/tmp/empty-config", "")
	if err != nil {
		t.Fatalf("buildAWSEnv: %v", err)
	}
	set := awsEnvSet(t, env)

	// Every ambient AWS_* variable must be gone.
	for name := range set {
		if strings.HasPrefix(name, "AWS_") {
			switch name {
			case "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_DEFAULT_REGION",
				"AWS_REGION", "AWS_ENDPOINT_URL", "AWS_PAGER", "AWS_EC2_METADATA_DISABLED",
				"AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CA_BUNDLE":
				// one of the injected set, checked below
			default:
				t.Errorf("ambient AWS_* variable %q leaked through", name)
			}
		}
	}

	// Non-AWS variables must survive untouched.
	if set["HOME"] != "/home/dev" || set["PATH"] != "/usr/bin" || set["NOT_AWS_RELATED"] != "keep-me" {
		t.Errorf("non-AWS variables were altered: %#v", set)
	}

	want := map[string]string{
		"AWS_ACCESS_KEY_ID":           "test",
		"AWS_SECRET_ACCESS_KEY":       "test",
		"AWS_DEFAULT_REGION":          "us-west-2",
		"AWS_REGION":                  "us-west-2",
		"AWS_ENDPOINT_URL":            "http://localhost:4566",
		"AWS_PAGER":                   "",
		"AWS_EC2_METADATA_DISABLED":   "true",
		"AWS_CONFIG_FILE":             "/tmp/empty-config",
		"AWS_SHARED_CREDENTIALS_FILE": "/tmp/empty-config",
	}
	for name, wantValue := range want {
		if got, ok := set[name]; !ok || got != wantValue {
			t.Errorf("%s = %q, %v; want %q", name, got, ok, wantValue)
		}
	}
	if _, present := set["AWS_CA_BUNDLE"]; present {
		t.Errorf("AWS_CA_BUNDLE should be absent when caBundle is empty, got %q", set["AWS_CA_BUNDLE"])
	}
}

func TestBuildAWSEnv_SetsCABundleWhenProvided(t *testing.T) {
	env, err := buildAWSEnv(nil, "https://localhost:4566", "us-east-1", "/tmp/cfg", "/home/dev/.overcast/ca/abc.pem")
	if err != nil {
		t.Fatalf("buildAWSEnv: %v", err)
	}
	set := awsEnvSet(t, env)
	if set["AWS_CA_BUNDLE"] != "/home/dev/.overcast/ca/abc.pem" {
		t.Errorf("AWS_CA_BUNDLE = %q, want the cache path", set["AWS_CA_BUNDLE"])
	}
}

func TestBuildAWSEnv_DefaultsEmptyRegion(t *testing.T) {
	env, err := buildAWSEnv(nil, "http://localhost:4566", "", "/tmp/cfg", "")
	if err != nil {
		t.Fatalf("buildAWSEnv: %v", err)
	}
	set := awsEnvSet(t, env)
	if set["AWS_REGION"] != "us-east-1" || set["AWS_DEFAULT_REGION"] != "us-east-1" {
		t.Errorf("empty region did not default to us-east-1: %#v", set)
	}
}

func TestBuildAWSEnv_RejectsEmptyEndpointOrConfigFile(t *testing.T) {
	if _, err := buildAWSEnv(nil, "", "us-east-1", "/tmp/cfg", ""); err == nil {
		t.Error("expected an error for an empty endpoint")
	}
	if _, err := buildAWSEnv(nil, "http://localhost:4566", "us-east-1", "", ""); err == nil {
		t.Error("expected an error for an empty config file path")
	}
}

func TestResolveAWSEndpointValue_Precedence(t *testing.T) {
	const flagDefault = "http://localhost:4566"

	cases := []struct {
		name             string
		override         string
		overrideGiven    bool
		overcastEndpoint string
		overcastPort     string
		want             string
	}{
		{
			name:          "explicit --endpoint before the subcommand wins",
			override:      "http://localhost:9999",
			overrideGiven: true,
			want:          "http://localhost:9999",
		},
		{
			name:             "OVERCAST_ENDPOINT applies when flag is at default",
			overcastEndpoint: "http://localhost:4580",
			want:             "http://localhost:4580",
		},
		{
			name:         "OVERCAST_PORT applies when flag is at default and OVERCAST_ENDPOINT is unset",
			overcastPort: "4580",
			want:         "http://localhost:4580",
		},
		{
			name:             "OVERCAST_ENDPOINT wins over OVERCAST_PORT",
			overcastEndpoint: "http://localhost:4590",
			overcastPort:     "4580",
			want:             "http://localhost:4590",
		},
		{
			name: "falls back to the flag default when nothing else is set",
			want: flagDefault,
		},
		{
			name:          "explicit override wins even over OVERCAST_ENDPOINT",
			override:      "http://localhost:7000",
			overrideGiven: true,
			// OVERCAST_ENDPOINT set too, but override must win.
			overcastEndpoint: "http://localhost:4580",
			want:             "http://localhost:7000",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OVERCAST_ENDPOINT", tc.overcastEndpoint)
			t.Setenv("OVERCAST_PORT", tc.overcastPort)
			got := resolveAWSEndpointValue(flagDefault, flagDefault, tc.override, tc.overrideGiven)
			if got != tc.want {
				t.Errorf("resolveAWSEndpointValue(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveAWSEndpointValue_NonDefaultCurrentValueWins(t *testing.T) {
	t.Setenv("OVERCAST_ENDPOINT", "http://localhost:4580")
	t.Setenv("OVERCAST_PORT", "")
	got := resolveAWSEndpointValue("http://localhost:5555", "http://localhost:4566", "", false)
	if got != "http://localhost:5555" {
		t.Errorf("got %q, want the already-parsed flag value to win over env", got)
	}
}

func TestResolveAWSRegion(t *testing.T) {
	t.Setenv("OVERCAST_REGION", "")
	if got := resolveAWSRegion(); got != "us-east-1" {
		t.Errorf("resolveAWSRegion() = %q, want us-east-1 default", got)
	}
	t.Setenv("OVERCAST_REGION", "ap-southeast-2")
	if got := resolveAWSRegion(); got != "ap-southeast-2" {
		t.Errorf("resolveAWSRegion() = %q, want ap-southeast-2", got)
	}
}

func TestExtractLeadingEndpointFlag(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantValue string
		wantGiven bool
		wantRest  []string
	}{
		{
			name:      "space-separated form",
			args:      []string{"--endpoint", "http://localhost:9999", "s3", "ls"},
			wantValue: "http://localhost:9999",
			wantGiven: true,
			wantRest:  []string{"s3", "ls"},
		},
		{
			name:      "equals form",
			args:      []string{"--endpoint=http://localhost:9999", "s3", "ls"},
			wantValue: "http://localhost:9999",
			wantGiven: true,
			wantRest:  []string{"s3", "ls"},
		},
		{
			name:      "not present",
			args:      []string{"s3", "ls", "--debug"},
			wantValue: "",
			wantGiven: false,
			wantRest:  []string{"s3", "ls", "--debug"},
		},
		{
			name:      "endpoint given later is left alone (not a leading flag)",
			args:      []string{"s3", "ls", "--endpoint", "http://localhost:9999"},
			wantValue: "",
			wantGiven: false,
			wantRest:  []string{"s3", "ls", "--endpoint", "http://localhost:9999"},
		},
		{
			name:      "empty args",
			args:      nil,
			wantValue: "",
			wantGiven: false,
			wantRest:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, given, rest := extractLeadingEndpointFlag(tc.args)
			if value != tc.wantValue || given != tc.wantGiven || !strSliceEqual(rest, tc.wantRest) {
				t.Errorf("extractLeadingEndpointFlag(%v) = (%q, %v, %v); want (%q, %v, %v)",
					tc.args, value, given, rest, tc.wantValue, tc.wantGiven, tc.wantRest)
			}
		})
	}
}

func TestStripLeadingDoubleDash(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"leading -- is stripped", []string{"--", "s3", "ls"}, []string{"s3", "ls"}},
		{"no leading -- leaves args alone", []string{"s3", "ls"}, []string{"s3", "ls"}},
		{"only a later -- is left alone", []string{"s3", "--", "ls"}, []string{"s3", "--", "ls"}},
		{"empty args", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripLeadingDoubleDash(tc.args)
			if !strSliceEqual(got, tc.want) {
				t.Errorf("stripLeadingDoubleDash(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCACachePath_DerivesFromHomeAndEndpointHash(t *testing.T) {
	p1 := caCachePath("/home/dev", "https://localhost:4566")
	p2 := caCachePath("/home/dev", "https://localhost:4566")
	p3 := caCachePath("/home/dev", "https://localhost:9999")

	if p1 != p2 {
		t.Errorf("caCachePath is not deterministic: %q != %q", p1, p2)
	}
	if p1 == p3 {
		t.Errorf("different endpoints produced the same cache path: %q", p1)
	}
	wantDir := filepath.Join("/home/dev", ".overcast", "ca")
	if filepath.Dir(p1) != wantDir {
		t.Errorf("caCachePath dir = %q, want %q", filepath.Dir(p1), wantDir)
	}
	if filepath.Ext(p1) != ".pem" {
		t.Errorf("caCachePath = %q, want a .pem file", p1)
	}
}

func TestEnsureCABundle_FetchesAndCaches(t *testing.T) {
	const fakePEM = "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_overcast/ca.pem" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(fakePEM))
	}))
	defer srv.Close()

	homeDir := t.TempDir()
	path, warning := ensureCABundle(context.Background(), srv.URL, homeDir, srv.Client())
	if warning != "" {
		t.Fatalf("unexpected warning: %s", warning)
	}
	wantPath := caCachePath(homeDir, srv.URL)
	if path != wantPath {
		t.Fatalf("ensureCABundle returned %q, want %q", path, wantPath)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading cached CA file: %v", err)
	}
	if string(got) != fakePEM {
		t.Errorf("cached CA content = %q, want %q", got, fakePEM)
	}
}

func TestEnsureCABundle_FallsBackToCacheOnFetchFailure(t *testing.T) {
	homeDir := t.TempDir()
	endpoint := unreachableEndpoint(t)
	path := caCachePath(homeDir, endpoint)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("seeding cache dir: %v", err)
	}
	const staleContent = "-----BEGIN CERTIFICATE-----\nstale\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(path, []byte(staleContent), 0o644); err != nil {
		t.Fatalf("seeding cache file: %v", err)
	}

	client := &http.Client{Timeout: caFetchTimeout}
	gotPath, warning := ensureCABundle(context.Background(), endpoint, homeDir, client)
	if warning != "" {
		t.Fatalf("expected no warning when a cached copy exists, got: %s", warning)
	}
	if gotPath != path {
		t.Fatalf("ensureCABundle returned %q, want cached path %q", gotPath, path)
	}
}

func TestEnsureCABundle_WarnsWhenNeitherFetchNorCacheAvailable(t *testing.T) {
	homeDir := t.TempDir()
	client := &http.Client{Timeout: caFetchTimeout}
	path, warning := ensureCABundle(context.Background(), unreachableEndpoint(t), homeDir, client)
	if path != "" {
		t.Errorf("expected an empty path when fetch and cache both fail, got %q", path)
	}
	if warning == "" {
		t.Error("expected a warning when fetch and cache both fail")
	}
}

func TestIsHTTPSEndpoint(t *testing.T) {
	if !isHTTPSEndpoint("https://localhost:4566") {
		t.Error("expected https:// to be detected")
	}
	if !isHTTPSEndpoint("HTTPS://localhost:4566") {
		t.Error("expected case-insensitive detection")
	}
	if isHTTPSEndpoint("http://localhost:4566") {
		t.Error("did not expect http:// to be detected as https")
	}
}

// TestRunAWS_MissingBinary points PATH at an empty directory so
// exec.LookPath("aws") fails, and checks the whole command surfaces the
// documented, actionable error rather than a bare "file not found".
func TestRunAWS_MissingBinary(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	t.Setenv("Path", emptyDir) // Windows env lookups are case-insensitive but t.Setenv is not

	root := newTestAWSRoot()
	var buf strings.Builder
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"aws", "s3", "ls"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when aws is not on PATH")
	}
	const wantSubstring = "aws CLI not found on PATH"
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), wantSubstring)
	}
}

// TestAWSCmd_DisableFlagParsing pins the property the whole command depends
// on: an AWS-CLI-native flag like --debug must not be swallowed or
// misinterpreted by overcast's own flag parser. It's exercised indirectly
// via the missing-binary path since that's the only outcome reachable
// without a real aws binary.
func TestAWSCmd_DisableFlagParsing(t *testing.T) {
	if !newAWSCmd().DisableFlagParsing {
		t.Error("expected newAWSCmd() to disable flag parsing so passthrough args are untouched")
	}
}

// awsEnvKeys is a small helper used only to keep an assertion below terse.
func awsEnvKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

func TestBuildAWSEnv_InjectedKeysAreExactlyTheDocumentedSet(t *testing.T) {
	env, err := buildAWSEnv([]string{"AWS_PROFILE=x"}, "http://localhost:4566", "us-east-1", "/tmp/cfg", "/tmp/ca.pem")
	if err != nil {
		t.Fatalf("buildAWSEnv: %v", err)
	}
	want := []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_CA_BUNDLE",
		"AWS_CONFIG_FILE",
		"AWS_DEFAULT_REGION",
		"AWS_EC2_METADATA_DISABLED",
		"AWS_ENDPOINT_URL",
		"AWS_PAGER",
		"AWS_REGION",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SHARED_CREDENTIALS_FILE",
	}
	sort.Strings(want)
	got := awsEnvKeys(env)
	if !strSliceEqual(got, want) {
		t.Errorf("AWS_* keys = %v, want exactly %v", got, want)
	}
}
