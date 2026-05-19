package rds

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var rdsOps = []string{
	"CreateDBInstance", "DescribeDBInstances", "DeleteDBInstance",
	"DescribeDBEngineVersions", "StopDBInstance", "StartDBInstance", "ModifyDBInstance",
	"CreateDBSubnetGroup", "DeleteDBSubnetGroup", "DescribeDBSubnetGroups",
	"CreateDBParameterGroup", "DeleteDBParameterGroup", "DescribeDBParameterGroups",
	"DescribeOrderableDBInstanceOptions",
	"CreateDBCluster", "DeleteDBCluster", "DescribeDBClusters",
	"ModifyDBCluster", "StartDBCluster", "StopDBCluster",
}

func TestTypedOps_matchKnownOperations(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	for _, name := range rdsOps {
		operation, ok := s.handler.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
	}
	// Verify that Operations() returns the expected count (20 implemented + 13 stubs)
	ops := s.Operations()
	if len(ops) != 33 {
		t.Fatalf("Operations() returned %d ops, want 33", len(ops))
	}
}
