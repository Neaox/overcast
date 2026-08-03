package serviceutil

import "testing"

func TestCallerIsSiblingContainer(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"loopback is the host", "127.0.0.1", false},
		{"IPv6 loopback is the host", "::1", false},
		// Overcast in a container: a request from the host comes through the
		// userland proxy and is seen as the bridge gateway. Classifying it as a
		// sibling hands the host an engine port that is not bound there.
		{"default bridge gateway is the host", "172.17.0.1", false},
		{"user-defined network gateway is the host", "172.20.0.1", false},
		{"a container on the bridge is a sibling", "172.17.0.4", true},
		{"an ECS task is a sibling", "172.20.0.7", true},
		{"a Lambda container is a sibling", "10.0.3.12", true},
		{"a public address is not a sibling", "203.0.113.9", false},
		// The gateway rule is IPv4-only; an IPv6 address must not reach As4.
		{"an IPv6 ULA container is a sibling", "fd00::5", true},
		{"an IPv4-mapped container address is a sibling", "::ffff:172.18.0.4", true},
		{"an unparseable address is not a sibling", "", false},
		{"a hostname is not an address", "localhost", false},
	}
	for _, tc := range tests {
		if got := CallerIsSiblingContainer(tc.addr); got != tc.want {
			t.Errorf("%s: CallerIsSiblingContainer(%q) = %v, want %v", tc.name, tc.addr, got, tc.want)
		}
	}
}
