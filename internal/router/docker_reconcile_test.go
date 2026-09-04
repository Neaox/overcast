package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/state"
)

type recordingDockerReconciler struct {
	name       string
	containers []docker.ContainerSummary
	networks   []docker.NetworkSummary
}

func (r *recordingDockerReconciler) Name() string              { return r.name }
func (r *recordingDockerReconciler) RegisterRoutes(chi.Router) {}
func (r *recordingDockerReconciler) ReconcileContainers(_ context.Context, containers []docker.ContainerSummary) {
	r.containers = append([]docker.ContainerSummary(nil), containers...)
}
func (r *recordingDockerReconciler) ReconcileNetworks(_ context.Context, networks []docker.NetworkSummary) {
	r.networks = append([]docker.NetworkSummary(nil), networks...)
}

func TestReconcileDockerDaemonUsesOneSnapshotPerObjectKind(t *testing.T) {
	var mu sync.Mutex
	containerLists, networkLists := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1.45/containers/json":
			containerLists++
			_ = json.NewEncoder(w).Encode([]docker.ContainerSummary{
				{ID: "ecs-1", Labels: map[string]string{docker.LabelManaged: "true", docker.LabelService: "ecs"}},
				{ID: "rds-1", Labels: map[string]string{docker.LabelManaged: "true", docker.LabelService: "rds"}},
			})
		case "/v1.45/networks":
			networkLists++
			_ = json.NewEncoder(w).Encode([]docker.NetworkSummary{
				{ID: "ecs-net", Labels: map[string]string{docker.LabelManaged: "true", docker.LabelService: "ecs"}},
				{ID: "rds-net", Labels: map[string]string{docker.LabelManaged: "true", docker.LabelService: "rds"}},
			})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	ecs := &recordingDockerReconciler{name: "ecs"}
	rds := &recordingDockerReconciler{name: "rds"}
	reconcileDockerDaemon(context.Background(), docker.NewClient("tcp://"+server.Listener.Addr().String(), zap.NewNop()), []dockerReconcileTarget{
		{name: "ecs", service: ecs},
		{name: "rds", service: rds},
	}, zap.NewNop())

	if containerLists != 1 || networkLists != 1 {
		t.Fatalf("daemon list calls = containers %d, networks %d; want one of each", containerLists, networkLists)
	}
	if len(ecs.containers) != 1 || ecs.containers[0].ID != "ecs-1" || len(rds.containers) != 1 || rds.containers[0].ID != "rds-1" {
		t.Fatalf("container fan-out = ecs %#v, rds %#v", ecs.containers, rds.containers)
	}
	if len(ecs.networks) != 1 || ecs.networks[0].ID != "ecs-net" || len(rds.networks) != 1 || rds.networks[0].ID != "rds-net" {
		t.Fatalf("network fan-out = ecs %#v, rds %#v", ecs.networks, rds.networks)
	}
}

func TestDockerReconcileTargetsGroupServicesByDaemon(t *testing.T) {
	first := docker.NewClient("http://first.invalid", zap.NewNop())
	second := docker.NewClient("http://second.invalid", zap.NewNop())
	ecs := &recordingDockerReconciler{name: "ecs"}
	rds := &recordingDockerReconciler{name: "rds"}

	got := dockerReconcileTargetsByClient([]docker.ServiceResult{
		{Name: "ecs", Client: first},
		{Name: "rds", Client: first},
		{Name: "missing", Client: second},
	}, map[string]Service{"ecs": ecs, "rds": rds})

	if len(got) != 1 || got[first] == nil || len(got[first].targets) != 2 {
		t.Fatalf("targets by client = %#v, want both services on the first daemon", got)
	}
}

func TestDockerConnectedHandlerReconcilesOnlyTheConnectedDaemon(t *testing.T) {
	var mu sync.Mutex
	lists := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lists++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(server.Close)
	client := docker.NewClient("tcp://"+server.Listener.Addr().String(), zap.NewNop())
	target := &recordingDockerReconciler{name: "ecs"}
	groups := dockerReconcileTargetsByClient(
		[]docker.ServiceResult{{Name: "ecs", Client: client}},
		map[string]Service{"ecs": target},
	)
	handle := dockerConnectedHandler(groups, state.NewMemoryStore(), zap.NewNop())

	handle(context.Background(), events.Event{Payload: docker.DaemonConnectedPayload{
		Client: docker.NewClient("http://other.invalid", zap.NewNop()), Reconnected: true,
	}})
	if lists != 0 {
		t.Fatalf("another daemon's event made %d list calls, want 0", lists)
	}

	handle(context.Background(), events.Event{Payload: docker.DaemonConnectedPayload{Client: client}})
	if lists != 2 {
		t.Fatalf("initial connection made %d list calls, want one container and one network snapshot", lists)
	}
	handle(context.Background(), events.Event{Payload: docker.DaemonConnectedPayload{Client: client, Reconnected: true}})
	if lists != 4 {
		t.Fatalf("reconnect made %d cumulative list calls, want one new snapshot of each kind", lists)
	}
}

// migratingStore reports the startup phase a HybridStore is in while its
// one-time schema migration runs, and becomes ready when release is closed.
type migratingStore struct {
	state.Store
	release chan struct{}
}

func (s *migratingStore) NotReady() bool {
	select {
	case <-s.release:
		return false
	default:
		return true
	}
}

func (s *migratingStore) WaitReady(ctx context.Context) error {
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestDockerConnectedHandlerWaitsForTheStoreMigration pins the ordering that
// #1599's real failure turned on: the reconcile must not read a store that is
// still migrating. Every reconciler decides what the daemon's objects mean by
// what the store claims, and an empty read means "nothing claims any of this"
// — which for EC2's network pass is a sweep of the very networks its VPC
// records name.
func TestDockerConnectedHandlerWaitsForTheStoreMigration(t *testing.T) {
	var mu sync.Mutex
	lists := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lists++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(server.Close)
	client := docker.NewClient("tcp://"+server.Listener.Addr().String(), zap.NewNop())
	target := &recordingDockerReconciler{name: "ecs"}
	groups := dockerReconcileTargetsByClient(
		[]docker.ServiceResult{{Name: "ecs", Client: client}},
		map[string]Service{"ecs": target},
	)
	store := &migratingStore{Store: state.NewMemoryStore(), release: make(chan struct{})}
	handle := dockerConnectedHandler(groups, store, zap.NewNop())

	done := make(chan struct{})
	go func() {
		defer close(done)
		handle(context.Background(), events.Event{Payload: docker.DaemonConnectedPayload{Client: client}})
	}()

	// While the migration is in flight the daemon is not listed at all.
	select {
	case <-done:
		t.Fatal("the reconcile ran while the store was still migrating")
	case <-time.After(50 * time.Millisecond):
	}
	mu.Lock()
	early := lists
	mu.Unlock()
	if early != 0 {
		t.Fatalf("the daemon was listed %d times before the store was ready, want 0", early)
	}

	// Once it finishes, the pass runs against a store that reads back.
	close(store.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the reconcile never ran after the store became ready")
	}
	mu.Lock()
	defer mu.Unlock()
	if lists != 2 {
		t.Fatalf("after the store was ready the daemon was listed %d times, want one container and one network snapshot", lists)
	}
}
