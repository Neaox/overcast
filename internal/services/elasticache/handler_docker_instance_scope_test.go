package elasticache

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/state"
)

// Two Overcasts sharing one Docker daemon keep separate state stores but draw
// resource IDs from the same place the user does: a cache cluster ID is a name
// the caller picks, so both can hold one called "sessions" and both containers
// carry overcast.resource-id=sessions. Matching a container to a record on that
// alone lets each one read the other's container as its own.
//
// These tests fix the two ways that goes wrong — a neighbour's container acted
// on as if it were ours, and a neighbour's container shadowing ours in the
// reconcile index — for each of the three record kinds ElastiCache reconciles.
const (
	neighbourContainerID = "d0d0d0d0d0d0"
	neighbourInstance    = "another-overcast-instance"
	ownContainerID       = "cafecafecafe"
)

// neighbourSummary is the other Overcast's container for resourceID, as it
// appears in a listing: same service, same resource ID, different identity.
func neighbourSummary(resourceID, state string) docker.ContainerSummary {
	return docker.ContainerSummary{
		ID:    neighbourContainerID,
		Names: []string{"/overcast-elasticache-" + resourceID},
		State: state,
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    serviceName,
			docker.LabelResourceID: resourceID,
			docker.LabelInstance:   neighbourInstance,
		},
	}
}

// ownSummary is the container this handler created for resourceID, stamped
// with the identity its own store resolves.
func ownSummary(t *testing.T, h *Handler, resourceID, state string) docker.ContainerSummary {
	t.Helper()
	return docker.ContainerSummary{
		ID:    ownContainerID,
		Names: []string{"/overcast-elasticache-" + resourceID},
		State: state,
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    serviceName,
			docker.LabelResourceID: resourceID,
			docker.LabelInstance:   h.instances.Resolve(context.Background()),
		},
	}
}

// storeCluster records an available cache cluster already backed by a
// container this instance created.
func storeCluster(t *testing.T, h *Handler, ctx context.Context, id string) {
	t.Helper()
	if aerr := h.store.putCacheCluster(ctx, &CacheCluster{
		CacheClusterId:        id,
		CacheClusterStatus:    "available",
		Engine:                "redis",
		CacheNodeType:         "cache.t3.micro",
		NumCacheNodes:         1,
		DockerContainerID:     ownContainerID,
		HostPort:              6379,
		ConfigurationEndpoint: &ClusterEndpoint{Address: "127.0.0.1", Port: 6379},
	}); aerr != nil {
		t.Fatalf("seed cache cluster: %s", aerr.Message)
	}
}

func clusterStatus(t *testing.T, h *Handler, ctx context.Context, id string) string {
	t.Helper()
	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("read cache cluster: %s", aerr.Message)
	}
	return got.CacheClusterStatus
}

// A die event for the neighbour's container names a cache cluster ID this
// instance also holds. Acting on it marks a cluster stopped whose own redis is
// still serving, and the next client to use the endpoint gets an error about a
// cluster nothing actually stopped.
func TestHandleContainerEvent_ignoresAnotherOvercastsCacheCluster(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	storeCluster(t, h, ctx, "sessions")

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: neighbourContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  "sessions",
			Instance:    neighbourInstance,
		},
	})

	if got := clusterStatus(t, h, ctx, "sessions"); got != "available" {
		t.Fatalf("another Overcast's container died and this instance's cluster went to %q; want it left available", got)
	}
}

// The same event for this instance's own container must still be acted on —
// the check has to reject a neighbour without also deafening the handler to
// its own containers.
func TestHandleContainerEvent_stillActsOnOwnCacheCluster(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	storeCluster(t, h, ctx, "sessions")

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: ownContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  "sessions",
			Instance:    h.instances.Resolve(ctx),
		},
	})
	fd.release()

	if got := clusterStatus(t, h, ctx, "sessions"); got != "modifying" {
		t.Fatalf("this instance's own container died and the cluster is %q; want modifying", got)
	}
}

// A container created before overcast.instance existed carries no identity.
// Refusing it would abandon a cluster this instance is genuinely running, so
// the record's own DockerContainerID vouches for it instead.
func TestHandleContainerEvent_unlabelledOwnContainerStillMatches(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	storeCluster(t, h, ctx, "sessions")

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: ownContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  "sessions",
		},
	})
	fd.release()

	if got := clusterStatus(t, h, ctx, "sessions"); got != "modifying" {
		t.Fatalf("an unlabelled container this instance recorded died and the cluster is %q; want modifying", got)
	}
}

// An unlabelled container that is not the one the record names is not this
// instance's either — nothing vouches for it, so it is left alone.
func TestHandleContainerEvent_unlabelledForeignContainerDoesNotMatch(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	storeCluster(t, h, ctx, "sessions")

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: neighbourContainerID,
			Action:      "die",
			Service:     serviceName,
			ResourceID:  "sessions",
		},
	})

	if got := clusterStatus(t, h, ctx, "sessions"); got != "available" {
		t.Fatalf("an unlabelled container this instance never recorded died and the cluster went to %q; want it left available", got)
	}
}

// The reconcile index kept one container per resource ID, so whichever of the
// two the daemon listed last won. Listed after ours, the neighbour's exited
// container decided the state of a cluster whose own redis was running.
func TestReconcileContainers_neighbourDoesNotShadowOwnCacheCluster(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	storeCluster(t, h, ctx, "sessions")

	h.reconcileContainers(ctx, []docker.ContainerSummary{
		ownSummary(t, h, "sessions", "running"),
		neighbourSummary("sessions", "exited"),
	})

	if got := clusterStatus(t, h, ctx, "sessions"); got != "available" {
		t.Fatalf("a neighbour's exited container shadowed this instance's running one and the cluster went to %q; want it left available", got)
	}
}

// The other direction: this instance's container is genuinely gone and only
// the neighbour's is on the daemon. Adopting it hands both Overcasts one redis
// — and this one will later stop and delete it on the strength of its own
// record.
func TestReconcileContainers_doesNotAdoptAnotherOvercastsCacheCluster(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	storeCluster(t, h, ctx, "sessions")

	h.reconcileContainers(ctx, []docker.ContainerSummary{
		neighbourSummary("sessions", "running"),
	})
	fd.release()

	if got := clusterStatus(t, h, ctx, "sessions"); got != "modifying" {
		t.Fatalf("this instance's container was gone and it adopted a neighbour's; cluster is %q, want modifying", got)
	}
}

// Replication groups reconcile through the same index under an "rg:" resource
// label, and have the same collision: the group ID is the caller's name.
func TestReconcileContainers_neighbourDoesNotShadowOwnReplicationGroup(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	if aerr := h.store.putReplicationGroup(ctx, &ReplicationGroup{
		ReplicationGroupId:    "carts",
		Status:                "available",
		Engine:                "redis",
		DockerContainerID:     ownContainerID,
		HostPort:              6379,
		ConfigurationEndpoint: &ClusterEndpoint{Address: "127.0.0.1", Port: 6379},
	}); aerr != nil {
		t.Fatalf("seed replication group: %s", aerr.Message)
	}

	h.reconcileContainers(ctx, []docker.ContainerSummary{
		ownSummary(t, h, "rg:carts", "running"),
		neighbourSummary("rg:carts", "exited"),
	})

	got, aerr := h.store.getReplicationGroup(ctx, "carts")
	if aerr != nil {
		t.Fatalf("read replication group: %s", aerr.Message)
	}
	if got.Status != "available" {
		t.Fatalf("a neighbour's exited container shadowed this instance's running one and the group went to %q; want it left available", got.Status)
	}
}

// Scoping reconciliation alone would not hold. A record whose container is
// genuinely gone rebuilds, and the rebuild looks the container up by a name
// derived from the cluster ID — so it finds the neighbour's, confirms it is an
// Overcast ElastiCache container for that ID, and adopts it. That is how two
// emulators come to share one redis before reconciliation ever runs.
func TestStartCacheContainer_refusesToReuseAnotherOvercastsContainer(t *testing.T) {
	// A daemon on which the container name is already taken by another
	// Overcast's container for a cluster of the same name.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/containers/overcast-elasticache-sessions") && strings.HasSuffix(r.URL.Path, "/json") {
			body, err := json.Marshal(map[string]any{
				"Id":   neighbourContainerID,
				"Name": "/overcast-elasticache-sessions",
				"Config": map[string]any{"Labels": map[string]string{
					docker.LabelManaged:    "true",
					docker.LabelService:    serviceName,
					docker.LabelResourceID: "sessions",
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
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)

	err := h.startCacheContainer(testCtx(), &CacheCluster{
		CacheClusterId: "sessions", Engine: "redis", CacheNodeType: "cache.t3.micro", NumCacheNodes: 1,
	})
	if err == nil {
		t.Fatal("reused another Overcast's container for a cluster of the same name; want a refusal naming the collision")
	}
	if !strings.Contains(err.Error(), neighbourInstance) {
		t.Fatalf("refusal does not name the other instance, so the real reason is invisible: %v", err)
	}
}

// And serverless caches, under "serverless:".
func TestReconcileContainers_neighbourDoesNotShadowOwnServerlessCache(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	if aerr := h.store.putServerlessCache(ctx, &ServerlessCache{
		ServerlessCacheName: "events",
		Status:              "available",
		Engine:              "redis",
		DockerContainerID:   ownContainerID,
		HostPort:            6379,
		Endpoint:            &ClusterEndpoint{Address: "127.0.0.1", Port: 6379},
	}); aerr != nil {
		t.Fatalf("seed serverless cache: %s", aerr.Message)
	}

	h.reconcileContainers(ctx, []docker.ContainerSummary{
		ownSummary(t, h, "serverless:events", "running"),
		neighbourSummary("serverless:events", "exited"),
	})

	got, aerr := h.store.getServerlessCache(ctx, "events")
	if aerr != nil {
		t.Fatalf("read serverless cache: %s", aerr.Message)
	}
	if got.Status != "available" {
		t.Fatalf("a neighbour's exited container shadowed this instance's running one and the cache went to %q; want it left available", got.Status)
	}
}
