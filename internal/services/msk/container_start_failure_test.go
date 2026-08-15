package msk

// container_start_failure_test.go — the cluster whose broker container could
// not be built at all.
//
// The MSK counterpart of the ElastiCache file of the same name, and the third
// of the three things that can happen to a create. metadata_only_test.go covers
// the deployment with no Docker, where startClusterAsync reports that nothing
// is coming and the create settles the cluster itself. readiness_test.go covers
// the broker that started but never answered, which the readiness watch settles
// on its own budget. This file is the case where Docker was wired, the start
// ran, and the daemon refused.
//
// startClusterAsync returned true for that case — a start *was* dispatched, so
// the create correctly kept its hands off CREATING — and then the goroutine
// behind it logged a warning and returned. The readiness watch that would have
// owned the cluster from there is scheduled only once the container is up, so
// nothing was left to move it: the cluster stayed CREATING for as long as the
// emulator ran, `aws kafka wait cluster-active` spun out its attempts, and a
// CloudFormation stack held its full 120s budget before failing on a timeout
// rather than on the refusal the emulator already knew about.
//
// failCluster (readiness.go) already records FAILED with a StateInfo carrying
// the reason, and guards on clusterAwaiting so a cluster something else has
// moved on is left alone. This test is the start path reaching it.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
)

func TestCreateCluster_reportsFailedWhenTheContainerCannotBeCreated(t *testing.T) {
	fd := newFakeMSKDockerDaemon(t)
	fd.refuseCreates()
	h, _, _ := newReadinessHandler(t)
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")

	arn := createClusterVia(t, h, h.createCluster, map[string]any{"clusterName": "unbuildable-kafka"})
	h.dockerWg.Wait()

	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("getCluster: %s", aerr.Message)
	}
	if got.State == "CREATING" {
		t.Fatal("State is still CREATING: the broker container could not be built and no " +
			"readiness watch was ever scheduled, so nothing will move it — every waiter " +
			"on it spins until it gives up, and a CloudFormation stack fails on a timeout " +
			"rather than on the reason")
	}
	if got.State != stateFailed {
		t.Errorf("State = %q, want %q", got.State, stateFailed)
	}
	if got.StateInfo == nil || got.StateInfo.Message == "" {
		t.Error("cluster failed with no StateInfo message — the caller is told it failed but not why")
	}
}
