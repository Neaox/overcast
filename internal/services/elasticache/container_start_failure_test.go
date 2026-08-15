package elasticache

// container_start_failure_test.go — the create whose container could not be
// built at all.
//
// Three different things can happen to a create, and only two of them were
// answered. metadata_only_test.go covers the deployment with no Docker, where
// nothing was ever going to start and the record is ready the moment it is
// written. readiness_test.go covers the container that started but whose engine
// never answered, which the readiness watch settles on its own budget. This
// file is the third: Docker was wired, the create started a container, and the
// daemon refused.
//
// That case reached neither answer. The start goroutine logged a warning and
// returned, and the readiness watch it would have scheduled is scheduled only
// after the start succeeds — so nothing was left that would ever move the
// record on, and it sat in "creating" for as long as the emulator ran. It is
// the same untruth as the metadata-only stall, with a worse excuse: the
// emulator was holding the reason the whole time and said it only to its own
// log.
//
// What that cost the caller: `aws elasticache wait cache-cluster-available`
// spun out its attempts and gave up with nothing to say, and a CloudFormation
// stack waited the full 60s stabilization budget and then failed the stack on a
// timeout rather than on the failure that had already happened.
//
// RDS answers this correctly, and has since the create-path status work — see
// TestCreateDBInstance_reportsFailedWhenTheContainerCannotBeCreated. The
// terminal statuses and their fail helpers already exist here (readiness.go,
// which documents why a cache cluster fails to `incompatible-network` while a
// replication group and a serverless cache fail to `create-failed`). These
// tests are the five create paths reaching them.
//
// Each shape is tested through both the form-encoded handler and the typed one
// where both exist. They are separate implementations of the same create,
// picked by whichever wire protocol the caller's SDK negotiated, so fixing one
// and not the other would make the status depend on which SDK asked.

import (
	"net/url"
	"testing"
)

func TestCreateCacheCluster_reportsFailedWhenTheContainerCannotBeCreated(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	fd.refuseCreates()
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	const id = "unbuildable-cluster"

	createViaForm(t, ctx, h.CreateCacheCluster, url.Values{
		"CacheClusterId": []string{id},
		"Engine":         []string{"redis"},
	})
	h.dockerWg.Wait()

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus == "creating" {
		t.Fatal(`status is still "creating": the container could not be built and no readiness ` +
			"watch was ever scheduled, so nothing will move it — every waiter on it spins " +
			"until it gives up, and a CloudFormation stack fails on a timeout rather than " +
			"on the reason")
	}
	if got.CacheClusterStatus != statusClusterUnreachable {
		t.Errorf("status = %q, want %q", got.CacheClusterStatus, statusClusterUnreachable)
	}
	if got.StatusReason == "" {
		t.Error("cluster failed with no reason recorded — the caller is told it failed but not why")
	}
}

func TestCreateCacheClusterTyped_reportsFailedWhenTheContainerCannotBeCreated(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	fd.refuseCreates()
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	const id = "unbuildable-cluster-typed"

	if _, aerr := h.createCacheClusterTyped(ctx, &ecCreateCacheClusterReq{
		CacheClusterId: id, Engine: "redis", CacheNodeType: "cache.t3.micro", NumCacheNodes: 1,
	}); aerr != nil {
		t.Fatalf("CreateCacheCluster: %s: %s", aerr.Code, aerr.Message)
	}
	h.dockerWg.Wait()

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus != statusClusterUnreachable {
		t.Errorf("status = %q, want %q", got.CacheClusterStatus, statusClusterUnreachable)
	}
	if got.StatusReason == "" {
		t.Error("cluster failed with no reason recorded")
	}
}

func TestCreateReplicationGroup_reportsFailedWhenTheContainerCannotBeCreated(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	fd.refuseCreates()
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	const id = "unbuildable-rg"

	createViaForm(t, ctx, h.CreateReplicationGroup, url.Values{
		"ReplicationGroupId":          []string{id},
		"ReplicationGroupDescription": []string{"nothing could be built for it"},
		"Engine":                      []string{"redis"},
	})
	h.dockerWg.Wait()

	got, aerr := h.store.getReplicationGroup(ctx, id)
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if got.Status != statusCreateFailed {
		t.Errorf("status = %q, want %q", got.Status, statusCreateFailed)
	}
	if got.StatusReason == "" {
		t.Error("replication group failed with no reason recorded")
	}
}

func TestCreateReplicationGroupTyped_reportsFailedWhenTheContainerCannotBeCreated(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	fd.refuseCreates()
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	const id = "unbuildable-rg-typed"

	if _, aerr := h.createReplicationGroupTyped(ctx, &ecCreateReplicationGroupReq{
		ReplicationGroupId: id, ReplicationGroupDescription: "nothing could be built for it", Engine: "redis",
	}); aerr != nil {
		t.Fatalf("CreateReplicationGroup: %s: %s", aerr.Code, aerr.Message)
	}
	h.dockerWg.Wait()

	got, aerr := h.store.getReplicationGroup(ctx, id)
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if got.Status != statusCreateFailed {
		t.Errorf("status = %q, want %q", got.Status, statusCreateFailed)
	}
	if got.StatusReason == "" {
		t.Error("replication group failed with no reason recorded")
	}
}

func TestCreateServerlessCache_reportsFailedWhenTheContainerCannotBeCreated(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	fd.refuseCreates()
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	const name = "unbuildable-serverless"

	createViaForm(t, ctx, h.CreateServerlessCache, url.Values{
		"ServerlessCacheName": []string{name},
		"Engine":              []string{"redis"},
	})
	h.dockerWg.Wait()

	got, aerr := h.store.getServerlessCache(ctx, name)
	if aerr != nil {
		t.Fatalf("getServerlessCache: %s", aerr.Message)
	}
	if got.Status != statusCreateFailed {
		t.Errorf("status = %q, want %q", got.Status, statusCreateFailed)
	}
	if got.StatusReason == "" {
		t.Error("serverless cache failed with no reason recorded")
	}
}

// The failure must not be written over a record that something else has already
// moved on. A delete landing while the doomed start is still in flight owns the
// record, and a create failure arriving afterwards must not resurrect it as a
// failed cache the caller then has to clean up a second time.
//
// This is the same guard readiness_test.go holds for a straggling watch; it is
// worth holding on the create path too, because that is the path where the
// window is widest — the start is running before the container ID has been
// persisted, so the delete has nothing to stop and returns immediately.
func TestDeletedCacheClusterIsNotFailedByARefusedCreate(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	fd.refuseCreates()
	h := newDockerTestHandler(t, fd)
	ctx := testCtx()
	const id = "deleted-before-the-refusal"

	if _, aerr := h.createCacheClusterTyped(ctx, &ecCreateCacheClusterReq{
		CacheClusterId: id, Engine: "redis", CacheNodeType: "cache.t3.micro", NumCacheNodes: 1,
	}); aerr != nil {
		t.Fatalf("create: %s: %s", aerr.Code, aerr.Message)
	}
	h.dockerWg.Wait()

	if _, aerr := h.deleteCacheClusterTyped(ctx, &ecDeleteCacheClusterReq{CacheClusterId: id}); aerr != nil {
		t.Fatalf("delete: %s: %s", aerr.Code, aerr.Message)
	}

	// A second failure arriving after the delete — the shape a slow start
	// takes when the daemon answers only once the record is already gone.
	h.failCacheCluster(ctx, id, "a refusal that arrived too late")

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		// Deleted outright is the other correct answer, and is what a delete
		// of a cluster with no container to stop does.
		return
	}
	if got.CacheClusterStatus == statusClusterUnreachable {
		t.Errorf("status = %q: a late create failure overwrote a cluster the caller had already deleted",
			got.CacheClusterStatus)
	}
}
