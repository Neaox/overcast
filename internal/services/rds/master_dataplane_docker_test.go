package rds

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/docker/dockertest"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/state"
)

// This test keeps the master-account guarantee on the real data plane. The
// lifecycle tests prove the initializer is installed before start; this proves
// the backing engine executes it and the requested account can bootstrap an
// application without a container-only administrator.
func TestMasterUser_realAuroraMySQLCanBootstrapDatabaseAndUser(t *testing.T) {
	svc, dc, ctx, suffix := newRealRDSTestService(t, "mysql:8.0")

	instanceID := "master-fidelity-" + suffix
	inst := startEngine(t, svc, dc, ctx, &createDBInstanceReq{
		DBInstanceIdentifier: instanceID,
		Engine:               "aurora-mysql",
		EngineVersion:        "3.04",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	})

	bootstrapSQL := "CREATE DATABASE application; " +
		"CREATE USER 'application_user'@'%' IDENTIFIED BY 'application-password'; " +
		"GRANT SELECT ON application.* TO 'application_user'@'%'; " +
		"SELECT CURRENT_ROLE(); " +
		"SHOW GRANTS FOR 'admin'@'%'; " +
		"SHOW GRANTS FOR 'rds_superuser_role'@'%'; " +
		"SELECT COUNT(*) AS remote_root_accounts FROM mysql.user WHERE User = 'root' AND Host = '%';"
	result, err := dc.Exec(ctx, inst.DockerContainerID, []string{
		"mysql", "--protocol=tcp", "--host=127.0.0.1", "--batch", "-u", "admin", "-e", bootstrapSQL,
	}, []string{"MYSQL_PWD=password123"})
	if err != nil {
		t.Fatalf("execute application bootstrap as master user: %v", err)
	}
	if result.ExitCode != 0 {
		logs, _ := dc.ContainerLogs(ctx, inst.DockerContainerID, "80")
		t.Fatalf("master-user bootstrap exited %d:\n%s\ncontainer logs:\n%s", result.ExitCode, result.Output, logs)
	}
	if !strings.Contains(result.Output, "`rds_superuser_role`@`%`") ||
		!strings.Contains(result.Output, " ON *.* TO `rds_superuser_role`@`%` WITH GRANT OPTION") {
		t.Errorf("master role or instance-wide grants do not match Aurora MySQL 3:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "remote_root_accounts\r\n0") &&
		!strings.Contains(result.Output, "remote_root_accounts\n0") {
		t.Errorf("remote root account exists or query output was unexpected:\n%s", result.Output)
	}

	change, err := dc.Exec(ctx, inst.DockerContainerID, []string{
		"mysql", "--protocol=tcp", "--host=127.0.0.1", "-u", "admin", "-e",
		"ALTER USER 'admin'@'%' IDENTIFIED BY 'changed-password';",
	}, []string{"MYSQL_PWD=password123"})
	if err != nil || change.ExitCode != 0 {
		t.Fatalf("change master password through SQL: err=%v exit=%d output=%s", err, change.ExitCode, change.Output)
	}
	if _, aerr := svc.handler.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: instanceID}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForDBInstanceStatus(t, svc.handler, dc, ctx, instanceID, "stopped")
	if _, aerr := svc.handler.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: instanceID}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst = waitForDBInstanceStatus(t, svc.handler, dc, ctx, instanceID, "available")
	probe, err := dc.Exec(ctx, inst.DockerContainerID, []string{
		"mysql", "--protocol=tcp", "--host=127.0.0.1", "-u", "admin", "-e", "SELECT 1;",
	}, []string{"MYSQL_PWD=changed-password"})
	if err != nil || probe.ExitCode != 0 {
		t.Fatalf("SQL-changed master password did not survive restart: err=%v exit=%d output=%s", err, probe.ExitCode, probe.Output)
	}
}

func TestMasterUser_realPostgresHasRDSSuperuserSemantics(t *testing.T) {
	svc, dc, ctx, suffix := newRealRDSTestService(t, "postgres:16")
	instanceID := "postgres-master-fidelity-" + suffix
	inst := startEngine(t, svc, dc, ctx, &createDBInstanceReq{
		DBInstanceIdentifier: instanceID,
		Engine:               "postgres",
		EngineVersion:        "16",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	})

	bootstrap, err := dc.Exec(ctx, inst.DockerContainerID, []string{
		"psql", "--host=127.0.0.1", "--username=admin", "--dbname=postgres",
		"--set=ON_ERROR_STOP=1", "--command=CREATE DATABASE application",
		"--command=CREATE ROLE application_user LOGIN",
	}, []string{"PGPASSWORD=password123"})
	if err != nil || bootstrap.ExitCode != 0 {
		t.Fatalf("execute application bootstrap as PostgreSQL master: err=%v exit=%d output=%s",
			err, bootstrap.ExitCode, bootstrap.Output)
	}

	roles, err := dc.Exec(ctx, inst.DockerContainerID, []string{
		"psql", "--host=127.0.0.1", "--username=admin", "--dbname=postgres", "--tuples-only", "--no-align",
		"--pset=pager=off",
		"--command=SELECT rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = 'admin'; " +
			"SELECT pg_has_role('admin', 'rds_superuser', 'MEMBER'), pg_has_role('admin', 'pg_monitor', 'MEMBER'), pg_has_role('admin', 'pg_signal_backend', 'MEMBER'), pg_has_role('admin', 'pg_checkpoint', 'MEMBER'), pg_has_role('admin', 'pg_use_reserved_connections', 'MEMBER'); " +
			"SELECT rolsuper FROM pg_roles WHERE rolname = 'rdsadmin';",
	}, []string{"PGPASSWORD=password123"})
	if err != nil || roles.ExitCode != 0 {
		t.Fatalf("inspect PostgreSQL master roles: err=%v exit=%d output=%s", err, roles.ExitCode, roles.Output)
	}
	if !strings.Contains(roles.Output, "f|t|t") || !strings.Contains(roles.Output, "t|t|t|t|t") {
		t.Errorf("PostgreSQL master lacks the expected RDS role capabilities:\n%s", roles.Output)
	}
	if !strings.HasSuffix(strings.TrimSpace(roles.Output), "t") {
		t.Errorf("rdsadmin is not the internal maintenance superuser:\n%s", roles.Output)
	}
}

func newRealRDSTestService(t *testing.T, image string) (*Service, *docker.Client, context.Context, string) {
	t.Helper()
	dc := docker.NewClient(config.DefaultDockerSocket(), zap.NewNop())
	if !dc.Available(5 * time.Second) {
		t.Skip("Docker not available, skipping RDS master-user data-plane test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	if err := docker.NewImagePuller(dc).Ensure(ctx, image); err != nil {
		t.Skipf("cannot fetch the %s backing image: %v", image, err)
	}

	suffix := strings.ReplaceAll(protocol.NewRequestID(), "-", "")[:12]
	cfg := &config.Config{
		Region: "us-east-1", AccountID: "000000000000", Network: "overcast_rds_master_test_" + suffix,
		RDSPortBase: probeFreePort(t),
	}
	planes := dataplane.Networks(cfg, dataplane.Placement{})
	// Registered before the planes are created, not after. There is more than
	// one, so a failure partway through the loop below is a t.Fatalf with the
	// earlier planes already created — and a cleanup registered after the loop
	// is never registered at all, leaking them for the life of the daemon.
	// dockertest explains why a leaked network is worse than untidy: the
	// daemon's address pool is small enough that leaks compound into "Docker
	// not available" for every later container test.
	//
	// RemoveOwned skips a plane the loop never reached, removes any container
	// of ours the service's Stop left behind before the network it holds, waits
	// out the daemon's asynchronous endpoint release, and logs whatever it
	// still could not remove. Data plane first, control last: the order the
	// containers were attached in, reversed.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		reversed := slices.Clone(planes)
		slices.Reverse(reversed)
		dockertest.RemoveOwned(cleanupCtx, dc, reversed, t.Logf)
	})
	for _, plane := range planes {
		if _, err := dc.CreateNetwork(ctx, plane); err != nil {
			t.Fatalf("create test network %s: %v", plane, err)
		}
	}

	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	svc.SetDocker(dc)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		svc.Stop(cleanupCtx)
	})
	return svc, dc, ctx, suffix
}

// engineStartAttempts bounds the retry in startEngine. Three is enough for a
// daemon that refused once and is otherwise working, and small enough that a
// daemon which cannot run this image at all is reported in seconds rather than
// after minutes of hopeful retrying.
const engineStartAttempts = 3

// startEngine creates the DB instance and returns it once the engine is
// available, retrying a container start the Docker daemon refused and skipping
// when it refuses every time.
//
// #1625: this failed once on a CI runner with "the database container could not
// be created: start container …", on a pull request that touched only docs. A
// daemon that will not start a container is a fact about the runner, not about
// the master-account guarantee these tests exist to keep, and reporting it as a
// failure spends somebody's morning on a red build that means nothing.
//
// **It is still a real test on a working daemon**, and that is the line drawn
// here: only the daemon's own refusal to create or start the container is
// retried, and only that is skipped (daemonRefusedTheContainer). An engine that
// starts and then behaves wrongly — the failure this file is actually for —
// fails exactly as loudly as it did before, now with the container's state and
// its last output attached.
func startEngine(t *testing.T, svc *Service, dc *docker.Client, ctx context.Context,
	req *createDBInstanceReq) *DBInstance {
	t.Helper()

	var refusals []string
	for attempt := 1; attempt <= engineStartAttempts; attempt++ {
		if _, aerr := svc.handler.createDBInstanceTyped(ctx, req); aerr != nil {
			t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
		}
		inst, reason := awaitDBInstanceStatus(svc.handler, ctx, req.DBInstanceIdentifier, "available")
		if reason == "" {
			return inst
		}
		if !daemonRefusedTheContainer(reason) {
			t.Fatalf("DB instance never became available: %s\n%s", reason, engineDiagnostics(dc, inst))
		}
		refusals = append(refusals, fmt.Sprintf("attempt %d/%d: %s", attempt, engineStartAttempts, reason))
		t.Logf("the Docker daemon refused this test's engine container (%s); retrying", reason)
		discardInstance(t, svc.handler, ctx, req.DBInstanceIdentifier)
	}

	// Skip, not fail. Every attempt was refused before the engine ran a single
	// query, so nothing this test asserts was ever exercised, and calling that
	// a data-plane regression points the next reader at the wrong code.
	t.Skipf("the Docker daemon would not start this test's %s container in %d attempts, so none of the "+
		"master-account behaviour under test ever ran. This is a fault in the daemon or the runner, not "+
		"in RDS:\n%s", req.Engine, engineStartAttempts, strings.Join(refusals, "\n"))
	return nil
}

// daemonRefusedTheContainer reports whether a failure reason is the Docker
// daemon declining to create or start the container, rather than anything
// Overcast or the engine did.
//
// Deliberately narrow. These are the two calls the handler makes straight
// through to the Engine API (see startDBContainer), and a refusal from either
// means the engine never ran — so there is nothing about RDS to conclude from
// it. Everything else, including a credential initializer that could not be
// built and an engine that started and then failed its health check, is a
// result and is reported as one.
func daemonRefusedTheContainer(reason string) bool {
	for _, refusal := range []string{"create container", "start container"} {
		if strings.Contains(reason, refusal) {
			return true
		}
	}
	return false
}

// discardInstance deletes an instance that failed to start and waits for its
// record to go, so the retry can reuse the identifier. Best effort: a delete
// that does not land leaves a CreateDBInstance conflict the caller will report,
// which is a clearer failure than anything invented here.
func discardInstance(t *testing.T, h *Handler, ctx context.Context, instanceID string) {
	t.Helper()
	if _, aerr := h.deleteDBInstanceTyped(ctx, &deleteDBInstanceReq{DBInstanceIdentifier: instanceID}); aerr != nil {
		t.Logf("discard failed instance %s: %s: %s", instanceID, aerr.Code, aerr.Message)
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, aerr := h.store.getDBInstance(ctx, instanceID); aerr != nil {
			return // gone, which is what the retry needs
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("instance %s was still on record 30s after its delete; the retry may collide", instanceID)
}

// engineDiagnostics is what the daemon can still be asked about a container
// that did not do what this test needed: its state, and the tail of its output.
//
// It exists because the reason on the record is one line — "the database
// container could not be created: start container …" — and a line is not enough
// to tell a runner fault from an engine that started and died on its own
// configuration. The logs are where the engine says which.
//
// Its own context: the caller's is the test's five-minute budget, and by the
// time this runs that budget is often what expired.
func engineDiagnostics(dc *docker.Client, inst *DBInstance) string {
	if inst == nil || inst.DockerContainerID == "" {
		return "no container is on record for this instance: the daemon refused it before it ran, " +
			"so there is no state and no output to show"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var b strings.Builder
	id := inst.DockerContainerID
	if info, err := dc.InspectContainer(ctx, id); err == nil {
		fmt.Fprintf(&b, "container %s: status=%q running=%t exit=%d oom=%t error=%q started=%s finished=%s",
			id[:min(12, len(id))], info.State.Status, info.State.Running, info.State.ExitCode,
			info.State.OOMKilled, info.State.Error, info.State.StartedAt, info.State.FinishedAt)
	} else {
		fmt.Fprintf(&b, "container %s could not be inspected: %v (it may already have been removed, "+
			"which is what the handler does to a container whose start failed)", id[:min(12, len(id))], err)
	}
	logs, err := dc.ContainerLogs(ctx, id, "40")
	// De-framed: the Engine's log endpoint returns a multiplexed stream, and its
	// 8-byte frame headers read as control characters in a test's output.
	plain := strings.TrimSpace(string(docker.DemuxStream(logs)))
	switch {
	case err != nil:
		fmt.Fprintf(&b, "\ncontainer logs unavailable: %v", err)
	case plain == "":
		b.WriteString("\ncontainer logs: (empty — the engine produced no output at all)")
	default:
		b.WriteString("\ncontainer logs (last 40 lines):\n" + plain)
	}
	return b.String()
}

// waitForDBInstanceStatus blocks until the instance reaches want, and fails the
// test with the container's state and output when it does not.
func waitForDBInstanceStatus(t *testing.T, h *Handler, dc *docker.Client, ctx context.Context,
	instanceID, want string) *DBInstance {
	t.Helper()
	inst, reason := awaitDBInstanceStatus(h, ctx, instanceID, want)
	if reason != "" {
		t.Fatalf("%s\n%s", reason, engineDiagnostics(dc, inst))
	}
	return inst
}

// awaitDBInstanceStatus polls until the instance reaches want, and returns the
// last record it read plus the reason it gave up — empty when it did not.
//
// Returning the reason rather than failing on it is what lets startEngine tell
// a daemon that refused the container from an engine that ran and misbehaved.
// The record comes back either way: it carries the container id the diagnostics
// need, when there is one.
func awaitDBInstanceStatus(h *Handler, ctx context.Context, instanceID, want string) (*DBInstance, string) {
	deadline := time.Now().Add(2 * time.Minute)
	var last *DBInstance
	for time.Now().Before(deadline) {
		got, aerr := h.store.getDBInstance(ctx, instanceID)
		if aerr == nil {
			last = got
			if got.DBInstanceStatus == want {
				return got, ""
			}
			if got.DBInstanceStatus == "failed" {
				return last, "DB instance failed: " + got.StatusReason
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	status := "(never read)"
	if last != nil {
		status = last.DBInstanceStatus
	}
	return last, fmt.Sprintf("DB instance never reached %q within 2m; last status %q", want, status)
}

// The classifier decides whether a failure is retried and skipped or reported,
// so getting it wrong in either direction is worse than not having it: a real
// regression skipped looks like a green run, and a runner fault reported looks
// like a broken data plane. Needs no daemon.
func TestDaemonRefusedTheContainer_separatesARunnerFaultFromAResult(t *testing.T) {
	refusals := []string{
		// The CI failure this came from (#1625).
		"the database container could not be created: start container: start container 6cd6f1b0: status 500: ",
		"the database container could not be created: create container: create container: status 500: " +
			"could not find an available, non-overlapping IPv4 address pool",
	}
	for _, reason := range refusals {
		if !daemonRefusedTheContainer(reason) {
			t.Errorf("daemonRefusedTheContainer(%q) = false, want true: the engine never ran", reason)
		}
	}

	results := []string{
		// Overcast's own doing, both of them: the engine would have started.
		"the database container could not be created: build database credential initializer: bad template",
		"the database container could not be created: install database credential initializer: no such file",
		// The engine started and then failed on its own configuration.
		"the database engine did not become reachable",
		"DB instance never reached \"available\" within 2m; last status \"creating\"",
	}
	for _, reason := range results {
		if daemonRefusedTheContainer(reason) {
			t.Errorf("daemonRefusedTheContainer(%q) = true, want false: this is a result, not a runner fault",
				reason)
		}
	}
}
