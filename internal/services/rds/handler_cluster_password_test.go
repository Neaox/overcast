package rds

// handler_cluster_password_test.go — rotating an Aurora cluster's master
// password.
//
// ModifyDBInstance's MasterUserPassword now reaches the running engine
// (password.go). ModifyDBCluster's did not reach anything at all: the
// parameter was not on modifyDBClusterReq, so a stack rotating an Aurora
// cluster's password reported a successful update having changed neither the
// record nor any engine.
//
// The cluster case is the harder one and is why it needs its own file. A
// DBCluster in Overcast is a logical record with no container behind it — the
// engines belong to its DBClusterMembers — so "change the cluster's password"
// only means anything if it reaches each member. These tests are written
// against the containers for that reason.

import (
	"context"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// newClusterWithMembers creates a cluster and attaches running member
// instances to it, which is the shape CreateDBCluster + CreateDBInstance
// produce and the only shape in which a cluster has engines at all.
func newClusterWithMembers(t *testing.T, h *Handler, clusterID, password string, memberIDs ...string) {
	t.Helper()
	ctx := context.Background()

	if _, aerr := h.createDBClusterTyped(ctx, &createDBClusterReq{
		DBClusterIdentifier: clusterID,
		Engine:              "aurora-mysql",
		MasterUsername:      "admin",
		MasterUserPassword:  password,
	}); aerr != nil {
		t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
	}

	members := make([]DBClusterMember, 0, len(memberIDs))
	for i, id := range memberIDs {
		createRunningInstanceWith(t, h, id, "aurora-mysql", "admin", password)
		members = append(members, DBClusterMember{DBInstanceIdentifier: id, IsClusterWriter: i == 0})
	}
	if _, aerr := h.mutateCluster(ctx, clusterID, func(cl *DBCluster) *protocol.AWSError {
		cl.DBClusterMembers = members
		return nil
	}); aerr != nil {
		t.Fatalf("attach members: %s", aerr.Message)
	}
}

// A cluster has no engine of its own, so the rotation is only real if it
// reaches every member's container.
func TestModifyDBCluster_masterPasswordReachesEveryMember(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()

	newClusterWithMembers(t, h, "aurora-cl", "old-password", "writer", "reader")

	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier: "aurora-cl",
		MasterUserPassword:  "new-password",
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster: %s: %s", aerr.Code, aerr.Message)
	}

	cmds, envs := d.recordedPasswordExecs()
	if len(cmds) != 2 {
		t.Fatalf("the rotation ran %d commands, want one per member: %v", len(cmds), cmds)
	}
	for i, cmd := range cmds {
		joined := strings.Join(cmd, " ")
		if !strings.Contains(joined, "ALTER USER") || !strings.Contains(joined, "new-password") {
			t.Errorf("member rotation did not carry the new password: %v", cmd)
		}
		if !strings.Contains(strings.Join(envs[i], " "), "MYSQL_PWD=old-password") {
			t.Errorf("member rotation did not authenticate with the password the engine still has: %v", envs[i])
		}
		if strings.Contains(joined, "old-password") {
			t.Errorf("the password the engine still has appears in argv: %v", cmd)
		}
	}

	for _, id := range []string{"writer", "reader"} {
		if got := storedPassword(t, h, id); got != "new-password" {
			t.Errorf("member %s stored password = %q, want the new one", id, got)
		}
	}
	cl, aerr := h.store.getDBCluster(ctx, "aurora-cl")
	if aerr != nil {
		t.Fatalf("getDBCluster: %s", aerr.Message)
	}
	if cl.MasterUserPassword != "new-password" {
		t.Errorf("cluster stored password = %q, want the new one", cl.MasterUserPassword)
	}
}

// A member that refuses fails the whole modification, and the cluster must not
// end up claiming a password its engines do not accept.
func TestModifyDBCluster_masterPasswordMemberRefusalIsNotStored(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()

	newClusterWithMembers(t, h, "aurora-cl", "old-password", "writer")
	d.failExecs(1, "ERROR 1396 (HY000): Operation ALTER USER failed for 'admin'@'%'")

	_, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier: "aurora-cl",
		MasterUserPassword:  "new-password",
	})
	if aerr == nil {
		t.Fatal("ModifyDBCluster reported success though a member's engine refused the change")
	}
	if !strings.Contains(aerr.Message, "ALTER USER") {
		t.Errorf("error does not carry the engine's own output, which is the diagnosis: %q", aerr.Message)
	}

	if got := storedPassword(t, h, "writer"); got != "old-password" {
		t.Errorf("member stored password = %q after a refused rotation, want it left alone", got)
	}
	cl, _ := h.store.getDBCluster(ctx, "aurora-cl")
	if cl.MasterUserPassword != "old-password" {
		t.Errorf("cluster stored password = %q after a refused rotation, want it left alone", cl.MasterUserPassword)
	}
}

// A modification that does not mention the password must not touch any engine
// — CloudFormation sends MasterUserPassword on every update, so treating an
// unchanged one as a rotation would exec into every member on every stack
// update.
func TestModifyDBCluster_unchangedMasterPasswordTouchesNoEngine(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)

	newClusterWithMembers(t, h, "aurora-cl", "old-password", "writer")

	if _, aerr := h.modifyDBClusterTyped(context.Background(), &modifyDBClusterReq{
		DBClusterIdentifier: "aurora-cl",
		MasterUserPassword:  "old-password",
		BackupRetentionPeriod: func() *int {
			v := 7
			return &v
		}(),
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster: %s: %s", aerr.Code, aerr.Message)
	}

	if cmds, _ := d.recordedPasswordExecs(); len(cmds) != 0 {
		t.Errorf("an unchanged password still ran %d commands in a container: %v", len(cmds), cmds)
	}
}

// RDS's own constraints on a master password. Applying them here is what stops
// a database working locally and being refused on deploy.
//
// The forbidden characters are worth spelling out because Overcast's own
// GetRandomPassword produces them by default — as AWS's does, which is why
// CDK excludes them. A generated password that trips this would trip it on AWS.
func TestValidateMasterUserPassword(t *testing.T) {
	tests := []struct {
		name     string
		engine   string
		password string
		wantErr  bool
	}{
		{"typical", "mysql", "correct-horse-battery", false},
		{"punctuation RDS allows", "mysql", "p#ssw%rd!12*", false},
		{"single quote is allowed", "mysql", "pass'word12", false},
		{"exactly the minimum", "mysql", "12345678", false},
		{"one under the minimum", "mysql", "1234567", true},
		{"MySQL maximum", "mysql", strings.Repeat("a", 41), false},
		{"over MySQL maximum", "mysql", strings.Repeat("a", 42), true},
		{"MariaDB maximum", "mariadb", strings.Repeat("a", 41), false},
		{"over MariaDB maximum", "mariadb", strings.Repeat("a", 42), true},
		{"PostgreSQL maximum", "postgres", strings.Repeat("a", 128), false},
		{"over PostgreSQL maximum", "postgres", strings.Repeat("a", 129), true},
		{"Aurora MySQL maximum", "aurora-mysql", strings.Repeat("a", 41), false},
		{"over Aurora MySQL maximum", "aurora-mysql", strings.Repeat("a", 42), true},
		{"Aurora PostgreSQL maximum", "aurora-postgresql", strings.Repeat("a", 99), false},
		{"over Aurora PostgreSQL maximum", "aurora-postgresql", strings.Repeat("a", 100), true},
		{"double quote", "mysql", "pass\"word12", true},
		{"at sign", "mysql", "pass@word12", true},
		{"forward slash", "mysql", "pass/word12", true},
		{"space", "mysql", "pass word12", true},
		{"non-printable", "mysql", "pass\tword12", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			aerr := validateMasterUserPassword(tc.engine, tc.password)
			if tc.wantErr && aerr == nil {
				t.Errorf("validateMasterUserPassword(%q) = nil, want a refusal", tc.password)
			}
			if !tc.wantErr && aerr != nil {
				t.Errorf("validateMasterUserPassword(%q) = %s, want nil", tc.password, aerr.Message)
			}
		})
	}
}

// A password RDS would refuse must be refused before anything runs against an
// engine — and on create as well as on modify, or a database could be created
// holding a password it could never afterwards change to.
func TestMasterPassword_invalidIsRefusedBeforeTheEngine(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()

	createRunningInstanceWith(t, h, "db", "mysql", "admin", "old-password")
	_, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: "db",
		MasterUserPassword:   "has@sign12",
	})
	if aerr == nil {
		t.Fatal("ModifyDBInstance accepted a password RDS forbids")
	}
	if aerr.Code != "InvalidParameterValue" {
		t.Errorf("error code = %q, want InvalidParameterValue", aerr.Code)
	}
	if cmds, _ := d.recordedPasswordExecs(); len(cmds) != 0 {
		t.Errorf("an invalid password reached the engine: %v", cmds)
	}
	if got := storedPassword(t, h, "db"); got != "old-password" {
		t.Errorf("stored password = %q after a refused change, want it left alone", got)
	}

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "fresh",
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "short",
	}); aerr == nil {
		t.Error("CreateDBInstance accepted a password too short for RDS")
	}
	if _, aerr := h.createDBClusterTyped(ctx, &createDBClusterReq{
		DBClusterIdentifier: "fresh-cl",
		Engine:              "aurora-mysql",
		MasterUsername:      "admin",
		MasterUserPassword:  "has/slash12",
	}); aerr == nil {
		t.Error("CreateDBCluster accepted a password RDS forbids")
	}
}

// A cluster with no members validates too. The per-member rotation is where
// the check would otherwise happen, and an empty member list never reaches it
// — so a memberless cluster would record a password RDS refuses and hand it to
// the first instance created under it.
func TestModifyDBCluster_invalidMasterPasswordIsRefusedWithNoMembers(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()

	newClusterWithMembers(t, h, "aurora-cl", "old-password")

	_, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier: "aurora-cl",
		MasterUserPassword:  "has@sign12",
	})
	if aerr == nil {
		t.Fatal("ModifyDBCluster accepted a password RDS forbids on a memberless cluster")
	}
	if aerr.Code != "InvalidParameterValue" {
		t.Errorf("error code = %q, want InvalidParameterValue", aerr.Code)
	}

	cl, _ := h.store.getDBCluster(ctx, "aurora-cl")
	if cl.MasterUserPassword != "old-password" {
		t.Errorf("cluster stored password = %q after a refused change, want it left alone", cl.MasterUserPassword)
	}
}

// A cluster with no members is the metadata-only case: there is no engine to
// disagree with the record, so the rotation is simply recorded.
func TestModifyDBCluster_masterPasswordWithNoMembersIsStored(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()

	newClusterWithMembers(t, h, "aurora-cl", "old-password")

	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier: "aurora-cl",
		MasterUserPassword:  "new-password",
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster: %s: %s", aerr.Code, aerr.Message)
	}
	if cmds, _ := d.recordedPasswordExecs(); len(cmds) != 0 {
		t.Errorf("a memberless cluster ran %d commands in a container: %v", len(cmds), cmds)
	}
	cl, _ := h.store.getDBCluster(ctx, "aurora-cl")
	if cl.MasterUserPassword != "new-password" {
		t.Errorf("cluster stored password = %q, want the new one", cl.MasterUserPassword)
	}
}
