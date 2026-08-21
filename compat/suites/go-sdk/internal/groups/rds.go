package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

func RDS(c *clients.Clients) ServiceGroup {
	g := &rdsGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"DescribeDBEngineVersions":  g.DescribeDBEngineVersions,
			"CreateDBInstance":          g.CreateDBInstance,
			"DescribeDBInstances":       g.DescribeDBInstances,
			"StopDBInstance":            g.StopDBInstance,
			"StartDBInstance":           g.StartDBInstance,
			"ModifyDBInstance":          g.ModifyDBInstance,
			"DeleteDBInstance":          g.DeleteDBInstance,
			"CreateDBSubnetGroup":       g.CreateDBSubnetGroup,
			"DescribeDBSubnetGroups":    g.DescribeDBSubnetGroups,
			"DeleteDBSubnetGroup":       g.DeleteDBSubnetGroup,
			"CreateDBParameterGroup":    g.CreateDBParameterGroup,
			"DescribeDBParameterGroups": g.DescribeDBParameterGroups,
			"DeleteDBParameterGroup":    g.DeleteDBParameterGroup,
			"CreateDBCluster":           g.CreateDBCluster,
			"DescribeDBClusters":        g.DescribeDBClusters,
			"ModifyDBCluster":           g.ModifyDBCluster,
			"StopDBCluster":             g.StopDBCluster,
			"StartDBCluster":            g.StartDBCluster,
			"DeleteDBCluster":           g.DeleteDBCluster,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"rds-instances":        g.setupInstances,
			"rds-subnet-groups":    g.setupNoop,
			"rds-parameter-groups": g.setupNoop,
			"rds-clusters":         g.setupNoop,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"rds-instances":        g.teardownInstances,
			"rds-subnet-groups":    g.teardownSubnetGroups,
			"rds-parameter-groups": g.teardownParameterGroups,
			"rds-clusters":         g.teardownClusters,
		},
	}
}

type rdsGroup struct{ c *clients.Clients }

func (g *rdsGroup) cl() *rds.Client { return g.c.RDS() }

// How long a DB instance is given to reach a target status, and how often it is
// asked. Shared with the node, python, java and cli suites, which wait for the
// same thing against the same emulator.
//
// A wall-clock budget rather than a count of attempts, because those two stop
// meaning the same thing the moment the machine is busy: "60 attempts, 500ms
// apart" is 30 seconds only while every DescribeDBInstances returns instantly.
// What is being waited for is a real database container's first boot, data
// directory initialisation included, and 30 seconds does not cover that on a
// loaded machine — the go, node and java suites all failed here during a
// release run with five suites in flight, and passed run alone. The CLI suite
// already carried 120 seconds for exactly this reason; this is that number,
// made common.
const (
	rdsWaitBudget   = 120 * time.Second
	rdsPollInterval = time.Second
)

func (g *rdsGroup) waitForDBStatus(ctx context.Context, dbID, target string) error {
	deadline := time.Now().Add(rdsWaitBudget)
	last := "(none reported)"
	for {
		resp, err := g.cl().DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(dbID),
		})
		if err == nil && len(resp.DBInstances) > 0 && resp.DBInstances[0].DBInstanceStatus != nil {
			last = *resp.DBInstances[0].DBInstanceStatus
			if last == target {
				return nil
			}
			// A failed instance will never reach any target, so spending the
			// rest of the budget on it only delays the report.
			if last == "failed" {
				return fmt.Errorf("DB instance %s went to %q while waiting for %q", dbID, last, target)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DB instance %s did not reach %q within %s (last status %q)",
				dbID, target, rdsWaitBudget, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rdsPollInterval):
		}
	}
}

func (g *rdsGroup) setupInstances(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *rdsGroup) setupNoop(_ context.Context, _ *harness.TestContext) error      { return nil }

func (g *rdsGroup) teardownInstances(ctx context.Context, t *harness.TestContext) error {
	if dbID := t.GetString("rds_db_id"); dbID != "" {
		g.cl().DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{ //nolint:errcheck
			DBInstanceIdentifier: aws.String(dbID),
			SkipFinalSnapshot:    aws.Bool(true),
		})
	}
	return nil
}

func (g *rdsGroup) DescribeDBEngineVersions(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DescribeDBEngineVersions(ctx, &rds.DescribeDBEngineVersionsInput{})
	return err
}

func (g *rdsGroup) CreateDBInstance(ctx context.Context, t *harness.TestContext) error {
	dbID := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbID),
		DBInstanceClass:      aws.String("db.t3.micro"),
		Engine:               aws.String("mysql"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("Password1!"),
		AllocatedStorage:     aws.Int32(20),
	})
	if err != nil {
		return err
	}
	if resp.DBInstance == nil || resp.DBInstance.DBInstanceIdentifier == nil {
		return fmt.Errorf("CreateDBInstance: missing DBInstanceIdentifier")
	}
	t.Set("rds_db_id", dbID)
	return nil
}

func (g *rdsGroup) DescribeDBInstances(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		return err
	}
	if resp.DBInstances == nil {
		return fmt.Errorf("DescribeDBInstances: missing DBInstances")
	}
	return nil
}

func (g *rdsGroup) DeleteDBInstance(ctx context.Context, t *harness.TestContext) error {
	dbID := t.GetString("rds_db_id")
	if dbID == "" {
		return nil
	}
	_, err := g.cl().DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbID),
		SkipFinalSnapshot:    aws.Bool(true),
	})
	return err
}

func (g *rdsGroup) StopDBInstance(ctx context.Context, t *harness.TestContext) error {
	dbID := t.GetString("rds_db_id")
	if dbID == "" {
		return fmt.Errorf("StopDBInstance: no db from CreateDBInstance")
	}
	if err := g.waitForDBStatus(ctx, dbID, "available"); err != nil {
		return err
	}
	resp, err := g.cl().StopDBInstance(ctx, &rds.StopDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbID),
	})
	if err != nil {
		return err
	}
	if resp.DBInstance == nil || resp.DBInstance.DBInstanceStatus == nil {
		return fmt.Errorf("StopDBInstance: missing DBInstanceStatus")
	}
	return nil
}

func (g *rdsGroup) StartDBInstance(ctx context.Context, t *harness.TestContext) error {
	dbID := t.GetString("rds_db_id")
	if dbID == "" {
		return fmt.Errorf("StartDBInstance: no db from CreateDBInstance")
	}
	if err := g.waitForDBStatus(ctx, dbID, "stopped"); err != nil {
		return err
	}
	resp, err := g.cl().StartDBInstance(ctx, &rds.StartDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbID),
	})
	if err != nil {
		return err
	}
	if resp.DBInstance == nil || resp.DBInstance.DBInstanceStatus == nil {
		return fmt.Errorf("StartDBInstance: missing DBInstanceStatus")
	}
	return nil
}

func (g *rdsGroup) ModifyDBInstance(ctx context.Context, t *harness.TestContext) error {
	dbID := t.GetString("rds_db_id")
	if dbID == "" {
		return fmt.Errorf("ModifyDBInstance: no db from CreateDBInstance")
	}
	resp, err := g.cl().ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbID),
		AllocatedStorage:     aws.Int32(30),
	})
	if err != nil {
		return err
	}
	if resp.DBInstance == nil || resp.DBInstance.DBInstanceIdentifier == nil {
		return fmt.Errorf("ModifyDBInstance: missing DBInstanceIdentifier")
	}
	return nil
}

// ── rds-subnet-groups ──────────────────────────────────────────────────────

func (g *rdsGroup) teardownSubnetGroups(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("rds_subnet_group"); name != "" {
		g.cl().DeleteDBSubnetGroup(ctx, &rds.DeleteDBSubnetGroupInput{ //nolint:errcheck
			DBSubnetGroupName: aws.String(name),
		})
	}
	return nil
}

func (g *rdsGroup) CreateDBSubnetGroup(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateDBSubnetGroup(ctx, &rds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String(name),
		DBSubnetGroupDescription: aws.String("compat test subnet group"),
		SubnetIds:                []string{"subnet-00000000", "subnet-00000001"},
	})
	if err != nil {
		return err
	}
	if resp.DBSubnetGroup == nil || resp.DBSubnetGroup.DBSubnetGroupName == nil {
		return fmt.Errorf("CreateDBSubnetGroup: missing DBSubnetGroupName")
	}
	t.Set("rds_subnet_group", name)
	return nil
}

func (g *rdsGroup) DescribeDBSubnetGroups(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rds_subnet_group")
	if name == "" {
		return fmt.Errorf("DescribeDBSubnetGroups: no group from CreateDBSubnetGroup")
	}
	resp, err := g.cl().DescribeDBSubnetGroups(ctx, &rds.DescribeDBSubnetGroupsInput{
		DBSubnetGroupName: aws.String(name),
	})
	if err != nil {
		return err
	}
	if len(resp.DBSubnetGroups) == 0 {
		return fmt.Errorf("DescribeDBSubnetGroups: no groups returned")
	}
	return nil
}

func (g *rdsGroup) DeleteDBSubnetGroup(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rds_subnet_group")
	if name == "" {
		return nil
	}
	_, err := g.cl().DeleteDBSubnetGroup(ctx, &rds.DeleteDBSubnetGroupInput{
		DBSubnetGroupName: aws.String(name),
	})
	return err
}

// ── rds-parameter-groups ───────────────────────────────────────────────────

func (g *rdsGroup) teardownParameterGroups(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("rds_param_group"); name != "" {
		g.cl().DeleteDBParameterGroup(ctx, &rds.DeleteDBParameterGroupInput{ //nolint:errcheck
			DBParameterGroupName: aws.String(name),
		})
	}
	return nil
}

func (g *rdsGroup) CreateDBParameterGroup(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-pg-%s", t.RunID)
	resp, err := g.cl().CreateDBParameterGroup(ctx, &rds.CreateDBParameterGroupInput{
		DBParameterGroupName:   aws.String(name),
		DBParameterGroupFamily: aws.String("aurora-mysql8.0"),
		Description:            aws.String("compat test parameter group"),
	})
	if err != nil {
		return err
	}
	if resp.DBParameterGroup == nil || resp.DBParameterGroup.DBParameterGroupName == nil {
		return fmt.Errorf("CreateDBParameterGroup: missing DBParameterGroupName")
	}
	if aws.ToString(resp.DBParameterGroup.DBParameterGroupFamily) != "aurora-mysql8.0" {
		return fmt.Errorf("CreateDBParameterGroup: family = %q, want aurora-mysql8.0", aws.ToString(resp.DBParameterGroup.DBParameterGroupFamily))
	}
	t.Set("rds_param_group", name)
	return nil
}

func (g *rdsGroup) DescribeDBParameterGroups(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rds_param_group")
	if name == "" {
		return fmt.Errorf("DescribeDBParameterGroups: no group from CreateDBParameterGroup")
	}
	resp, err := g.cl().DescribeDBParameterGroups(ctx, &rds.DescribeDBParameterGroupsInput{
		DBParameterGroupName: aws.String(name),
	})
	if err != nil {
		return err
	}
	if len(resp.DBParameterGroups) == 0 {
		return fmt.Errorf("DescribeDBParameterGroups: no groups returned")
	}
	return nil
}

func (g *rdsGroup) DeleteDBParameterGroup(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rds_param_group")
	if name == "" {
		return nil
	}
	_, err := g.cl().DeleteDBParameterGroup(ctx, &rds.DeleteDBParameterGroupInput{
		DBParameterGroupName: aws.String(name),
	})
	return err
}

// ── rds-clusters ───────────────────────────────────────────────────────────
//
// Aurora DB clusters. A cluster is a logical record in Overcast — member
// instances are what start engine containers — so the waits here are for the
// emulator's own status transitions rather than for a database to boot. They
// still have to be waited for: StopDBCluster refuses a cluster that is not
// "available" and StartDBCluster refuses one that is not "stopped", here and on
// AWS, so going straight from create to stop asks for InvalidDBClusterStateFault.
//
// The poll interval is tighter than the instance one for the same reason: there
// is no container boot to wait on, only a scheduled transition, so a coarser
// interval would spend seconds observing something that had already happened.
const rdsClusterPollInterval = 500 * time.Millisecond

const (
	rdsClusterEngine         = "aurora-mysql"
	rdsClusterMasterUsername = "admin"
	rdsClusterMasterPassword = "Password1!"
	// The retention period ModifyDBCluster sets. Non-zero so it is
	// distinguishable from the create-time default, and echoed back by
	// DescribeDBClusters so the change can be observed rather than assumed.
	rdsClusterBackupRetention = 7
)

// clusterID is this group's own cluster, named apart from the instance group's
// so the two never collide when both run against the same emulator.
func (g *rdsGroup) clusterID(t *harness.TestContext) string {
	return fmt.Sprintf("compat-cluster-%s", t.RunID)
}

func (g *rdsGroup) waitForClusterStatus(ctx context.Context, clusterID, target string) error {
	deadline := time.Now().Add(rdsWaitBudget)
	last := "(none reported)"
	for {
		resp, err := g.cl().DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
			DBClusterIdentifier: aws.String(clusterID),
		})
		if err == nil && len(resp.DBClusters) > 0 && resp.DBClusters[0].Status != nil {
			last = *resp.DBClusters[0].Status
			if last == target {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DB cluster %s did not reach %q within %s (last status %q)",
				clusterID, target, rdsWaitBudget, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rdsClusterPollInterval):
		}
	}
}

// describeCluster returns this group's cluster, or an error naming what came
// back instead. Every assertion below reads through it, so a describe that
// comes back empty reports as a missing cluster rather than an index panic.
func (g *rdsGroup) describeCluster(ctx context.Context, clusterID string) (*rds.DescribeDBClustersOutput, error) {
	resp, err := g.cl().DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		return nil, err
	}
	if len(resp.DBClusters) == 0 {
		return nil, fmt.Errorf("DescribeDBClusters: no cluster returned for %s", clusterID)
	}
	return resp, nil
}

func (g *rdsGroup) teardownClusters(ctx context.Context, t *harness.TestContext) error {
	if clusterID := t.GetString("rds_cluster_id"); clusterID != "" {
		g.cl().DeleteDBCluster(ctx, &rds.DeleteDBClusterInput{ //nolint:errcheck
			DBClusterIdentifier: aws.String(clusterID),
			SkipFinalSnapshot:   aws.Bool(true),
		})
	}
	return nil
}

func (g *rdsGroup) CreateDBCluster(ctx context.Context, t *harness.TestContext) error {
	clusterID := g.clusterID(t)
	// Recorded before the call, not after it: a create that fails partway still
	// leaves something for teardown to remove.
	t.Set("rds_cluster_id", clusterID)
	resp, err := g.cl().CreateDBCluster(ctx, &rds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
		Engine:              aws.String(rdsClusterEngine),
		MasterUsername:      aws.String(rdsClusterMasterUsername),
		MasterUserPassword:  aws.String(rdsClusterMasterPassword),
	})
	if err != nil {
		return err
	}
	if resp.DBCluster == nil {
		return fmt.Errorf("CreateDBCluster: missing DBCluster")
	}
	if got := aws.ToString(resp.DBCluster.DBClusterIdentifier); got != clusterID {
		return fmt.Errorf("CreateDBCluster: DBClusterIdentifier = %q, want %q", got, clusterID)
	}
	if aws.ToString(resp.DBCluster.DBClusterArn) == "" {
		return fmt.Errorf("CreateDBCluster: missing DBClusterArn")
	}
	return nil
}

func (g *rdsGroup) DescribeDBClusters(ctx context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("rds_cluster_id")
	if clusterID == "" {
		return fmt.Errorf("DescribeDBClusters: no cluster from CreateDBCluster")
	}
	resp, err := g.describeCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	cluster := resp.DBClusters[0]
	if got := aws.ToString(cluster.DBClusterIdentifier); got != clusterID {
		return fmt.Errorf("DescribeDBClusters: DBClusterIdentifier = %q, want %q", got, clusterID)
	}
	if got := aws.ToString(cluster.Engine); got != rdsClusterEngine {
		return fmt.Errorf("DescribeDBClusters: Engine = %q, want %q", got, rdsClusterEngine)
	}
	if aws.ToString(cluster.Status) == "" {
		return fmt.Errorf("DescribeDBClusters: missing Status")
	}
	// The writer and reader endpoints are what makes a cluster a cluster, and
	// nothing outside the emulator's own Go tests looked at them until this
	// group existed. Both must be present and they must differ — a reader
	// endpoint collapsed onto the writer reads as success everywhere else.
	// Both stay distinct only while the cluster has no writer member: since #1076
	// a host caller that cannot resolve the names is given the loopback address
	// for each, deliberately. This group creates no members, so it never reaches
	// that case — one that adds an instance would.
	writer := aws.ToString(cluster.Endpoint)
	reader := aws.ToString(cluster.ReaderEndpoint)
	if writer == "" {
		return fmt.Errorf("DescribeDBClusters: missing Endpoint")
	}
	if reader == "" {
		return fmt.Errorf("DescribeDBClusters: missing ReaderEndpoint")
	}
	if writer == reader {
		return fmt.Errorf("DescribeDBClusters: Endpoint and ReaderEndpoint are both %q", writer)
	}
	return nil
}

func (g *rdsGroup) ModifyDBCluster(ctx context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("rds_cluster_id")
	if clusterID == "" {
		return fmt.Errorf("ModifyDBCluster: no cluster from CreateDBCluster")
	}
	resp, err := g.cl().ModifyDBCluster(ctx, &rds.ModifyDBClusterInput{
		DBClusterIdentifier:   aws.String(clusterID),
		BackupRetentionPeriod: aws.Int32(rdsClusterBackupRetention),
	})
	if err != nil {
		return err
	}
	if resp.DBCluster == nil {
		return fmt.Errorf("ModifyDBCluster: missing DBCluster")
	}
	if got := aws.ToInt32(resp.DBCluster.BackupRetentionPeriod); got != rdsClusterBackupRetention {
		return fmt.Errorf("ModifyDBCluster: BackupRetentionPeriod = %d, want %d",
			got, rdsClusterBackupRetention)
	}
	// Read it back rather than trusting the modify response to have echoed a
	// value it never stored.
	after, err := g.describeCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	if got := aws.ToInt32(after.DBClusters[0].BackupRetentionPeriod); got != rdsClusterBackupRetention {
		return fmt.Errorf("ModifyDBCluster: BackupRetentionPeriod after describe = %d, want %d",
			got, rdsClusterBackupRetention)
	}
	return nil
}

func (g *rdsGroup) StopDBCluster(ctx context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("rds_cluster_id")
	if clusterID == "" {
		return fmt.Errorf("StopDBCluster: no cluster from CreateDBCluster")
	}
	// StopDBCluster requires an available cluster, here and on AWS.
	if err := g.waitForClusterStatus(ctx, clusterID, "available"); err != nil {
		return fmt.Errorf("StopDBCluster: %w", err)
	}
	resp, err := g.cl().StopDBCluster(ctx, &rds.StopDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		return err
	}
	if resp.DBCluster == nil || aws.ToString(resp.DBCluster.Status) == "" {
		return fmt.Errorf("StopDBCluster: missing Status")
	}
	// The stop is this test's mutation, so this test is where it is confirmed
	// to have landed — not StartDBCluster's precondition wait.
	return g.waitForClusterStatus(ctx, clusterID, "stopped")
}

func (g *rdsGroup) StartDBCluster(ctx context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("rds_cluster_id")
	if clusterID == "" {
		return fmt.Errorf("StartDBCluster: no cluster from CreateDBCluster")
	}
	if err := g.waitForClusterStatus(ctx, clusterID, "stopped"); err != nil {
		return fmt.Errorf("StartDBCluster: %w", err)
	}
	resp, err := g.cl().StartDBCluster(ctx, &rds.StartDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		return err
	}
	if resp.DBCluster == nil || aws.ToString(resp.DBCluster.Status) == "" {
		return fmt.Errorf("StartDBCluster: missing Status")
	}
	return g.waitForClusterStatus(ctx, clusterID, "available")
}

func (g *rdsGroup) DeleteDBCluster(ctx context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("rds_cluster_id")
	if clusterID == "" {
		return fmt.Errorf("DeleteDBCluster: no cluster from CreateDBCluster")
	}
	if _, err := g.cl().DeleteDBCluster(ctx, &rds.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
		SkipFinalSnapshot:   aws.Bool(true),
	}); err != nil {
		return err
	}
	if err := g.waitForClusterGone(ctx, clusterID); err != nil {
		return err
	}
	t.Set("rds_cluster_id", "")
	return nil
}

// waitForClusterGone polls the unfiltered DescribeDBClusters until this run's
// cluster is no longer listed. Deletion is asynchronous — the call marks the
// cluster "deleting" and the record goes shortly after — so absence is what has
// to be waited for. The unfiltered form rather than a lookup by identifier, so
// the check does not depend on which not-found shape each SDK surfaces.
func (g *rdsGroup) waitForClusterGone(ctx context.Context, clusterID string) error {
	deadline := time.Now().Add(rdsWaitBudget)
	for {
		resp, err := g.cl().DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{})
		if err != nil {
			return err
		}
		found := false
		for _, cluster := range resp.DBClusters {
			if aws.ToString(cluster.DBClusterIdentifier) == clusterID {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DB cluster %s still listed %s after DeleteDBCluster",
				clusterID, rdsWaitBudget)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rdsClusterPollInterval):
		}
	}
}
