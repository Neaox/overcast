package rds

import (
	"context"
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
)

func testHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg, store: newRDSStore(nil, cfg.Region)}
}

// A caller is handed the endpoint under the hostname their own request arrived
// on, not the one the record was minted with — the rule every other service
// follows for the URLs it returns (docs/plans/client-facing-url-minting.md).
func TestInstanceEndpoint_mintedOnTheCallersHostname(t *testing.T) {
	// Given: an instance stored with the canonical (unconfigured) hostname.
	h := testHandler(&config.Config{Region: "ap-southeast-2", Port: 4566})
	inst := &DBInstance{
		DBInstanceIdentifier: "wordpress-db",
		Engine:               "mysql",
		Port:                 3306,
		Endpoint:             &Endpoint{Address: "wordpress-db.ap-southeast-2.rds.localhost", Port: 3306},
	}

	// When: an ECS task asks over the split-horizon hostname.
	ctx := middleware.ContextWithClientAddr(
		middleware.ContextWithClientEndpoint(context.Background(), "http://localhost.overcast.sh:4566"),
		"172.18.0.7")
	address, port := h.instanceEndpointFor(ctx, inst)

	// Then: the name is a subdomain of the hostname it reached Overcast on.
	if want := "wordpress-db.ap-southeast-2.rds.localhost.overcast.sh"; address != want {
		t.Errorf("address = %q, want %q", address, want)
	}
	if port != 3306 {
		t.Errorf("port = %d, want the engine port 3306", port)
	}
}

// A configured OVERCAST_HOSTNAME is the operator asserting one name resolves
// for every party, so it wins over the caller's own host.
func TestInstanceEndpoint_configuredHostnameWins(t *testing.T) {
	h := testHandler(&config.Config{Region: "us-east-1", Port: 4566, Hostname: "localhost.overcast.sh"})
	inst := &DBInstance{DBInstanceIdentifier: "db-1", Engine: "postgres", Port: 5432, Endpoint: &Endpoint{}}

	ctx := middleware.ContextWithClientAddr(
		middleware.ContextWithClientEndpoint(context.Background(), "http://172.18.0.2:4566"),
		"172.18.0.7")
	address, _ := h.instanceEndpointFor(ctx, inst)

	if want := "db-1.us-east-1.rds.localhost.overcast.sh"; address != want {
		t.Errorf("address = %q, want %q", address, want)
	}
}

// The endpoint stays a hostname once the container is running: an address is
// dialable by one party only, and this one crosses into task definitions.
func TestInstanceEndpoint_containerCallerGetsTheNameAndEnginePort(t *testing.T) {
	h := testHandler(&config.Config{Region: "us-east-1", Port: 4566, Hostname: "localhost.overcast.sh"})
	inst := &DBInstance{
		DBInstanceIdentifier: "db-1",
		Engine:               "mysql",
		Port:                 3306,
		Endpoint:             &Endpoint{Address: "db-1.us-east-1.rds.localhost.overcast.sh", Port: 3306},
		DockerContainerID:    "abc123",
		HostPort:             33061,
		DialAddress:          "172.19.0.4",
		DialPort:             3306,
	}

	ctx := middleware.ContextWithClientAddr(context.Background(), "172.18.0.7")
	address, port := h.instanceEndpointFor(ctx, inst)

	if want := "db-1.us-east-1.rds.localhost.overcast.sh"; address != want {
		t.Errorf("address = %q, want the endpoint hostname %q", address, want)
	}
	if port != 3306 {
		t.Errorf("port = %d, want the engine port 3306 — the host port is not bound inside the network", port)
	}
}

// The host reaches the same container only through its published port, and
// through a name it can actually resolve.
func TestInstanceEndpoint_hostCallerGetsThePublishedPort(t *testing.T) {
	tests := []struct {
		name        string
		hostname    string
		wantAddress string
	}{
		{"wildcard DNS resolves the name", "localhost.overcast.sh", "db-1.us-east-1.rds.localhost.overcast.sh"},
		// *.localhost does not resolve on Windows, so a name would be worse
		// than the loopback address it stands for.
		{"bare localhost degrades to loopback", "", "127.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(&config.Config{Region: "us-east-1", Port: 4566, Hostname: tc.hostname})
			inst := &DBInstance{
				DBInstanceIdentifier: "db-1",
				Engine:               "mysql",
				Port:                 3306,
				Endpoint:             &Endpoint{},
				DockerContainerID:    "abc123",
				HostPort:             33061,
			}

			ctx := middleware.ContextWithClientAddr(context.Background(), "127.0.0.1")
			address, port := h.instanceEndpointFor(ctx, inst)

			if address != tc.wantAddress {
				t.Errorf("address = %q, want %q", address, tc.wantAddress)
			}
			if port != 33061 {
				t.Errorf("port = %d, want the published host port 33061", port)
			}
		})
	}
}

// Docker aliases are exact-match, so every hostname an endpoint could be minted
// under has to be registered or the name resolves nowhere for the caller who
// holds it.
func TestInstanceEndpointAliases_coverEveryMintableHostname(t *testing.T) {
	h := testHandler(&config.Config{Region: "us-east-1", Hostname: "localhost.overcast.sh"})
	inst := &DBInstance{
		DBInstanceIdentifier: "db-1",
		Endpoint:             &Endpoint{Address: "db-1.us-east-1.rds.localhost.overcast.sh"},
	}

	got := h.instanceEndpointAliases("us-east-1", inst)

	for _, want := range []string{
		"db-1.us-east-1.rds.localhost.overcast.sh",
		"db-1.us-east-1.rds.localhost.localstack.cloud",
		"db-1.us-east-1.rds.localhost",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("aliases %v missing %q", got, want)
		}
	}
}

// Docker startup used to overwrite the stored endpoint with a container IP; the
// alias set must survive a record in that shape (state written by an older
// build) rather than losing the name it was reachable under.
func TestInstanceEndpointAliases_skipAddressLiterals(t *testing.T) {
	h := testHandler(&config.Config{Region: "us-east-1", Hostname: "localhost.overcast.sh"})
	inst := &DBInstance{DBInstanceIdentifier: "db-1", Endpoint: &Endpoint{Address: "172.18.0.4"}}

	got := h.instanceEndpointAliases("us-east-1", inst)

	if slices.Contains(got, "172.18.0.4") {
		t.Errorf("aliases %v include an address literal", got)
	}
	if !slices.Contains(got, "db-1.us-east-1.rds.localhost.overcast.sh") {
		t.Errorf("aliases %v missing the canonical name", got)
	}
}
