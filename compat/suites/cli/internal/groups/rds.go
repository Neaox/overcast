package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// RDS returns the RDS service group.
func RDS() ServiceGroup {
	g := &rdsCliGroup{}
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
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"rds-instances":        g.setupInstances,
			"rds-subnet-groups":    g.setupNoop,
			"rds-parameter-groups": g.setupNoop,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"rds-instances":        g.teardownInstances,
			"rds-subnet-groups":    g.teardownSubnetGroups,
			"rds-parameter-groups": g.teardownParameterGroups,
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
func (g *rdsCliGroup) StopDBInstance(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("db_id")
	if id == "" {
		return fmt.Errorf("StopDBInstance: no db_id from CreateDBInstance")
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
		"--db-parameter-group-family", "mysql8.0",
		"--description", "compat test parameter group",
	)
	if err != nil {
		return err
	}
	pg, _ := out["DBParameterGroup"].(map[string]interface{})
	if pg["DBParameterGroupName"] == nil {
		return fmt.Errorf("CreateDBParameterGroup: missing DBParameterGroupName")
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
