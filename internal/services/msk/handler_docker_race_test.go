package msk

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

// TestSetClusterEndpoint_deletedMidStartTearsDownContainer covers the window
// between a container starting and its ID reaching the cluster record.
// deleteCluster stops the ID it finds on the record, so a delete landing in
// that window has nothing to stop — the start goroutine is the only party
// holding the ID and therefore owns the teardown. Same race as RDS (#412) and
// ElastiCache (#459); tracked for MSK in #460.
func TestSetClusterEndpoint_deletedMidStartTearsDownContainer(t *testing.T) {
	// Given: a handler whose Docker calls go to a daemon that is not there.
	// The teardown path must still run — what matters is that the record does
	// not gain a container ID and the port is released, not that Docker
	// answers.
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://127.0.0.1:1", zap.NewNop())
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	const arn = "arn:aws:kafka:us-east-1:123456789012:cluster/compat-msk/abcd"
	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: arn, ClusterName: "compat-msk", State: "DELETING",
		CreationTime: time.Now(),
	}); aerr != nil {
		t.Fatalf("seed cluster: %v", aerr)
	}

	// When: the in-flight start finally reports its container
	h.setClusterEndpoint(ctx, arn, "cafecafecafe", 49092)

	// Then: the ID is not written onto a deleting record. Recording it would
	// leave the container running behind a record that is about to vanish,
	// with the delete's stop having already run against an empty ID.
	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("get cluster: %v", aerr)
	}
	if got.DockerContainerID != "" {
		t.Errorf("DockerContainerID = %q, want empty — a deleting cluster must not adopt the container", got.DockerContainerID)
	}
}

func TestSetClusterEndpoint_recordsContainerForALiveCluster(t *testing.T) {
	// Given: a cluster that is still being created
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	const arn = "arn:aws:kafka:us-east-1:123456789012:cluster/compat-msk/abcd"
	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: arn, ClusterName: "compat-msk", State: "CREATING",
		CreationTime: time.Now(),
	}); aerr != nil {
		t.Fatalf("seed cluster: %v", aerr)
	}

	// When/Then: the normal path is unchanged — the cluster adopts its
	// container so deleteCluster can stop it later.
	h.setClusterEndpoint(ctx, arn, "cafecafecafe", 49092)
	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("get cluster: %v", aerr)
	}
	if got.DockerContainerID != "cafecafecafe" || got.HostPort != 49092 {
		t.Errorf("cluster = %q/%d, want the container id and port recorded", got.DockerContainerID, got.HostPort)
	}
}
