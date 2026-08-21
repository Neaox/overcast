package rds

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

func testHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg, store: newRDSStore(state.NewMemoryStore(), cfg.Region)}
}

// testCluster stores c and its members, and returns a handler that can read
// them back — the shape every cluster-endpoint question needs, because a
// cluster's address is really its writer's.
func testClusterHandler(t *testing.T, cfg *config.Config, c *DBCluster, members ...*DBInstance) *Handler {
	t.Helper()
	h := testHandler(cfg)
	ctx := middleware.ContextWithRegion(context.Background(), cfg.Region)
	if aerr := h.store.putDBCluster(ctx, c); aerr != nil {
		t.Fatalf("putDBCluster: %s", aerr.Message)
	}
	for _, m := range members {
		if aerr := h.store.putDBInstance(ctx, m); aerr != nil {
			t.Fatalf("putDBInstance %s: %s", m.DBInstanceIdentifier, aerr.Message)
		}
	}
	return h
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

// A cluster endpoint is the name CDK's rds.DatabaseCluster hands an application
// — `cluster.clusterEndpoint.hostname` — so it is the one most consumers hold,
// and it has to resolve wherever the instance endpoint does. Registering only
// the instance name left it falling through to public DNS, which answers any
// subdomain of the split-horizon domains with a loopback address: the task
// resolved the cluster, dialled itself, and got a refused connection.
func TestClusterEndpointAliases_coverBothRolesOnEveryMintableHostname(t *testing.T) {
	h := testHandler(&config.Config{Region: "ap-southeast-2", Hostname: "localhost.overcast.sh"})
	c := &DBCluster{
		DBClusterIdentifier: "orders",
		Endpoint:            "orders.cluster.ap-southeast-2.rds.localhost.overcast.sh",
		ReaderEndpoint:      "orders.cluster-ro.ap-southeast-2.rds.localhost.overcast.sh",
	}

	got := h.clusterEndpointAliases("ap-southeast-2", c)

	for _, want := range []string{
		"orders.cluster.ap-southeast-2.rds.localhost.overcast.sh",
		"orders.cluster-ro.ap-southeast-2.rds.localhost.overcast.sh",
		"orders.cluster.ap-southeast-2.rds.localhost.localstack.cloud",
		"orders.cluster-ro.ap-southeast-2.rds.localhost.localstack.cloud",
		"orders.cluster.ap-southeast-2.rds.localhost",
		"orders.cluster-ro.ap-southeast-2.rds.localhost",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("aliases %v missing %q", got, want)
		}
	}
}

// Both cluster names go on the writer, and only on the writer. Overcast gives
// every Aurora member its own engine container with its own storage — there is
// no shared cluster volume — so a replica's database is empty. A reader
// endpoint that load-balanced onto it would answer half the queries with data
// that was never written.
func TestClusterAliasesForInstance_onlyTheWriterCarriesThem(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", Hostname: "localhost.overcast.sh"}
	c := &DBCluster{
		DBClusterIdentifier: "orders",
		DBClusterMembers: []DBClusterMember{
			{DBInstanceIdentifier: "orders-writer", IsClusterWriter: true},
			{DBInstanceIdentifier: "orders-reader"},
		},
	}
	writer := &DBInstance{DBInstanceIdentifier: "orders-writer", DBClusterIdentifier: "orders"}
	reader := &DBInstance{DBInstanceIdentifier: "orders-reader", DBClusterIdentifier: "orders"}
	h := testClusterHandler(t, cfg, c, writer, reader)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	if got := h.clusterAliasesForInstance(ctx, "us-east-1", writer); len(got) == 0 {
		t.Error("the writer carries no cluster aliases, so no cluster endpoint resolves")
	} else if !slices.Contains(got, "orders.cluster-ro.us-east-1.rds.localhost.overcast.sh") {
		t.Errorf("writer aliases %v missing the reader endpoint", got)
	}

	if got := h.clusterAliasesForInstance(ctx, "us-east-1", reader); len(got) != 0 {
		t.Errorf("reader carries cluster aliases %v — its container holds none of the cluster's data", got)
	}
}

// The set startDBContainer actually attaches with. Both halves have to be in
// it: the instance endpoint alone is what shipped, and it left every consumer
// holding the cluster endpoint unable to reach the database at all.
func TestContainerAliases_carryBothTheInstanceAndClusterEndpoints(t *testing.T) {
	cfg := &config.Config{Region: "ap-southeast-2", Hostname: "localhost.overcast.sh"}
	c := &DBCluster{
		DBClusterIdentifier: "orders",
		DBClusterMembers:    []DBClusterMember{{DBInstanceIdentifier: "ordersinstance1", IsClusterWriter: true}},
	}
	writer := &DBInstance{
		DBInstanceIdentifier: "ordersinstance1",
		DBClusterIdentifier:  "orders",
		Endpoint:             &Endpoint{Address: "ordersinstance1.ap-southeast-2.rds.localhost.overcast.sh"},
	}
	h := testClusterHandler(t, cfg, c, writer)
	ctx := middleware.ContextWithRegion(context.Background(), "ap-southeast-2")

	got := h.containerAliases(ctx, "ap-southeast-2", writer)

	for _, want := range []string{
		"ordersinstance1.ap-southeast-2.rds.localhost.overcast.sh",
		"orders.cluster.ap-southeast-2.rds.localhost.overcast.sh",
		"orders.cluster-ro.ap-southeast-2.rds.localhost.overcast.sh",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("container aliases %v missing %q", got, want)
		}
	}
}

// A standalone DB instance has no cluster, and must not grow one.
func TestContainerAliases_standaloneInstanceCarriesOnlyItsOwn(t *testing.T) {
	h := testHandler(&config.Config{Region: "us-east-1", Hostname: "localhost.overcast.sh"})
	inst := &DBInstance{DBInstanceIdentifier: "db-1", Endpoint: &Endpoint{}}

	got := h.containerAliases(middleware.ContextWithRegion(context.Background(), "us-east-1"), "us-east-1", inst)

	for _, alias := range got {
		if strings.Contains(alias, ".cluster.") || strings.Contains(alias, ".cluster-ro.") {
			t.Errorf("standalone instance carries cluster alias %q", alias)
		}
	}
}

// CreateDBInstance registers cluster membership around the same moment it
// starts the container, so the alias set is computed for an instance the
// cluster may not list yet. The first member is the writer, and anticipating
// that is what keeps the race from costing the cluster its endpoints.
func TestClusterAliasesForInstance_anticipatesTheFirstMembersWriterRole(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", Hostname: "localhost.overcast.sh"}
	c := &DBCluster{DBClusterIdentifier: "orders"}
	inst := &DBInstance{DBInstanceIdentifier: "orders-writer", DBClusterIdentifier: "orders"}
	h := testClusterHandler(t, cfg, c, inst)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	got := h.clusterAliasesForInstance(ctx, "us-east-1", inst)

	if !slices.Contains(got, "orders.cluster.us-east-1.rds.localhost.overcast.sh") {
		t.Errorf("aliases %v missing the writer endpoint for a cluster's first member", got)
	}
}

// A cluster has no container of its own; its endpoints point at the writer's.
// So they are rendered per caller on exactly the terms the instance endpoint
// is — a sibling container gets the name and the engine's own port.
func TestClusterEndpoints_containerCallerGetsTheNamesAndEnginePort(t *testing.T) {
	cfg := &config.Config{Region: "ap-southeast-2", Port: 4566, Hostname: "localhost.overcast.sh"}
	c := &DBCluster{
		DBClusterIdentifier: "orders",
		Port:                3306,
		DBClusterMembers:    []DBClusterMember{{DBInstanceIdentifier: "orders-writer", IsClusterWriter: true}},
	}
	writer := &DBInstance{
		DBInstanceIdentifier: "orders-writer",
		DBClusterIdentifier:  "orders",
		Engine:               "aurora-mysql",
		Port:                 3306,
		DockerContainerID:    "abc123",
		HostPort:             33061,
	}
	h := testClusterHandler(t, cfg, c, writer)

	ctx := middleware.ContextWithClientAddr(middleware.ContextWithRegion(context.Background(), "ap-southeast-2"), "10.42.0.9")
	writerName, readerName, port := h.clusterEndpointsFor(ctx, c)

	if want := "orders.cluster.ap-southeast-2.rds.localhost.overcast.sh"; writerName != want {
		t.Errorf("writer endpoint = %q, want %q", writerName, want)
	}
	if want := "orders.cluster-ro.ap-southeast-2.rds.localhost.overcast.sh"; readerName != want {
		t.Errorf("reader endpoint = %q, want %q", readerName, want)
	}
	if port != 3306 {
		t.Errorf("port = %d, want the engine port 3306 — the host port is not bound inside the network", port)
	}
}

// And a host caller gets what it can actually reach: the writer's published
// port, on a loopback address when the base carries no wildcard DNS. Handing
// back the cluster's own 3306 pointed the host at a port nothing binds.
func TestClusterEndpoints_hostCallerGetsThePublishedPort(t *testing.T) {
	tests := []struct {
		name        string
		hostname    string
		wantAddress string
	}{
		{"wildcard DNS resolves the name", "localhost.overcast.sh", "orders.cluster.us-east-1.rds.localhost.overcast.sh"},
		{"bare localhost degrades to loopback", "", "127.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Region: "us-east-1", Port: 4566, Hostname: tc.hostname}
			c := &DBCluster{
				DBClusterIdentifier: "orders",
				Port:                3306,
				DBClusterMembers:    []DBClusterMember{{DBInstanceIdentifier: "orders-writer", IsClusterWriter: true}},
			}
			writer := &DBInstance{
				DBInstanceIdentifier: "orders-writer",
				DBClusterIdentifier:  "orders",
				Engine:               "aurora-mysql",
				Port:                 3306,
				DockerContainerID:    "abc123",
				HostPort:             33061,
			}
			h := testClusterHandler(t, cfg, c, writer)

			ctx := middleware.ContextWithClientAddr(middleware.ContextWithRegion(context.Background(), "us-east-1"), "127.0.0.1")
			writerName, readerName, port := h.clusterEndpointsFor(ctx, c)

			if writerName != tc.wantAddress {
				t.Errorf("writer endpoint = %q, want %q", writerName, tc.wantAddress)
			}
			if tc.wantAddress == "127.0.0.1" && readerName != tc.wantAddress {
				t.Errorf("reader endpoint = %q, want the same loopback address %q", readerName, tc.wantAddress)
			}
			if port != 33061 {
				t.Errorf("port = %d, want the writer's published host port 33061", port)
			}
		})
	}
}

// A cluster whose members have no container yet — Docker unavailable, or the
// instance still creating — keeps the AWS-shaped names and the cluster's own
// port. There is nothing dialable to offer, and a name is a better answer than
// a loopback address that is certainly wrong.
//
// This is also what keeps CloudFormation faithful. The DBCluster handler
// freezes Endpoint/ReaderEndpoint/Port from the CreateDBCluster response, and a
// CDK stack creates the cluster before its instances — so `Fn::GetAtt
// Endpoint.Port` resolves here, to AWS's 3306, and the host-side port never
// reaches a task definition. Changing this case is how that would break.
func TestClusterEndpoints_withoutAWriterKeepTheNameAndClusterPort(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", Port: 4566, Hostname: "localhost.overcast.sh"}
	c := &DBCluster{DBClusterIdentifier: "orders", Port: 3306}
	h := testClusterHandler(t, cfg, c)

	ctx := middleware.ContextWithClientAddr(middleware.ContextWithRegion(context.Background(), "us-east-1"), "127.0.0.1")
	writerName, _, port := h.clusterEndpointsFor(ctx, c)

	if want := "orders.cluster.us-east-1.rds.localhost.overcast.sh"; writerName != want {
		t.Errorf("writer endpoint = %q, want %q", writerName, want)
	}
	if port != 3306 {
		t.Errorf("port = %d, want the cluster's own port 3306", port)
	}
}
