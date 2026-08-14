package rds

// aurora_vpc_inherit_test.go — an Aurora member instance belongs to the VPC its
// cluster belongs to.
//
// AWS puts the subnet group on the cluster and the instances follow it. CDK's
// rds.DatabaseCluster emits AWS::RDS::DBInstance with a DBClusterIdentifier and
// no DBSubnetGroupName, so reading only the instance's own field left the writer
// outside the VPC its own application was in.

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func auroraHandler(t *testing.T) *Handler {
	t.Helper()
	return newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Network: "overcast"},
		newRDSStore(state.NewMemoryStore(), "us-east-1"),
		serviceutil.NewServiceLogger(zap.NewNop(), "rds"),
		clock.New(),
	)
}

// seedClusterInSubnetGroup creates a subnet group in vpcID and an Aurora cluster
// using it, which is the shape CDK produces.
func seedClusterInSubnetGroup(t *testing.T, h *Handler, ctx context.Context, groupName, vpcID, clusterID string) {
	t.Helper()
	if aerr := h.store.putDBSubnetGroup(ctx, &DBSubnetGroup{
		DBSubnetGroupName: groupName,
		VpcId:             vpcID,
	}); aerr != nil {
		t.Fatalf("putDBSubnetGroup: %s", aerr.Message)
	}
	if aerr := h.store.putDBCluster(ctx, &DBCluster{
		DBClusterIdentifier: clusterID,
		Engine:              "aurora-mysql",
		DBSubnetGroupName:   groupName,
		MasterUsername:      "clusteradmin",
		MasterUserPassword:  "cluster-secret-1",
	}); aerr != nil {
		t.Fatalf("putDBCluster: %s", aerr.Message)
	}
}

// A member instance takes its effective credentials from the cluster. Without
// them the engine container has nothing to start with.
func TestCreateDBInstance_auroraMemberInheritsTheClustersCredentials(t *testing.T) {
	h := auroraHandler(t)
	ctx := context.Background()
	seedClusterInSubnetGroup(t, h, ctx, "app-subnets", "vpc-0abc", "app-cluster")

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "app-cluster-writer",
		Engine:               "aurora-mysql",
		DBClusterIdentifier:  "app-cluster",
	}); aerr != nil {
		t.Fatalf("createDBInstanceTyped: %s", aerr.Message)
	}

	got, _ := h.store.getDBInstance(ctx, "app-cluster-writer")
	if got.MasterUsername != "clusteradmin" {
		t.Errorf("MasterUsername = %q, want the cluster's", got.MasterUsername)
	}
	if got.MasterUserPassword != "cluster-secret-1" {
		t.Errorf("MasterUserPassword = %q, want the cluster's", got.MasterUserPassword)
	}
}

func TestCreateDBInstance_auroraMemberInheritsTheClustersDatabaseName(t *testing.T) {
	h := auroraHandler(t)
	ctx := context.Background()
	if aerr := h.store.putDBCluster(ctx, &DBCluster{
		DBClusterIdentifier: "app-cluster",
		Engine:              "aurora-mysql",
		MasterUsername:      "clusteradmin",
		MasterUserPassword:  "cluster-secret-1",
		DatabaseName:        "application",
	}); aerr != nil {
		t.Fatalf("putDBCluster: %s", aerr.Message)
	}

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "app-cluster-writer",
		Engine:               "aurora-mysql",
		DBClusterIdentifier:  "app-cluster",
	}); aerr != nil {
		t.Fatalf("createDBInstanceTyped: %s", aerr.Message)
	}

	got, _ := h.store.getDBInstance(ctx, "app-cluster-writer")
	if got.DBName != "application" {
		t.Errorf("DBName = %q, want the cluster's DatabaseName", got.DBName)
	}
}

func TestCreateDBInstance_auroraMemberUsesClusterOwnedSettings(t *testing.T) {
	// Given: an Aurora cluster owns all settings shared by its members.
	h := auroraHandler(t)
	ctx := context.Background()
	if aerr := h.store.putDBCluster(ctx, &DBCluster{
		DBClusterIdentifier: "app-cluster",
		Engine:              "aurora-mysql",
		EngineVersion:       "8.0.mysql_aurora.3.09.0",
		MasterUsername:      "clusteradmin",
		MasterUserPassword:  "cluster-secret-1",
		DatabaseName:        "application",
		Port:                4406,
	}); aerr != nil {
		t.Fatalf("putDBCluster: %s", aerr.Message)
	}

	// When: ignored instance-level fields contain conflicting values.
	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "app-cluster-writer",
		Engine:               "aurora-mysql",
		DBClusterIdentifier:  "app-cluster",
		EngineVersion:        "2.11",
		MasterUsername:       "instanceadmin",
		MasterUserPassword:   "instance-secret-1",
		DBName:               "instance_database",
		Port:                 3307,
	}); aerr != nil {
		t.Fatalf("createDBInstanceTyped: %s", aerr.Message)
	}

	// Then: the member records only the cluster-owned values.
	got, _ := h.store.getDBInstance(ctx, "app-cluster-writer")
	if got.EngineVersion != "8.0.mysql_aurora.3.09.0" || got.MasterUsername != "clusteradmin" ||
		got.MasterUserPassword != "cluster-secret-1" || got.DBName != "application" || got.Port != 4406 {
		t.Fatalf("member settings = version %q user %q password %q database %q port %d, want cluster-owned values",
			got.EngineVersion, got.MasterUsername, got.MasterUserPassword, got.DBName, got.Port)
	}
}

func TestCreateDBInstance_auroraMemberEngineMustMatchCluster(t *testing.T) {
	// Given: an Aurora MySQL cluster exists.
	h := auroraHandler(t)
	ctx := context.Background()
	if aerr := h.store.putDBCluster(ctx, &DBCluster{DBClusterIdentifier: "app-cluster", Engine: "aurora-mysql"}); aerr != nil {
		t.Fatalf("putDBCluster: %s", aerr.Message)
	}

	// When: a PostgreSQL member is requested for that cluster.
	_, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "wrong-engine",
		Engine:               "aurora-postgresql",
		DBClusterIdentifier:  "app-cluster",
	})

	// Then: the incompatible relationship is rejected synchronously.
	if aerr == nil || aerr.Code != "InvalidParameterCombination" {
		t.Fatalf("error = %#v, want InvalidParameterCombination", aerr)
	}
}

// The exemption is for cluster members only: a standalone instance still has to
// bring its own credentials, because nothing else can supply them.
func TestCreateDBInstance_standaloneStillNeedsCredentials(t *testing.T) {
	h := auroraHandler(t)
	ctx := context.Background()

	_, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "lonely",
		Engine:               "postgres",
	})
	if aerr == nil {
		t.Fatal("createDBInstanceTyped succeeded, want MasterUsername to still be required")
	}
	if !strings.Contains(aerr.Message, "MasterUsername") {
		t.Fatalf("error = %q, want it to name MasterUsername", aerr.Message)
	}
}

func TestCreateDBInstance_auroraMemberInheritsTheClustersVPC(t *testing.T) {
	h := auroraHandler(t)
	ctx := context.Background()
	seedClusterInSubnetGroup(t, h, ctx, "app-subnets", "vpc-0abc", "app-cluster")

	// The member instance names the cluster and no subnet group of its own.
	_, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "app-cluster-writer",
		Engine:               "aurora-mysql",
		DBClusterIdentifier:  "app-cluster",
	})
	if aerr != nil {
		t.Fatalf("createDBInstanceTyped: %s", aerr.Message)
	}

	got, aerr := h.store.getDBInstance(ctx, "app-cluster-writer")
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if got.VpcID != "vpc-0abc" {
		t.Errorf("VpcID = %q, want the cluster's VPC — the writer would land outside it", got.VpcID)
	}
	if got.DBSubnetGroupName != "app-subnets" {
		t.Errorf("DBSubnetGroupName = %q, want the cluster's group", got.DBSubnetGroupName)
	}
	// Inheriting a subnet group makes the instance private by the same rule a
	// standalone instance in that group follows.
	if got.PubliclyAccessibleOrDefault() {
		t.Error("PubliclyAccessible = true, want false for an instance in a subnet group")
	}
}

// DBSubnetGroupName does not apply to an Aurora member, even when present in
// the shared CreateDBInstance request shape.
func TestCreateDBInstance_clusterSubnetGroupBeatsTheInstances(t *testing.T) {
	h := auroraHandler(t)
	ctx := context.Background()
	seedClusterInSubnetGroup(t, h, ctx, "cluster-subnets", "vpc-cluster", "app-cluster")
	if aerr := h.store.putDBSubnetGroup(ctx, &DBSubnetGroup{
		DBSubnetGroupName: "instance-subnets",
		VpcId:             "vpc-instance",
	}); aerr != nil {
		t.Fatalf("putDBSubnetGroup: %s", aerr.Message)
	}

	_, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "odd-one-out",
		Engine:               "aurora-mysql",
		DBClusterIdentifier:  "app-cluster",
		DBSubnetGroupName:    "instance-subnets",
	})
	if aerr != nil {
		t.Fatalf("createDBInstanceTyped: %s", aerr.Message)
	}

	got, _ := h.store.getDBInstance(ctx, "odd-one-out")
	if got.VpcID != "vpc-cluster" || got.DBSubnetGroupName != "cluster-subnets" {
		t.Fatalf("member placement = VPC %q subnet group %q, want the cluster's", got.VpcID, got.DBSubnetGroupName)
	}
}

// Aurora defaults to private even when no subnet group is specified.
func TestCreateDBInstance_clusterWithoutASubnetGroupChangesNothing(t *testing.T) {
	h := auroraHandler(t)
	ctx := context.Background()
	if aerr := h.store.putDBCluster(ctx, &DBCluster{
		DBClusterIdentifier: "bare-cluster",
		Engine:              "aurora-mysql",
		MasterUsername:      "clusteradmin",
		MasterUserPassword:  "cluster-secret-1",
	}); aerr != nil {
		t.Fatalf("putDBCluster: %s", aerr.Message)
	}

	_, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: "bare-writer",
		Engine:               "aurora-mysql",
		DBClusterIdentifier:  "bare-cluster",
	})
	if aerr != nil {
		t.Fatalf("createDBInstanceTyped: %s", aerr.Message)
	}

	got, _ := h.store.getDBInstance(ctx, "bare-writer")
	if got.VpcID != "" || got.DBSubnetGroupName != "" {
		t.Fatalf("instance = vpc %q group %q, want neither", got.VpcID, got.DBSubnetGroupName)
	}
	if got.PubliclyAccessibleOrDefault() {
		t.Error("PubliclyAccessible = true, want Aurora's false default")
	}
}
