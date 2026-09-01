package helpers_test

import (
	"errors"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// The environmental cases below are the verbatim errors GitHub's runners
// produced when Docker Hub became unreachable mid-run (PR #491), which reds
// the ECR and ECS Docker tests without anything in Overcast having changed.
func TestRegistryUnreachable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "hub connect timeout",
			err: errors.New(`pull image alpine:latest: status 500: {"message":"Get ` +
				`\"https://registry-1.docker.io/v2/\": context deadline exceeded"}`),
			want: true,
		},
		{
			name: "hub client timeout awaiting headers",
			err: errors.New(`pull image alpine:latest: status 500: {"message":"Get ` +
				`\"https://registry-1.docker.io/v2/\": net/http: request canceled while ` +
				`waiting for connection (Client.Timeout exceeded while awaiting headers)"}`),
			want: true,
		},
		{
			name: "anonymous pull rate limit",
			err:  errors.New("pull image alpine:latest: toomanyrequests: You have reached your pull rate limit"),
			want: true,
		},
		{
			name: "dns failure",
			err:  errors.New(`pull image registry:2: dial tcp: lookup registry-1.docker.io: no such host`),
			want: true,
		},
		{
			name: "registry down",
			err:  errors.New("pull image registry:2: 503 Service Unavailable"),
			want: true,
		},
		{
			name: "unknown manifest is a real failure",
			err:  errors.New("pull image alpine:nonexistent: manifest unknown"),
			want: false,
		},
		{
			name: "unauthorized is a real failure",
			err:  errors.New("pull image private/thing:1: unauthorized: authentication required"),
			want: false,
		},
		{
			name: "no such image is a real failure",
			err:  errors.New(`create container: 404: {"message":"No such image: repo@sha256:abc"}`),
			want: false,
		},
		{
			name: "an unrecognised error stays a failure",
			err:  errors.New("pull image alpine:latest: something nobody has seen before"),
			want: false,
		},
		{
			name: "nil is not a failure at all",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := helpers.RegistryUnreachable(tc.err); got != tc.want {
				t.Fatalf("RegistryUnreachable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
