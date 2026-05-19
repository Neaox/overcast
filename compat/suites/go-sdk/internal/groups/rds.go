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

type rdsGroup struct{ c *clients.Clients }

func (g *rdsGroup) cl() *rds.Client { return g.c.RDS() }

func (g *rdsGroup) waitForDBStatus(ctx context.Context, dbID, target string) error {
	for i := 0; i < 60; i++ {
		resp, err := g.cl().DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(dbID),
		})
		if err == nil && len(resp.DBInstances) > 0 && resp.DBInstances[0].DBInstanceStatus != nil {
			if *resp.DBInstances[0].DBInstanceStatus == target {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("DB instance %s did not reach %q after 60 attempts", dbID, target)
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
		DBParameterGroupFamily: aws.String("mysql8.0"),
		Description:            aws.String("compat test parameter group"),
	})
	if err != nil {
		return err
	}
	if resp.DBParameterGroup == nil || resp.DBParameterGroup.DBParameterGroupName == nil {
		return fmt.Errorf("CreateDBParameterGroup: missing DBParameterGroupName")
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
