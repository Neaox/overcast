package main

import (
	"strings"
	"testing"
)

// TestRemoteCAGate pins the trust decision in front of remote CA fetches:
// loopback endpoints pass silently (the Docker case), anything else needs
// the explicit --trust-remote acknowledgement.
func TestRemoteCAGate(t *testing.T) {
	cases := []struct {
		name        string
		endpoint    string
		trustRemote bool
		wantErr     bool
	}{
		{"localhost passes silently", "http://localhost:4566", false, false},
		{"127.0.0.1 passes silently", "http://127.0.0.1:4566", false, false},
		{"IPv6 loopback passes silently", "https://[::1]:4566", false, false},
		{"remote host refused without ack", "http://build-host:4566", false, true},
		{"LAN address refused without ack", "http://192.168.1.50:4566", false, true},
		// localhost.overcast.sh resolves to 127.0.0.1, but only via a DNS
		// answer someone else controls — not literal loopback, so gated.
		{"wildcard DNS name refused without ack", "http://localhost.overcast.sh:4566", false, true},
		{"remote host allowed with --trust-remote", "http://build-host:4566", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := remoteCAGate(tc.endpoint, tc.trustRemote)
			if tc.wantErr && err == nil {
				t.Errorf("remoteCAGate(%q, %v) = nil, want an error", tc.endpoint, tc.trustRemote)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("remoteCAGate(%q, %v) = %v, want nil", tc.endpoint, tc.trustRemote, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "--trust-remote") {
				t.Errorf("refusal should name the acknowledgement flag, got: %v", err)
			}
		})
	}
}
