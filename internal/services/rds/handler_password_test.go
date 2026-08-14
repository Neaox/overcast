package rds

// handler_password_test.go — rotating a DB instance's master password.
//
// ModifyDBInstance ignored MasterUserPassword entirely: the parameter was not
// on the request type, so a stack that rotated a database's master password
// reported a successful update and left both the record and the running
// container on the old one. CloudFormation had been passing the parameter all
// along, which made the silence look like success at every level above it.
//
// A password only means something if the engine honours it, so these tests are
// written against the container rather than the record: the change reaches the
// running engine, a change the engine refuses is reported rather than stored,
// and an engine that is not running is refused up front rather than left
// disagreeing with the record.

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func storedPassword(t *testing.T, h *Handler, id string) string {
	t.Helper()
	inst, aerr := h.store.getDBInstance(context.Background(), id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	return inst.MasterUserPassword
}

// The whole point: the new password has to reach the engine that is serving
// connections, not just the record describing it.
func TestModifyDBInstance_masterPasswordReachesTheRunningEngine(t *testing.T) {
	tests := []struct {
		name    string
		engine  string
		user    string
		wantCmd string   // argv[0] — the engine's own client
		wantSQL []string // statements the exec must carry
		// wantEnv is how the exec authenticates, with the *old* password. Every
		// engine does it by environment: argv is visible to `docker inspect`
		// and to the host process table, and both clients also print a warning
		// about a command-line password that would otherwise land in the middle
		// of whatever the engine says when a statement fails.
		wantEnv []string
	}{
		{
			name:    "mysql",
			engine:  "mysql",
			user:    "admin",
			wantCmd: "mysql",
			wantSQL: []string{
				`ALTER USER IF EXISTS 'root'@'localhost' IDENTIFIED BY 'new-password'`,
				`ALTER USER IF EXISTS 'admin'@'%' IDENTIFIED BY 'new-password'`,
			},
			wantEnv: []string{"MYSQL_PWD=old-password"},
		},
		{
			name:    "mariadb",
			engine:  "mariadb",
			user:    "root",
			wantCmd: "mariadb",
			wantSQL: []string{`ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY 'new-password'`},
			wantEnv: []string{"MYSQL_PWD=old-password"},
		},
		{
			name:    "postgres",
			engine:  "postgres",
			user:    "admin",
			wantCmd: "psql",
			wantSQL: []string{
				`ALTER USER "admin" WITH PASSWORD 'new-password'`,
				`ALTER USER "rdsadmin" WITH PASSWORD`,
			},
			wantEnv: []string{"PGPASSWORD=" + credentialBootstrapPassword("old-password")},
		},
		{
			name:    "aurora-postgresql runs the engine underneath it",
			engine:  "aurora-postgresql",
			user:    "admin",
			wantCmd: "psql",
			wantSQL: []string{
				`ALTER USER "admin" WITH PASSWORD 'new-password'`,
				`ALTER USER "rdsadmin" WITH PASSWORD`,
			},
			wantEnv: []string{"PGPASSWORD=" + credentialBootstrapPassword("old-password")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newLifecycleDaemon(t)
			h := newLifecycleHandler(t, d)
			const id = "rotate-me"
			createRunningInstanceWith(t, h, id, tc.engine, tc.user, "old-password")

			if _, aerr := h.modifyDBInstanceTyped(context.Background(), &modifyDBInstanceReq{
				DBInstanceIdentifier: id,
				MasterUserPassword:   "new-password",
			}); aerr != nil {
				t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
			}

			cmds, envs := d.recordedPasswordExecs()
			if len(cmds) != 1 {
				t.Fatalf("the password change ran %d commands in the container, want exactly 1: %v", len(cmds), cmds)
			}
			cmd := cmds[0]
			if cmd[0] != tc.wantCmd {
				t.Errorf("password change ran %q, want the engine's own client %q", cmd[0], tc.wantCmd)
			}
			joined := strings.Join(cmd, " ")
			for _, want := range tc.wantSQL {
				if !strings.Contains(joined, want) {
					t.Errorf("password change does not carry %q; ran: %v", want, cmd)
				}
			}
			for _, want := range tc.wantEnv {
				if !strings.Contains(strings.Join(envs[0], " "), want) {
					t.Errorf("password change environment %v does not carry %q", envs[0], want)
				}
			}
			// The old password authenticates the change, so it has to travel
			// somewhere — and argv is the one place it must not, because that
			// is what `docker inspect` and the host process table both show.
			if strings.Contains(joined, "old-password") {
				t.Errorf("the password the engine still has appears in argv, where it is visible outside the container: %v", cmd)
			}

			if got := storedPassword(t, h, id); got != "new-password" {
				t.Errorf("stored MasterUserPassword = %q, want the new password", got)
			}
		})
	}
}

// An engine that refuses the change must not leave the record claiming it
// happened — that is exactly the silent dishonesty this parameter had before.
func TestModifyDBInstance_masterPasswordEngineFailureIsNotStored(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	const id = "rotate-refused"
	createRunningInstanceWith(t, h, id, "mysql", "admin", "old-password")
	d.failExecs(1, "ERROR 1819 (HY000): Your password does not satisfy the current policy requirements")

	_, aerr := h.modifyDBInstanceTyped(context.Background(), &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		MasterUserPassword:   "new-password",
	})
	if aerr == nil {
		t.Fatal("ModifyDBInstance reported success for a password the engine rejected")
	}
	if aerr.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("HTTP status = %d, want 500 for a change the emulator could not apply", aerr.HTTPStatus)
	}
	if !strings.Contains(aerr.Message, "1819") {
		t.Errorf("error message %q does not carry the engine's own explanation", aerr.Message)
	}
	if got := storedPassword(t, h, id); got != "old-password" {
		t.Errorf("stored MasterUserPassword = %q, want it left at the password the engine still has", got)
	}
}

// A database that is not running cannot be told a new password, and there is
// no later moment at which Overcast could apply one: the container keeps the
// credentials it was initialised with. Refusing is the honest answer.
func TestModifyDBInstance_masterPasswordNeedsARunningEngine(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "rotate-stopped"
	createRunningInstanceWith(t, h, id, "mysql", "admin", "old-password")

	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	_, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		DBInstanceClass:      "db.t3.large",
		MasterUserPassword:   "new-password",
	})
	if aerr == nil {
		t.Fatal("ModifyDBInstance reported success for a password no running engine received")
	}
	if aerr.Code != "InvalidDBInstanceState" {
		t.Errorf("error code = %q, want InvalidDBInstanceState", aerr.Code)
	}
	if got := storedPassword(t, h, id); got != "old-password" {
		t.Errorf("stored MasterUserPassword = %q, want it left at the password the container has", got)
	}
	if cmds, _ := d.recordedPasswordExecs(); len(cmds) != 0 {
		t.Errorf("a stopped instance was sent %d commands: %v", len(cmds), cmds)
	}

	// Everything else on the same request is still refused with it — a
	// half-applied modification is its own kind of dishonesty.
	inst, sErr := h.store.getDBInstance(ctx, id)
	if sErr != nil {
		t.Fatalf("getDBInstance: %s", sErr.Message)
	}
	if inst.DBInstanceClass == "db.t3.large" {
		t.Error("the rest of the modification was applied around the rejected password")
	}
}

// Re-sending the password an instance already has is not a change, and must
// not go near the engine — CloudFormation replays every property on every
// update, so this is the common case, not an edge one.
func TestModifyDBInstance_unchangedMasterPasswordTouchesNothing(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	const id = "rotate-noop"
	createRunningInstanceWith(t, h, id, "mysql", "admin", "old-password")

	if _, aerr := h.modifyDBInstanceTyped(context.Background(), &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		MasterUserPassword:   "old-password",
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	if cmds, _ := d.recordedPasswordExecs(); len(cmds) != 0 {
		t.Errorf("an unchanged password ran %d commands in the container: %v", len(cmds), cmds)
	}
}

// With no Docker there is no engine to disagree with the record, so the new
// password is simply stored — metadata-only mode is honest about being
// metadata-only, and a rebuilt container is seeded from the record.
func TestModifyDBInstance_masterPasswordWithoutDockerIsStored(t *testing.T) {
	h, clk := newDispatchHandler(t)
	const id = "rotate-metadata"
	createAvailableInstance(t, h, clk, id)

	if _, aerr := h.modifyDBInstanceTyped(context.Background(), &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		MasterUserPassword:   "new-password",
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	if got := storedPassword(t, h, id); got != "new-password" {
		t.Errorf("stored MasterUserPassword = %q, want the new password", got)
	}
}

// The same change through the raw Query path, which is what a form-encoded
// ModifyDBInstance with no codec in context reaches.
func TestRawQueryDispatch_masterPasswordIsApplied(t *testing.T) {
	h, clk := newDispatchHandler(t)
	const id = "rotate-raw"
	createAvailableInstance(t, h, clk, id)

	rec := rawQuery(t, h, "ModifyDBInstance", map[string]string{
		"DBInstanceIdentifier": id,
		"MasterUserPassword":   "new-password",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ModifyDBInstance via raw dispatch: %d: %s", rec.Code, rec.Body.String())
	}
	if got := storedPassword(t, h, id); got != "new-password" {
		t.Errorf("stored MasterUserPassword = %q, want the new password", got)
	}
	// AWS never echoes the password back, and neither may we.
	if strings.Contains(rec.Body.String(), "new-password") {
		t.Errorf("ModifyDBInstance response carries the master password: %s", rec.Body.String())
	}
}

// The statements are built by string concatenation, so the quoting is the part
// that has to be right: a password is arbitrary text, and the engine must
// receive it verbatim rather than as SQL.
func TestMasterPasswordStatements_quoting(t *testing.T) {
	t.Run("mysql", func(t *testing.T) {
		got := mysqlPasswordStatements("ad'min", `pa'ss\word`)
		if !strings.Contains(got, `ALTER USER IF EXISTS 'ad''min'@'%' IDENTIFIED BY 'pa''ss\\word';`) {
			t.Errorf("mysqlPasswordStatements quoting: %s", got)
		}
	})
	t.Run("postgres", func(t *testing.T) {
		got := postgresPasswordStatements(`ad"min`, `pa'ss\word`)
		if !strings.HasPrefix(got, `ALTER USER "ad""min" WITH PASSWORD 'pa''ss\word'; `) {
			t.Errorf("postgresPasswordStatements quoting: %s", got)
		}
	})
}
