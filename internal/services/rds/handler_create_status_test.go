package rds

import (
	"context"
	"testing"
)

// handler_create_status_test.go — what "available" means on the create path.
//
// CreateDBInstance scheduled an unconditional zero-delay creating → available
// transition, which the scheduler runs synchronously with a real clock. It
// fired whether or not a container was being started, and the health check
// only ever moves creating/starting → available — so on the create path the
// health check found the instance already available and did nothing.
//
// Measured against a real MySQL container: the instance reported "available"
// 0.9s after CreateDBInstance and did not accept a connection until 27.8s. A
// create whose container never came up stayed "available" indefinitely, which
// is the failure reporting added for the start path never reaching the path
// most people use.

// A create that is starting a container must not claim the database is
// available before anything has confirmed it is. The health check owns that
// transition; create's job is to report "creating".
//
// The daemon is told not to answer on the published port, so this is the state
// of the world while an engine is still initialising its data directory —
// which against a real MySQL image is around half a minute.
func TestCreateDBInstance_doesNotClaimAvailableBeforeTheEngineAnswers(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "create-status"

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	h.dockerWg.Wait()

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceStatus == "available" {
		t.Fatal("instance reported available while its database was still starting — " +
			"nothing has confirmed the engine accepts connections")
	}
	if inst.DBInstanceStatus != "creating" {
		t.Errorf("status = %q, want %q while the container comes up", inst.DBInstanceStatus, "creating")
	}

	// The record must carry a dial target at all. Dropping it on the create
	// path left the health check falling back to the endpoint name, which only
	// a sibling container resolves — so it dialled something unreachable and
	// would now fail an instance whose database was perfectly fine.
	host, port := dialTarget(inst)
	if host == "" || port == 0 {
		t.Errorf("instance has no dial target (%q:%d) — the health check has nothing to dial", host, port)
	}
}

// And once the engine does answer, the health check promotes it.
func TestCreateDBInstance_becomesAvailableWhenTheEngineAnswers(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	const id = "create-status-ready"

	createRunningInstance(t, h, id)

	inst, aerr := h.store.getDBInstance(context.Background(), id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceStatus != "available" {
		t.Errorf("status = %q, want %q once the engine answers", inst.DBInstanceStatus, "available")
	}
	if inst.StatusReason != "" {
		t.Errorf("StatusReason = %q, want it cleared once available", inst.StatusReason)
	}
}

// With no Docker behind it there is no container to wait for, so a
// metadata-only instance still becomes available immediately — that path is
// what makes the emulator usable without Docker and must not regress.
func TestCreateDBInstance_metadataOnlyStillBecomesAvailableImmediately(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	h.dockerReady.Store(false)
	ctx := context.Background()
	const id = "create-metadata-only"

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceStatus != "available" {
		t.Errorf("metadata-only status = %q, want %q", inst.DBInstanceStatus, "available")
	}
}

// A create whose container cannot be built must report the failure, not sit at
// "available" with nothing behind it. This is the create-path counterpart of
// the start-path recovery.
func TestCreateDBInstance_reportsFailedWhenTheContainerCannotBeCreated(t *testing.T) {
	d := newLifecycleDaemon(t)
	d.refuseCreates()
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "create-unbuildable"

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	h.dockerWg.Wait()

	inst := waitForStatus(t, h, id, "failed")
	if inst.StatusReason == "" {
		t.Error("instance failed with no reason recorded")
	}
}
