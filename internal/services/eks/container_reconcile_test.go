package eks

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/state"
)

func newEKSReconcileService(t *testing.T, name string) (*Service, chan string) {
	t.Helper()
	s := New(&config.Config{
		Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive,
	}, state.NewMemoryStore(), zap.NewNop(), clock.New())
	s.SetDocker(docker.NewClient("http://docker.invalid", zap.NewNop()))
	cluster := &Cluster{
		Name: name, Arn: s.clusterARN("us-east-1", name), Status: "ACTIVE", Version: "1.31",
		Endpoint: "https://example.invalid", CreatedAt: time.Now(),
	}
	if err := s.putCluster(context.Background(), "us-east-1", cluster); err != nil {
		t.Fatalf("putCluster: %v", err)
	}
	started := make(chan string, 1)
	s.startLiveClusterHook = func(_ context.Context, _ string, cluster *Cluster) {
		started <- cluster.Name
	}
	t.Cleanup(s.liveCancel)
	return s, started
}

func TestReconcileContainersRestartsMissingActiveControlPlane(t *testing.T) {
	s, started := newEKSReconcileService(t, "demo")

	s.ReconcileContainers(context.Background(), nil)

	select {
	case got := <-started:
		if got != "demo" {
			t.Fatalf("recovered cluster = %q, want demo", got)
		}
	case <-time.After(time.Second):
		t.Fatal("missing ACTIVE control plane was not recovered")
	}
}

func TestReconcileContainersAdoptsRunningControlPlaneWithoutRestart(t *testing.T) {
	s, started := newEKSReconcileService(t, "demo")

	s.ReconcileContainers(context.Background(), []docker.ContainerSummary{{
		ID: "container-1", State: "running",
		Labels: s.instances.ManagedLabels(context.Background(), serviceName, "demo"),
	}})

	if runtime, ok := s.getLiveClusterRuntime("us-east-1", "demo"); !ok || runtime.containerID != "container-1" {
		t.Fatalf("adopted runtime = %#v, %v; want container-1", runtime, ok)
	}
	select {
	case got := <-started:
		t.Fatalf("running control plane unexpectedly restarted %q", got)
	default:
	}
}

func TestReconcileContainersDoesNotAdoptAnotherOvercastsControlPlane(t *testing.T) {
	s, started := newEKSReconcileService(t, "demo")
	labels := docker.ManagedLabels(serviceName, "demo")
	labels[docker.LabelInstance] = "another-overcast-instance"

	s.ReconcileContainers(context.Background(), []docker.ContainerSummary{{
		ID: "foreign-container", State: "running", Labels: labels,
	}})

	select {
	case got := <-started:
		if got != "demo" {
			t.Fatalf("recovered cluster = %q, want demo", got)
		}
	case <-time.After(time.Second):
		t.Fatal("foreign control plane was adopted instead of starting an owned replacement")
	}
}

func TestLiveRuntimeExitTriggersImmediateRecovery(t *testing.T) {
	s, started := newEKSReconcileService(t, "demo")
	s.setLiveClusterRuntime("us-east-1", "demo", &liveClusterRuntime{containerID: "container-1"})

	s.handleLiveRuntimeDied(context.Background(), events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: "container-1", Service: serviceName, ResourceID: "demo", Action: "die",
			Instance: s.instances.Resolve(context.Background()),
		},
	})

	select {
	case got := <-started:
		if got != "demo" {
			t.Fatalf("recovered cluster = %q, want demo", got)
		}
	case <-time.After(time.Second):
		t.Fatal("live control-plane exit was not recovered")
	}
}
