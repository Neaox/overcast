package helpers

// Internal because the thing worth testing is the branch, not the login. Which
// way dockerUsesCredentialHelper answers decides whether DockerLoginOrSkip
// isolates DOCKER_CONFIG, and each answer is wrong on the other kind of machine:
// isolating where a helper owns the credential leaks entries into the platform
// store, and not isolating where the config file owns it leaves two packages
// racing to rewrite one file.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDockerUsesCredentialHelper(t *testing.T) {
	tests := []struct {
		name   string
		config string // "" writes no config.json at all
		want   bool
	}{
		{
			name:   "credsStore names a platform helper",
			config: `{"credsStore":"desktop"}`,
			want:   true,
		},
		{
			name:   "credHelpers names one per registry",
			config: `{"credHelpers":{"public.ecr.aws":"ecr-login"}}`,
			want:   true,
		},
		{
			// The CI shape: credentials are base64 in the file and nothing else
			// touches them, so this is the machine that needs isolating.
			name:   "auths only",
			config: `{"auths":{"registry.example":{"auth":"dXNlcjpwYXNz"}}}`,
			want:   false,
		},
		{
			// Written by isolateDockerConfig itself. It must not read as a
			// helper, or a nested call would decline to isolate.
			name:   "explicitly empty credsStore",
			config: `{"auths":{},"credsStore":""}`,
			want:   false,
		},
		{
			name:   "no config file",
			config: "",
			want:   false,
		},
		{
			// Unreadable config resolves to "no helper", so a machine with a
			// broken config still gets the isolation rather than the flake.
			name:   "malformed config",
			config: `{"credsStore":`,
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.config), 0o600); err != nil {
					t.Fatalf("write the config: %v", err)
				}
			}
			t.Setenv("DOCKER_CONFIG", dir)

			if got := dockerUsesCredentialHelper(); got != tc.want {
				t.Errorf("dockerUsesCredentialHelper() = %t, want %t for %s", got, tc.want, tc.config)
			}
		})
	}
}

// isolateDockerConfig has to leave a config the Docker CLI will actually read,
// and the empty credsStore in it is load-bearing: given an empty directory the
// CLI goes looking for a platform helper instead of using the file store.
func TestIsolateDockerConfigWritesAConfigTheCLIWillUse(t *testing.T) {
	// Given: a machine whose config names no helper.
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "config.json"), []byte(`{"auths":{}}`), 0o600); err != nil {
		t.Fatalf("write the base config: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", base)

	// When: a test isolates its config.
	isolateDockerConfig(t)

	// Then: DOCKER_CONFIG moved somewhere else, and that somewhere holds a
	// config that does not send the CLI to a helper.
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == base {
		t.Fatal("DOCKER_CONFIG was not moved off the machine's own config")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("isolated config.json: %v", err)
	}
	if dockerUsesCredentialHelper() {
		t.Error("the isolated config reads as using a credential helper, so a login would not stay in it")
	}
}

// A second login in one test has to land in the same isolated config as the
// first. Isolating again would strand the credential the first login stored,
// and whatever noticed would fail on an auth error nowhere near the cause.
func TestIsolateDockerConfigIsolatesOncePerTest(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "config.json"), []byte(`{"auths":{}}`), 0o600); err != nil {
		t.Fatalf("write the base config: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", base)

	isolateDockerConfig(t)
	first := os.Getenv("DOCKER_CONFIG")
	if first == base {
		t.Fatal("the first call did not isolate, so this proves nothing")
	}

	isolateDockerConfig(t)

	if second := os.Getenv("DOCKER_CONFIG"); second != first {
		t.Errorf("DOCKER_CONFIG moved from %q to %q on a second login, stranding the first credential", first, second)
	}
}

// The other direction, which is the one that regressed a development machine
// when it was got wrong: where a helper owns credentials, isolating breaks
// `docker logout` and the entries accumulate to a hard ceiling.
func TestIsolateDockerConfigLeavesAHelperMachineAlone(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "config.json"), []byte(`{"credsStore":"desktop"}`), 0o600); err != nil {
		t.Fatalf("write the base config: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", base)

	isolateDockerConfig(t)

	if got := os.Getenv("DOCKER_CONFIG"); got != base {
		t.Errorf("DOCKER_CONFIG = %q, want it left at %q — isolating here strands credentials in the platform store", got, base)
	}
}
