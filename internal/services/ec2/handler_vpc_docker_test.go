package ec2

// handler_vpc_docker_test.go — Overcast's own attachment to the Docker networks
// it creates for VPCs.
//
// A VPC network is a private bridge. Without joining it, nothing in the
// Overcast process can dial an address on it — which is exactly what the ELB
// data plane has to do to forward a request to an awsvpc task.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/state"
)

// networkDaemon records the network create/connect/disconnect/remove calls a
// handler makes.
type networkDaemon struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []string
}

func newNetworkDaemon(t *testing.T) *networkDaemon {
	t.Helper()
	d := &networkDaemon{}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.calls = append(d.calls, r.Method+" "+r.URL.Path)
		d.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/networks/create"):
			_, _ = w.Write([]byte(`{"Id":"net-created"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(d.server.Close)
	return d
}

func (d *networkDaemon) saw(call string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == call {
			return true
		}
	}
	return false
}

// containerisedHandler returns a handler that believes it is running in the
// container named "overcast-self", wired to d.
func containerisedHandler(t *testing.T, d *networkDaemon) *Handler {
	t.Helper()
	origInContainer, origHostname := runningInContainer, ownHostname
	t.Cleanup(func() { runningInContainer, ownHostname = origInContainer, origHostname })
	runningInContainer = func() bool { return true }
	ownHostname = func() (string, error) { return "overcast-self", nil }

	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	h := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.NewMock()).handler
	h.docker = docker.NewClient(strings.Replace(d.server.URL, "http://", "tcp://", 1), zap.NewNop())
	h.dockerReady.Store(true)
	return h
}

func TestCreateDockerVPCNetwork_attachesOvercastToIt(t *testing.T) {
	// Given: a containerised Overcast with Docker available.
	daemon := newNetworkDaemon(t)
	h := containerisedHandler(t, daemon)

	// When: a VPC's Docker network is created.
	netID, err := h.createDockerVPCNetwork(context.Background(), &VPC{VpcID: "vpc-1", CidrBlock: "10.9.0.0/16"})
	if err != nil {
		t.Fatalf("createDockerVPCNetwork: %v", err)
	}

	// Then: Overcast joined it. Otherwise the load balancer data plane cannot
	// reach any task placed in this VPC, and forwarding times out.
	if !daemon.saw("POST /v1.45/networks/" + netID + "/connect") {
		t.Errorf("expected Overcast to connect to %s, saw %v", netID, daemon.calls)
	}
}

func TestRemoveDockerVPCNetwork_detachesOvercastFirst(t *testing.T) {
	// Given: a containerised Overcast attached to a VPC network.
	daemon := newNetworkDaemon(t)
	h := containerisedHandler(t, daemon)

	// When: the network is removed.
	if err := h.removeDockerVPCNetwork(context.Background(), "net-created"); err != nil {
		t.Fatalf("removeDockerVPCNetwork: %v", err)
	}

	// Then: Overcast left it first — Docker refuses to remove a network that
	// still has endpoints, so joining without leaving makes VPCs undeletable.
	if !daemon.saw("POST /v1.45/networks/net-created/disconnect") {
		t.Errorf("expected Overcast to disconnect first, saw %v", daemon.calls)
	}
	if !daemon.saw("DELETE /v1.45/networks/net-created") {
		t.Errorf("expected the network to be removed, saw %v", daemon.calls)
	}
}

func TestSelfContainer_notContainerised(t *testing.T) {
	// Given: Overcast running on the host rather than in a container.
	daemon := newNetworkDaemon(t)
	h := containerisedHandler(t, daemon)
	runningInContainer = func() bool { return false }

	// When/Then: there is no container to attach, so nothing is attempted —
	// reaching a bridge subnet from the host is the host's routing question.
	if _, ok := h.selfContainer(); ok {
		t.Error("expected no self container when not containerised")
	}
	h.joinVPCNetwork(context.Background(), "net-created")
	if daemon.saw("POST /v1.45/networks/net-created/connect") {
		t.Error("expected no connect attempt from a host-mode Overcast")
	}
}
