package msk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/state"
)

// Two Overcasts sharing one Docker daemon keep separate state stores, and a
// container is matched to a cluster record by the overcast.resource-id label
// alone. These tests fix the two ways that goes wrong — a neighbour's
// container acted on as if it were ours, and a neighbour's container shadowing
// ours in the reconcile index.
//
// An MSK cluster ARN carries a minted UUID, so unlike ElastiCache's
// caller-chosen IDs two independent stores do not collide on one by accident.
// What the check earns here is the other half: the record's own container ID
// picks this instance's broker out of a listing that holds more than one
// container for the same ARN — a stale container from an earlier run beside
// the live one — instead of taking whichever the daemon listed last.
const (
	neighbourContainerID = "d0d0d0d0d0d0"
	neighbourInstance    = "another-overcast-instance"
	ownContainerID       = "mskownbroker01"
	scopeClusterARN      = "arn:aws:kafka:us-east-1:123456789012:cluster/events/abcd-1234"
)

// newScopeHandler wires a handler against a daemon that answers inspects, so
// the reconcile paths under test can resolve an endpoint.
func newScopeHandler(t *testing.T) *Handler {
	t.Helper()
	fd := newFakeMSKDockerDaemon(t)
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.dockerReady.Store(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
	})
	return h
}

// seedActiveCluster records an ACTIVE cluster already backed by a container
// this instance created.
func seedActiveCluster(t *testing.T, h *Handler) context.Context {
	t.Helper()
	ctx := clusterRegionCtx(scopeClusterARN)
	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn:        scopeClusterARN,
		ClusterName:       "events",
		State:             "ACTIVE",
		CreationTime:      time.Now(),
		DockerContainerID: ownContainerID,
		HostPort:          49092,
	}); aerr != nil {
		t.Fatalf("seed cluster: %v", aerr)
	}
	return ctx
}

func clusterState(t *testing.T, h *Handler, ctx context.Context) string {
	t.Helper()
	got, aerr := h.store.getCluster(ctx, scopeClusterARN)
	if aerr != nil {
		t.Fatalf("get cluster: %v", aerr)
	}
	return got.State
}

// neighbourBroker is another Overcast's container for the same cluster ARN, as
// it appears in a listing.
func neighbourBroker(state string) docker.ContainerSummary {
	return docker.ContainerSummary{
		ID:    neighbourContainerID,
		Names: []string{"/overcast-msk-abcd-1234"},
		State: state,
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    serviceName,
			docker.LabelResourceID: scopeClusterARN,
			docker.LabelInstance:   neighbourInstance,
		},
	}
}

// ownBroker is the container this handler created, stamped with the identity
// its own store resolves.
func ownBroker(t *testing.T, h *Handler, state string) docker.ContainerSummary {
	t.Helper()
	return docker.ContainerSummary{
		ID:    ownContainerID,
		Names: []string{"/overcast-msk-abcd-1234"},
		State: state,
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    serviceName,
			docker.LabelResourceID: scopeClusterARN,
			docker.LabelInstance:   h.instances.Resolve(context.Background()),
		},
	}
}

// A die event for a container this instance never created must not fail its
// cluster: the broker the record actually names never went anywhere, so the
// cluster is reported FAILED while it is still serving.
func TestHandleContainerEvent_ignoresAnotherOvercastsBroker(t *testing.T) {
	h := newScopeHandler(t)
	ctx := seedActiveCluster(t, h)

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: neighbourContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  scopeClusterARN,
			Instance:    neighbourInstance,
		},
	})

	if got := clusterState(t, h, ctx); got != "ACTIVE" {
		t.Fatalf("another Overcast's broker died and this instance's cluster went to %q; want it left ACTIVE", got)
	}
}

// The same event for this instance's own broker must still be acted on.
func TestHandleContainerEvent_stillActsOnOwnBroker(t *testing.T) {
	h := newScopeHandler(t)
	ctx := seedActiveCluster(t, h)

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: ownContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  scopeClusterARN,
			Instance:    h.instances.Resolve(ctx),
		},
	})

	if got := clusterState(t, h, ctx); got != "FAILED" {
		t.Fatalf("this instance's own broker died and the cluster is %q; want FAILED", got)
	}
}

// A container that predates overcast.instance carries no identity, and the
// record's own container ID has to vouch for it — refusing it would fail a
// cluster whose broker is genuinely this instance's.
func TestHandleContainerEvent_unlabelledOwnBrokerStillMatches(t *testing.T) {
	h := newScopeHandler(t)
	ctx := seedActiveCluster(t, h)

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: ownContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  scopeClusterARN,
		},
	})

	if got := clusterState(t, h, ctx); got != "FAILED" {
		t.Fatalf("an unlabelled broker this instance recorded died and the cluster is %q; want FAILED", got)
	}
}

// An unlabelled container the record does not name is not this instance's
// either — nothing vouches for it, so it is left alone.
func TestHandleContainerEvent_unlabelledForeignBrokerDoesNotMatch(t *testing.T) {
	h := newScopeHandler(t)
	ctx := seedActiveCluster(t, h)

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: neighbourContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  scopeClusterARN,
		},
	})

	if got := clusterState(t, h, ctx); got != "ACTIVE" {
		t.Fatalf("an unlabelled broker this instance never recorded died and the cluster went to %q; want it left ACTIVE", got)
	}
}

// The reconcile index kept one container per resource ID, so whichever the
// daemon listed last won. Listed after ours, a foreign exited container
// decided the state of a cluster whose own broker was running.
func TestReconcileContainers_neighbourDoesNotShadowOwnBroker(t *testing.T) {
	h := newScopeHandler(t)
	ctx := seedActiveCluster(t, h)

	h.reconcileContainers(ctx, []docker.ContainerSummary{
		ownBroker(t, h, "running"),
		neighbourBroker("exited"),
	})

	if got := clusterState(t, h, ctx); got != "ACTIVE" {
		t.Fatalf("a foreign exited container shadowed this instance's running broker and the cluster went to %q; want it left ACTIVE", got)
	}
}

// The other direction: this instance's broker is gone and only a foreign
// container for the ARN is on the daemon. Adopting it health-checks a broker
// this instance does not own and reports the cluster ACTIVE on it.
func TestReconcileContainers_doesNotAdoptAnotherOvercastsBroker(t *testing.T) {
	h := newScopeHandler(t)
	ctx := seedActiveCluster(t, h)

	h.reconcileContainers(ctx, []docker.ContainerSummary{
		neighbourBroker("running"),
	})

	if got := clusterState(t, h, ctx); got != "FAILED" {
		t.Fatalf("this instance's broker was gone and it adopted a foreign one; cluster is %q, want FAILED", got)
	}
}

// Scoping reconciliation alone would not hold: a cluster whose container is
// gone is restarted, and the restart looks the container up by a name derived
// from the cluster name, so it finds and adopts the neighbour's.
func TestStartClusterContainer_refusesToReuseAnotherOvercastsContainer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/containers/overcast-msk-abcd-1234") && strings.HasSuffix(r.URL.Path, "/json") {
			body, err := json.Marshal(map[string]any{
				"Id":   neighbourContainerID,
				"Name": "/overcast-msk-abcd-1234",
				"Config": map[string]any{"Labels": map[string]string{
					docker.LabelManaged:    "true",
					docker.LabelService:    serviceName,
					docker.LabelResourceID: scopeClusterARN,
					docker.LabelInstance:   neighbourInstance,
				}},
				"State":           map[string]any{"Status": "running", "Running": true},
				"NetworkSettings": map[string]any{"Networks": map[string]any{}, "Ports": map[string]any{}},
			})
			if err != nil {
				t.Errorf("encode the neighbouring container: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write(body) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	ctx := clusterRegionCtx(scopeClusterARN)
	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: scopeClusterARN, ClusterName: "events", State: "CREATING",
		CreationTime: time.Now(),
	}); aerr != nil {
		t.Fatalf("seed cluster: %v", aerr)
	}

	err := h.startClusterContainer(ctx, scopeClusterARN)
	if err == nil {
		t.Fatal("reused another Overcast's container for a cluster of the same name; want a refusal naming the collision")
	}
	if !strings.Contains(err.Error(), neighbourInstance) {
		t.Fatalf("refusal does not name the other instance, so the real reason is invisible: %v", err)
	}
}
