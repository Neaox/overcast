package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// ElastiCache returns the ElastiCache service group.
func ElastiCache() ServiceGroup {
	g := &elasticacheGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// elasticache-clusters
			"CreateCacheCluster":    g.CreateCacheCluster,
			"DescribeCacheClusters": g.DescribeCacheClusters,
			"DeleteCacheCluster":    g.DeleteCacheCluster,
			// elasticache-modify
			"ModifyCacheCluster":     g.ModifyCacheCluster,
			"ModifyReplicationGroup": g.ModifyReplicationGroup,
			// elasticache-replication-groups
			"CreateReplicationGroup":    g.CreateReplicationGroup,
			"DescribeReplicationGroups": g.DescribeReplicationGroups,
			// elasticache-subnet-groups
			"CreateCacheSubnetGroup":    g.CreateCacheSubnetGroup,
			"DescribeCacheSubnetGroups": g.DescribeCacheSubnetGroups,
			// elasticache-parameter-groups
			"CreateCacheParameterGroup":    g.CreateCacheParameterGroup,
			"DescribeCacheParameterGroups": g.DescribeCacheParameterGroups,
			"DeleteCacheParameterGroup":    g.DeleteCacheParameterGroup,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"elasticache-clusters":           g.setupClusters,
			"elasticache-modify":             g.setupModify,
			"elasticache-replication-groups": g.setupReplicationGroups,
			"elasticache-subnet-groups":      g.setupSubnetGroups,
			"elasticache-parameter-groups":   g.setupParameterGroups,
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

type elasticacheGroup struct{}

func (g *elasticacheGroup) uniqueName(desc string) string {
	return fmt.Sprintf("compat-%s-%d", desc, time.Now().Unix())
}

// ─── elasticache-clusters ─────────────────────────────────────────────────────

func (g *elasticacheGroup) setupClusters(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *elasticacheGroup) CreateCacheCluster(_ context.Context, t *harness.TestContext) error {
	id := g.uniqueName("cluster")
	t.Set("_clusterId", id)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-cache-cluster",
		"--cache-cluster-id", id,
		"--engine", "redis",
		"--cache-node-type", "cache.t3.micro",
		"--num-cache-nodes", "1",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "describe-cache-clusters",
		"--cache-cluster-id", id,
	)
	if err != nil {
		return fmt.Errorf("elasticache CreateCacheCluster: describe failed: %w", err)
	}
	clusters, _ := out["CacheClusters"].([]any)
	if len(clusters) != 1 {
		return fmt.Errorf("elasticache CreateCacheCluster: expected 1 cluster, got %d", len(clusters))
	}
	cc, _ := clusters[0].(map[string]any)
	if cc["CacheClusterId"] != id {
		return fmt.Errorf("elasticache CreateCacheCluster: expected CacheClusterId %q, got %v", id, cc["CacheClusterId"])
	}
	return nil
}

func (g *elasticacheGroup) DescribeCacheClusters(_ context.Context, t *harness.TestContext) error {
	id := g.uniqueName("describe")
	t.Set("_describeClusterId", id)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-cache-cluster",
		"--cache-cluster-id", id,
		"--engine", "redis",
		"--cache-node-type", "cache.t3.micro",
		"--num-cache-nodes", "1",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "describe-cache-clusters",
		"--cache-cluster-id", id,
	)
	if err != nil {
		return fmt.Errorf("elasticache DescribeCacheClusters: describe failed: %w", err)
	}
	clusters, _ := out["CacheClusters"].([]any)
	if len(clusters) != 1 {
		return fmt.Errorf("elasticache DescribeCacheClusters: expected 1 cluster, got %d", len(clusters))
	}
	cc, _ := clusters[0].(map[string]any)
	if cc["CacheClusterId"] != id {
		return fmt.Errorf("elasticache DescribeCacheClusters: expected CacheClusterId %q, got %v", id, cc["CacheClusterId"])
	}
	return nil
}

func (g *elasticacheGroup) DeleteCacheCluster(_ context.Context, t *harness.TestContext) error {
	id := g.uniqueName("delete")
	t.Set("_deleteClusterId", id)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-cache-cluster",
		"--cache-cluster-id", id,
		"--engine", "redis",
		"--cache-node-type", "cache.t3.micro",
		"--num-cache-nodes", "1",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "delete-cache-cluster",
		"--cache-cluster-id", id,
	)
	if err != nil {
		return fmt.Errorf("elasticache DeleteCacheCluster: delete failed: %w", err)
	}
	cc, _ := out["CacheCluster"].(map[string]any)
	if cc == nil {
		return fmt.Errorf("elasticache DeleteCacheCluster: no CacheCluster in response")
	}
	if cc["CacheClusterStatus"] != "deleting" {
		return fmt.Errorf("elasticache DeleteCacheCluster: expected CacheClusterStatus 'deleting', got %v", cc["CacheClusterStatus"])
	}
	return nil
}

func (g *elasticacheGroup) teardownClusters(_ context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_clusterId", "_describeClusterId", "_deleteClusterId"} {
		if id := t.GetString(key); id != "" {
			awscli.Run(t.Endpoint, t.Region, "elasticache", "delete-cache-cluster", "--cache-cluster-id", id) //nolint:errcheck
		}
	}
	return nil
}

// ─── elasticache-modify ───────────────────────────────────────────────────────

func (g *elasticacheGroup) setupModify(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *elasticacheGroup) ModifyCacheCluster(_ context.Context, t *harness.TestContext) error {
	id := g.uniqueName("modify-cluster")
	t.Set("_modifyClusterId", id)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-cache-cluster",
		"--cache-cluster-id", id,
		"--engine", "redis",
		"--cache-node-type", "cache.t3.micro",
		"--num-cache-nodes", "1",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "modify-cache-cluster",
		"--cache-cluster-id", id,
		"--cache-node-type", "cache.t3.small",
		"--apply-immediately",
	)
	if err != nil {
		return fmt.Errorf("elasticache ModifyCacheCluster: modify failed: %w", err)
	}
	cc, _ := out["CacheCluster"].(map[string]any)
	if cc == nil {
		return fmt.Errorf("elasticache ModifyCacheCluster: no CacheCluster in response")
	}
	if cc["CacheClusterId"] != id {
		return fmt.Errorf("elasticache ModifyCacheCluster: expected CacheClusterId %q, got %v", id, cc["CacheClusterId"])
	}
	if cc["CacheClusterStatus"] != "modifying" {
		return fmt.Errorf("elasticache ModifyCacheCluster: expected CacheClusterStatus 'modifying', got %v", cc["CacheClusterStatus"])
	}
	if cc["CacheNodeType"] != "cache.t3.small" {
		return fmt.Errorf("elasticache ModifyCacheCluster: expected CacheNodeType 'cache.t3.small', got %v", cc["CacheNodeType"])
	}
	return nil
}

func (g *elasticacheGroup) ModifyReplicationGroup(_ context.Context, t *harness.TestContext) error {
	id := g.uniqueName("modify-rg")
	t.Set("_modifyRgId", id)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-replication-group",
		"--replication-group-id", id,
		"--replication-group-description", "original",
		"--cache-node-type", "cache.t3.micro",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "modify-replication-group",
		"--replication-group-id", id,
		"--replication-group-description", "updated",
		"--apply-immediately",
	)
	if err != nil {
		return fmt.Errorf("elasticache ModifyReplicationGroup: modify failed: %w", err)
	}
	rg, _ := out["ReplicationGroup"].(map[string]any)
	if rg == nil {
		return fmt.Errorf("elasticache ModifyReplicationGroup: no ReplicationGroup in response")
	}
	if rg["ReplicationGroupId"] != id {
		return fmt.Errorf("elasticache ModifyReplicationGroup: expected ReplicationGroupId %q, got %v", id, rg["ReplicationGroupId"])
	}
	if rg["Status"] != "modifying" {
		return fmt.Errorf("elasticache ModifyReplicationGroup: expected Status 'modifying', got %v", rg["Status"])
	}
	if rg["Description"] != "updated" {
		return fmt.Errorf("elasticache ModifyReplicationGroup: expected Description 'updated', got %v", rg["Description"])
	}
	return nil
}

func (g *elasticacheGroup) teardownModify(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("_modifyClusterId"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "elasticache", "delete-cache-cluster", "--cache-cluster-id", id) //nolint:errcheck
	}
	if id := t.GetString("_modifyRgId"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "elasticache", "delete-replication-group", "--replication-group-id", id) //nolint:errcheck
	}
	return nil
}

// ─── elasticache-replication-groups ───────────────────────────────────────────

func (g *elasticacheGroup) setupReplicationGroups(_ context.Context, _ *harness.TestContext) error {
	return nil
}

func (g *elasticacheGroup) CreateReplicationGroup(_ context.Context, t *harness.TestContext) error {
	id := g.uniqueName("rg")
	t.Set("_rgId", id)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-replication-group",
		"--replication-group-id", id,
		"--replication-group-description", "compat test group",
		"--cache-node-type", "cache.t3.micro",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "describe-replication-groups",
		"--replication-group-id", id,
	)
	if err != nil {
		return fmt.Errorf("elasticache CreateReplicationGroup: describe failed: %w", err)
	}
	groups, _ := out["ReplicationGroups"].([]any)
	if len(groups) != 1 {
		return fmt.Errorf("elasticache CreateReplicationGroup: expected 1 group, got %d", len(groups))
	}
	rg, _ := groups[0].(map[string]any)
	if rg["ReplicationGroupId"] != id {
		return fmt.Errorf("elasticache CreateReplicationGroup: expected ReplicationGroupId %q, got %v", id, rg["ReplicationGroupId"])
	}
	return nil
}

func (g *elasticacheGroup) DescribeReplicationGroups(_ context.Context, t *harness.TestContext) error {
	id := g.uniqueName("rg-desc")
	t.Set("_rgDescId", id)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-replication-group",
		"--replication-group-id", id,
		"--replication-group-description", "compat describe",
		"--cache-node-type", "cache.t3.micro",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "describe-replication-groups",
		"--replication-group-id", id,
	)
	if err != nil {
		return fmt.Errorf("elasticache DescribeReplicationGroups: describe failed: %w", err)
	}
	groups, _ := out["ReplicationGroups"].([]any)
	if len(groups) != 1 {
		return fmt.Errorf("elasticache DescribeReplicationGroups: expected 1 group, got %d", len(groups))
	}
	rg, _ := groups[0].(map[string]any)
	if rg["ReplicationGroupId"] != id {
		return fmt.Errorf("elasticache DescribeReplicationGroups: expected ReplicationGroupId %q, got %v", id, rg["ReplicationGroupId"])
	}
	return nil
}

func (g *elasticacheGroup) teardownReplicationGroups(_ context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_rgId", "_rgDescId"} {
		if id := t.GetString(key); id != "" {
			awscli.Run(t.Endpoint, t.Region, "elasticache", "delete-replication-group", "--replication-group-id", id) //nolint:errcheck
		}
	}
	return nil
}

// ─── elasticache-subnet-groups ────────────────────────────────────────────────

func (g *elasticacheGroup) setupSubnetGroups(_ context.Context, _ *harness.TestContext) error {
	return nil
}

func (g *elasticacheGroup) CreateCacheSubnetGroup(_ context.Context, t *harness.TestContext) error {
	name := g.uniqueName("sngrp")
	t.Set("_sngrpName", name)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "create-cache-subnet-group",
		"--cache-subnet-group-name", name,
		"--cache-subnet-group-description", "compat test",
		"--subnet-ids", "subnet-aabbccdd",
	)
	if err != nil {
		return fmt.Errorf("elasticache CreateCacheSubnetGroup: create failed: %w", err)
	}
	sg, _ := out["CacheSubnetGroup"].(map[string]any)
	if sg == nil {
		return fmt.Errorf("elasticache CreateCacheSubnetGroup: no CacheSubnetGroup in response")
	}
	if sg["CacheSubnetGroupName"] != name {
		return fmt.Errorf("elasticache CreateCacheSubnetGroup: expected CacheSubnetGroupName %q, got %v", name, sg["CacheSubnetGroupName"])
	}
	return nil
}

func (g *elasticacheGroup) DescribeCacheSubnetGroups(_ context.Context, t *harness.TestContext) error {
	name := g.uniqueName("sngrp-desc")
	t.Set("_sngrpDescName", name)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-cache-subnet-group",
		"--cache-subnet-group-name", name,
		"--cache-subnet-group-description", "describe test",
		"--subnet-ids", "subnet-11223344",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "describe-cache-subnet-groups",
		"--cache-subnet-group-name", name,
	)
	if err != nil {
		return fmt.Errorf("elasticache DescribeCacheSubnetGroups: describe failed: %w", err)
	}
	groups, _ := out["CacheSubnetGroups"].([]any)
	if len(groups) != 1 {
		return fmt.Errorf("elasticache DescribeCacheSubnetGroups: expected 1 group, got %d", len(groups))
	}
	sg, _ := groups[0].(map[string]any)
	if sg["CacheSubnetGroupName"] != name {
		return fmt.Errorf("elasticache DescribeCacheSubnetGroups: expected CacheSubnetGroupName %q, got %v", name, sg["CacheSubnetGroupName"])
	}
	return nil
}

func (g *elasticacheGroup) teardownSubnetGroups(_ context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_sngrpName", "_sngrpDescName"} {
		if name := t.GetString(key); name != "" {
			awscli.Run(t.Endpoint, t.Region, "elasticache", "delete-cache-subnet-group", "--cache-subnet-group-name", name) //nolint:errcheck
		}
	}
	return nil
}

// ─── elasticache-parameter-groups ─────────────────────────────────────────────

func (g *elasticacheGroup) setupParameterGroups(_ context.Context, _ *harness.TestContext) error {
	return nil
}

func (g *elasticacheGroup) CreateCacheParameterGroup(_ context.Context, t *harness.TestContext) error {
	name := g.uniqueName("pg")
	t.Set("_pgName", name)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "create-cache-parameter-group",
		"--cache-parameter-group-name", name,
		"--cache-parameter-group-family", "redis7",
		"--description", "compat test group",
	)
	if err != nil {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: create failed: %w", err)
	}
	pg, _ := out["CacheParameterGroup"].(map[string]any)
	if pg == nil {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: no CacheParameterGroup in response")
	}
	if pg["CacheParameterGroupName"] != name {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: expected CacheParameterGroupName %q, got %v", name, pg["CacheParameterGroupName"])
	}
	if pg["CacheParameterGroupFamily"] != "redis7" {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: expected CacheParameterGroupFamily 'redis7', got %v", pg["CacheParameterGroupFamily"])
	}
	arn, _ := pg["ARN"].(string)
	if arn == "" {
		return fmt.Errorf("elasticache CreateCacheParameterGroup: ARN is empty")
	}
	return nil
}

func (g *elasticacheGroup) DescribeCacheParameterGroups(_ context.Context, t *harness.TestContext) error {
	name := g.uniqueName("pg-desc")
	t.Set("_pgDescName", name)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-cache-parameter-group",
		"--cache-parameter-group-name", name,
		"--cache-parameter-group-family", "redis7",
		"--description", "describe test",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "describe-cache-parameter-groups",
		"--cache-parameter-group-name", name,
	)
	if err != nil {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: describe failed: %w", err)
	}
	groups, _ := out["CacheParameterGroups"].([]any)
	if len(groups) != 1 {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: expected 1 group, got %d", len(groups))
	}
	pg, _ := groups[0].(map[string]any)
	if pg["CacheParameterGroupName"] != name {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: expected CacheParameterGroupName %q, got %v", name, pg["CacheParameterGroupName"])
	}
	if pg["CacheParameterGroupFamily"] != "redis7" {
		return fmt.Errorf("elasticache DescribeCacheParameterGroups: expected CacheParameterGroupFamily 'redis7', got %v", pg["CacheParameterGroupFamily"])
	}
	return nil
}

func (g *elasticacheGroup) DeleteCacheParameterGroup(_ context.Context, t *harness.TestContext) error {
	name := g.uniqueName("pg-del")
	t.Set("_pgDelName", name)
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "create-cache-parameter-group",
		"--cache-parameter-group-name", name,
		"--cache-parameter-group-family", "redis7",
		"--description", "delete test",
	); err != nil {
		return err
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"elasticache", "delete-cache-parameter-group",
		"--cache-parameter-group-name", name,
	); err != nil {
		return fmt.Errorf("elasticache DeleteCacheParameterGroup: delete failed: %w", err)
	}
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"elasticache", "describe-cache-parameter-groups",
		"--cache-parameter-group-name", name,
	)
	if err == nil {
		return fmt.Errorf("elasticache DeleteCacheParameterGroup: parameter group still exists after delete")
	}
	return nil
}

func (g *elasticacheGroup) teardownParameterGroups(_ context.Context, t *harness.TestContext) error {
	for _, key := range []string{"_pgName", "_pgDescName", "_pgDelName"} {
		if name := t.GetString(key); name != "" {
			awscli.Run(t.Endpoint, t.Region, "elasticache", "delete-cache-parameter-group", "--cache-parameter-group-name", name) //nolint:errcheck
		}
	}
	return nil
}
