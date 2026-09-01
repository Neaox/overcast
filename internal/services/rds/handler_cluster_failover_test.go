package rds

// handler_cluster_failover_test.go — the cluster endpoint aliases following the
// writer when a promotion moves it.
//
// The alias half of the membership fix. clusterAliasesForInstance puts both
// cluster endpoint names on the *writer's* engine container, and Docker fixes a
// container's aliases when it joins a network — so promoting a replica updates
// the record and nothing else. The names stay on the container that was just
// deleted, and the cluster endpoint resolves to a stopped engine.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/state"
)

// networkDaemon is the slice of the Docker API an alias move touches: the
// connect and disconnect calls, recorded in order with the aliases each connect
// advertised.
type networkDaemon struct {
	srv *httptest.Server

	mu      sync.Mutex
	calls   []string            // "connect <network>" / "disconnect <network>"
	aliases map[string][]string // container → aliases from its most recent connect
}

func newNetworkDaemon(t *testing.T) *networkDaemon {
	t.Helper()
	d := &networkDaemon{aliases: map[string][]string{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Container      string `json:"Container"`
			EndpointConfig *struct {
				Aliases []string `json:"Aliases"`
			} `json:"EndpointConfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		p := r.URL.Path
		network := strings.TrimPrefix(p, "/v1.45/networks/")
		network = strings.TrimSuffix(strings.TrimSuffix(network, "/connect"), "/disconnect")

		d.mu.Lock()
		switch {
		case strings.HasSuffix(p, "/connect"):
			d.calls = append(d.calls, "connect "+network)
			if body.EndpointConfig != nil {
				d.aliases[body.Container] = body.EndpointConfig.Aliases
			} else {
				d.aliases[body.Container] = nil
			}
		case strings.HasSuffix(p, "/disconnect"):
			d.calls = append(d.calls, "disconnect "+network)
		}
		d.mu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *networkDaemon) snapshot() ([]string, map[string][]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	calls := slices.Clone(d.calls)
	aliases := map[string][]string{}
	for k, v := range d.aliases {
		aliases[k] = slices.Clone(v)
	}
	return calls, aliases
}

func newFailoverHandler(t *testing.T, d *networkDaemon) *Handler {
	t.Helper()
	cfg := &config.Config{
		Region:    "us-east-1",
		AccountID: "123456789012",
		Network:   "overcast",
		Hostname:  "localhost.overcast.sh",
	}
	h := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New()).handler
	h.docker = docker.NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	h.dockerReady.Store(true)
	return h
}

// seedContainerMember is seedClusterMember with an engine container behind the
// instance, which is what an alias move has to act on.
func seedContainerMember(t *testing.T, h *Handler, clusterID, instanceID, containerID string) {
	t.Helper()
	ctx := t.Context()
	if aerr := h.store.putDBInstance(ctx, &DBInstance{
		DBInstanceIdentifier: instanceID,
		DBClusterIdentifier:  clusterID,
		Engine:               "aurora-mysql",
		DBInstanceStatus:     "available",
		Port:                 3306,
		DockerContainerID:    containerID,
	}); aerr != nil {
		t.Fatalf("putDBInstance %s: %s", instanceID, aerr.Message)
	}
	h.addInstanceToCluster(ctx, clusterID, instanceID)
}

// The promoted member's container has to leave its plane and rejoin it, because
// that is the only way its alias set changes. Rejoining must advertise the
// cluster endpoints it has just inherited.
func TestAdoptClusterEndpoints_movesTheClusterNamesOntoTheNewWriter(t *testing.T) {
	d := newNetworkDaemon(t)
	h := newFailoverHandler(t, d)
	ctx := t.Context()

	seedCluster(t, h, "orders")
	seedContainerMember(t, h, "orders", "orders-writer", "container-writer")
	seedContainerMember(t, h, "orders", "orders-reader", "container-reader")

	// Promote the reader the way removeInstanceFromCluster does, then let the
	// endpoints follow.
	if got := h.removeInstanceFromCluster(ctx, "orders", "orders-writer"); got != "orders-reader" {
		t.Fatalf("promoted = %q, want orders-reader", got)
	}
	h.adoptClusterEndpoints(ctx, "orders-reader")

	calls, aliases := d.snapshot()
	if want := []string{"disconnect overcast", "connect overcast"}; !slices.Equal(calls, want) {
		t.Errorf("daemon calls = %v, want %v — aliases only change across a rejoin", calls, want)
	}

	// Built from the same helpers the handler mints with, not spelled out: the
	// role discriminators are a deliberate deviation from AWS's account-hashed
	// names and have moved once already.
	got := aliases["container-reader"]
	for _, want := range []string{
		clusterEndpointHostname("orders", clusterRoleWriter, "us-east-1", "localhost.overcast.sh"),
		clusterEndpointHostname("orders", clusterRoleReader, "us-east-1", "localhost.overcast.sh"),
		instanceEndpointHostname("orders-reader", "us-east-1", "localhost.overcast.sh"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("new writer aliases %v missing %q", got, want)
		}
	}
}

// Deleting the writer is the path that produces a promotion in the first place.
// End to end: the cluster's own endpoint must resolve to the survivor's
// container afterwards, which is the name a client holding the cluster endpoint
// actually dials.
func TestDeleteDBInstance_clusterEndpointFollowsThePromotedWriter(t *testing.T) {
	d := newNetworkDaemon(t)
	h := newFailoverHandler(t, d)
	ctx := t.Context()

	seedCluster(t, h, "orders")
	seedContainerMember(t, h, "orders", "orders-writer", "container-writer")
	seedContainerMember(t, h, "orders", "orders-reader", "container-reader")

	deleteInstance(t, h, "orders-writer")
	h.scheduler.Settle()

	c, aerr := h.store.getDBCluster(ctx, "orders")
	if aerr != nil {
		t.Fatalf("getDBCluster: %s", aerr.Message)
	}
	writer := h.clusterWriterInstance(ctx, c)
	if writer == nil {
		t.Fatal("cluster has no writer instance after its writer was deleted")
	}
	if writer.DBInstanceIdentifier != "orders-reader" {
		t.Errorf("cluster writer = %q, want orders-reader", writer.DBInstanceIdentifier)
	}

	_, aliases := d.snapshot()
	want := clusterEndpointHostname("orders", clusterRoleWriter, "us-east-1", "localhost.overcast.sh")
	if got := aliases["container-reader"]; !slices.Contains(got, want) {
		t.Errorf("promoted container aliases %v do not include the cluster writer endpoint %q", got, want)
	}
}

// A cluster left with no members has nobody to hand the names to, and must not
// go looking. This is the case
// TestClusterEndpoints_withoutAWriterKeepTheNameAndClusterPort pins on the
// response side — CloudFormation depends on that fallback staying put.
func TestDeleteDBInstance_lastMemberMovesNoAliases(t *testing.T) {
	d := newNetworkDaemon(t)
	h := newFailoverHandler(t, d)

	seedCluster(t, h, "orders")
	seedContainerMember(t, h, "orders", "orders-only", "container-only")

	deleteInstance(t, h, "orders-only")
	h.scheduler.Settle()

	if calls, _ := d.snapshot(); len(calls) != 0 {
		t.Errorf("daemon calls = %v, want none — there is no writer to move the names to", calls)
	}
}
