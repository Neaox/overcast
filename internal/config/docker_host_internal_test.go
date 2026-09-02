package config

import "testing"

// TestNormaliseDockerHost covers every DOCKER_HOST spelling the Docker CLI
// documents, and the two transports Overcast has no dialer for.
func TestNormaliseDockerHost(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantOK  bool
		comment string
	}{
		{
			name:    "unix scheme is stripped to a bare path",
			raw:     "unix:///var/run/docker.sock",
			want:    "/var/run/docker.sock",
			wantOK:  true,
			comment: "dialEndpoint dials a bare path for Unix sockets",
		},
		{
			name:   "colima socket",
			raw:    "unix:///Users/dev/.colima/default/docker.sock",
			want:   "/Users/dev/.colima/default/docker.sock",
			wantOK: true,
		},
		{
			name:   "tcp passes through unchanged",
			raw:    "tcp://docker:2375",
			want:   "tcp://docker:2375",
			wantOK: true,
		},
		{
			name:   "npipe passes through unchanged",
			raw:    `npipe:////./pipe/docker_engine`,
			want:   `npipe:////./pipe/docker_engine`,
			wantOK: true,
		},
		{
			name:    "http is Docker's synonym for tcp",
			raw:     "http://127.0.0.1:2375",
			want:    "tcp://127.0.0.1:2375",
			wantOK:  true,
			comment: "dialEndpoint only knows tcp://",
		},
		{
			name:   "a bare absolute path is accepted",
			raw:    "/run/user/1000/docker.sock",
			want:   "/run/user/1000/docker.sock",
			wantOK: true,
		},
		{
			name:    "ssh is rejected",
			raw:     "ssh://dev@build-host",
			wantOK:  false,
			comment: "needs an SSH client to tunnel the socket",
		},
		{
			name:    "https is rejected",
			raw:     "https://docker:2376",
			wantOK:  false,
			comment: "needs the client certificates DOCKER_CERT_PATH names",
		},
		{
			name:   "an unrecognised scheme is rejected rather than guessed at",
			raw:    "podman://whatever",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normaliseDockerHost(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("normaliseDockerHost(%q) ok = %v, want %v (%s)", tt.raw, ok, tt.wantOK, tt.comment)
			}
			if ok && got != tt.want {
				t.Fatalf("normaliseDockerHost(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestResolveDockerEndpoint covers the three outcomes: no DOCKER_HOST, a
// usable one, and one naming a transport with no dialer.
func TestResolveDockerEndpoint(t *testing.T) {
	t.Run("unset falls back to the platform default with no provenance", func(t *testing.T) {
		t.Setenv(dockerHostVar, "")
		endpoint, source, unsupported := resolveDockerEndpoint()
		if endpoint != DefaultDockerSocket() {
			t.Fatalf("endpoint = %q, want the platform default %q", endpoint, DefaultDockerSocket())
		}
		if source != "" || unsupported != "" {
			t.Fatalf("source = %q, unsupported = %q, want both empty", source, unsupported)
		}
	})

	t.Run("a usable value supplies the endpoint and names itself", func(t *testing.T) {
		t.Setenv(dockerHostVar, "  tcp://docker:2375  ")
		endpoint, source, unsupported := resolveDockerEndpoint()
		if endpoint != "tcp://docker:2375" {
			t.Fatalf("endpoint = %q, want tcp://docker:2375 (surrounding whitespace trimmed)", endpoint)
		}
		if source != dockerHostVar {
			t.Fatalf("source = %q, want %q", source, dockerHostVar)
		}
		if unsupported != "" {
			t.Fatalf("unsupported = %q, want empty", unsupported)
		}
	})

	t.Run("an undialable value is reported, not silently dropped", func(t *testing.T) {
		t.Setenv(dockerHostVar, "ssh://dev@build-host")
		endpoint, source, unsupported := resolveDockerEndpoint()
		if endpoint != DefaultDockerSocket() {
			t.Fatalf("endpoint = %q, want the platform default %q", endpoint, DefaultDockerSocket())
		}
		if source != "" {
			t.Fatalf("source = %q, want empty — the value did not supply the endpoint", source)
		}
		if unsupported != "ssh://dev@build-host" {
			t.Fatalf("unsupported = %q, want the raw value so startup can warn about it", unsupported)
		}
	})
}

// TestAdoptLocalStackVolume pins the three conditions that must all hold
// before a /var/lib/localstack mount is read as the state directory. Each
// case turns exactly one of them off.
func TestAdoptLocalStackVolume(t *testing.T) {
	mounts := func(paths ...string) func(string) bool {
		set := make(map[string]bool, len(paths))
		for _, p := range paths {
			set[p] = true
		}
		return func(path string) bool { return set[path] }
	}

	tests := []struct {
		name          string
		dataDirEnvRaw string
		imageBaked    string
		mounted       func(string) bool
		want          string
	}{
		{
			name:       "migrated compose: only LocalStack's volume path is mounted",
			imageBaked: "/data",
			mounted:    mounts(LocalStackVolumeDir),
			want:       LocalStackVolumeDir,
		},
		{
			name:          "an explicit data directory always wins",
			dataDirEnvRaw: "/persist",
			imageBaked:    "/data",
			mounted:       mounts(LocalStackVolumeDir),
			want:          "",
		},
		{
			name:       "a volume mounted the Overcast way wins",
			imageBaked: "/data",
			mounted:    mounts("/data", LocalStackVolumeDir),
			want:       "",
		},
		{
			name:       "an unmounted directory is just a directory",
			imageBaked: "/data",
			mounted:    mounts(),
			want:       "",
		},
		{
			name:       "native runs are untouched — there is no image-baked default",
			imageBaked: "",
			mounted:    mounts(LocalStackVolumeDir),
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adoptLocalStackVolumeWith(tt.dataDirEnvRaw, tt.imageBaked, tt.mounted)
			if got != tt.want {
				t.Fatalf("adoptLocalStackVolumeWith(%q, %q) = %q, want %q",
					tt.dataDirEnvRaw, tt.imageBaked, got, tt.want)
			}
		})
	}
}
