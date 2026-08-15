package groups

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// MSK returns the Amazon MSK (Managed Streaming for Apache Kafka) group.
//
// The CLI command is `aws kafka`, not `aws msk` — the model's service id is
// "kafka" and that is what every URI below is read from.
//
// MSK is restJson1 with no target prefix, so every operation is reached by
// method and path alone, and the paths are unusually varied for one service:
// the v1 surface lives under /v1, the v2 cluster surface under **/api/v2** (not
// /v2), tagging under /v1/tags/{ResourceArn} — a prefix shared with other
// taggable services — and several operations hang a sub-resource off an ARN
// that is itself percent-encoded into a single path segment
// (/v1/clusters/{ClusterArn}/nodes). That is the family of bug issue #963
// caught in Backup, so every collection operation here is driven through the
// AWS CLI: the CLI takes the URI from the pinned model, and a route bound at a
// spelling no client sends fails here and nowhere else.
//
// The V1/V2 pairs are the point of the coverage rather than a duplicate of it.
// ListClusters and ListClustersV2, DescribeCluster and DescribeClusterV2,
// ListClusterOperations and ListClusterOperationsV2 are separate bindings under
// different prefixes, and a service can implement one and not the other — so
// each is exercised on its own.
//
// Error statuses are pinned as well as error codes. The kafka model declares
// NotFoundException at HTTP 404, ConflictException at 409 and
// BadRequestException at 400; it declares no ValidationException at all, which
// is why a malformed MSK request is a BadRequestException. awscli.RunStatus
// reads the status out of the CLI's own --debug wire log, the only place a
// CLI-driven test can see it: the CLI prints a modeled error's code and never
// its status.
func MSK() ServiceGroup {
	g := &mskGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// msk-clusters — the v1 cluster surface under /v1.
			"msk-clusters:ListKafkaVersions":                              g.ListKafkaVersions,
			"msk-clusters:CreateCluster":                                  g.CreateCluster,
			"msk-clusters:DescribeCluster":                                g.DescribeCluster,
			"msk-clusters:ListClusters":                                   g.ListClusters,
			"msk-clusters:ListClustersByNameFilter":                       g.ListClustersByNameFilter,
			"msk-clusters:GetBootstrapBrokers":                            g.GetBootstrapBrokers,
			"msk-clusters:ListNodes":                                      g.ListNodes,
			"msk-clusters:ListClusterOperations":                          g.ListClusterOperations,
			"msk-clusters:UpdateClusterConfiguration":                     g.UpdateClusterConfiguration,
			"msk-clusters:UpdateClusterConfigurationStaleVersion":         g.UpdateClusterConfigurationStaleVersion,
			"msk-clusters:UpdateClusterConfigurationUnknownConfiguration": g.UpdateClusterConfigurationUnknownConfiguration,
			"msk-clusters:DescribeClusterNotFound":                        g.DescribeClusterNotFound,
			"msk-clusters:GetBootstrapBrokersNotFound":                    g.GetBootstrapBrokersNotFound,
			"msk-clusters:CreateClusterConflict":                          g.CreateClusterConflict,
			"msk-clusters:DeleteCluster":                                  g.DeleteCluster,
			"msk-clusters:DeleteClusterNotFound":                          g.DeleteClusterNotFound,
			// msk-clusters-v2 — the v2 cluster surface under /api/v2.
			"msk-clusters-v2:CreateClusterV2Serverless":    g.CreateClusterV2Serverless,
			"msk-clusters-v2:CreateClusterV2Provisioned":   g.CreateClusterV2Provisioned,
			"msk-clusters-v2:CreateClusterV2NeitherType":   g.CreateClusterV2NeitherType,
			"msk-clusters-v2:DescribeClusterV2":            g.DescribeClusterV2,
			"msk-clusters-v2:DescribeClusterV2Provisioned": g.DescribeClusterV2Provisioned,
			"msk-clusters-v2:ListClustersV2":               g.ListClustersV2,
			"msk-clusters-v2:ListClustersV2ByClusterType":  g.ListClustersV2ByClusterType,
			"msk-clusters-v2:ListClustersV2Paginated":      g.ListClustersV2Paginated,
			"msk-clusters-v2:ListClusterOperationsV2":      g.ListClusterOperationsV2,
			"msk-clusters-v2:DescribeClusterV2NotFound":    g.DescribeClusterV2NotFound,
			// msk-configurations
			"msk-configurations:CreateConfiguration":           g.CreateConfiguration,
			"msk-configurations:DescribeConfiguration":         g.DescribeConfiguration,
			"msk-configurations:ListConfigurations":            g.ListConfigurations,
			"msk-configurations:ListConfigurationRevisions":    g.ListConfigurationRevisions,
			"msk-configurations:CreateConfigurationConflict":   g.CreateConfigurationConflict,
			"msk-configurations:DescribeConfigurationNotFound": g.DescribeConfigurationNotFound,
			"msk-configurations:DeleteConfiguration":           g.DeleteConfiguration,
			"msk-configurations:DeleteConfigurationNotFound":   g.DeleteConfigurationNotFound,
			// msk-tags — /v1/tags/{ResourceArn}. Registered group-qualified
			// because several services model these operation names; a bare key
			// would be ambiguous and the loader refuses it.
			"msk-tags:TagResource":             g.TagResource,
			"msk-tags:ListTagsForResource":     g.ListTagsForResource,
			"msk-tags:UntagResource":           g.UntagResource,
			"msk-tags:CreateClusterV2WithTags": g.CreateClusterV2WithTags,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"msk-clusters":       g.setupClusters,
			"msk-clusters-v2":    g.setupClustersV2,
			"msk-configurations": g.setupConfigurations,
			"msk-tags":           g.setupTags,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"msk-clusters":       g.teardownClusters,
			"msk-clusters-v2":    g.teardownClustersV2,
			"msk-configurations": g.teardownConfigurations,
			"msk-tags":           g.teardownTags,
		},
	}
}

type mskGroup struct{}

// Each group owns its own clusters and configurations, named exactly and torn
// down by name, so two groups never contend and nothing outside the run is
// touched. An MSK cluster name is 1–64 characters of [0-9A-Za-z][0-9A-Za-z-]*,
// which "{runID}-{tag}" satisfies for every run ID the runner mints.
var (
	mskClusterNamer       = harness.NewNamer("mskc")
	mskDeleteClusterNamer = harness.NewNamer("mskdel")
	mskServerlessNamer    = harness.NewNamer("mskv2sl")
	mskProvisionedNamer   = harness.NewNamer("mskv2pv")
	mskConfigNamer        = harness.NewNamer("mskcfg")
	mskDeleteConfigNamer  = harness.NewNamer("mskcfgdel")
	mskUpdateConfigNamer  = harness.NewNamer("mskupcfg")
	mskTagClusterNamer    = harness.NewNamer("msktag")
	mskInlineTagNamer     = harness.NewNamer("msktagc")
)

const (
	// Pinned rather than defaulted so DescribeCluster has something the create
	// request actually asked for to assert against.
	mskKafkaVersion = "3.5.1"
	mskBrokerNodes  = "3"
	mskInstanceType = "kafka.m5.large"
	// AWS requires a provisioned cluster's brokers to be spread over at least
	// two client subnets, so a realistic request names two.
	mskBrokerNodeGroupInfo = `{"InstanceType":"kafka.m5.large","ClientSubnets":["subnet-0a1b2c3d","subnet-4e5f6a7b"]}`
	// The serverless request shape: VpcConfigs is required on AWS.
	mskServerlessConfig = `{"VpcConfigs":[{"SubnetIds":["subnet-0a1b2c3d","subnet-4e5f6a7b"]}]}`
)

// ─── shared helpers ───────────────────────────────────────────────────────────

// absentClusterARN returns a syntactically valid MSK cluster ARN in the run's
// region that names nothing. A real ARN is used rather than a bare name because
// ClusterArn is an httpLabel: the CLI percent-encodes it into a single path
// segment, and a malformed one would test the CLI rather than the emulator.
func absentClusterARN(t *harness.TestContext, tag string) string {
	return fmt.Sprintf("arn:aws:kafka:%s:000000000000:cluster/%s-%s-absent/00000000-0000-0000-0000-000000000000",
		t.Region, t.RunID, tag)
}

// absentConfigurationARN is absentClusterARN for the configuration resource.
func absentConfigurationARN(t *harness.TestContext, tag string) string {
	return fmt.Sprintf("arn:aws:kafka:%s:000000000000:configuration/%s-%s-absent/00000000-0000-0000-0000-000000000000",
		t.Region, t.RunID, tag)
}

// assertKafkaError asserts the CLI reported the named modeled error code and
// that it arrived with the HTTP status the kafka model declares for it.
//
// Both halves matter. The code alone passes against a service answering the
// right name with the wrong status, and the status alone passes against a
// wildcard route from another service that happens to answer 404 — which is
// exactly what an unbound MSK path does here, coming back as an S3 NoSuchKey.
func assertKafkaError(status int, err error, op, wantCode string, wantStatus int) error {
	if err == nil {
		return fmt.Errorf("kafka %s: expected %s, got a successful response", op, wantCode)
	}
	if !strings.Contains(err.Error(), wantCode) {
		return fmt.Errorf("kafka %s: expected error code %s, got: %v", op, wantCode, err)
	}
	if status != wantStatus {
		return fmt.Errorf("kafka %s: %s arrived with HTTP %d, want HTTP %d — the kafka model declares this status",
			op, wantCode, status, wantStatus)
	}
	return nil
}

// createProvisionedCluster creates a v1 (always provisioned) cluster and
// returns the create response.
func (g *mskGroup) createProvisionedCluster(t *harness.TestContext, name string, extra ...string) (map[string]any, error) {
	args := append([]string{
		"kafka", "create-cluster",
		"--cluster-name", name,
		"--kafka-version", mskKafkaVersion,
		"--number-of-broker-nodes", mskBrokerNodes,
		"--broker-node-group-info", mskBrokerNodeGroupInfo,
	}, extra...)
	return awscli.RunOutput(t.Endpoint, t.Region, args...)
}

// deleteClustersNamed removes every cluster whose ClusterName is exactly one of
// names, in the order given. Best effort: each delete is independent, so one
// failure does not stop the rest.
//
// Teardown works from names rather than from recorded ARNs because two tests
// here expect a create to be rejected, and when it is not the cluster it left
// behind has an ARN no test captured. The match is an exact string comparison
// against names this group minted from its own namers, so nothing another group
// or another concurrent run owns can be caught by it.
func (g *mskGroup) deleteClustersNamed(t *harness.TestContext, names ...string) {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kafka", "list-clusters")
	if err != nil {
		return
	}
	entries, _ := out["ClusterInfoList"].([]any)
	for _, want := range names {
		for _, e := range entries {
			info, _ := e.(map[string]any)
			arn, _ := info["ClusterArn"].(string)
			if got, _ := info["ClusterName"].(string); got != want || arn == "" {
				continue
			}
			awscli.Run(t.Endpoint, t.Region, "kafka", "delete-cluster", "--cluster-arn", arn) //nolint:errcheck
		}
	}
}

// deleteConfigurationsNamed is deleteClustersNamed for configurations.
// ListConfigurations models no name filter, so the whole collection is read and
// matched exactly.
func (g *mskGroup) deleteConfigurationsNamed(t *harness.TestContext, names ...string) {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kafka", "list-configurations")
	if err != nil {
		return
	}
	entries, _ := out["Configurations"].([]any)
	for _, want := range names {
		for _, e := range entries {
			info, _ := e.(map[string]any)
			arn, _ := info["Arn"].(string)
			if got, _ := info["Name"].(string); got != want || arn == "" {
				continue
			}
			awscli.Run(t.Endpoint, t.Region, "kafka", "delete-configuration", "--arn", arn) //nolint:errcheck
		}
	}
}

// createConfiguration creates a configuration. serverProperties is a required
// blob member, so it travels as a file the CLI reads with fileb:// — the same
// route a real caller uses, and the only one the CLI accepts.
func (g *mskGroup) createConfiguration(t *harness.TestContext, name, description string) (map[string]any, error) {
	path, cleanup, err := writeTempFile("oc-msk-server-properties-*.properties",
		"auto.create.topics.enable=true\nnum.partitions=1\n")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "create-configuration",
		"--name", name,
		"--description", description,
		"--kafka-versions", mskKafkaVersion, "3.6.0",
		"--server-properties", "fileb://"+filepath.ToSlash(path),
	)
}

// currentVersionOf reads a cluster's currentVersion back through
// DescribeCluster.
//
// Every mutating cluster operation carries the version it expects to be acting
// on and the server mints a new one on success, so a test that reuses a stale
// value fails on the second mutation — the same shape as WAF's LockToken. Each
// mutation therefore fetches a fresh one rather than caching.
func (g *mskGroup) currentVersionOf(t *harness.TestContext, clusterARN string) (string, error) {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-cluster", "--cluster-arn", clusterARN)
	if err != nil {
		return "", err
	}
	info, _ := out["ClusterInfo"].(map[string]any)
	version, _ := info["CurrentVersion"].(string)
	if version == "" {
		return "", fmt.Errorf("kafka describe-cluster: no CurrentVersion on %s: %v", clusterARN, out)
	}
	return version, nil
}

// clusterNames returns the ClusterName of every cluster ListClusters reports,
// optionally filtered by the modeled clusterNameFilter query parameter.
func (g *mskGroup) clusterNames(t *harness.TestContext, nameFilter string) ([]string, error) {
	args := []string{"kafka", "list-clusters"}
	if nameFilter != "" {
		args = append(args, "--cluster-name-filter", nameFilter)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, err
	}
	entries, ok := out["ClusterInfoList"].([]any)
	if !ok {
		return nil, fmt.Errorf("kafka list-clusters: ClusterInfoList is %T, want a list: %v", out["ClusterInfoList"], out)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		info, _ := e.(map[string]any)
		name, _ := info["ClusterName"].(string)
		if name == "" {
			return nil, fmt.Errorf("kafka list-clusters: entry with no ClusterName: %v", info)
		}
		names = append(names, name)
	}
	return names, nil
}

// clustersV2 returns ListClustersV2's entries keyed by cluster name, with the
// modeled query parameters applied.
func (g *mskGroup) clustersV2(t *harness.TestContext, nameFilter, typeFilter string) (map[string]map[string]any, error) {
	args := []string{"kafka", "list-clusters-v2"}
	if nameFilter != "" {
		args = append(args, "--cluster-name-filter", nameFilter)
	}
	if typeFilter != "" {
		args = append(args, "--cluster-type-filter", typeFilter)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, err
	}
	entries, ok := out["ClusterInfoList"].([]any)
	if !ok {
		return nil, fmt.Errorf("kafka list-clusters-v2: ClusterInfoList is %T, want a list: %v", out["ClusterInfoList"], out)
	}
	byName := make(map[string]map[string]any, len(entries))
	for _, e := range entries {
		info, _ := e.(map[string]any)
		name, _ := info["ClusterName"].(string)
		if name == "" {
			return nil, fmt.Errorf("kafka list-clusters-v2: entry with no ClusterName: %v", info)
		}
		byName[name] = info
	}
	return byName, nil
}

// tagsOf returns a resource's tags through ListTagsForResource.
func (g *mskGroup) tagsOf(t *harness.TestContext, arn string) (map[string]string, error) {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "list-tags-for-resource", "--resource-arn", arn)
	if err != nil {
		return nil, err
	}
	raw, ok := out["Tags"].(map[string]any)
	if !ok && out["Tags"] != nil {
		return nil, fmt.Errorf("kafka list-tags-for-resource: Tags is %T, want a map: %v", out["Tags"], out)
	}
	tags := make(map[string]string, len(raw))
	for k, v := range raw {
		tags[k], _ = v.(string)
	}
	return tags, nil
}

// pollClusterGone polls DescribeCluster until it stops answering, bounded and
// with no sleep — each attempt is a fresh CLI invocation, which paces it. AWS
// deletes a cluster asynchronously and reports DELETING while brokers drain, so
// a future Overcast that models the drain must not turn this into a flake.
func (g *mskGroup) pollClusterGone(t *harness.TestContext, arn string) (int, error) {
	const maxPolls = 10
	var status int
	var err error
	for attempt := 0; attempt < maxPolls; attempt++ {
		status, err = awscli.RunStatus(t.Endpoint, t.Region,
			"kafka", "describe-cluster", "--cluster-arn", arn)
		if err != nil {
			return status, err
		}
	}
	return status, nil
}

// ─── msk-clusters ─────────────────────────────────────────────────────────────

func (g *mskGroup) setupClusters(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *mskGroup) teardownClusters(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order: the configuration UpdateClusterConfiguration
	// applied goes after the clusters that reference it. Matching by name also
	// picks up the duplicate CreateClusterConflict leaves behind on a server
	// that accepts a name already in use.
	g.deleteClustersNamed(t, mskDeleteClusterNamer.Name(t), mskClusterNamer.Name(t))
	g.deleteConfigurationsNamed(t, mskUpdateConfigNamer.Name(t))
	return nil
}

// ListKafkaVersions is a collection with no resource behind it, at
// GET /v1/kafka-versions — a sibling of /v1/clusters rather than something
// under it, and the kind of standalone collection URI #963 was about.
func (g *mskGroup) ListKafkaVersions(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region, "kafka", "list-kafka-versions")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListKafkaVersions: the collection URI answered HTTP %d, want 200 — the URI the kafka model binds is not being served", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kafka", "list-kafka-versions")
	if err != nil {
		return err
	}
	entries, ok := out["KafkaVersions"].([]any)
	if !ok {
		return fmt.Errorf("kafka ListKafkaVersions: KafkaVersions is %T, want a list: %v", out["KafkaVersions"], out)
	}
	if len(entries) == 0 {
		return fmt.Errorf("kafka ListKafkaVersions: no versions returned")
	}
	// The version this group's clusters ask for must be one a caller could have
	// discovered here; otherwise the two answers disagree about the same fact.
	var versions []string
	for _, e := range entries {
		info, _ := e.(map[string]any)
		version, _ := info["Version"].(string)
		if version == "" {
			return fmt.Errorf("kafka ListKafkaVersions: entry with no Version: %v", info)
		}
		if _, ok := info["Status"].(string); !ok {
			return fmt.Errorf("kafka ListKafkaVersions: entry %q has no Status: %v", version, info)
		}
		versions = append(versions, version)
	}
	if !containsString(versions, mskKafkaVersion) {
		return fmt.Errorf("kafka ListKafkaVersions: %q missing from %v, but a cluster can be created with it", mskKafkaVersion, versions)
	}
	return nil
}

// CreateCluster creates the provisioned cluster the rest of the group reads.
func (g *mskGroup) CreateCluster(_ context.Context, t *harness.TestContext) error {
	name := mskClusterNamer.Name(t)
	out, err := g.createProvisionedCluster(t, name)
	if err != nil {
		return err
	}
	arn, _ := out["ClusterArn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka CreateCluster: ClusterArn is empty: %v", out)
	}
	// Store before asserting: a partially wrong response still leaves a cluster
	// behind, and teardown must be able to find it.
	t.Set("msk_cluster", arn)
	t.Set("msk_cluster_name", name)

	if got, _ := out["ClusterName"].(string); got != name {
		return fmt.Errorf("kafka CreateCluster: ClusterName = %q, want %q", got, name)
	}
	if !strings.Contains(arn, ":cluster/"+name+"/") {
		return fmt.Errorf("kafka CreateCluster: ClusterArn = %q, want one naming :cluster/%s/", arn, name)
	}
	// A provisioned cluster is created asynchronously on AWS, so CREATING is the
	// expected answer; ACTIVE is accepted for an emulator that settles sooner.
	switch state, _ := out["State"].(string); state {
	case "CREATING", "ACTIVE":
	default:
		return fmt.Errorf("kafka CreateCluster: State = %q, want CREATING or ACTIVE", state)
	}

	// Roundtrip: the create must be visible to the matching describe.
	described, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-cluster", "--cluster-arn", arn)
	if err != nil {
		return fmt.Errorf("kafka CreateCluster: describe-cluster after create failed: %w", err)
	}
	info, _ := described["ClusterInfo"].(map[string]any)
	if got, _ := info["ClusterArn"].(string); got != arn {
		return fmt.Errorf("kafka CreateCluster: describe-cluster returned ClusterArn %q, want the created %q", got, arn)
	}
	return nil
}

// DescribeCluster reads the cluster back at GET /v1/clusters/{ClusterArn},
// where the ARN is one percent-encoded path segment, and pins the status the
// CLI's own wire log recorded — which is what proves the request reached MSK
// rather than a wildcard route that happened to answer.
func (g *mskGroup) DescribeCluster(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_cluster")
	if arn == "" {
		return fmt.Errorf("kafka DescribeCluster: no msk_cluster from CreateCluster")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "describe-cluster", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka DescribeCluster: the modeled URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-cluster", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	info, _ := out["ClusterInfo"].(map[string]any)
	if info == nil {
		return fmt.Errorf("kafka DescribeCluster: no ClusterInfo in response: %v", out)
	}
	if got, _ := info["ClusterName"].(string); got != t.GetString("msk_cluster_name") {
		return fmt.Errorf("kafka DescribeCluster: ClusterName = %q, want %q", got, t.GetString("msk_cluster_name"))
	}
	// The kafka model puts the running version at
	// clusterInfo.currentBrokerSoftwareInfo.kafkaVersion. ClusterInfo has no
	// top-level kafkaVersion member, so a server that answers with one has it
	// dropped by any model-driven client and the version the caller asked for
	// is never reported back at all.
	software, _ := info["CurrentBrokerSoftwareInfo"].(map[string]any)
	if got, _ := software["KafkaVersion"].(string); got != mskKafkaVersion {
		return fmt.Errorf("kafka DescribeCluster: CurrentBrokerSoftwareInfo.KafkaVersion = %q, want the requested %q — the model binds no top-level kafkaVersion member, so that is the only place a client can read it",
			got, mskKafkaVersion)
	}
	if got, _ := info["NumberOfBrokerNodes"].(float64); got != 3 {
		return fmt.Errorf("kafka DescribeCluster: NumberOfBrokerNodes = %v, want 3", info["NumberOfBrokerNodes"])
	}
	bng, _ := info["BrokerNodeGroupInfo"].(map[string]any)
	if got, _ := bng["InstanceType"].(string); got != mskInstanceType {
		return fmt.Errorf("kafka DescribeCluster: BrokerNodeGroupInfo.InstanceType = %q, want the requested %q", got, mskInstanceType)
	}
	subnets, _ := bng["ClientSubnets"].([]any)
	if len(subnets) != 2 {
		return fmt.Errorf("kafka DescribeCluster: BrokerNodeGroupInfo.ClientSubnets = %v, want the 2 requested", bng["ClientSubnets"])
	}
	return nil
}

// ListClusters is the collection operation AWS binds to GET /v1/clusters. The
// wire status is asserted as well as the body: unsigned, an MSK path Overcast
// has not bound falls through to another service's wildcard and can answer 200
// in a shape the CLI half-parses, which is the second symptom of issue #963.
func (g *mskGroup) ListClusters(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("msk_cluster_name")
	if name == "" {
		return fmt.Errorf("kafka ListClusters: no msk_cluster_name from CreateCluster")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region, "kafka", "list-clusters")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListClusters: the collection URI answered HTTP %d, want 200 — the URI the kafka model binds is not being served", status)
	}

	names, err := g.clusterNames(t, "")
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("kafka ListClusters: no clusters listed, want at least %q", name)
	}
	if !containsString(names, name) {
		return fmt.Errorf("kafka ListClusters: %q missing from %v", name, names)
	}
	return nil
}

// ListClustersByNameFilter exercises the modeled clusterNameFilter query
// parameter, which AWS documents as a prefix match. ListClusters is a GET with
// no body, so the filter can only travel in the query string — a service that
// reads it from anywhere else answers with the clusters it was asked to exclude.
func (g *mskGroup) ListClustersByNameFilter(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("msk_cluster_name")
	if name == "" {
		return fmt.Errorf("kafka ListClustersByNameFilter: no msk_cluster_name from CreateCluster")
	}

	matching, err := g.clusterNames(t, name)
	if err != nil {
		return err
	}
	if !containsString(matching, name) {
		return fmt.Errorf("kafka list-clusters --cluster-name-filter %s: %q missing from %v", name, name, matching)
	}

	// A prefix that matches nothing must come back empty rather than unfiltered.
	other, err := g.clusterNames(t, name+"-no-such-suffix")
	if err != nil {
		return err
	}
	if len(other) != 0 {
		return fmt.Errorf("kafka list-clusters --cluster-name-filter %s-no-such-suffix: returned %v, want none — the filter is not being applied",
			name, other)
	}
	return nil
}

// GetBootstrapBrokers reads the cluster's client endpoint from
// GET /v1/clusters/{ClusterArn}/bootstrap-brokers — an ARN with a sub-resource
// hung off it, which is the URI shape most easily bound wrong.
//
// The broker string itself is not asserted non-empty: a provisioned cluster is
// still CREATING at this point on AWS and on Overcast alike, and AWS answers
// with no endpoint until the brokers exist. What is asserted is that the URI is
// MSK's and answers in MSK's shape, and that a value, when present, is a
// dialable host:port rather than a hostname alone.
func (g *mskGroup) GetBootstrapBrokers(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_cluster")
	if arn == "" {
		return fmt.Errorf("kafka GetBootstrapBrokers: no msk_cluster from CreateCluster")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "get-bootstrap-brokers", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka GetBootstrapBrokers: the modeled URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "get-bootstrap-brokers", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	brokers, present := out["BootstrapBrokerString"]
	if !present {
		return fmt.Errorf("kafka GetBootstrapBrokers: no BootstrapBrokerString in response: %v", out)
	}
	s, ok := brokers.(string)
	if !ok {
		return fmt.Errorf("kafka GetBootstrapBrokers: BootstrapBrokerString is %T, want a string: %v", brokers, out)
	}
	if s != "" && !strings.Contains(s, ":") {
		return fmt.Errorf("kafka GetBootstrapBrokers: BootstrapBrokerString = %q, want host:port", s)
	}
	return nil
}

// ListNodes is a collection hung off the cluster ARN at
// GET /v1/clusters/{ClusterArn}/nodes.
//
// It is the exact shape #963 was about: a route that matches the ARN but not
// the sub-resource swallows "/nodes" into the ARN and answers a question nobody
// asked. The assertion is deliberately the modeled contract — HTTP 200 and a
// NodeInfoList — so that an emulator which has not implemented the operation
// reports 501 (recorded as unimplemented, the honest result) rather than a
// plausible-looking not-found.
func (g *mskGroup) ListNodes(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_cluster")
	if arn == "" {
		return fmt.Errorf("kafka ListNodes: no msk_cluster from CreateCluster")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "list-nodes", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListNodes: the nodes collection URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "list-nodes", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if _, ok := out["NodeInfoList"].([]any); !ok {
		return fmt.Errorf("kafka ListNodes: NodeInfoList is %T, want a list: %v", out["NodeInfoList"], out)
	}
	return nil
}

// ListClusterOperations is the v1 half of the operations pair, at
// GET /v1/clusters/{ClusterArn}/operations. Its v2 twin lives under a different
// prefix entirely and is covered in msk-clusters-v2, because a service can
// implement one and not the other.
func (g *mskGroup) ListClusterOperations(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_cluster")
	if arn == "" {
		return fmt.Errorf("kafka ListClusterOperations: no msk_cluster from CreateCluster")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "list-cluster-operations", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListClusterOperations: the operations collection URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "list-cluster-operations", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if _, ok := out["ClusterOperationInfoList"].([]any); !ok {
		return fmt.Errorf("kafka ListClusterOperations: ClusterOperationInfoList is %T, want a list: %v",
			out["ClusterOperationInfoList"], out)
	}
	return nil
}

// UpdateClusterConfiguration is a PUT to
// /v1/clusters/{ClusterArn}/configuration, and the one MSK mutation the CLI
// reaches with an optimistic-concurrency token: currentVersion must be the
// cluster's current one, and a new one is minted on success.
//
// The configuration reference travels as the modeled `configurationInfo`
// object, not as a flat member — a detail only a model-driven client gets
// right, and the reason this test is worth having.
func (g *mskGroup) UpdateClusterConfiguration(_ context.Context, t *harness.TestContext) error {
	clusterARN := t.GetString("msk_cluster")
	if clusterARN == "" {
		return fmt.Errorf("kafka UpdateClusterConfiguration: no msk_cluster from CreateCluster")
	}

	// The configuration this group applies is its own, created here and removed
	// in teardown, so msk-configurations and this group never share one.
	created, err := g.createConfiguration(t, mskUpdateConfigNamer.Name(t), "applied by UpdateClusterConfiguration")
	if err != nil {
		return fmt.Errorf("kafka UpdateClusterConfiguration: create-configuration failed: %w", err)
	}
	configARN, _ := created["Arn"].(string)
	if configARN == "" {
		return fmt.Errorf("kafka UpdateClusterConfiguration: create-configuration returned no Arn: %v", created)
	}
	t.Set("msk_update_config", configARN)

	before, err := g.currentVersionOf(t, clusterARN)
	if err != nil {
		return err
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "update-cluster-configuration",
		"--cluster-arn", clusterARN,
		"--current-version", before,
		"--configuration-info", fmt.Sprintf(`{"Arn":%q,"Revision":1}`, configARN))
	if err != nil {
		return err
	}
	if got, _ := out["ClusterArn"].(string); got != clusterARN {
		return fmt.Errorf("kafka UpdateClusterConfiguration: ClusterArn = %q, want %q", got, clusterARN)
	}
	opARN, _ := out["ClusterOperationArn"].(string)
	if opARN == "" {
		return fmt.Errorf("kafka UpdateClusterConfiguration: ClusterOperationArn is empty: %v", out)
	}
	if !strings.Contains(opARN, ":cluster-operation/") {
		return fmt.Errorf("kafka UpdateClusterConfiguration: ClusterOperationArn = %q, want one naming :cluster-operation/", opARN)
	}

	// Roundtrip: the mutation must have moved the cluster's version on, or the
	// token means nothing and the next caller's stale value would be accepted.
	after, err := g.currentVersionOf(t, clusterARN)
	if err != nil {
		return err
	}
	if after == before {
		return fmt.Errorf("kafka UpdateClusterConfiguration: CurrentVersion is still %q after the update — the update did not take", before)
	}
	return nil
}

// UpdateClusterConfigurationStaleVersion pins the modeled BadRequestException,
// which the kafka model puts at HTTP 400. MSK declares no ValidationException:
// BadRequestException is the only client-error shape its operations carry for a
// malformed or stale request.
func (g *mskGroup) UpdateClusterConfigurationStaleVersion(_ context.Context, t *harness.TestContext) error {
	clusterARN := t.GetString("msk_cluster")
	if clusterARN == "" {
		return fmt.Errorf("kafka UpdateClusterConfigurationStaleVersion: no msk_cluster from CreateCluster")
	}
	configARN := t.GetString("msk_update_config")
	if configARN == "" {
		return fmt.Errorf("kafka UpdateClusterConfigurationStaleVersion: no msk_update_config from UpdateClusterConfiguration")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "update-cluster-configuration",
		"--cluster-arn", clusterARN,
		"--current-version", "not-this-cluster-s-version",
		"--configuration-info", fmt.Sprintf(`{"Arn":%q,"Revision":1}`, configARN))
	return assertKafkaError(status, err, "UpdateClusterConfiguration", "BadRequestException", 400)
}

// UpdateClusterConfigurationUnknownConfiguration names a configuration that
// does not exist. AWS answers NotFoundException — the configuration is a
// resource the request refers to, and referring to a missing one cannot
// succeed.
//
// This is the test that only a model-driven client can write: the ARN travels
// inside the modeled `configurationInfo` object, so a service reading it from a
// flat `configurationArn` member never sees it and reports success on a
// reference it never resolved.
func (g *mskGroup) UpdateClusterConfigurationUnknownConfiguration(_ context.Context, t *harness.TestContext) error {
	clusterARN := t.GetString("msk_cluster")
	if clusterARN == "" {
		return fmt.Errorf("kafka UpdateClusterConfigurationUnknownConfiguration: no msk_cluster from CreateCluster")
	}
	version, err := g.currentVersionOf(t, clusterARN)
	if err != nil {
		return err
	}
	status, callErr := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "update-cluster-configuration",
		"--cluster-arn", clusterARN,
		"--current-version", version,
		"--configuration-info", fmt.Sprintf(`{"Arn":%q,"Revision":1}`, absentConfigurationARN(t, "upd")))
	return assertKafkaError(status, callErr, "UpdateClusterConfiguration", "NotFoundException", 404)
}

// DescribeClusterNotFound pins NotFoundException at HTTP 404, the status the
// kafka model declares — unlike OpenSearch, whose not-found is a 409.
func (g *mskGroup) DescribeClusterNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "describe-cluster", "--cluster-arn", absentClusterARN(t, "desc"))
	return assertKafkaError(status, err, "DescribeCluster", "NotFoundException", 404)
}

// GetBootstrapBrokersNotFound pins the not-found path on the sub-resource URI
// too. It is a different binding from DescribeCluster's and can be bound
// independently, so a service that answers one correctly proves nothing about
// the other.
func (g *mskGroup) GetBootstrapBrokersNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "get-bootstrap-brokers", "--cluster-arn", absentClusterARN(t, "brokers"))
	return assertKafkaError(status, err, "GetBootstrapBrokers", "NotFoundException", 404)
}

// CreateClusterConflict pins ConflictException at HTTP 409. AWS documents MSK
// as rejecting a cluster name already in use outright — "this cluster name
// already exists" — rather than minting a second cluster beside the first.
//
// The request is made once, through RunStatus, because a second attempt would
// leave a second unwanted cluster behind. A server that accepts it leaves one
// sharing the group's own cluster name, which teardown removes by name.
func (g *mskGroup) CreateClusterConflict(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("msk_cluster_name")
	if name == "" {
		return fmt.Errorf("kafka CreateClusterConflict: no msk_cluster_name from CreateCluster")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "create-cluster",
		"--cluster-name", name,
		"--kafka-version", mskKafkaVersion,
		"--number-of-broker-nodes", mskBrokerNodes,
		"--broker-node-group-info", mskBrokerNodeGroupInfo)
	return assertKafkaError(status, err, "CreateCluster", "ConflictException", 409)
}

// DeleteCluster deletes a cluster of its own rather than the one the rest of
// the group depends on, then polls until it is gone.
func (g *mskGroup) DeleteCluster(_ context.Context, t *harness.TestContext) error {
	name := mskDeleteClusterNamer.Name(t)
	created, err := g.createProvisionedCluster(t, name)
	if err != nil {
		return fmt.Errorf("kafka DeleteCluster: create-cluster failed: %w", err)
	}
	arn, _ := created["ClusterArn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka DeleteCluster: create-cluster returned no ClusterArn: %v", created)
	}
	t.Set("msk_delete_cluster", arn)

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "delete-cluster", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if got, _ := out["ClusterArn"].(string); got != arn {
		return fmt.Errorf("kafka DeleteCluster: ClusterArn = %q, want %q", got, arn)
	}
	if got, _ := out["State"].(string); got != "DELETING" {
		return fmt.Errorf("kafka DeleteCluster: State = %q, want DELETING", got)
	}

	status, describeErr := g.pollClusterGone(t, arn)
	if describeErr == nil {
		return fmt.Errorf("kafka DeleteCluster: describe-cluster still answers for %q after the delete", arn)
	}
	if err := assertKafkaError(status, describeErr, "DescribeCluster after delete", "NotFoundException", 404); err != nil {
		return err
	}

	names, err := g.clusterNames(t, "")
	if err != nil {
		return fmt.Errorf("kafka DeleteCluster: list-clusters after delete failed: %w", err)
	}
	if containsString(names, name) {
		return fmt.Errorf("kafka DeleteCluster: %q is still listed after the delete", name)
	}
	return nil
}

// DeleteClusterNotFound pins the not-found status on the delete path, which is
// a different handler from DescribeCluster's.
func (g *mskGroup) DeleteClusterNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "delete-cluster", "--cluster-arn", absentClusterARN(t, "del"))
	return assertKafkaError(status, err, "DeleteCluster", "NotFoundException", 404)
}

// ─── msk-clusters-v2 ──────────────────────────────────────────────────────────

func (g *mskGroup) setupClustersV2(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *mskGroup) teardownClustersV2(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order. DeleteCluster (v1) is the only delete the kafka
	// model has — there is no DeleteClusterV2 — so a v2-created cluster is
	// removed through it. The "-neither" name covers the cluster
	// CreateClusterV2NeitherType leaves behind on a server that defaults the
	// union instead of rejecting the request.
	g.deleteClustersNamed(t,
		mskProvisionedNamer.Name(t)+"-neither",
		mskProvisionedNamer.Name(t),
		mskServerlessNamer.Name(t))
	return nil
}

// CreateClusterV2Serverless creates a serverless cluster at
// POST /api/v2/clusters — note **/api/v2**, not /v2, which is what the kafka
// model binds and what nothing but a model-driven client would send.
func (g *mskGroup) CreateClusterV2Serverless(_ context.Context, t *harness.TestContext) error {
	name := mskServerlessNamer.Name(t)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "create-cluster-v2",
		"--cluster-name", name,
		"--serverless", mskServerlessConfig)
	if err != nil {
		return err
	}
	arn, _ := out["ClusterArn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka CreateClusterV2 (serverless): ClusterArn is empty: %v", out)
	}
	t.Set("msk_v2_serverless", arn)
	t.Set("msk_v2_serverless_name", name)

	if got, _ := out["ClusterName"].(string); got != name {
		return fmt.Errorf("kafka CreateClusterV2 (serverless): ClusterName = %q, want %q", got, name)
	}
	if got, _ := out["ClusterType"].(string); got != "SERVERLESS" {
		return fmt.Errorf("kafka CreateClusterV2 (serverless): ClusterType = %q, want SERVERLESS", got)
	}

	// Roundtrip through the v2 describe, which is a separate binding from the
	// v1 one and the only place the v2 response shape is visible.
	described, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-cluster-v2", "--cluster-arn", arn)
	if err != nil {
		return fmt.Errorf("kafka CreateClusterV2 (serverless): describe-cluster-v2 after create failed: %w", err)
	}
	info, _ := described["ClusterInfo"].(map[string]any)
	if got, _ := info["ClusterArn"].(string); got != arn {
		return fmt.Errorf("kafka CreateClusterV2 (serverless): describe returned ClusterArn %q, want the created %q", got, arn)
	}
	return nil
}

// CreateClusterV2Provisioned creates the other arm of the same operation. The
// two request shapes are mutually exclusive members of one union and take
// different code paths, so one passing says nothing about the other.
func (g *mskGroup) CreateClusterV2Provisioned(_ context.Context, t *harness.TestContext) error {
	name := mskProvisionedNamer.Name(t)
	provisioned := fmt.Sprintf(`{"BrokerNodeGroupInfo":%s,"KafkaVersion":%q,"NumberOfBrokerNodes":3}`,
		mskBrokerNodeGroupInfo, mskKafkaVersion)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "create-cluster-v2",
		"--cluster-name", name,
		"--provisioned", provisioned)
	if err != nil {
		return err
	}
	arn, _ := out["ClusterArn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka CreateClusterV2 (provisioned): ClusterArn is empty: %v", out)
	}
	t.Set("msk_v2_provisioned", arn)
	t.Set("msk_v2_provisioned_name", name)

	if got, _ := out["ClusterName"].(string); got != name {
		return fmt.Errorf("kafka CreateClusterV2 (provisioned): ClusterName = %q, want %q", got, name)
	}
	if got, _ := out["ClusterType"].(string); got != "PROVISIONED" {
		return fmt.Errorf("kafka CreateClusterV2 (provisioned): ClusterType = %q, want PROVISIONED", got)
	}
	switch state, _ := out["State"].(string); state {
	case "CREATING", "ACTIVE":
	default:
		return fmt.Errorf("kafka CreateClusterV2 (provisioned): State = %q, want CREATING or ACTIVE", state)
	}
	return nil
}

// CreateClusterV2NeitherType names neither arm of the union. AWS rejects it
// with BadRequestException — the shape is required — and an emulator that
// defaults instead invents a cluster the caller never asked for.
func (g *mskGroup) CreateClusterV2NeitherType(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "create-cluster-v2",
		"--cluster-name", mskProvisionedNamer.Name(t)+"-neither")
	return assertKafkaError(status, err, "CreateClusterV2", "BadRequestException", 400)
}

// DescribeClusterV2 reads a serverless cluster back at
// GET /api/v2/clusters/{ClusterArn} and asserts the v2 response shape, which
// differs from v1's: a clusterType discriminator and a `serverless` or
// `provisioned` sub-object rather than flat broker members.
func (g *mskGroup) DescribeClusterV2(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_v2_serverless")
	if arn == "" {
		return fmt.Errorf("kafka DescribeClusterV2: no msk_v2_serverless from CreateClusterV2Serverless")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "describe-cluster-v2", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka DescribeClusterV2: the modeled URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-cluster-v2", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	info, _ := out["ClusterInfo"].(map[string]any)
	if info == nil {
		return fmt.Errorf("kafka DescribeClusterV2: no ClusterInfo in response: %v", out)
	}
	if got, _ := info["ClusterName"].(string); got != t.GetString("msk_v2_serverless_name") {
		return fmt.Errorf("kafka DescribeClusterV2: ClusterName = %q, want %q", got, t.GetString("msk_v2_serverless_name"))
	}
	if got, _ := info["ClusterType"].(string); got != "SERVERLESS" {
		return fmt.Errorf("kafka DescribeClusterV2: ClusterType = %q, want SERVERLESS", got)
	}
	if _, ok := info["Serverless"].(map[string]any); !ok {
		return fmt.Errorf("kafka DescribeClusterV2: no Serverless sub-object on a SERVERLESS cluster: %v", info)
	}
	// A serverless cluster has no brokers to provision, so it is ACTIVE at once.
	if got, _ := info["State"].(string); got != "ACTIVE" {
		return fmt.Errorf("kafka DescribeClusterV2: State = %q, want ACTIVE — a serverless cluster has no brokers to wait for", got)
	}
	return nil
}

// DescribeClusterV2Provisioned is the other arm: the same binding must render a
// provisioned cluster with the `provisioned` sub-object and the broker members
// nested inside it, not at the top level as v1 has them.
func (g *mskGroup) DescribeClusterV2Provisioned(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_v2_provisioned")
	if arn == "" {
		return fmt.Errorf("kafka DescribeClusterV2Provisioned: no msk_v2_provisioned from CreateClusterV2Provisioned")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-cluster-v2", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	info, _ := out["ClusterInfo"].(map[string]any)
	if info == nil {
		return fmt.Errorf("kafka DescribeClusterV2Provisioned: no ClusterInfo in response: %v", out)
	}
	if got, _ := info["ClusterType"].(string); got != "PROVISIONED" {
		return fmt.Errorf("kafka DescribeClusterV2Provisioned: ClusterType = %q, want PROVISIONED", got)
	}
	provisioned, _ := info["Provisioned"].(map[string]any)
	if provisioned == nil {
		return fmt.Errorf("kafka DescribeClusterV2Provisioned: no Provisioned sub-object on a PROVISIONED cluster: %v", info)
	}
	if got, _ := provisioned["NumberOfBrokerNodes"].(float64); got != 3 {
		return fmt.Errorf("kafka DescribeClusterV2Provisioned: Provisioned.NumberOfBrokerNodes = %v, want the requested 3",
			provisioned["NumberOfBrokerNodes"])
	}
	software, _ := provisioned["CurrentBrokerSoftwareInfo"].(map[string]any)
	if got, _ := software["KafkaVersion"].(string); got != mskKafkaVersion {
		return fmt.Errorf("kafka DescribeClusterV2Provisioned: Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion = %q, want the requested %q",
			got, mskKafkaVersion)
	}
	return nil
}

// ListClustersV2 is the v2 collection, GET /api/v2/clusters. It is a separate
// binding from ListClusters under a different prefix, so both are exercised.
func (g *mskGroup) ListClustersV2(_ context.Context, t *harness.TestContext) error {
	serverless := t.GetString("msk_v2_serverless_name")
	provisioned := t.GetString("msk_v2_provisioned_name")
	if serverless == "" || provisioned == "" {
		return fmt.Errorf("kafka ListClustersV2: no clusters from the CreateClusterV2 tests")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region, "kafka", "list-clusters-v2")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListClustersV2: the collection URI answered HTTP %d, want 200 — /api/v2/clusters is what the kafka model binds", status)
	}

	byName, err := g.clustersV2(t, "", "")
	if err != nil {
		return err
	}
	for _, want := range []string{serverless, provisioned} {
		if _, ok := byName[want]; !ok {
			names := make([]string, 0, len(byName))
			for n := range byName {
				names = append(names, n)
			}
			return fmt.Errorf("kafka ListClustersV2: %q missing from %v", want, names)
		}
	}
	if got, _ := byName[serverless]["ClusterType"].(string); got != "SERVERLESS" {
		return fmt.Errorf("kafka ListClustersV2: ClusterType for %q = %q, want SERVERLESS", serverless, got)
	}
	if got, _ := byName[provisioned]["ClusterType"].(string); got != "PROVISIONED" {
		return fmt.Errorf("kafka ListClustersV2: ClusterType for %q = %q, want PROVISIONED", provisioned, got)
	}
	return nil
}

// ListClustersV2ByClusterType exercises the modeled clusterTypeFilter query
// parameter. Every ListClustersV2 input is an httpQuery member — the request
// carries no body at all — so a service reading them from anywhere else answers
// with the clusters it was asked to exclude.
func (g *mskGroup) ListClustersV2ByClusterType(_ context.Context, t *harness.TestContext) error {
	serverless := t.GetString("msk_v2_serverless_name")
	provisioned := t.GetString("msk_v2_provisioned_name")
	if serverless == "" || provisioned == "" {
		return fmt.Errorf("kafka ListClustersV2ByClusterType: no clusters from the CreateClusterV2 tests")
	}

	onlyServerless, err := g.clustersV2(t, "", "SERVERLESS")
	if err != nil {
		return err
	}
	if _, ok := onlyServerless[serverless]; !ok {
		return fmt.Errorf("kafka list-clusters-v2 --cluster-type-filter SERVERLESS: %q missing", serverless)
	}
	if _, ok := onlyServerless[provisioned]; ok {
		return fmt.Errorf("kafka list-clusters-v2 --cluster-type-filter SERVERLESS: %q is PROVISIONED but was returned — the filter is not being applied",
			provisioned)
	}

	onlyProvisioned, err := g.clustersV2(t, "", "PROVISIONED")
	if err != nil {
		return err
	}
	if _, ok := onlyProvisioned[provisioned]; !ok {
		return fmt.Errorf("kafka list-clusters-v2 --cluster-type-filter PROVISIONED: %q missing", provisioned)
	}
	if _, ok := onlyProvisioned[serverless]; ok {
		return fmt.Errorf("kafka list-clusters-v2 --cluster-type-filter PROVISIONED: %q is SERVERLESS but was returned — the filter is not being applied",
			serverless)
	}
	return nil
}

// ListClustersV2Paginated walks the collection one entry at a time through the
// modeled maxResults and nextToken query parameters, and asserts the walk
// reaches both of this group's clusters. A service that ignores maxResults
// returns everything at once and never issues a token, which this catches.
func (g *mskGroup) ListClustersV2Paginated(_ context.Context, t *harness.TestContext) error {
	serverless := t.GetString("msk_v2_serverless_name")
	provisioned := t.GetString("msk_v2_provisioned_name")
	if serverless == "" || provisioned == "" {
		return fmt.Errorf("kafka ListClustersV2Paginated: no clusters from the CreateClusterV2 tests")
	}

	// --no-paginate stops the CLI following the token for us; the point of the
	// test is that the token works, so the walk is done here.
	const maxPages = 50
	seen := map[string]bool{}
	token := ""
	pages := 0
	for pages < maxPages {
		args := []string{"kafka", "list-clusters-v2", "--max-results", "1", "--no-paginate"}
		if token != "" {
			args = append(args, "--next-token", token)
		}
		out, err := awscli.RunOutput(t.Endpoint, t.Region, args...)
		if err != nil {
			return err
		}
		entries, ok := out["ClusterInfoList"].([]any)
		if !ok {
			return fmt.Errorf("kafka ListClustersV2Paginated: ClusterInfoList is %T, want a list: %v", out["ClusterInfoList"], out)
		}
		if len(entries) > 1 {
			return fmt.Errorf("kafka ListClustersV2Paginated: --max-results 1 returned %d entries — the page size is being ignored", len(entries))
		}
		for _, e := range entries {
			info, _ := e.(map[string]any)
			if name, _ := info["ClusterName"].(string); name != "" {
				seen[name] = true
			}
		}
		pages++
		token, _ = out["NextToken"].(string)
		if token == "" {
			break
		}
	}
	if pages >= maxPages {
		return fmt.Errorf("kafka ListClustersV2Paginated: still paging after %d pages — nextToken is not advancing", maxPages)
	}
	for _, want := range []string{serverless, provisioned} {
		if !seen[want] {
			return fmt.Errorf("kafka ListClustersV2Paginated: %q was never returned across %d pages", want, pages)
		}
	}
	return nil
}

// ListClusterOperationsV2 is the v2 twin of ListClusterOperations, at
// GET /api/v2/clusters/{ClusterArn}/operations.
//
// It is the sharpest instance of this file's premise. The ARN is a non-greedy
// label, so the CLI sends it percent-encoded as one segment with /operations
// after it — a URI that matches neither a bare-ARN route nor a v1 prefix, and
// which an emulator serving several services off one listener can hand to
// whichever wildcard route claims /api first. That answer is not MSK's, and the
// status assertion is what tells the two apart: an operation Overcast has not
// implemented must answer 501, recorded as unimplemented, and nothing else.
func (g *mskGroup) ListClusterOperationsV2(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_v2_provisioned")
	if arn == "" {
		return fmt.Errorf("kafka ListClusterOperationsV2: no msk_v2_provisioned from CreateClusterV2Provisioned")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "list-cluster-operations-v2", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListClusterOperationsV2: the v2 operations collection URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "list-cluster-operations-v2", "--cluster-arn", arn)
	if err != nil {
		return err
	}
	if _, ok := out["ClusterOperationInfoList"].([]any); !ok {
		return fmt.Errorf("kafka ListClusterOperationsV2: ClusterOperationInfoList is %T, want a list: %v",
			out["ClusterOperationInfoList"], out)
	}
	return nil
}

// DescribeClusterV2NotFound pins the not-found shape on the v2 binding, which
// is a different handler from the v1 one.
func (g *mskGroup) DescribeClusterV2NotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "describe-cluster-v2", "--cluster-arn", absentClusterARN(t, "descv2"))
	return assertKafkaError(status, err, "DescribeClusterV2", "NotFoundException", 404)
}

// ─── msk-configurations ───────────────────────────────────────────────────────

func (g *mskGroup) setupConfigurations(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *mskGroup) teardownConfigurations(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order. Matching by name also picks up the duplicate
	// CreateConfigurationConflict leaves behind on a server that accepts a name
	// already in use.
	g.deleteConfigurationsNamed(t, mskDeleteConfigNamer.Name(t), mskConfigNamer.Name(t))
	return nil
}

// CreateConfiguration creates the configuration the rest of the group reads.
func (g *mskGroup) CreateConfiguration(_ context.Context, t *harness.TestContext) error {
	name := mskConfigNamer.Name(t)
	out, err := g.createConfiguration(t, name, "created by the compat cli suite")
	if err != nil {
		return err
	}
	arn, _ := out["Arn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka CreateConfiguration: Arn is empty: %v", out)
	}
	t.Set("msk_config", arn)
	t.Set("msk_config_name", name)

	if got, _ := out["Name"].(string); got != name {
		return fmt.Errorf("kafka CreateConfiguration: Name = %q, want %q", got, name)
	}
	if !strings.Contains(arn, ":configuration/"+name+"/") {
		return fmt.Errorf("kafka CreateConfiguration: Arn = %q, want one naming :configuration/%s/", arn, name)
	}
	revision, _ := out["LatestRevision"].(map[string]any)
	if got, _ := revision["Revision"].(float64); got != 1 {
		return fmt.Errorf("kafka CreateConfiguration: LatestRevision.Revision = %v, want 1 for a new configuration", revision["Revision"])
	}
	if got, _ := out["State"].(string); got != "ACTIVE" {
		return fmt.Errorf("kafka CreateConfiguration: State = %q, want ACTIVE", got)
	}

	// Roundtrip: the create must be visible to the matching describe.
	described, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-configuration", "--arn", arn)
	if err != nil {
		return fmt.Errorf("kafka CreateConfiguration: describe-configuration after create failed: %w", err)
	}
	if got, _ := described["Arn"].(string); got != arn {
		return fmt.Errorf("kafka CreateConfiguration: describe-configuration returned Arn %q, want the created %q", got, arn)
	}
	return nil
}

// DescribeConfiguration reads it back at GET /v1/configurations/{Arn} and pins
// the wire status as well as the body.
func (g *mskGroup) DescribeConfiguration(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_config")
	if arn == "" {
		return fmt.Errorf("kafka DescribeConfiguration: no msk_config from CreateConfiguration")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "describe-configuration", "--arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka DescribeConfiguration: the modeled URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "describe-configuration", "--arn", arn)
	if err != nil {
		return err
	}
	if got, _ := out["Name"].(string); got != t.GetString("msk_config_name") {
		return fmt.Errorf("kafka DescribeConfiguration: Name = %q, want %q", got, t.GetString("msk_config_name"))
	}
	if got, _ := out["Description"].(string); got != "created by the compat cli suite" {
		return fmt.Errorf("kafka DescribeConfiguration: Description = %q, want the one CreateConfiguration sent", got)
	}
	versions, _ := out["KafkaVersions"].([]any)
	var got []string
	for _, v := range versions {
		s, _ := v.(string)
		got = append(got, s)
	}
	if !containsString(got, mskKafkaVersion) || !containsString(got, "3.6.0") {
		return fmt.Errorf("kafka DescribeConfiguration: KafkaVersions = %v, want both requested versions", got)
	}
	return nil
}

// ListConfigurations is the collection at GET /v1/configurations.
func (g *mskGroup) ListConfigurations(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("msk_config_name")
	if name == "" {
		return fmt.Errorf("kafka ListConfigurations: no msk_config_name from CreateConfiguration")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region, "kafka", "list-configurations")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListConfigurations: the collection URI answered HTTP %d, want 200 — the URI the kafka model binds is not being served", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kafka", "list-configurations")
	if err != nil {
		return err
	}
	entries, ok := out["Configurations"].([]any)
	if !ok {
		return fmt.Errorf("kafka ListConfigurations: Configurations is %T, want a list: %v", out["Configurations"], out)
	}
	var names []string
	var found map[string]any
	for _, e := range entries {
		info, _ := e.(map[string]any)
		got, _ := info["Name"].(string)
		names = append(names, got)
		if got == name {
			found = info
		}
	}
	if found == nil {
		return fmt.Errorf("kafka ListConfigurations: %q missing from %v", name, names)
	}
	if got, _ := found["Arn"].(string); got != t.GetString("msk_config") {
		return fmt.Errorf("kafka ListConfigurations: Arn for %q = %q, want the created %q", name, got, t.GetString("msk_config"))
	}
	return nil
}

// ListConfigurationRevisions is a collection hung off the configuration ARN at
// GET /v1/configurations/{Arn}/revisions — the configuration-side twin of
// ListNodes, and bound wrong in the same way if the sub-resource is swallowed
// into the ARN.
func (g *mskGroup) ListConfigurationRevisions(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_config")
	if arn == "" {
		return fmt.Errorf("kafka ListConfigurationRevisions: no msk_config from CreateConfiguration")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "list-configuration-revisions", "--arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListConfigurationRevisions: the revisions collection URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "list-configuration-revisions", "--arn", arn)
	if err != nil {
		return err
	}
	entries, ok := out["Revisions"].([]any)
	if !ok {
		return fmt.Errorf("kafka ListConfigurationRevisions: Revisions is %T, want a list: %v", out["Revisions"], out)
	}
	// CreateConfiguration reported revision 1, so the revisions collection has
	// to agree with it.
	for _, e := range entries {
		info, _ := e.(map[string]any)
		if got, _ := info["Revision"].(float64); got == 1 {
			return nil
		}
	}
	return fmt.Errorf("kafka ListConfigurationRevisions: revision 1 missing from %v, but CreateConfiguration reported it as the latest", entries)
}

// CreateConfigurationConflict pins ConflictException at HTTP 409 — AWS rejects
// a configuration name already in use rather than minting a second one.
func (g *mskGroup) CreateConfigurationConflict(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("msk_config_name")
	if name == "" {
		return fmt.Errorf("kafka CreateConfigurationConflict: no msk_config_name from CreateConfiguration")
	}
	// Made once, through RunStatus: a second attempt would leave a second
	// unwanted configuration behind. A server that accepts it leaves one
	// sharing the group's own configuration name, which teardown removes by
	// name.
	path, cleanup, err := writeTempFile("oc-msk-server-properties-*.properties", "auto.create.topics.enable=true\n")
	if err != nil {
		return err
	}
	defer cleanup()
	status, callErr := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "create-configuration",
		"--name", name,
		"--kafka-versions", mskKafkaVersion,
		"--server-properties", "fileb://"+filepath.ToSlash(path))
	return assertKafkaError(status, callErr, "CreateConfiguration", "ConflictException", 409)
}

// DescribeConfigurationNotFound pins NotFoundException at HTTP 404.
func (g *mskGroup) DescribeConfigurationNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "describe-configuration", "--arn", absentConfigurationARN(t, "desc"))
	return assertKafkaError(status, err, "DescribeConfiguration", "NotFoundException", 404)
}

// DeleteConfiguration deletes a configuration of its own, then asserts it is
// gone from both the direct read and the collection.
func (g *mskGroup) DeleteConfiguration(_ context.Context, t *harness.TestContext) error {
	name := mskDeleteConfigNamer.Name(t)
	created, err := g.createConfiguration(t, name, "created to be deleted")
	if err != nil {
		return fmt.Errorf("kafka DeleteConfiguration: create-configuration failed: %w", err)
	}
	arn, _ := created["Arn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka DeleteConfiguration: create-configuration returned no Arn: %v", created)
	}
	t.Set("msk_delete_config", arn)

	if err := awscli.Run(t.Endpoint, t.Region, "kafka", "delete-configuration", "--arn", arn); err != nil {
		return err
	}

	status, describeErr := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "describe-configuration", "--arn", arn)
	if err := assertKafkaError(status, describeErr, "DescribeConfiguration after delete", "NotFoundException", 404); err != nil {
		return err
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kafka", "list-configurations")
	if err != nil {
		return fmt.Errorf("kafka DeleteConfiguration: list-configurations after delete failed: %w", err)
	}
	entries, _ := out["Configurations"].([]any)
	for _, e := range entries {
		info, _ := e.(map[string]any)
		if got, _ := info["Arn"].(string); got == arn {
			return fmt.Errorf("kafka DeleteConfiguration: %q is still listed after the delete", arn)
		}
	}
	return nil
}

// DeleteConfigurationNotFound pins the not-found shape on the delete handler.
func (g *mskGroup) DeleteConfigurationNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "delete-configuration", "--arn", absentConfigurationARN(t, "del"))
	return assertKafkaError(status, err, "DeleteConfiguration", "NotFoundException", 404)
}

// ─── msk-tags ─────────────────────────────────────────────────────────────────

// setupTags creates the cluster the tag operations address. A serverless
// cluster is used because it has no brokers to provision, so the group owns a
// real, ACTIVE MSK resource without waiting on one.
func (g *mskGroup) setupTags(_ context.Context, t *harness.TestContext) error {
	name := mskTagClusterNamer.Name(t)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "create-cluster-v2",
		"--cluster-name", name,
		"--serverless", mskServerlessConfig)
	if err != nil {
		return fmt.Errorf("kafka tags setup: create-cluster-v2 %q: %w", name, err)
	}
	arn, _ := out["ClusterArn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka tags setup: create-cluster-v2 %q returned no ClusterArn", name)
	}
	t.Set("msk_tag_cluster", arn)
	return nil
}

func (g *mskGroup) teardownTags(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order. Deleting a cluster removes its tags with it, so
	// there is nothing to untag first.
	g.deleteClustersNamed(t, mskInlineTagNamer.Name(t), mskTagClusterNamer.Name(t))
	return nil
}

// TagResource writes tags at POST /v1/tags/{ResourceArn} and reads them back.
//
// That prefix is shared: several AWS services model tagging at /v1/tags, so a
// server picks the owner from the ARN in the path. A service that picks wrong
// stores this cluster's tags somewhere else, which the read-back catches.
func (g *mskGroup) TagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_tag_cluster")
	if arn == "" {
		return fmt.Errorf("kafka TagResource: no msk_tag_cluster from setup")
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"kafka", "tag-resource", "--resource-arn", arn, "--tags", "team=streaming,env=compat",
	); err != nil {
		return err
	}
	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("kafka TagResource: list-tags-for-resource after tagging failed: %w", err)
	}
	if tags["team"] != "streaming" {
		return fmt.Errorf("kafka TagResource: tag team = %q, want streaming (tags: %v)", tags["team"], tags)
	}
	if tags["env"] != "compat" {
		return fmt.Errorf("kafka TagResource: tag env = %q, want compat (tags: %v)", tags["env"], tags)
	}
	return nil
}

// ListTagsForResource is the read at GET /v1/tags/{ResourceArn} — the URI shape
// that carried a trailing slash in Backup and OpenSearch and does not here, per
// the pinned kafka model and the CLI's own wire log.
func (g *mskGroup) ListTagsForResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_tag_cluster")
	if arn == "" {
		return fmt.Errorf("kafka ListTagsForResource: no msk_tag_cluster from setup")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"kafka", "list-tags-for-resource", "--resource-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("kafka ListTagsForResource: the tags URI answered HTTP %d, want 200 — the URI the kafka model binds is not being served", status)
	}

	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return err
	}
	if len(tags) != 2 || tags["team"] != "streaming" || tags["env"] != "compat" {
		return fmt.Errorf("kafka ListTagsForResource: tags = %v, want exactly team=streaming and env=compat from TagResource", tags)
	}
	return nil
}

// UntagResource drops one key and leaves the other, so a handler that clears
// the whole tag set fails here rather than passing on "no error". The keys
// travel as a repeated tagKeys query parameter, which is a different binding
// from TagResource's JSON body.
func (g *mskGroup) UntagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("msk_tag_cluster")
	if arn == "" {
		return fmt.Errorf("kafka UntagResource: no msk_tag_cluster from setup")
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"kafka", "untag-resource", "--resource-arn", arn, "--tag-keys", "env",
	); err != nil {
		return err
	}
	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("kafka UntagResource: list-tags-for-resource after untagging failed: %w", err)
	}
	if _, still := tags["env"]; still {
		return fmt.Errorf("kafka UntagResource: tag env is still present after removal (tags: %v)", tags)
	}
	if tags["team"] != "streaming" {
		return fmt.Errorf("kafka UntagResource: tag team = %q, want streaming — removing env took the other tag with it (tags: %v)",
			tags["team"], tags)
	}
	return nil
}

// CreateClusterV2WithTags covers the `tags` member the create operations accept
// inline, which is a second, separate path into the tag store from TagResource.
// On AWS the two write the same tag set: a cluster created with tags reports
// them from ListTagsForResource, not only from its own describe.
func (g *mskGroup) CreateClusterV2WithTags(_ context.Context, t *harness.TestContext) error {
	name := mskInlineTagNamer.Name(t)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kafka", "create-cluster-v2",
		"--cluster-name", name,
		"--serverless", mskServerlessConfig,
		"--tags", "owner=compat")
	if err != nil {
		return err
	}
	arn, _ := out["ClusterArn"].(string)
	if arn == "" {
		return fmt.Errorf("kafka CreateClusterV2WithTags: ClusterArn is empty: %v", out)
	}
	t.Set("msk_inline_tag_cluster", arn)

	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("kafka CreateClusterV2WithTags: list-tags-for-resource failed: %w", err)
	}
	if tags["owner"] != "compat" {
		return fmt.Errorf("kafka CreateClusterV2WithTags: tag owner = %q, want compat — the creation-time tags are not visible to ListTagsForResource (tags: %v)",
			tags["owner"], tags)
	}
	return nil
}
