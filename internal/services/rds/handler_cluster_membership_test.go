package rds

// handler_cluster_membership_test.go — DBClusterMembers tracking the instances
// that actually exist.
//
// addInstanceToCluster appended a member and marked the first one the writer,
// and nothing ever took one back out. DeleteDBInstance removed the instance
// record and left the membership entry behind, so DescribeDBClusters went on
// reporting a member that no longer existed. Worse, when the deleted member was
// the writer the cluster was left with no writer at all — and every cluster
// endpoint question answers from the writer (endpoint.go), so a cluster whose
// writer had been replaced stopped resolving to any container.
//
// AWS removes the member, and deleting the writer of a cluster that still has
// replicas is a failover that promotes one of them:
// https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/Concepts.AuroraHighAvailability.html

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// seedClusterMember stores an instance and registers it with the cluster the
// way CreateDBInstance does — through addInstanceToCluster itself, so these
// tests exercise the real writer-election rule rather than a hand-built list.
func seedClusterMember(t *testing.T, h *Handler, clusterID, instanceID string) {
	t.Helper()
	ctx := context.Background()
	if aerr := h.store.putDBInstance(ctx, &DBInstance{
		DBInstanceIdentifier: instanceID,
		DBClusterIdentifier:  clusterID,
		Engine:               "aurora-mysql",
		DBInstanceStatus:     "available",
		Port:                 3306,
	}); aerr != nil {
		t.Fatalf("putDBInstance %s: %s", instanceID, aerr.Message)
	}
	h.addInstanceToCluster(ctx, clusterID, instanceID)
}

func clusterMembers(t *testing.T, h *Handler, clusterID string) []DBClusterMember {
	t.Helper()
	c, aerr := h.store.getDBCluster(context.Background(), clusterID)
	if aerr != nil {
		t.Fatalf("getDBCluster %s: %s", clusterID, aerr.Message)
	}
	return c.DBClusterMembers
}

func memberIDs(members []DBClusterMember) []string {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.DBInstanceIdentifier)
	}
	return ids
}

// writerOf is the member marked IsClusterWriter, or "" when the cluster has
// none — the state this file exists to make impossible while members remain.
func writerOf(members []DBClusterMember) string {
	for _, m := range members {
		if m.IsClusterWriter {
			return m.DBInstanceIdentifier
		}
	}
	return ""
}

func deleteInstance(t *testing.T, h *Handler, id string) {
	t.Helper()
	if _, aerr := h.deleteDBInstanceTyped(context.Background(), &deleteDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("DeleteDBInstance %s: %s: %s", id, aerr.Code, aerr.Message)
	}
}

// The plain case: a deleted member stops being reported as one.
func TestDeleteDBInstance_dropsTheMemberFromItsCluster(t *testing.T) {
	h := newClusterTestHandler(t)
	seedCluster(t, h, "orders")
	seedClusterMember(t, h, "orders", "orders-writer")
	seedClusterMember(t, h, "orders", "orders-reader")

	deleteInstance(t, h, "orders-reader")

	got := memberIDs(clusterMembers(t, h, "orders"))
	if len(got) != 1 || got[0] != "orders-writer" {
		t.Errorf("members = %v, want only [orders-writer] — the deleted instance is still listed", got)
	}
}

// Deleting the writer is a failover on AWS: a surviving replica takes over.
// Leaving IsClusterWriter false everywhere strands the cluster endpoints, which
// have no other way to choose a container.
func TestDeleteDBInstance_promotesASurvivorWhenTheWriterIsDeleted(t *testing.T) {
	h := newClusterTestHandler(t)
	seedCluster(t, h, "orders")
	seedClusterMember(t, h, "orders", "orders-writer")
	seedClusterMember(t, h, "orders", "orders-reader-1")
	seedClusterMember(t, h, "orders", "orders-reader-2")

	if got := writerOf(clusterMembers(t, h, "orders")); got != "orders-writer" {
		t.Fatalf("writer before delete = %q, want orders-writer", got)
	}

	deleteInstance(t, h, "orders-writer")

	members := clusterMembers(t, h, "orders")
	if got := memberIDs(members); len(got) != 2 {
		t.Errorf("members = %v, want the two survivors", got)
	}
	// Both replicas sit at the default promotion tier, so either is a correct
	// answer on AWS's own terms — it promotes "an arbitrary replica in the same
	// promotion tier". Overcast settles that arbitrariness on the oldest
	// survivor, so the choice is reproducible from one run to the next.
	if got := writerOf(members); got != "orders-reader-1" {
		t.Errorf("writer after deleting the writer = %q, want orders-reader-1", got)
	}
}

// A replica leaving is not a failover — the writer must not move.
func TestDeleteDBInstance_deletingAReplicaLeavesTheWriterInPlace(t *testing.T) {
	h := newClusterTestHandler(t)
	seedCluster(t, h, "orders")
	seedClusterMember(t, h, "orders", "orders-writer")
	seedClusterMember(t, h, "orders", "orders-reader")

	deleteInstance(t, h, "orders-reader")

	if got := writerOf(clusterMembers(t, h, "orders")); got != "orders-writer" {
		t.Errorf("writer = %q, want orders-writer to be untouched by a replica deletion", got)
	}
}

// A cluster outlives its instances — CloudFormation deletes the members before
// the cluster. Nothing is promoted, and nothing is left behind either.
//
// This is the state TestClusterEndpoints_withoutAWriterKeepTheNameAndClusterPort
// pins: with no writer the cluster endpoint keeps its AWS-shaped name and the
// cluster's own port, which is what holds Fn::GetAtt Endpoint.Port at 3306.
func TestDeleteDBInstance_lastMemberLeavesTheClusterEmpty(t *testing.T) {
	h := newClusterTestHandler(t)
	seedCluster(t, h, "orders")
	seedClusterMember(t, h, "orders", "orders-only")

	deleteInstance(t, h, "orders-only")

	if got := clusterMembers(t, h, "orders"); len(got) != 0 {
		t.Errorf("members = %v, want none", memberIDs(got))
	}
}

// AWS promotes by priority first: "Priorities range from 0 for the highest
// priority to 15 for the lowest priority... Amazon RDS promotes the Aurora
// Replica with the highest priority." Position in the list loses to tier.
func TestDeleteDBInstance_promotesTheHighestPriorityTier(t *testing.T) {
	h := newClusterTestHandler(t)
	seedCluster(t, h, "orders")
	seedClusterMember(t, h, "orders", "orders-writer")
	seedClusterMember(t, h, "orders", "orders-tier-default")
	seedClusterMember(t, h, "orders", "orders-tier-zero")

	// Put the last member in tier 0 — highest priority, but last in line.
	if _, aerr := h.mutateCluster(context.Background(), "orders", func(c *DBCluster) *protocol.AWSError {
		for i := range c.DBClusterMembers {
			if c.DBClusterMembers[i].DBInstanceIdentifier == "orders-tier-zero" {
				c.DBClusterMembers[i].PromotionTier = 0
			}
		}
		return nil
	}); aerr != nil {
		t.Fatalf("mutateCluster: %s", aerr.Message)
	}

	deleteInstance(t, h, "orders-writer")

	if got := writerOf(clusterMembers(t, h, "orders")); got != "orders-tier-zero" {
		t.Errorf("writer = %q, want orders-tier-zero — tier 0 outranks the default tier", got)
	}
}

// A standalone instance names no cluster, and an instance whose cluster record
// has already gone must not turn a missing cluster into a failure on the delete
// path — the caller's response is committed by the time this runs.
func TestRemoveInstanceFromCluster_unknownClusterIsANoOp(t *testing.T) {
	h := newClusterTestHandler(t)
	seedCluster(t, h, "orders")
	seedClusterMember(t, h, "orders", "orders-writer")

	if got := h.removeInstanceFromCluster(context.Background(), "no-such-cluster", "orders-writer"); got != "" {
		t.Errorf("promoted = %q, want none for a cluster that does not exist", got)
	}
	if got := memberIDs(clusterMembers(t, h, "orders")); len(got) != 1 {
		t.Errorf("members = %v, want the real cluster untouched", got)
	}
}

// An instance that is not a member is not an error either, and must not cost
// the cluster its writer.
func TestRemoveInstanceFromCluster_unknownMemberLeavesTheWriterAlone(t *testing.T) {
	h := newClusterTestHandler(t)
	seedCluster(t, h, "orders")
	seedClusterMember(t, h, "orders", "orders-writer")

	if got := h.removeInstanceFromCluster(context.Background(), "orders", "never-joined"); got != "" {
		t.Errorf("promoted = %q, want none — no member was removed", got)
	}
	if got := writerOf(clusterMembers(t, h, "orders")); got != "orders-writer" {
		t.Errorf("writer = %q, want orders-writer", got)
	}
}
