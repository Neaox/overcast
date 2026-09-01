package ecr

// registry_advertised_host_test.go — advertise the address that was proved,
// not a different one.
//
// Startup picks the registry's port by asking the Docker daemon to dial
// "localhost:<port>", because the daemon performs every push, pull and login.
// When that answers as ours, "localhost:<port>" is a demonstrated fact. The
// configured hostname is not: OVERCAST_HOSTNAME names Overcast's API for
// clients and containers, and a Docker daemon treats it as an ordinary domain
// even when it resolves to loopback — no automatic plain-HTTP trust, and no
// automatic proxy bypass. On a machine with a proxy configured that produced:
//
//	failed to do request: Head "https://localhost.overcast.sh:4510/v2/…/blobs/…":
//	proxyconnect tcp: dial tcp 192.168.65.1:3128: i/o timeout
//
// — the daemon sending a registry request to Docker Desktop's proxy, which it
// could not reach, so it never arrived at a registry that was listening the
// whole time, on the very address the startup probe had already proved.

import (
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

func hostService(t *testing.T, hostname string) *Service {
	t.Helper()
	return &Service{
		cfg: &config.Config{Hostname: hostname, Port: 4566, AccountID: "000000000000", Region: "ap-southeast-2"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
	}
}

func TestRegistryEndpoint_advertisesTheHostTheDaemonProved(t *testing.T) {
	// Given: a registry whose port the daemon answered on, under a configured
	// hostname that is not "localhost".
	s := hostService(t, "localhost.overcast.sh")
	s.adoptRegistryAddress(4510, true)

	// When: the address is minted.
	got := s.registryEndpoint()

	// Then: it names what the probe established. Anything else is an address
	// nobody checked, offered to the one party whose opinion was available.
	if want := "http://localhost:4510"; got != want {
		t.Errorf("registryEndpoint() = %q, want %q", got, want)
	}
	if uri, want := s.repoURI("ap-southeast-2", "app"), "localhost:4510/000000000000/app"; uri != want {
		t.Errorf("repoURI() = %q, want %q", uri, want)
	}
}

func TestRegistryEndpoint_keepsTheConfiguredHostWhenTheDaemonProvedNothing(t *testing.T) {
	// Given: a registry the daemon could not be shown to reach — a remote
	// daemon, or published ports that are not on its loopback.
	s := hostService(t, "overcast.internal")
	s.adoptRegistryAddress(4510, false)

	// When: the address is minted.
	got := s.registryEndpoint()

	// Then: "localhost" would be a guess about someone else's machine, so the
	// configured hostname stands — the address the operator is told to add to
	// the daemon's insecure-registries.
	if want := "http://overcast.internal:4510"; got != want {
		t.Errorf("registryEndpoint() = %q, want %q", got, want)
	}
}

func TestRegistryEndpoint_provedHostIsTheOneTheProbeDialled(t *testing.T) {
	// The probe dials this host and the advertisement claims it; they are the
	// same constant so they cannot drift apart into two different addresses,
	// which is the bug this file exists for.
	s := hostService(t, "elsewhere.example")
	s.adoptRegistryAddress(4510, true)

	if want := "http://" + registryProbeHost + ":4510"; s.registryEndpoint() != want {
		t.Errorf("registryEndpoint() = %q, want %q", s.registryEndpoint(), want)
	}
}
