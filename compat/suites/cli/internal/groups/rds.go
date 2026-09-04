package groups

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// RDS returns the RDS service group.
func RDS() ServiceGroup {
	g := &rdsCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"rds-instances:DescribeDBEngineVersions":         g.DescribeDBEngineVersions,
			"rds-instances:CreateDBInstance":                 g.CreateDBInstance,
			"rds-instances:DescribeDBInstances":              g.DescribeDBInstances,
			"rds-instances:StopDBInstance":                   g.StopDBInstance,
			"rds-instances:StartDBInstance":                  g.StartDBInstance,
			"rds-instances:ModifyDBInstance":                 g.ModifyDBInstance,
			"rds-instances:DeleteDBInstance":                 g.DeleteDBInstance,
			"rds-subnet-groups:CreateDBSubnetGroup":          g.CreateDBSubnetGroup,
			"rds-subnet-groups:DescribeDBSubnetGroups":       g.DescribeDBSubnetGroups,
			"rds-subnet-groups:DeleteDBSubnetGroup":          g.DeleteDBSubnetGroup,
			"rds-parameter-groups:CreateDBParameterGroup":    g.CreateDBParameterGroup,
			"rds-parameter-groups:DescribeDBParameterGroups": g.DescribeDBParameterGroups,
			"rds-parameter-groups:DeleteDBParameterGroup":    g.DeleteDBParameterGroup,
			"rds-clusters:CreateDBCluster":                   g.CreateDBCluster,
			"rds-clusters:DescribeDBClusters":                g.DescribeDBClusters,
			"rds-clusters:ModifyDBCluster":                   g.ModifyDBCluster,
			"rds-clusters:StopDBCluster":                     g.StopDBCluster,
			"rds-clusters:StartDBCluster":                    g.StartDBCluster,
			"rds-clusters:DeleteDBCluster":                   g.DeleteDBCluster,

			"rds-cluster-members:CreateClusterForMembers":         g.CreateClusterForMembers,
			"rds-cluster-members:AddClusterMember":                g.AddClusterMember,
			"rds-cluster-members:DescribeClusterMembers":          g.DescribeClusterMembers,
			"rds-cluster-members:DescribeMemberInheritedSettings": g.DescribeMemberInheritedSettings,
			"rds-cluster-members:RemoveClusterMember":             g.RemoveClusterMember,
			"rds-cluster-members:DeleteClusterAfterMembers":       g.DeleteClusterAfterMembers,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"rds-instances":        g.setupInstances,
			"rds-subnet-groups":    g.setupNoop,
			"rds-parameter-groups": g.setupNoop,
			"rds-clusters":         g.setupNoop,
			"rds-cluster-members":  g.setupNoop,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"rds-instances":        g.teardownInstances,
			"rds-subnet-groups":    g.teardownSubnetGroups,
			"rds-parameter-groups": g.teardownParameterGroups,
			"rds-clusters":         g.teardownClusters,
			"rds-cluster-members":  g.teardownClusterMembers,
		},
	}
}

type rdsCliGroup struct{}

func (g *rdsCliGroup) setupNoop(_ context.Context, _ *harness.TestContext) error      { return nil }
func (g *rdsCliGroup) setupInstances(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *rdsCliGroup) teardownInstances(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("db_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-instance", //nolint:errcheck
			"--db-instance-identifier", id,
			"--skip-final-snapshot",
		)
	}
	return nil
}

func (g *rdsCliGroup) DescribeDBEngineVersions(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-engine-versions",
		"--engine", "mysql",
	)
	if err != nil {
		return err
	}
	versions, _ := out["DBEngineVersions"].([]interface{})
	if len(versions) == 0 {
		return fmt.Errorf("DescribeDBEngineVersions: no versions returned")
	}
	return nil
}

func (g *rdsCliGroup) CreateDBInstance(_ context.Context, t *harness.TestContext) error {
	id := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "create-db-instance",
		"--db-instance-identifier", id,
		"--db-instance-class", "db.t3.micro",
		"--engine", "mysql",
		"--master-username", "admin",
		"--master-user-password", "password123",
		"--allocated-storage", "20",
	)
	if err != nil {
		return err
	}
	db, _ := out["DBInstance"].(map[string]interface{})
	arn, _ := db["DBInstanceArn"].(string)
	if arn == "" {
		return fmt.Errorf("CreateDBInstance: missing DBInstanceArn")
	}
	t.Set("db_id", id)
	return nil
}

func (g *rdsCliGroup) DescribeDBInstances(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("db_id")
	if id == "" {
		return fmt.Errorf("DescribeDBInstances: no db_id from CreateDBInstance")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-instances",
		"--db-instance-identifier", id,
	)
	if err != nil {
		return err
	}
	instances, _ := out["DBInstances"].([]interface{})
	if len(instances) == 0 {
		return fmt.Errorf("DescribeDBInstances: no instances returned")
	}
	return nil
}

func (g *rdsCliGroup) DeleteDBInstance(_ context.Context, t *harness.TestContext) error {
	id := fmt.Sprintf("compat-del-%s", t.RunID)
	awscli.Run(t.Endpoint, t.Region, "rds", "create-db-instance", //nolint:errcheck
		"--db-instance-identifier", id,
		"--db-instance-class", "db.t3.micro",
		"--engine", "mysql",
		"--master-username", "admin",
		"--master-user-password", "password123",
		"--allocated-storage", "20",
	)
	return awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-instance",
		"--db-instance-identifier", id,
		"--skip-final-snapshot",
	)
}

// waitForDBStatus polls DescribeDBInstances until the instance reports target.
//
// The go, java, node and python suites have carried this for a while; the CLI
// suite went straight from create to stop and only passed because Overcast used
// to report an instance available the instant it was created. It does not any
// more — the status now waits on the engine actually accepting a connection —
// so the CLI suite was the one asking for something real AWS refuses too:
// stop-db-instance on a creating instance is InvalidDBInstanceState there as
// well.
// The budget this suite already carried — two minutes, because a first boot
// initialises the data directory — now expressed as wall-clock time and shared
// with the other four suites. Counting attempts and counting seconds stop
// meaning the same thing the moment the machine is busy, and here each attempt
// shells out to the AWS CLI, so the drift is larger than anywhere else.
const (
	rdsWaitBudget   = 120 * time.Second
	rdsPollInterval = 2 * time.Second
)

func waitForDBStatus(t *harness.TestContext, id, target string) error {
	deadline := time.Now().Add(rdsWaitBudget)
	last := "(none reported)"
	for {
		out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-instances",
			"--db-instance-identifier", id,
		)
		if err != nil {
			return fmt.Errorf("waiting for %s to be %s: %w", id, target, err)
		}
		instances, _ := out["DBInstances"].([]interface{})
		if len(instances) > 0 {
			db, _ := instances[0].(map[string]interface{})
			status, _ := db["DBInstanceStatus"].(string)
			if status != "" {
				last = status
			}
			if status == target {
				return nil
			}
			if status == "failed" {
				return fmt.Errorf("%s went to failed while waiting for %s", id, target)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not reach %s within %s (last status %q)",
				id, target, rdsWaitBudget, last)
		}
		time.Sleep(rdsPollInterval)
	}
}

func (g *rdsCliGroup) StopDBInstance(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("db_id")
	if id == "" {
		return fmt.Errorf("StopDBInstance: no db_id from CreateDBInstance")
	}
	// stop-db-instance requires an available instance, here and on AWS.
	if err := waitForDBStatus(t, id, "available"); err != nil {
		return fmt.Errorf("StopDBInstance: %w", err)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "stop-db-instance",
		"--db-instance-identifier", id,
	)
	if err != nil {
		return err
	}
	db, _ := out["DBInstance"].(map[string]interface{})
	if db["DBInstanceStatus"] == nil {
		return fmt.Errorf("StopDBInstance: missing DBInstanceStatus")
	}
	return nil
}

func (g *rdsCliGroup) StartDBInstance(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("db_id")
	if id == "" {
		return fmt.Errorf("StartDBInstance: no db_id from CreateDBInstance")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "start-db-instance",
		"--db-instance-identifier", id,
	)
	if err != nil {
		return err
	}
	db, _ := out["DBInstance"].(map[string]interface{})
	if db["DBInstanceStatus"] == nil {
		return fmt.Errorf("StartDBInstance: missing DBInstanceStatus")
	}
	return nil
}

func (g *rdsCliGroup) ModifyDBInstance(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("db_id")
	if id == "" {
		return fmt.Errorf("ModifyDBInstance: no db_id from CreateDBInstance")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "modify-db-instance",
		"--db-instance-identifier", id,
		"--allocated-storage", "30",
	)
	if err != nil {
		return err
	}
	db, _ := out["DBInstance"].(map[string]interface{})
	if db["DBInstanceIdentifier"] == nil {
		return fmt.Errorf("ModifyDBInstance: missing DBInstanceIdentifier")
	}
	return nil
}

// ── rds-subnet-groups ──────────────────────────────────────────────────────

func (g *rdsCliGroup) teardownSubnetGroups(_ context.Context, t *harness.TestContext) error {
	if name := t.GetString("subnet_group_name"); name != "" {
		awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-subnet-group", "--db-subnet-group-name", name) //nolint:errcheck
	}
	return nil
}

func (g *rdsCliGroup) CreateDBSubnetGroup(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "create-db-subnet-group",
		"--db-subnet-group-name", name,
		"--db-subnet-group-description", "compat test subnet group",
		"--subnet-ids", "subnet-00000000", "subnet-00000001",
	)
	if err != nil {
		return err
	}
	sg, _ := out["DBSubnetGroup"].(map[string]interface{})
	if sg["DBSubnetGroupName"] == nil {
		return fmt.Errorf("CreateDBSubnetGroup: missing DBSubnetGroupName")
	}
	t.Set("subnet_group_name", name)
	return nil
}

func (g *rdsCliGroup) DescribeDBSubnetGroups(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("subnet_group_name")
	if name == "" {
		return fmt.Errorf("DescribeDBSubnetGroups: no group from CreateDBSubnetGroup")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-subnet-groups",
		"--db-subnet-group-name", name,
	)
	if err != nil {
		return err
	}
	groups, _ := out["DBSubnetGroups"].([]interface{})
	if len(groups) == 0 {
		return fmt.Errorf("DescribeDBSubnetGroups: no groups returned")
	}
	return nil
}

func (g *rdsCliGroup) DeleteDBSubnetGroup(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("subnet_group_name")
	if name == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-subnet-group",
		"--db-subnet-group-name", name,
	)
}

// ── rds-parameter-groups ───────────────────────────────────────────────────

func (g *rdsCliGroup) teardownParameterGroups(_ context.Context, t *harness.TestContext) error {
	if name := t.GetString("param_group_name"); name != "" {
		awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-parameter-group", "--db-parameter-group-name", name) //nolint:errcheck
	}
	return nil
}

func (g *rdsCliGroup) CreateDBParameterGroup(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-pg-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "create-db-parameter-group",
		"--db-parameter-group-name", name,
		"--db-parameter-group-family", "aurora-mysql8.0",
		"--description", "compat test parameter group",
	)
	if err != nil {
		return err
	}
	pg, _ := out["DBParameterGroup"].(map[string]interface{})
	if pg["DBParameterGroupName"] == nil {
		return fmt.Errorf("CreateDBParameterGroup: missing DBParameterGroupName")
	}
	if pg["DBParameterGroupFamily"] != "aurora-mysql8.0" {
		return fmt.Errorf("CreateDBParameterGroup: family = %v, want aurora-mysql8.0", pg["DBParameterGroupFamily"])
	}
	t.Set("param_group_name", name)
	return nil
}

func (g *rdsCliGroup) DescribeDBParameterGroups(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("param_group_name")
	if name == "" {
		return fmt.Errorf("DescribeDBParameterGroups: no group from CreateDBParameterGroup")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-parameter-groups",
		"--db-parameter-group-name", name,
	)
	if err != nil {
		return err
	}
	groups, _ := out["DBParameterGroups"].([]interface{})
	if len(groups) == 0 {
		return fmt.Errorf("DescribeDBParameterGroups: no groups returned")
	}
	return nil
}

func (g *rdsCliGroup) DeleteDBParameterGroup(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("param_group_name")
	if name == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-parameter-group",
		"--db-parameter-group-name", name,
	)
}

// ── rds-clusters ───────────────────────────────────────────────────────────
//
// Aurora DB clusters. A cluster is a logical record in Overcast — member
// instances are what start engine containers — so the waits here are for the
// emulator's own status transitions rather than for a database to boot. They
// still have to be waited for: stop-db-cluster refuses a cluster that is not
// "available" and start-db-cluster refuses one that is not "stopped", here and
// on AWS, so going straight from create to stop asks for
// InvalidDBClusterStateFault.
//
// The poll interval is tighter than the instance one for the same reason: there
// is no container boot to wait on, only a scheduled transition, so a coarser
// interval would spend seconds observing something that had already happened.
const rdsClusterPollInterval = 500 * time.Millisecond

const (
	rdsClusterEngine         = "aurora-mysql"
	rdsClusterMasterUsername = "admin"
	rdsClusterMasterPassword = "Password1!"
	// The retention period modify-db-cluster sets. Non-zero so it is
	// distinguishable from the create-time default, and echoed back by
	// describe-db-clusters so the change can be observed rather than assumed.
	rdsClusterBackupRetention = 7
)

func (g *rdsCliGroup) teardownClusters(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("cluster_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-cluster", //nolint:errcheck
			"--db-cluster-identifier", id,
			"--skip-final-snapshot",
		)
	}
	return nil
}

// describeDBClusterByID returns this group's cluster, or an error naming what came
// back instead. Every assertion below reads through it, so a describe that
// comes back empty reports as a missing cluster rather than a nil map.
func describeDBClusterByID(t *harness.TestContext, id string) (map[string]any, error) {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-clusters",
		"--db-cluster-identifier", id,
	)
	if err != nil {
		return nil, err
	}
	clusters, _ := out["DBClusters"].([]any)
	if len(clusters) == 0 {
		return nil, fmt.Errorf("describe-db-clusters: no cluster returned for %s", id)
	}
	cluster, _ := clusters[0].(map[string]any)
	if cluster == nil {
		return nil, fmt.Errorf("describe-db-clusters: malformed DBClusters entry for %s", id)
	}
	return cluster, nil
}

func waitForDBClusterStatus(t *harness.TestContext, id, target string) error {
	deadline := time.Now().Add(rdsWaitBudget)
	last := "(none reported)"
	for {
		cluster, err := describeDBClusterByID(t, id)
		if err != nil {
			return fmt.Errorf("waiting for cluster %s to be %s: %w", id, target, err)
		}
		if status, _ := cluster["Status"].(string); status != "" {
			last = status
			if status == target {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DB cluster %s did not reach %s within %s (last status %q)",
				id, target, rdsWaitBudget, last)
		}
		time.Sleep(rdsClusterPollInterval)
	}
}

// waitForDBClusterGone polls the unfiltered describe-db-clusters until this run's
// cluster is no longer listed. Deletion is asynchronous — the call marks the
// cluster "deleting" and the record goes shortly after — so absence is what has
// to be waited for. The unfiltered form rather than a lookup by identifier, so
// the check reads a list rather than depending on the CLI's error text.
func waitForDBClusterGone(t *harness.TestContext, id string) error {
	deadline := time.Now().Add(rdsWaitBudget)
	for {
		out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-clusters")
		if err != nil {
			return err
		}
		found := false
		clusters, _ := out["DBClusters"].([]any)
		for _, entry := range clusters {
			cluster, _ := entry.(map[string]any)
			if cluster != nil && cluster["DBClusterIdentifier"] == id {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DB cluster %s still listed %s after delete-db-cluster", id, rdsWaitBudget)
		}
		time.Sleep(rdsClusterPollInterval)
	}
}

func (g *rdsCliGroup) CreateDBCluster(_ context.Context, t *harness.TestContext) error {
	// Named apart from the instance group's resource so the two never collide
	// when both run against the same emulator.
	id := fmt.Sprintf("compat-cluster-%s", t.RunID)
	// Recorded before the call, not after it: a create that fails partway still
	// leaves something for teardown to remove.
	t.Set("cluster_id", id)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "create-db-cluster",
		"--db-cluster-identifier", id,
		"--engine", rdsClusterEngine,
		"--master-username", rdsClusterMasterUsername,
		"--master-user-password", rdsClusterMasterPassword,
	)
	if err != nil {
		return err
	}
	cluster, _ := out["DBCluster"].(map[string]any)
	if cluster == nil {
		return fmt.Errorf("CreateDBCluster: missing DBCluster")
	}
	if got, _ := cluster["DBClusterIdentifier"].(string); got != id {
		return fmt.Errorf("CreateDBCluster: DBClusterIdentifier = %q, want %q", got, id)
	}
	if arn, _ := cluster["DBClusterArn"].(string); arn == "" {
		return fmt.Errorf("CreateDBCluster: missing DBClusterArn")
	}
	return nil
}

func (g *rdsCliGroup) DescribeDBClusters(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cluster_id")
	if id == "" {
		return fmt.Errorf("DescribeDBClusters: no cluster_id from CreateDBCluster")
	}
	cluster, err := describeDBClusterByID(t, id)
	if err != nil {
		return err
	}
	if got, _ := cluster["DBClusterIdentifier"].(string); got != id {
		return fmt.Errorf("DescribeDBClusters: DBClusterIdentifier = %q, want %q", got, id)
	}
	if got, _ := cluster["Engine"].(string); got != rdsClusterEngine {
		return fmt.Errorf("DescribeDBClusters: Engine = %q, want %q", got, rdsClusterEngine)
	}
	if status, _ := cluster["Status"].(string); status == "" {
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
	writer, _ := cluster["Endpoint"].(string)
	reader, _ := cluster["ReaderEndpoint"].(string)
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

func (g *rdsCliGroup) ModifyDBCluster(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cluster_id")
	if id == "" {
		return fmt.Errorf("ModifyDBCluster: no cluster_id from CreateDBCluster")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "modify-db-cluster",
		"--db-cluster-identifier", id,
		"--backup-retention-period", strconv.Itoa(rdsClusterBackupRetention),
	)
	if err != nil {
		return err
	}
	cluster, _ := out["DBCluster"].(map[string]any)
	if cluster == nil {
		return fmt.Errorf("ModifyDBCluster: missing DBCluster")
	}
	if got := dbClusterBackupRetention(cluster); got != rdsClusterBackupRetention {
		return fmt.Errorf("ModifyDBCluster: BackupRetentionPeriod = %d, want %d",
			got, rdsClusterBackupRetention)
	}
	// Read it back rather than trusting the modify response to have echoed a
	// value it never stored.
	after, err := describeDBClusterByID(t, id)
	if err != nil {
		return err
	}
	if got := dbClusterBackupRetention(after); got != rdsClusterBackupRetention {
		return fmt.Errorf("ModifyDBCluster: BackupRetentionPeriod after describe = %d, want %d",
			got, rdsClusterBackupRetention)
	}
	return nil
}

// clusterBackupRetention reads BackupRetentionPeriod out of a decoded cluster.
// JSON numbers decode to float64, and -1 is returned for a missing or
// non-numeric field so that "absent" cannot be mistaken for a retention of 0.
func dbClusterBackupRetention(cluster map[string]any) int {
	value, ok := cluster["BackupRetentionPeriod"].(float64)
	if !ok {
		return -1
	}
	return int(value)
}

func (g *rdsCliGroup) StopDBCluster(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cluster_id")
	if id == "" {
		return fmt.Errorf("StopDBCluster: no cluster_id from CreateDBCluster")
	}
	// stop-db-cluster requires an available cluster, here and on AWS.
	if err := waitForDBClusterStatus(t, id, "available"); err != nil {
		return fmt.Errorf("StopDBCluster: %w", err)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "stop-db-cluster",
		"--db-cluster-identifier", id,
	)
	if err != nil {
		return err
	}
	cluster, _ := out["DBCluster"].(map[string]any)
	if cluster == nil || cluster["Status"] == nil {
		return fmt.Errorf("StopDBCluster: missing Status")
	}
	// The stop is this test's mutation, so this test is where it is confirmed
	// to have landed — not StartDBCluster's precondition wait.
	return waitForDBClusterStatus(t, id, "stopped")
}

func (g *rdsCliGroup) StartDBCluster(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cluster_id")
	if id == "" {
		return fmt.Errorf("StartDBCluster: no cluster_id from CreateDBCluster")
	}
	if err := waitForDBClusterStatus(t, id, "stopped"); err != nil {
		return fmt.Errorf("StartDBCluster: %w", err)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "start-db-cluster",
		"--db-cluster-identifier", id,
	)
	if err != nil {
		return err
	}
	cluster, _ := out["DBCluster"].(map[string]any)
	if cluster == nil || cluster["Status"] == nil {
		return fmt.Errorf("StartDBCluster: missing Status")
	}
	return waitForDBClusterStatus(t, id, "available")
}

func (g *rdsCliGroup) DeleteDBCluster(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cluster_id")
	if id == "" {
		return fmt.Errorf("DeleteDBCluster: no cluster_id from CreateDBCluster")
	}
	if err := awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-cluster",
		"--db-cluster-identifier", id,
		"--skip-final-snapshot",
	); err != nil {
		return err
	}
	if err := waitForDBClusterGone(t, id); err != nil {
		return err
	}
	t.Set("cluster_id", "")
	return nil
}

// ── rds-cluster-members ────────────────────────────────────────────────────
//
// An Aurora cluster with an instance in it. rds-clusters deliberately has none,
// which leaves two things nothing outside the emulator's own Go tests looks at:
// DBClusterMembers, and the settings a member is supposed to take from its
// cluster rather than from its own request.
//
// Kept apart from rds-clusters rather than bolted onto it: a member changes
// what the cluster endpoints mean — with a writer on record they can collapse
// onto one loopback address for a host caller, which is deliberate and would
// break that group's "the two endpoints differ" assertion — and a second group
// declaring create-db-cluster would make the bare impl key for it ambiguous in
// every suite, which aborts the run.

const (
	rdsMemberEngine   = "aurora-mysql"
	rdsMemberUsername = "clusteradmin"
	rdsMemberPassword = "Password1!"
	rdsMemberClass    = "db.t3.micro"
)

func (g *rdsCliGroup) teardownClusterMembers(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order: the member owns a container, the cluster owns the
	// member.
	if id := t.GetString("member_instance"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-instance", //nolint:errcheck
			"--db-instance-identifier", id, "--skip-final-snapshot")
	}
	if id := t.GetString("member_cluster"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-cluster", //nolint:errcheck
			"--db-cluster-identifier", id, "--skip-final-snapshot")
	}
	return nil
}

func (g *rdsCliGroup) CreateClusterForMembers(_ context.Context, t *harness.TestContext) error {
	clusterID := fmt.Sprintf("compat-members-%s", t.RunID)
	t.Set("member_cluster", clusterID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "create-db-cluster",
		"--db-cluster-identifier", clusterID,
		"--engine", rdsMemberEngine,
		"--master-username", rdsMemberUsername,
		"--master-user-password", rdsMemberPassword,
	)
	if err != nil {
		return err
	}
	cluster, _ := out["DBCluster"].(map[string]any)
	if cluster == nil {
		return fmt.Errorf("CreateClusterForMembers: missing DBCluster")
	}
	if arn, _ := cluster["DBClusterArn"].(string); arn == "" {
		return fmt.Errorf("CreateClusterForMembers: missing DBClusterArn")
	}
	// The engine version the cluster settled on is what the member must
	// inherit, so it is recorded here rather than assumed.
	version, _ := cluster["EngineVersion"].(string)
	t.Set("member_cluster_engine_version", version)
	return nil
}

func (g *rdsCliGroup) AddClusterMember(_ context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("member_cluster")
	if clusterID == "" {
		return fmt.Errorf("AddClusterMember: no cluster from CreateClusterForMembers")
	}
	instanceID := fmt.Sprintf("compat-member-%s", t.RunID)
	t.Set("member_instance", instanceID)
	// No --master-username or --master-user-password: an Aurora member takes
	// both from its cluster, and AWS documents them as not applying here.
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "create-db-instance",
		"--db-instance-identifier", instanceID,
		"--db-cluster-identifier", clusterID,
		"--engine", rdsMemberEngine,
		"--db-instance-class", rdsMemberClass,
	)
	if err != nil {
		return err
	}
	db, _ := out["DBInstance"].(map[string]any)
	if db == nil {
		return fmt.Errorf("AddClusterMember: missing DBInstance")
	}
	if got, _ := db["DBInstanceIdentifier"].(string); got != instanceID {
		return fmt.Errorf("AddClusterMember: DBInstanceIdentifier = %q, want %q", got, instanceID)
	}
	if got, _ := db["DBClusterIdentifier"].(string); got != clusterID {
		return fmt.Errorf("AddClusterMember: DBClusterIdentifier = %q, want %q", got, clusterID)
	}
	return nil
}

// clusterMemberIdentifiers returns the DBInstanceIdentifier of every member
// listed on the cluster, and whether the named one is recorded as the writer.
func clusterMemberIdentifiers(cluster map[string]any, want string) (ids []string, isWriter bool) {
	members, _ := cluster["DBClusterMembers"].([]any)
	for _, entry := range members {
		m, _ := entry.(map[string]any)
		if m == nil {
			continue
		}
		id, _ := m["DBInstanceIdentifier"].(string)
		ids = append(ids, id)
		if id == want {
			isWriter, _ = m["IsClusterWriter"].(bool)
		}
	}
	return ids, isWriter
}

func (g *rdsCliGroup) DescribeClusterMembers(_ context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("member_cluster")
	instanceID := t.GetString("member_instance")
	if clusterID == "" || instanceID == "" {
		return fmt.Errorf("DescribeClusterMembers: no cluster or member from the earlier tests")
	}
	cluster, err := describeDBClusterByID(t, clusterID)
	if err != nil {
		return err
	}
	ids, isWriter := clusterMemberIdentifiers(cluster, instanceID)
	if len(ids) == 0 {
		return fmt.Errorf("DescribeClusterMembers: DBClusterMembers is empty after adding %s", instanceID)
	}
	found := false
	for _, id := range ids {
		if id == instanceID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("DescribeClusterMembers: %s is not among the listed members %v", instanceID, ids)
	}
	// The first member of a cluster is its writer. A membership recorded with
	// no writer is the shape that leaves the cluster endpoints pointing at
	// nothing.
	if !isWriter {
		return fmt.Errorf("DescribeClusterMembers: %s is listed but IsClusterWriter is false, and it is the only member", instanceID)
	}
	return nil
}

func (g *rdsCliGroup) DescribeMemberInheritedSettings(_ context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("member_cluster")
	instanceID := t.GetString("member_instance")
	if instanceID == "" {
		return fmt.Errorf("DescribeMemberInheritedSettings: no member from AddClusterMember")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "rds", "describe-db-instances",
		"--db-instance-identifier", instanceID,
	)
	if err != nil {
		return err
	}
	instances, _ := out["DBInstances"].([]any)
	if len(instances) == 0 {
		return fmt.Errorf("DescribeMemberInheritedSettings: no instance returned for %s", instanceID)
	}
	member, _ := instances[0].(map[string]any)
	if member == nil {
		return fmt.Errorf("DescribeMemberInheritedSettings: malformed DBInstances entry for %s", instanceID)
	}

	// The member never sent a master username; it must be the cluster's.
	if got, _ := member["MasterUsername"].(string); got != rdsMemberUsername {
		return fmt.Errorf("DescribeMemberInheritedSettings: MasterUsername = %q, want the cluster's %q",
			got, rdsMemberUsername)
	}
	if got, _ := member["DBClusterIdentifier"].(string); got != clusterID {
		return fmt.Errorf("DescribeMemberInheritedSettings: DBClusterIdentifier = %q, want %q", got, clusterID)
	}
	if want := t.GetString("member_cluster_engine_version"); want != "" {
		if got, _ := member["EngineVersion"].(string); got != want {
			return fmt.Errorf("DescribeMemberInheritedSettings: EngineVersion = %q, want the cluster's %q", got, want)
		}
	}
	// Port is deliberately not asserted: Overcast answers with a port the
	// caller can actually dial, which differs between a host and a sibling
	// container, so any fixed expectation here would be wrong for one of them.
	return nil
}

func (g *rdsCliGroup) RemoveClusterMember(_ context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("member_cluster")
	instanceID := t.GetString("member_instance")
	if clusterID == "" || instanceID == "" {
		return fmt.Errorf("RemoveClusterMember: no cluster or member from the earlier tests")
	}
	if err := awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-instance",
		"--db-instance-identifier", instanceID, "--skip-final-snapshot"); err != nil {
		return err
	}

	// Deleting the instance must take it out of the cluster too. It did not
	// until recently: the record went and the membership entry stayed, so
	// describe-db-clusters kept reporting a member that no longer existed.
	deadline := time.Now().Add(rdsWaitBudget)
	for {
		cluster, err := describeDBClusterByID(t, clusterID)
		if err != nil {
			return err
		}
		ids, _ := clusterMemberIdentifiers(cluster, instanceID)
		listed := false
		for _, id := range ids {
			if id == instanceID {
				listed = true
				break
			}
		}
		if !listed {
			t.Set("member_instance", "")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DB instance %s was still listed in %s's DBClusterMembers %s after delete-db-instance",
				instanceID, clusterID, rdsWaitBudget)
		}
		time.Sleep(rdsClusterPollInterval)
	}
}

func (g *rdsCliGroup) DeleteClusterAfterMembers(_ context.Context, t *harness.TestContext) error {
	clusterID := t.GetString("member_cluster")
	if clusterID == "" {
		return fmt.Errorf("DeleteClusterAfterMembers: no cluster from CreateClusterForMembers")
	}
	if err := awscli.Run(t.Endpoint, t.Region, "rds", "delete-db-cluster",
		"--db-cluster-identifier", clusterID, "--skip-final-snapshot"); err != nil {
		return err
	}
	if err := waitForDBClusterGone(t, clusterID); err != nil {
		return err
	}
	t.Set("member_cluster", "")
	return nil
}
