package rds

// health_race_test.go — the health check is a writer too.
//
// While an instance is coming up, the check reads its record, looks at the
// container, dials the engine, and then settles the instance. Everything
// between the read and the write takes real time — a Docker inspect and a
// connection attempt, repeated every healthCheckInterval until the engine
// answers or the budget runs out — and an API call landing in that window used
// to be reverted by it: the check wrote back the record it had read before the
// dial, silently undoing whatever the call had changed.
//
// The same stale-snapshot bug the container-start tests next door cover, on the
// path with by far the widest window. See locks.go.

import (
	"context"
	"net"
	"strconv"
	"testing"
)

func TestHealthCheck_settlingDoesNotRevertAModifyThatLandedWhileItPolled(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := context.Background()
	const id = "race-health"

	// An engine that answers, so the check's dial succeeds and it settles the
	// instance rather than retrying.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	// Given: an instance still coming up, with a container to inspect
	if aerr := h.store.putDBInstance(ctx, &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		EngineVersion:        "8.0",
		DBInstanceClass:      "db.t3.micro",
		DBInstanceStatus:     "creating",
		DockerContainerID:    fd.containerID,
		Endpoint:             &Endpoint{Address: host, Port: port},
		DialAddress:          host,
		DialPort:             port,
	}); aerr != nil {
		t.Fatalf("seed instance: %s", aerr.Message)
	}

	// When: a ModifyDBInstance lands after the check has read the record and
	// before its dial returns — held open at the container inspect, which the
	// check does in exactly that gap
	fd.holdInspect()
	h.scheduleHealthCheck("us-east-1", id, host, port)
	fd.waitInspect(t)

	if _, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		DBInstanceClass:      "db.t3.large",
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	fd.releaseInspect()
	h.scheduler.Settle()

	// Then: the modification is still there. A check that wrote its snapshot
	// back would have reverted the class to what it read, and declared the
	// instance available on top — a status the modify had moved off.
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceClass != "db.t3.large" {
		t.Errorf("DBInstanceClass = %q, want db.t3.large — the health check wrote back the record it read before dialling (status %q)",
			inst.DBInstanceClass, inst.DBInstanceStatus)
	}
}
