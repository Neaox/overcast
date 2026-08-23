package rds

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

// This test keeps the master-account guarantee on the real data plane. The
// lifecycle tests prove the initializer is installed before start; this proves
// the backing engine executes it and the requested account can bootstrap an
// application without a container-only administrator.
func TestMasterUser_realAuroraMySQLCanBootstrapDatabaseAndUser(t *testing.T) {
	svc, dc, ctx, suffix := newRealRDSTestService(t, "mysql:8.0")

	instanceID := "master-fidelity-" + suffix
	if _, aerr := svc.handler.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: instanceID,
		Engine:               "aurora-mysql",
		EngineVersion:        "3.04",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	inst := waitForDBInstanceStatus(t, svc.handler, ctx, instanceID, "available")

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
	waitForDBInstanceStatus(t, svc.handler, ctx, instanceID, "stopped")
	if _, aerr := svc.handler.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: instanceID}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst = waitForDBInstanceStatus(t, svc.handler, ctx, instanceID, "available")
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
	if _, aerr := svc.handler.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: instanceID,
		Engine:               "postgres",
		EngineVersion:        "16",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst := waitForDBInstanceStatus(t, svc.handler, ctx, instanceID, "available")

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
	// helpers.WithECSDocker explains why a leaked network is worse than untidy:
	// the daemon's address pool is small enough that leaks compound into
	// "Docker not available" for every later container test.
	// Removing a plane the loop never reached is not an error — RemoveNetwork
	// reports not-found, which the loop below already tolerates.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		for i := len(planes) - 1; i >= 0; i-- {
			if err := dc.RemoveNetwork(cleanupCtx, planes[i]); err != nil && !docker.IsNotFound(err) {
				t.Logf("cleanup: remove network %s: %v", planes[i], err)
			}
		}
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

func waitForDBInstanceStatus(t *testing.T, h *Handler, ctx context.Context, instanceID, want string) *DBInstance {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last *DBInstance
	for time.Now().Before(deadline) {
		got, aerr := h.store.getDBInstance(ctx, instanceID)
		if aerr == nil {
			last = got
			if got.DBInstanceStatus == want {
				return got
			}
			if got.DBInstanceStatus == "failed" {
				t.Fatalf("DB instance failed: %s", got.StatusReason)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("DB instance never reached %q; last record: %#v", want, last)
	return nil
}
