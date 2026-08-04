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

	"github.com/Neaox/overcast/internal/protocol"
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

	cmds, _ := d.recordedExecs()
	if len(cmds) != 2 {
		t.Fatalf("the rotation ran %d commands, want one per member: %v", len(cmds), cmds)
	}
	for _, cmd := range cmds {
		joined := strings.Join(cmd, " ")
		if !strings.Contains(joined, "ALTER USER") || !strings.Contains(joined, "new-password") {
			t.Errorf("member rotation did not carry the new password: %v", cmd)
		}
		if !strings.Contains(joined, "-pold-password") {
			t.Errorf("member rotation did not authenticate with the password the engine still has: %v", cmd)
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

	if cmds, _ := d.recordedExecs(); len(cmds) != 0 {
		t.Errorf("an unchanged password still ran %d commands in a container: %v", len(cmds), cmds)
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
	if cmds, _ := d.recordedExecs(); len(cmds) != 0 {
		t.Errorf("a memberless cluster ran %d commands in a container: %v", len(cmds), cmds)
	}
	cl, _ := h.store.getDBCluster(ctx, "aurora-cl")
	if cl.MasterUserPassword != "new-password" {
		t.Errorf("cluster stored password = %q, want the new one", cl.MasterUserPassword)
	}
}
