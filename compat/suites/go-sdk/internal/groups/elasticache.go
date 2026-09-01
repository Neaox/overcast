package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

func ElastiCache(c *clients.Clients) ServiceGroup {
	g := &elasticacheGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateCacheCluster":           g.CreateCacheCluster,
			"DescribeCacheClusters":        g.DescribeCacheClusters,
			"DeleteCacheCluster":           g.DeleteCacheCluster,
			"ModifyCacheCluster":           g.ModifyCacheCluster,
			"ModifyReplicationGroup":       g.ModifyReplicationGroup,
			"CreateReplicationGroup":       g.CreateReplicationGroup,
			"DescribeReplicationGroups":    g.DescribeReplicationGroups,
			"CreateCacheSubnetGroup":       g.CreateCacheSubnetGroup,
			"DescribeCacheSubnetGroups":    g.DescribeCacheSubnetGroups,
			"CreateCacheParameterGroup":    g.CreateCacheParameterGroup,
			"DescribeCacheParameterGroups": g.DescribeCacheParameterGroups,
			"DeleteCacheParameterGroup":    g.DeleteCacheParameterGroup,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"elasticache-clusters":           g.setupNoop,
			"elasticache-modify":             g.setupNoop,
			"elasticache-replication-groups": g.setupNoop,
			"elasticache-subnet-groups":      g.setupNoop,
			"elasticache-parameter-groups":   g.setupNoop,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"elasticache-clusters":           g.teardownClusters,
			"elasticache-modify":             g.teardownModify,
			"elasticache-replication-groups": g.teardownReplicationGroups,
			"elasticache-subnet-groups":      g.teardownSubnetGroups,
			"elasticache-parameter-groups":   g.teardownParameterGroups,
		},
	}
}

type elasticacheGroup struct{ c *clients.Clients }

func (g *elasticacheGroup) cl() *elasticache.Client { return g.c.ElastiCache() }
func (g *elasticacheGroup) setupNoop(_ context.Context, _ *harness.TestContext) error {
	return nil
}
func (g *elasticacheGroup) uniqueName(desc string) string {
	return fmt.Sprintf("compat-%s-%d", desc, time.Now().Unix())
}

// ─── elasticache-clusters ─────────────────────────────────────────────────────

func (g *elasticacheGroup) CreateCacheCluster(ctx context.Context, t *harness.TestContext) error {
	id := g.uniqueName("cluster")
	t.Set("_clusterId", id)
	_, err := g.cl().CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String(id),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String(id),
	})
	if err != nil {
		return fmt.Errorf("elasticache CreateCacheCluster: describe failed: %w", err)
	}
	if len(resp.CacheClusters) != 1 {
		return fmt.Errorf("elasticache CreateCacheCluster: expected 1 cluster, got %d", len(resp.CacheClusters))
	}
	if aws.ToString(resp.CacheClusters[0].CacheClusterId) != id {
		return fmt.Errorf("elasticache CreateCacheCluster: expected CacheClusterId %q, got %v", id, aws.ToString(resp.CacheClusters[0].CacheClusterId))
	}
	return nil
}

func (g *elasticacheGroup) DescribeCacheClusters(ctx context.Context, t *harness.TestContext) error {
	id := g.uniqueName("describe")
	t.Set("_describeClusterId", id)
	_, err := g.cl().CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String(id),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String(id),
	})
	if err != nil {
		return fmt.Errorf("elasticache DescribeCacheClusters: describe failed: %w", err)
	}
	if len(resp.CacheClusters) != 1 {
		return fmt.Errorf("elasticache DescribeCacheClusters: expected 1 cluster, got %d", len(resp.CacheClusters))
	}
	if aws.ToString(resp.CacheClusters[0].CacheClusterId) != id {
		return fmt.Errorf("elasticache DescribeCacheClusters: expected CacheClusterId %q, got %v", id, aws.ToString(resp.CacheClusters[0].CacheClusterId))
	}
	return nil
}

func (g *elasticacheGroup) DeleteCacheCluster(ctx context.Context, t *harness.TestContext) error {
	id := g.uniqueName("delete")
	t.Set("_deleteClusterId", id)
	_, err := g.cl().CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String(id),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{
		CacheClusterId: aws.String(id),
	})
	if err != nil {
		return fmt.Errorf("elasticache DeleteCacheCluster: delete failed: %w", err)
	}
	if resp.CacheCluster == nil {
		return fmt.Errorf("elasticache DeleteCacheCluster: no CacheCluster in response")
	}
	if aws.ToString(resp.CacheCluster.CacheClusterStatus) != "deleting" {
		return fmt.Errorf("elasticache DeleteCacheCluster: expected CacheClusterStatus 'deleting', got %v", aws.ToString(resp.CacheCluster.CacheClusterStatus))
	}
	return nil
}

func (g *elasticacheGroup) teardownClusters(ctx context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_clusterId", "_describeClusterId", "_deleteClusterId"} {
		if id := t.GetString(key); id != "" {
			g.cl().DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{CacheClusterId: aws.String(id)}) //nolint:errcheck
		}
	}
	return nil
}

// ─── elasticache-modify ───────────────────────────────────────────────────────

func (g *elasticacheGroup) ModifyCacheCluster(ctx context.Context, t *harness.TestContext) error {
	id := g.uniqueName("modify-cluster")
	t.Set("_modifyClusterId", id)
	_, err := g.cl().CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String(id),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ModifyCacheCluster(ctx, &elasticache.ModifyCacheClusterInput{
		CacheClusterId:   aws.String(id),
		CacheNodeType:    aws.String("cache.t3.small"),
		ApplyImmediately: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("elasticache ModifyCacheCluster: modify failed: %w", err)
	}
	if resp.CacheCluster == nil {
		return fmt.Errorf("elasticache ModifyCacheCluster: no CacheCluster in response")
	}
	if aws.ToString(resp.CacheCluster.CacheClusterId) != id {
		return fmt.Errorf("elasticache ModifyCacheCluster: expected CacheClusterId %q, got %v", id, aws.ToString(resp.CacheCluster.CacheClusterId))
	}
	if aws.ToString(resp.CacheCluster.CacheClusterStatus) != "modifying" {
		return fmt.Errorf("elasticache ModifyCacheCluster: expected CacheClusterStatus 'modifying', got %v", aws.ToString(resp.CacheCluster.CacheClusterStatus))
	}
	if aws.ToString(resp.CacheCluster.CacheNodeType) != "cache.t3.small" {
		return fmt.Errorf("elasticache ModifyCacheCluster: expected CacheNodeType 'cache.t3.small', got %v", aws.ToString(resp.CacheCluster.CacheNodeType))
	}
	return nil
}

func (g *elasticacheGroup) ModifyReplicationGroup(ctx context.Context, t *harness.TestContext) error {
	id := g.uniqueName("modify-rg")
	t.Set("_modifyRgId", id)
	_, err := g.cl().CreateReplicationGroup(ctx, &elasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String(id),
		ReplicationGroupDescription: aws.String("original"),
		CacheNodeType:               aws.String("cache.t3.micro"),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ModifyReplicationGroup(ctx, &elasticache.ModifyReplicationGroupInput{
		ReplicationGroupId:          aws.String(id),
		ReplicationGroupDescription: aws.String("updated"),
		ApplyImmediately:            aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("elasticache ModifyReplicationGroup: modify failed: %w", err)
	}
	if resp.ReplicationGroup == nil {
		return fmt.Errorf("elasticache ModifyReplicationGroup: no ReplicationGroup in response")
	}
	if aws.ToString(resp.ReplicationGroup.ReplicationGroupId) != id {
		return fmt.Errorf("elasticache ModifyReplicationGroup: expected ReplicationGroupId %q, got %v", id, aws.ToString(resp.ReplicationGroup.ReplicationGroupId))
	}
	if aws.ToString(resp.ReplicationGroup.Status) != "modifying" {
		return fmt.Errorf("elasticache ModifyReplicationGroup: expected Status 'modifying', got %v", aws.ToString(resp.ReplicationGroup.Status))
	}
	if aws.ToString(resp.ReplicationGroup.Description) != "updated" {
		return fmt.Errorf("elasticache ModifyReplicationGroup: expected Description 'updated', got %v", aws.ToString(resp.ReplicationGroup.Description))
	}
	return nil
}

func (g *elasticacheGroup) teardownModify(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("_modifyClusterId"); id != "" {
		g.cl().DeleteCacheCluster(ctx, &elasticache.DeleteCacheClusterInput{CacheClusterId: aws.String(id)}) //nolint:errcheck
	}
	if id := t.GetString("_modifyRgId"); id != "" {
		g.cl().DeleteReplicationGroup(ctx, &elasticache.DeleteReplicationGroupInput{ReplicationGroupId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

// ─── elasticache-replication-groups ───────────────────────────────────────────

func (g *elasticacheGroup) CreateReplicationGroup(ctx context.Context, t *harness.TestContext) error {
	id := g.uniqueName("rg")
	t.Set("_rgId", id)
	_, err := g.cl().CreateReplicationGroup(ctx, &elasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String(id),
		ReplicationGroupDescription: aws.String("compat test group"),
		CacheNodeType:               aws.String("cache.t3.micro"),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeReplicationGroups(ctx, &elasticache.DescribeReplicationGroupsInput{
		ReplicationGroupId: aws.String(id),
	})
	if err != nil {
		return fmt.Errorf("elasticache CreateReplicationGroup: describe failed: %w", err)
	}
	if len(resp.ReplicationGroups) != 1 {
		return fmt.Errorf("elasticache CreateReplicationGroup: expected 1 group, got %d", len(resp.ReplicationGroups))
	}
	if aws.ToString(resp.ReplicationGroups[0].ReplicationGroupId) != id {
		return fmt.Errorf("elasticache CreateReplicationGroup: expected ReplicationGroupId %q, got %v", id, aws.ToString(resp.ReplicationGroups[0].ReplicationGroupId))
	}
	return nil
}

func (g *elasticacheGroup) DescribeReplicationGroups(ctx context.Context, t *harness.TestContext) error {
	id := g.uniqueName("rg-desc")
	t.Set("_rgDescId", id)
	_, err := g.cl().CreateReplicationGroup(ctx, &elasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String(id),
		ReplicationGroupDescription: aws.String("compat describe"),
		CacheNodeType:               aws.String("cache.t3.micro"),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeReplicationGroups(ctx, &elasticache.DescribeReplicationGroupsInput{
		ReplicationGroupId: aws.String(id),
	})
	if err != nil {
		return fmt.Errorf("elasticache DescribeReplicationGroups: describe failed: %w", err)
	}
	if len(resp.ReplicationGroups) != 1 {
		return fmt.Errorf("elasticache DescribeReplicationGroups: expected 1 group, got %d", len(resp.ReplicationGroups))
	}
	if aws.ToString(resp.ReplicationGroups[0].ReplicationGroupId) != id {
		return fmt.Errorf("elasticache DescribeReplicationGroups: expected ReplicationGroupId %q, got %v", id, aws.ToString(resp.ReplicationGroups[0].ReplicationGroupId))
	}
	return nil
}

func (g *elasticacheGroup) teardownReplicationGroups(ctx context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_rgId", "_rgDescId"} {
		if id := t.GetString(key); id != "" {
			g.cl().DeleteReplicationGroup(ctx, &elasticache.DeleteReplicationGroupInput{ReplicationGroupId: aws.String(id)}) //nolint:errcheck
		}
	}
	return nil
}

// ─── elasticache-subnet-groups ────────────────────────────────────────────────

func (g *elasticacheGroup) CreateCacheSubnetGroup(ctx context.Context, t *harness.TestContext) error {
	name := g.uniqueName("sngrp")
	t.Set("_sngrpName", name)
	resp, err := g.cl().CreateCacheSubnetGroup(ctx, &elasticache.CreateCacheSubnetGroupInput{
		CacheSubnetGroupName:        aws.String(name),
		CacheSubnetGroupDescription: aws.String("compat test"),
		SubnetIds:                   []string{"subnet-aabbccdd"},
	})
	if err != nil {
		return fmt.Errorf("elasticache CreateCacheSubnetGroup: create failed: %w", err)
	}
	if resp.CacheSubnetGroup == nil {
		return fmt.Errorf("elasticache CreateCacheSubnetGroup: no CacheSubnetGroup in response")
	}
	if aws.ToString(resp.CacheSubnetGroup.CacheSubnetGroupName) != name {
		return fmt.Errorf("elasticache CreateCacheSubnetGroup: expected CacheSubnetGroupName %q, got %v", name, aws.ToString(resp.CacheSubnetGroup.CacheSubnetGroupName))
	}
	return nil
}

func (g *elasticacheGroup) DescribeCacheSubnetGroups(ctx context.Context, t *harness.TestContext) error {
	name := g.uniqueName("sngrp-desc")
	t.Set("_sngrpDescName", name)
	_, err := g.cl().CreateCacheSubnetGroup(ctx, &elasticache.CreateCacheSubnetGroupInput{
		CacheSubnetGroupName:        aws.String(name),
		CacheSubnetGroupDescription: aws.String("describe test"),
		SubnetIds:                   []string{"subnet-11223344"},
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeCacheSubnetGroups(ctx, &elasticache.DescribeCacheSubnetGroupsInput{
		CacheSubnetGroupName: aws.String(name),
	})
	if err != nil {
		return fmt.Errorf("elasticache DescribeCacheSubnetGroups: describe failed: %w", err)
	}
	if len(resp.CacheSubnetGroups) != 1 {
		return fmt.Errorf("elasticache DescribeCacheSubnetGroups: expected 1 group, got %d", len(resp.CacheSubnetGroups))
	}
	if aws.ToString(resp.CacheSubnetGroups[0].CacheSubnetGroupName) != name {
		return fmt.Errorf("elasticache DescribeCacheSubnetGroups: expected CacheSubnetGroupName %q, got %v", name, aws.ToString(resp.CacheSubnetGroups[0].CacheSubnetGroupName))
	}
	return nil
}

func (g *elasticacheGroup) teardownSubnetGroups(ctx context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_sngrpName", "_sngrpDescName"} {
		if name := t.GetString(key); name != "" {
			g.cl().DeleteCacheSubnetGroup(ctx, &elasticache.DeleteCacheSubnetGroupInput{CacheSubnetGroupName: aws.String(name)}) //nolint:errcheck
		}
	}
	return nil
}

// ─── elasticache-parameter-groups ─────────────────────────────────────────────

func (g *elasticacheGroup) CreateCacheParameterGroup(ctx context.Context, t *harness.TestContext) error {
	name := g.uniqueName("pg")
	t.Set("_pgName", name)
	resp, err := g.cl().CreateCacheParameterGroup(ctx, &elasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName:   aws.String(name),
		CacheParameterGroupFamily: aws.String("redis7"),
		Description:               aws.String("compat test group"),
	})
	if err != nil {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: create failed: %w", err)
	}
	if resp.CacheParameterGroup == nil {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: no CacheParameterGroup in response")
	}
	if aws.ToString(resp.CacheParameterGroup.CacheParameterGroupName) != name {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: expected CacheParameterGroupName %q, got %v", name, aws.ToString(resp.CacheParameterGroup.CacheParameterGroupName))
	}
	if aws.ToString(resp.CacheParameterGroup.CacheParameterGroupFamily) != "redis7" {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: expected CacheParameterGroupFamily 'redis7', got %v", aws.ToString(resp.CacheParameterGroup.CacheParameterGroupFamily))
	}
	if arn := aws.ToString(resp.CacheParameterGroup.ARN); arn == "" {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: ARN is empty")
	}
	return nil
}

func (g *elasticacheGroup) DescribeCacheParameterGroups(ctx context.Context, t *harness.TestContext) error {
	name := g.uniqueName("pg-desc")
	t.Set("_pgDescName", name)
	_, err := g.cl().CreateCacheParameterGroup(ctx, &elasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName:   aws.String(name),
		CacheParameterGroupFamily: aws.String("redis7"),
		Description:               aws.String("describe test"),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeCacheParameterGroups(ctx, &elasticache.DescribeCacheParameterGroupsInput{
		CacheParameterGroupName: aws.String(name),
	})
	if err != nil {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: describe failed: %w", err)
	}
	if len(resp.CacheParameterGroups) != 1 {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: expected 1 group, got %d", len(resp.CacheParameterGroups))
	}
	if aws.ToString(resp.CacheParameterGroups[0].CacheParameterGroupName) != name {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: expected CacheParameterGroupName %q, got %v", name, aws.ToString(resp.CacheParameterGroups[0].CacheParameterGroupName))
	}
	if aws.ToString(resp.CacheParameterGroups[0].CacheParameterGroupFamily) != "redis7" {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: expected CacheParameterGroupFamily 'redis7', got %v", aws.ToString(resp.CacheParameterGroups[0].CacheParameterGroupFamily))
	}
	return nil
}

func (g *elasticacheGroup) DeleteCacheParameterGroup(ctx context.Context, t *harness.TestContext) error {
	name := g.uniqueName("pg-del")
	t.Set("_pgDelName", name)
	_, err := g.cl().CreateCacheParameterGroup(ctx, &elasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName:   aws.String(name),
		CacheParameterGroupFamily: aws.String("redis7"),
		Description:               aws.String("delete test"),
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteCacheParameterGroup(ctx, &elasticache.DeleteCacheParameterGroupInput{
		CacheParameterGroupName: aws.String(name),
	})
	if err != nil {
		return fmt.Errorf("elasticache DeleteCacheParameterGroup: delete failed: %w", err)
	}
	_, err = g.cl().DescribeCacheParameterGroups(ctx, &elasticache.DescribeCacheParameterGroupsInput{
		CacheParameterGroupName: aws.String(name),
	})
	if err == nil {
		return fmt.Errorf("elasticache DeleteCacheParameterGroup: parameter group still exists after delete")
	}
	return nil
}

func (g *elasticacheGroup) teardownParameterGroups(ctx context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_pgName", "_pgDescName", "_pgDelName"} {
		if name := t.GetString(key); name != "" {
			g.cl().DeleteCacheParameterGroup(ctx, &elasticache.DeleteCacheParameterGroupInput{CacheParameterGroupName: aws.String(name)}) //nolint:errcheck
		}
	}
	return nil
}
