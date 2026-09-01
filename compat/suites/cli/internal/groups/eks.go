package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// EKS returns the Amazon EKS service group.
//
// EKS is REST-JSON with the deepest collection hierarchy of any service in the
// emulator: /clusters, /clusters/{name}/node-groups,
// /clusters/{name}/fargate-profiles, /clusters/{name}/addons,
// /clusters/{name}/access-entries, /clusters/{name}/access-entries/{principalArn}/access-policies,
// /clusters/{name}/identity-provider-configs, /clusters/{name}/updates,
// /addons/supported-versions, /access-policies, /cluster-versions and
// /tags/{resourceArn}. Eleven of those are collection URIs, and a collection
// URI bound at a spelling no client sends is exactly issue #963 — Backup's
// ListBackupVaults answered 501 to an unmodified SDK because AWS binds it with
// a trailing slash and Overcast matched only the slash-less form. Every call
// below therefore goes through the AWS CLI, which takes the URI from the
// pinned AWS model, rather than a hand-built request that would encode this
// author's guess instead.
//
// Two service-specific facts shaped this file.
//
// First, on trailing slashes: the EKS model carries none. All 66 operations in
// the pinned manifest (internal/awsapi/manifest.gen.go) and in the botocore
// model the AWS CLI ships bind a slash-less URI, so the literal #963 fault
// cannot occur here. The general fault can — a nested collection registered one
// segment off, or not registered at all — and that is what the collection
// tests pin, each asserting the HTTP status the CLI's own wire log recorded as
// well as the body.
//
// Second, on signing: every call here is signed. The suite's usual
// --no-sign-request is a convenience, and for most services it is also the
// harsher test, because an unbound route then falls through to whatever would
// answer it. EKS is the case where it is neither. Overcast's REST fallback
// treats unsigned traffic as S3's (internal/router, addressesNonS3), and EKS
// deliberately does not claim its root segments in detectService, so an
// unsigned call to any EKS operation Overcast has not bound reaches S3's
// wildcard and comes back as a bucket error — S3's answer, reported as EKS's.
// A signed call carries the eks credential scope, so the generated model
// registry answers 501 and the result is recorded as the honest
// "unimplemented" instead of a misleading "fail". Signing is also simply what
// a production EKS client does.
//
// Error statuses come from the model (eks-2017-11-01), not from what Overcast
// happens to return: ResourceNotFoundException is 404, ResourceInUseException
// is 409 and InvalidParameterException is 400. The AWS CLI prints an error's
// code and never its status, so awscli.RunStatusSigned reads the status back
// out of the CLI's --debug trace.
func EKS() ServiceGroup {
	g := &eksGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// eks-clusters
			"eks-clusters:CreateCluster":                g.CreateCluster,
			"eks-clusters:DescribeCluster":              g.DescribeCluster,
			"eks-clusters:ListClusters":                 g.ListClusters,
			"eks-clusters:ListClustersPaginated":        g.ListClustersPaginated,
			"eks-clusters:UpdateClusterConfig":          g.UpdateClusterConfig,
			"eks-clusters:UpdateClusterVersion":         g.UpdateClusterVersion,
			"eks-clusters:ListUpdates":                  g.ListUpdates,
			"eks-clusters:DescribeUpdate":               g.DescribeUpdate,
			"eks-clusters:DescribeClusterVersions":      g.DescribeClusterVersions,
			"eks-clusters:DescribeClusterNotFound":      g.DescribeClusterNotFound,
			"eks-clusters:CreateClusterAlreadyExists":   g.CreateClusterAlreadyExists,
			"eks-clusters:UpdateClusterConfigNoChanges": g.UpdateClusterConfigNoChanges,
			"eks-clusters:DeleteCluster":                g.DeleteCluster,
			"eks-clusters:DeleteClusterNotFound":        g.DeleteClusterNotFound,
			// eks-nodegroups
			"eks-nodegroups:CreateNodegroup":               g.CreateNodegroup,
			"eks-nodegroups:DescribeNodegroup":             g.DescribeNodegroup,
			"eks-nodegroups:ListNodegroups":                g.ListNodegroups,
			"eks-nodegroups:UpdateNodegroupConfig":         g.UpdateNodegroupConfig,
			"eks-nodegroups:UpdateNodegroupVersion":        g.UpdateNodegroupVersion,
			"eks-nodegroups:CreateNodegroupAlreadyExists":  g.CreateNodegroupAlreadyExists,
			"eks-nodegroups:DescribeNodegroupNotFound":     g.DescribeNodegroupNotFound,
			"eks-nodegroups:ListNodegroupsClusterNotFound": g.ListNodegroupsClusterNotFound,
			"eks-nodegroups:DeleteNodegroup":               g.DeleteNodegroup,
			// eks-fargate-profiles
			"eks-fargate-profiles:CreateFargateProfile":           g.CreateFargateProfile,
			"eks-fargate-profiles:DescribeFargateProfile":         g.DescribeFargateProfile,
			"eks-fargate-profiles:ListFargateProfiles":            g.ListFargateProfiles,
			"eks-fargate-profiles:DescribeFargateProfileNotFound": g.DescribeFargateProfileNotFound,
			"eks-fargate-profiles:DeleteFargateProfile":           g.DeleteFargateProfile,
			// eks-addons
			"eks-addons:CreateAddon":                g.CreateAddon,
			"eks-addons:DescribeAddon":              g.DescribeAddon,
			"eks-addons:ListAddons":                 g.ListAddons,
			"eks-addons:UpdateAddon":                g.UpdateAddon,
			"eks-addons:DescribeAddonVersions":      g.DescribeAddonVersions,
			"eks-addons:DescribeAddonConfiguration": g.DescribeAddonConfiguration,
			"eks-addons:DescribeAddonNotFound":      g.DescribeAddonNotFound,
			"eks-addons:DeleteAddon":                g.DeleteAddon,
			// eks-cluster-access
			"eks-cluster-access:CreateAccessEntry":                  g.CreateAccessEntry,
			"eks-cluster-access:DescribeAccessEntry":                g.DescribeAccessEntry,
			"eks-cluster-access:ListAccessEntries":                  g.ListAccessEntries,
			"eks-cluster-access:UpdateAccessEntry":                  g.UpdateAccessEntry,
			"eks-cluster-access:AssociateAccessPolicy":              g.AssociateAccessPolicy,
			"eks-cluster-access:ListAssociatedAccessPolicies":       g.ListAssociatedAccessPolicies,
			"eks-cluster-access:DisassociateAccessPolicy":           g.DisassociateAccessPolicy,
			"eks-cluster-access:ListAccessPolicies":                 g.ListAccessPolicies,
			"eks-cluster-access:DeleteAccessEntry":                  g.DeleteAccessEntry,
			"eks-cluster-access:AssociateIdentityProviderConfig":    g.AssociateIdentityProviderConfig,
			"eks-cluster-access:ListIdentityProviderConfigs":        g.ListIdentityProviderConfigs,
			"eks-cluster-access:DescribeIdentityProviderConfig":     g.DescribeIdentityProviderConfig,
			"eks-cluster-access:DisassociateIdentityProviderConfig": g.DisassociateIdentityProviderConfig,
			// eks-tags
			"eks-tags:TagResource":           g.TagResource,
			"eks-tags:ListTagsForResource":   g.ListTagsForResource,
			"eks-tags:UntagResource":         g.UntagResource,
			"eks-tags:CreateClusterWithTags": g.CreateClusterWithTags,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"eks-clusters":         g.setupClusters,
			"eks-nodegroups":       g.setupNodegroups,
			"eks-fargate-profiles": g.setupFargateProfiles,
			"eks-addons":           g.setupAddons,
			"eks-cluster-access":   g.setupClusterAccess,
			"eks-tags":             g.setupTags,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"eks-clusters":         g.teardownClusters,
			"eks-nodegroups":       g.teardownNodegroups,
			"eks-fargate-profiles": g.teardownFargateProfiles,
			"eks-addons":           g.teardownAddons,
			"eks-cluster-access":   g.teardownClusterAccess,
			"eks-tags":             g.teardownTags,
		},
	}
}

type eksGroup struct{}

// Each group owns its own cluster, named exactly and torn down by name, so no
// group can delete another's resources and two concurrent runs never collide.
// An EKS cluster name is [0-9A-Za-z][A-Za-z0-9\-_]* up to 100 characters,
// which "{runID}-{tag}" satisfies for every run ID the runner mints.
var (
	eksClusterNamer       = harness.NewNamer("ekscl")
	eksClusterPageNamer   = harness.NewNamer("ekscl2")
	eksClusterDeleteNamer = harness.NewNamer("eksdel")
	eksNodegroupNamer     = harness.NewNamer("eksng")
	eksFargateNamer       = harness.NewNamer("eksfg")
	eksAddonNamer         = harness.NewNamer("eksad")
	eksAccessNamer        = harness.NewNamer("eksac")
	eksTagNamer           = harness.NewNamer("ekstag")
	eksInlineTagNamer     = harness.NewNamer("ekstagi")
)

const (
	// eksClusterVersion is pinned rather than defaulted so DescribeCluster has
	// a value the create request actually asked for to assert against.
	eksClusterVersion = "1.30"
	// eksUpgradeVersion is what UpdateClusterVersion moves the cluster to.
	eksUpgradeVersion = "1.31"

	eksClusterRoleARN = "arn:aws:iam::000000000000:role/eks-compat-cluster-role"
	eksNodeRoleARN    = "arn:aws:iam::000000000000:role/eks-compat-node-role"
	eksPodExecRoleARN = "arn:aws:iam::000000000000:role/eks-compat-pod-execution-role"

	// The two subnets every cluster and nodegroup in this file is placed in.
	eksSubnetA = "subnet-0a1b2c3d"
	eksSubnetB = "subnet-1a2b3c4d"

	// eksAddonName and its versions come from the add-on catalog
	// DescribeAddonVersions serves, so DescribeAddonConfiguration has a
	// version it can resolve a schema for.
	eksAddonName       = "vpc-cni"
	eksAddonVersion    = "v1.18.3-eksbuild.3"
	eksAddonNewVersion = "v1.18.2-eksbuild.1"
	eksSecondAddonName = "coredns"

	eksViewPolicyARN = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"

	// eksPollAttempts bounds every wait in this file. Each attempt is a fresh
	// CLI invocation, which paces the loop — there is no sleep anywhere here.
	eksPollAttempts = 10
)

// ─── shared helpers ───────────────────────────────────────────────────────────

// assertEKSError asserts the CLI reported the modeled error code and that it
// arrived with the HTTP status the AWS model declares for it.
//
// Both halves matter. The code alone passes against a service answering the
// right name with the wrong status, and it also passes against a *different*
// service that happens to use the same name — which is the failure mode a
// shared URI space invites.
func assertEKSError(status int, err error, op, wantCode string, wantStatus int) error {
	if err == nil {
		return fmt.Errorf("eks %s: expected %s, got a successful response", op, wantCode)
	}
	if !strings.Contains(err.Error(), wantCode) {
		return fmt.Errorf("eks %s: expected error code %s, got: %v", op, wantCode, err)
	}
	if status != wantStatus {
		return fmt.Errorf("eks %s: %s arrived with HTTP %d, want HTTP %d — the AWS model declares this status",
			op, wantCode, status, wantStatus)
	}
	return nil
}

// createClusterArgs builds a CreateCluster invocation.
//
// The request goes in through --cli-input-json rather than one flag per
// member, because CreateCluster's `version` member cannot be expressed as a
// flag at all: `--version` is the AWS CLI's own global flag as well, and
// argparse matches an optional anywhere in argv, so
// `aws eks create-cluster … --version 1.30` prints "aws-cli/2.36.18 …" to
// stdout and exits 0 without sending a request. That is an AWS CLI
// limitation, not an Overcast one, and --cli-input-json is the documented way
// round it — the URI, method and body serialisation still come from the
// pinned model, which is the property this suite exists to test.
func (g *eksGroup) createClusterArgs(name string, tags map[string]string) []string {
	body := map[string]any{
		"name":    name,
		"roleArn": eksClusterRoleARN,
		"version": eksClusterVersion,
		"resourcesVpcConfig": map[string]any{
			"subnetIds":            []string{eksSubnetA, eksSubnetB},
			"endpointPublicAccess": true,
		},
	}
	if len(tags) > 0 {
		body["tags"] = tags
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		// Every value here is a literal, so this cannot fire; returning a
		// truncated request instead would be far worse than saying so.
		return []string{"eks", "create-cluster", "--cli-input-json", fmt.Sprintf("encode failed: %v", err)}
	}
	return []string{"eks", "create-cluster", "--cli-input-json", string(encoded)}
}

// createCluster creates a cluster and returns the modeled Cluster document.
func (g *eksGroup) createCluster(t *harness.TestContext, name string, tags map[string]string) (map[string]any, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, g.createClusterArgs(name, tags)...)
	if err != nil {
		return nil, err
	}
	cluster, _ := out["cluster"].(map[string]any)
	if cluster == nil {
		return nil, fmt.Errorf("eks create-cluster %q: no cluster in response: %v", name, out)
	}
	return cluster, nil
}

// createClusterFor creates a cluster for a group's setup and records its name
// and ARN under the given state keys.
func (g *eksGroup) createClusterFor(t *harness.TestContext, name, nameKey, arnKey string) error {
	cluster, err := g.createCluster(t, name, nil)
	if err != nil {
		return fmt.Errorf("eks setup: create-cluster %q: %w", name, err)
	}
	t.Set(nameKey, name)
	arn, _ := cluster["arn"].(string)
	if arn == "" {
		return fmt.Errorf("eks setup: create-cluster %q returned no arn: %v", name, cluster)
	}
	t.Set(arnKey, arn)
	if err := g.waitClusterActive(t, name); err != nil {
		return fmt.Errorf("eks setup: %w", err)
	}
	return nil
}

// describeCluster reads a cluster back through GET /clusters/{name}.
func (g *eksGroup) describeCluster(t *harness.TestContext, name string) (map[string]any, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "eks", "describe-cluster", "--name", name)
	if err != nil {
		return nil, err
	}
	cluster, _ := out["cluster"].(map[string]any)
	if cluster == nil {
		return nil, fmt.Errorf("eks describe-cluster %q: no cluster in response: %v", name, out)
	}
	return cluster, nil
}

// waitClusterActive polls DescribeCluster until the cluster leaves CREATING.
//
// Real AWS creates a cluster asynchronously, and Overcast models a live mode
// that does the same, so a test that assumed ACTIVE on the first read would be
// a flake waiting for a configuration change. The loop is bounded and has no
// sleep in it: each attempt is a fresh CLI invocation, which paces it.
func (g *eksGroup) waitClusterActive(t *harness.TestContext, name string) error {
	var last string
	for attempt := 0; attempt < eksPollAttempts; attempt++ {
		cluster, err := g.describeCluster(t, name)
		if err != nil {
			return fmt.Errorf("eks describe-cluster %q while waiting for ACTIVE: %w", name, err)
		}
		last, _ = cluster["status"].(string)
		switch last {
		case "ACTIVE":
			return nil
		case "CREATING":
			continue
		default:
			return fmt.Errorf("eks cluster %q reached status %q, want ACTIVE", name, last)
		}
	}
	return fmt.Errorf("eks cluster %q still %q after %d polls, want ACTIVE", name, last, eksPollAttempts)
}

// clusterNames returns every name ListClusters reports.
func (g *eksGroup) clusterNames(t *harness.TestContext, extra ...string) ([]string, error) {
	args := append([]string{"eks", "list-clusters"}, extra...)
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, err
	}
	return stringList(out, "clusters", "eks list-clusters")
}

// stringList reads a modeled list-of-string response member.
func stringList(out map[string]any, member, op string) ([]string, error) {
	raw, ok := out[member].([]any)
	if !ok {
		if out[member] == nil {
			return nil, fmt.Errorf("%s: response has no %s member: %v", op, member, out)
		}
		return nil, fmt.Errorf("%s: %s is %T, want a list: %v", op, member, out[member], out)
	}
	names := make([]string, 0, len(raw))
	for _, e := range raw {
		s, _ := e.(string)
		if s == "" {
			return nil, fmt.Errorf("%s: %s contains a non-string entry: %v", op, member, e)
		}
		names = append(names, s)
	}
	return names, nil
}

// tagsOf reads a resource's tags through ListTagsForResource, the EKS
// operation bound to the /tags/{resourceArn} path space that several services
// share. Overcast dispatches that path on the ARN's own service prefix.
func (g *eksGroup) tagsOf(t *harness.TestContext, arn string) (map[string]string, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-tags-for-resource", "--resource-arn", arn)
	if err != nil {
		return nil, err
	}
	raw, ok := out["tags"].(map[string]any)
	if !ok && out["tags"] != nil {
		return nil, fmt.Errorf("eks list-tags-for-resource: tags is %T, want a map: %v", out["tags"], out)
	}
	tags := make(map[string]string, len(raw))
	for k, v := range raw {
		tags[k], _ = v.(string)
	}
	return tags, nil
}

// deleteClusterQuietly is the teardown form: best effort, never returns.
func (g *eksGroup) deleteClusterQuietly(t *harness.TestContext, key string) {
	if name := t.GetString(key); name != "" {
		awscli.RunSigned(t.Endpoint, t.Region, "eks", "delete-cluster", "--name", name) //nolint:errcheck
	}
}

// ─── eks-clusters ─────────────────────────────────────────────────────────────

func (g *eksGroup) setupClusters(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *eksGroup) teardownClusters(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order. DeleteCluster cascades to the nodegroups,
	// add-ons, profiles and tags AWS documents it removing with the cluster.
	g.deleteClusterQuietly(t, "eks_delete_cluster")
	g.deleteClusterQuietly(t, "eks_page_cluster")
	g.deleteClusterQuietly(t, "eks_cluster")
	return nil
}

// CreateCluster creates the cluster the rest of the group reads, and checks
// the create is visible to the matching describe.
//
// ECS and MSK declare a test of this name too, so the implementation key is
// group-qualified: a bare key claimed by several groups binds one group to
// another's implementation and reports its result under this name, which every
// suite loader refuses. The test keeps the AWS operation's own name — the
// group prefix is what disambiguates it.
func (g *eksGroup) CreateCluster(_ context.Context, t *harness.TestContext) error {
	name := eksClusterNamer.Name(t)
	t.Set("eks_cluster", name)

	cluster, err := g.createCluster(t, name, nil)
	if err != nil {
		return err
	}
	if got, _ := cluster["name"].(string); got != name {
		return fmt.Errorf("eks CreateCluster: name = %q, want %q", got, name)
	}
	arn, _ := cluster["arn"].(string)
	if arn == "" {
		return fmt.Errorf("eks CreateCluster: arn is empty: %v", cluster)
	}
	if !strings.HasSuffix(arn, ":cluster/"+name) {
		return fmt.Errorf("eks CreateCluster: arn = %q, want one ending in :cluster/%s", arn, name)
	}
	t.Set("eks_cluster_arn", arn)
	if got, _ := cluster["roleArn"].(string); got != eksClusterRoleARN {
		return fmt.Errorf("eks CreateCluster: roleArn = %q, want %q", got, eksClusterRoleARN)
	}
	if got, _ := cluster["version"].(string); got != eksClusterVersion {
		return fmt.Errorf("eks CreateCluster: version = %q, want %q", got, eksClusterVersion)
	}
	t.Set("eks_cluster_version", eksClusterVersion)
	if err := g.waitClusterActive(t, name); err != nil {
		return err
	}

	// Roundtrip: the create must be visible to the matching describe, with the
	// members the request carried.
	described, err := g.describeCluster(t, name)
	if err != nil {
		return fmt.Errorf("eks CreateCluster: describe-cluster after create failed: %w", err)
	}
	if got, _ := described["arn"].(string); got != arn {
		return fmt.Errorf("eks CreateCluster: describe-cluster returned arn %q, want the created %q", got, arn)
	}
	vpc, _ := described["resourcesVpcConfig"].(map[string]any)
	subnets, err := stringList(vpc, "subnetIds", "eks describe-cluster resourcesVpcConfig")
	if err != nil {
		return fmt.Errorf("eks CreateCluster: %w", err)
	}
	if !containsString(subnets, "subnet-0a1b2c3d") || !containsString(subnets, "subnet-1a2b3c4d") {
		return fmt.Errorf("eks CreateCluster: resourcesVpcConfig.subnetIds = %v, want the two subnets the create request sent", subnets)
	}
	return nil
}

// DescribeCluster reads the cluster back at GET /clusters/{name} and pins the
// HTTP status the CLI's wire log recorded, which is what proves the request
// reached EKS rather than a route that happened to answer.
func (g *eksGroup) DescribeCluster(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	if name == "" {
		return fmt.Errorf("eks DescribeCluster: no eks_cluster from CreateCluster")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region, "eks", "describe-cluster", "--name", name)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeCluster: the modeled URI answered HTTP %d, want 200", status)
	}

	cluster, err := g.describeCluster(t, name)
	if err != nil {
		return err
	}
	if got, _ := cluster["name"].(string); got != name {
		return fmt.Errorf("eks DescribeCluster: name = %q, want %q", got, name)
	}
	if got, _ := cluster["arn"].(string); got != t.GetString("eks_cluster_arn") {
		return fmt.Errorf("eks DescribeCluster: arn = %q, want the created %q", got, t.GetString("eks_cluster_arn"))
	}
	want := t.GetString("eks_cluster_version")
	if got, _ := cluster["version"].(string); got != want {
		return fmt.Errorf("eks DescribeCluster: version = %q, want %q", got, want)
	}
	if got, _ := cluster["status"].(string); got != "ACTIVE" {
		return fmt.Errorf("eks DescribeCluster: status = %q, want ACTIVE", got)
	}
	if got, _ := cluster["endpoint"].(string); got == "" {
		return fmt.Errorf("eks DescribeCluster: endpoint is empty on an ACTIVE cluster: %v", cluster)
	}
	return nil
}

// ListClusters covers the root collection URI, GET /clusters. It is the
// operation shaped most like the one issue #963 was found in, so the wire
// status is asserted as well as the body.
func (g *eksGroup) ListClusters(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	if name == "" {
		return fmt.Errorf("eks ListClusters: no eks_cluster from CreateCluster")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region, "eks", "list-clusters")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListClusters: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	names, err := g.clusterNames(t)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("eks ListClusters: no clusters listed, want at least %q", name)
	}
	if !containsString(names, name) {
		return fmt.Errorf("eks ListClusters: %q missing from %v", name, names)
	}
	return nil
}

// ListClustersPaginated drives the CLI's own pagination loop over the modeled
// maxResults/nextToken members with a page smaller than the result set.
//
// The assertion is that every cluster is still enumerated exactly once. That
// is what catches the two ways a paged collection goes wrong: a service that
// returns a nextToken it never advances past hands the CLI the first page
// forever, and one that drops the token after the first page hides the rest.
func (g *eksGroup) ListClustersPaginated(_ context.Context, t *harness.TestContext) error {
	first := t.GetString("eks_cluster")
	if first == "" {
		return fmt.Errorf("eks ListClusters: no eks_cluster from CreateCluster")
	}
	second := eksClusterPageNamer.Name(t)
	t.Set("eks_page_cluster", second)
	if _, err := g.createCluster(t, second, nil); err != nil {
		return fmt.Errorf("eks ListClustersPaginated: create-cluster %q: %w", second, err)
	}

	names, err := g.clusterNames(t, "--page-size", "1")
	if err != nil {
		return err
	}
	seen := map[string]int{}
	for _, n := range names {
		seen[n]++
	}
	for _, want := range []string{first, second} {
		switch seen[want] {
		case 1:
		case 0:
			return fmt.Errorf("eks ListClusters --page-size 1: %q missing from %v — a page was dropped", want, names)
		default:
			return fmt.Errorf("eks ListClusters --page-size 1: %q returned %d times in %v — the nextToken is not advancing", want, seen[want], names)
		}
	}
	return nil
}

// UpdateClusterConfig covers POST /clusters/{name}/update-config, a sub-resource
// URI distinct from the cluster's own, and checks the change actually landed
// rather than only that an Update document came back.
func (g *eksGroup) UpdateClusterConfig(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	if name == "" {
		return fmt.Errorf("eks UpdateClusterConfig: no eks_cluster from CreateCluster")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "update-cluster-config",
		"--name", name,
		"--logging", `{"clusterLogging":[{"types":["api","audit"],"enabled":true}]}`,
	)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks UpdateClusterConfig: no update in response: %v", out)
	}
	id, _ := update["id"].(string)
	if id == "" {
		return fmt.Errorf("eks UpdateClusterConfig: update.id is empty: %v", update)
	}
	t.Set("eks_update_id", id)
	if got, _ := update["type"].(string); got != "LoggingUpdate" {
		return fmt.Errorf("eks UpdateClusterConfig: update.type = %q, want LoggingUpdate", got)
	}

	cluster, err := g.describeCluster(t, name)
	if err != nil {
		return fmt.Errorf("eks UpdateClusterConfig: describe-cluster after update failed: %w", err)
	}
	logging, _ := cluster["logging"].(map[string]any)
	entries, _ := logging["clusterLogging"].([]any)
	if len(entries) == 0 {
		return fmt.Errorf("eks UpdateClusterConfig: describe-cluster shows logging %v, want the clusterLogging the update sent", cluster["logging"])
	}
	entry, _ := entries[0].(map[string]any)
	if enabled, _ := entry["enabled"].(bool); !enabled {
		return fmt.Errorf("eks UpdateClusterConfig: clusterLogging[0].enabled = %v, want true", entry["enabled"])
	}
	types, err := stringList(entry, "types", "eks describe-cluster clusterLogging[0]")
	if err != nil {
		return fmt.Errorf("eks UpdateClusterConfig: %w", err)
	}
	if !containsString(types, "audit") {
		return fmt.Errorf("eks UpdateClusterConfig: clusterLogging[0].types = %v, want the audit type the update enabled", types)
	}
	return nil
}

// UpdateClusterVersion covers POST /clusters/{name}/updates — the same URI
// ListUpdates reads with GET, which is exactly the pairing that hides a route
// bound for one method only.
func (g *eksGroup) UpdateClusterVersion(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	if name == "" {
		return fmt.Errorf("eks UpdateClusterVersion: no eks_cluster from CreateCluster")
	}
	// --cli-input-json for the same reason CreateCluster uses it: the modeled
	// `version` member collides with the AWS CLI's global --version flag.
	body := fmt.Sprintf(`{"name":%q,"version":%q}`, name, eksUpgradeVersion)
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "update-cluster-version", "--cli-input-json", body)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks UpdateClusterVersion: no update in response: %v", out)
	}
	if got, _ := update["type"].(string); got != "VersionUpdate" {
		return fmt.Errorf("eks UpdateClusterVersion: update.type = %q, want VersionUpdate", got)
	}
	if got, _ := update["status"].(string); got == "" {
		return fmt.Errorf("eks UpdateClusterVersion: update.status is empty: %v", update)
	}

	cluster, err := g.describeCluster(t, name)
	if err != nil {
		return fmt.Errorf("eks UpdateClusterVersion: describe-cluster after update failed: %w", err)
	}
	if got, _ := cluster["version"].(string); got != eksUpgradeVersion {
		return fmt.Errorf("eks UpdateClusterVersion: describe-cluster reports version %q, want the requested %q", got, eksUpgradeVersion)
	}
	// Later reads in this group must expect the upgraded version.
	t.Set("eks_cluster_version", eksUpgradeVersion)
	return nil
}

// ListUpdates covers GET /clusters/{name}/updates and must show the update
// UpdateClusterConfig recorded.
func (g *eksGroup) ListUpdates(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	id := t.GetString("eks_update_id")
	if name == "" || id == "" {
		return fmt.Errorf("eks ListUpdates: no cluster or update id from UpdateClusterConfig")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region, "eks", "list-updates", "--name", name)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListUpdates: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "eks", "list-updates", "--name", name)
	if err != nil {
		return err
	}
	ids, err := stringList(out, "updateIds", "eks list-updates")
	if err != nil {
		return err
	}
	if !containsString(ids, id) {
		return fmt.Errorf("eks ListUpdates: update %q missing from %v", id, ids)
	}
	return nil
}

// DescribeUpdate reads one update back at GET /clusters/{name}/updates/{updateId}.
func (g *eksGroup) DescribeUpdate(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	id := t.GetString("eks_update_id")
	if name == "" || id == "" {
		return fmt.Errorf("eks DescribeUpdate: no cluster or update id from UpdateClusterConfig")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-update", "--name", name, "--update-id", id)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks DescribeUpdate: no update in response: %v", out)
	}
	if got, _ := update["id"].(string); got != id {
		return fmt.Errorf("eks DescribeUpdate: update.id = %q, want %q", got, id)
	}
	if got, _ := update["type"].(string); got != "LoggingUpdate" {
		return fmt.Errorf("eks DescribeUpdate: update.type = %q, want the LoggingUpdate UpdateClusterConfig recorded", got)
	}
	if got, _ := update["status"].(string); got == "" {
		return fmt.Errorf("eks DescribeUpdate: update.status is empty: %v", update)
	}
	return nil
}

// DescribeClusterVersions covers GET /cluster-versions, a root collection URI
// with no relationship to /clusters despite the name.
func (g *eksGroup) DescribeClusterVersions(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region, "eks", "describe-cluster-versions")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeClusterVersions: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "eks", "describe-cluster-versions")
	if err != nil {
		return err
	}
	versions, ok := out["clusterVersions"].([]any)
	if !ok {
		return fmt.Errorf("eks DescribeClusterVersions: clusterVersions is %T, want a list: %v", out["clusterVersions"], out)
	}
	if len(versions) == 0 {
		return fmt.Errorf("eks DescribeClusterVersions: clusterVersions is empty")
	}
	for _, e := range versions {
		entry, _ := e.(map[string]any)
		if got, _ := entry["clusterVersion"].(string); got == "" {
			return fmt.Errorf("eks DescribeClusterVersions: an entry has no clusterVersion: %v", entry)
		}
	}
	return nil
}

// DescribeClusterNotFound pins the modeled ResourceNotFoundException, which the
// EKS model declares as HTTP 404 — unlike OpenSearch's, which is 409.
func (g *eksGroup) DescribeClusterNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-cluster", "--name", eksClusterNamer.Name(t)+"-nope")
	return assertEKSError(status, err, "DescribeCluster", "ResourceNotFoundException", 404)
}

// CreateClusterAlreadyExists pins ResourceInUseException, HTTP 409 — the code
// EKS uses where most services use an AlreadyExists name.
func (g *eksGroup) CreateClusterAlreadyExists(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	if name == "" {
		return fmt.Errorf("eks CreateClusterAlreadyExists: no eks_cluster from CreateCluster")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region, g.createClusterArgs(name, nil)...)
	return assertEKSError(status, err, "CreateCluster", "ResourceInUseException", 409)
}

// UpdateClusterConfigNoChanges pins InvalidParameterException, HTTP 400. No
// body member of UpdateClusterConfig is required, so the CLI sends the call
// happily and the service is the only thing that can reject it.
func (g *eksGroup) UpdateClusterConfigNoChanges(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("eks_cluster")
	if name == "" {
		return fmt.Errorf("eks UpdateClusterConfigNoChanges: no eks_cluster from CreateCluster")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "update-cluster-config", "--name", name)
	return assertEKSError(status, err, "UpdateClusterConfig", "InvalidParameterException", 400)
}

// DeleteCluster deletes a cluster of its own rather than the one the rest of
// the group depends on, then proves it is gone both ways the assertion
// contract allows: the direct describe returns the modeled not-found error, and
// the collection no longer lists it.
func (g *eksGroup) DeleteCluster(_ context.Context, t *harness.TestContext) error {
	name := eksClusterDeleteNamer.Name(t)
	t.Set("eks_delete_cluster", name)
	if _, err := g.createCluster(t, name, nil); err != nil {
		return fmt.Errorf("eks DeleteCluster: create failed: %w", err)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "eks", "delete-cluster", "--name", name)
	if err != nil {
		return err
	}
	cluster, _ := out["cluster"].(map[string]any)
	if cluster == nil {
		return fmt.Errorf("eks DeleteCluster: no cluster in response: %v", out)
	}
	if got, _ := cluster["name"].(string); got != name {
		return fmt.Errorf("eks DeleteCluster: name = %q, want %q", got, name)
	}
	if got, _ := cluster["status"].(string); got != "DELETING" {
		return fmt.Errorf("eks DeleteCluster: status = %q, want DELETING", got)
	}

	var lastStatus int
	var lastErr error
	gone := false
	for attempt := 0; attempt < eksPollAttempts && !gone; attempt++ {
		lastStatus, lastErr = awscli.RunStatusSigned(t.Endpoint, t.Region,
			"eks", "describe-cluster", "--name", name)
		gone = lastErr != nil
	}
	if !gone {
		return fmt.Errorf("eks DeleteCluster: describe-cluster still returns %q after %d polls", name, eksPollAttempts)
	}
	if err := assertEKSError(lastStatus, lastErr, "DescribeCluster after delete", "ResourceNotFoundException", 404); err != nil {
		return err
	}

	names, err := g.clusterNames(t)
	if err != nil {
		return fmt.Errorf("eks DeleteCluster: list-clusters after delete failed: %w", err)
	}
	if containsString(names, name) {
		return fmt.Errorf("eks DeleteCluster: %q still listed after delete: %v", name, names)
	}
	return nil
}

// DeleteClusterNotFound pins the not-found status on the delete path, which is
// a different handler from DescribeCluster's.
func (g *eksGroup) DeleteClusterNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "delete-cluster", "--name", eksClusterDeleteNamer.Name(t)+"-nope")
	return assertEKSError(status, err, "DeleteCluster", "ResourceNotFoundException", 404)
}

// ─── eks-nodegroups ───────────────────────────────────────────────────────────

func (g *eksGroup) setupNodegroups(_ context.Context, t *harness.TestContext) error {
	return g.createClusterFor(t, eksNodegroupNamer.Name(t)+"-c", "eks_ng_cluster", "eks_ng_cluster_arn")
}

func (g *eksGroup) teardownNodegroups(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order: nodegroups before the cluster that owns them.
	cluster := t.GetString("eks_ng_cluster")
	for _, key := range []string{"eks_ng_delete", "eks_nodegroup"} {
		if name := t.GetString(key); name != "" && cluster != "" {
			awscli.RunSigned(t.Endpoint, t.Region, //nolint:errcheck
				"eks", "delete-nodegroup", "--cluster-name", cluster, "--nodegroup-name", name)
		}
	}
	g.deleteClusterQuietly(t, "eks_ng_cluster")
	return nil
}

// createNodegroup creates a nodegroup and returns the modeled Nodegroup
// document. POST /clusters/{name}/node-groups is a nested collection URI, and
// the hyphen in "node-groups" against the operation's "Nodegroup" spelling is
// exactly the kind of mismatch a hand-written route gets wrong.
func (g *eksGroup) createNodegroup(t *harness.TestContext, cluster, name string, extra ...string) (map[string]any, error) {
	args := append([]string{
		"eks", "create-nodegroup",
		"--cluster-name", cluster,
		"--nodegroup-name", name,
		"--node-role", eksNodeRoleARN,
		"--subnets", "subnet-0a1b2c3d", "subnet-1a2b3c4d",
		"--instance-types", "t3.medium",
		"--ami-type", "AL2_x86_64",
		"--capacity-type", "ON_DEMAND",
		"--disk-size", "20",
		"--scaling-config", `{"minSize":1,"maxSize":3,"desiredSize":2}`,
		"--labels", `{"role":"compat"}`,
	}, extra...)
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, err
	}
	ng, _ := out["nodegroup"].(map[string]any)
	if ng == nil {
		return nil, fmt.Errorf("eks create-nodegroup %q: no nodegroup in response: %v", name, out)
	}
	return ng, nil
}

func (g *eksGroup) describeNodegroup(t *harness.TestContext, cluster, name string) (map[string]any, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-nodegroup", "--cluster-name", cluster, "--nodegroup-name", name)
	if err != nil {
		return nil, err
	}
	ng, _ := out["nodegroup"].(map[string]any)
	if ng == nil {
		return nil, fmt.Errorf("eks describe-nodegroup %q: no nodegroup in response: %v", name, out)
	}
	return ng, nil
}

func (g *eksGroup) nodegroupNames(t *harness.TestContext, cluster string) ([]string, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-nodegroups", "--cluster-name", cluster)
	if err != nil {
		return nil, err
	}
	return stringList(out, "nodegroups", "eks list-nodegroups")
}

func (g *eksGroup) CreateNodegroup(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_ng_cluster")
	if cluster == "" {
		return fmt.Errorf("eks CreateNodegroup: no eks_ng_cluster from setup")
	}
	name := eksNodegroupNamer.Name(t)
	t.Set("eks_nodegroup", name)

	ng, err := g.createNodegroup(t, cluster, name)
	if err != nil {
		return err
	}
	if got, _ := ng["nodegroupName"].(string); got != name {
		return fmt.Errorf("eks CreateNodegroup: nodegroupName = %q, want %q", got, name)
	}
	arn, _ := ng["nodegroupArn"].(string)
	if arn == "" {
		return fmt.Errorf("eks CreateNodegroup: nodegroupArn is empty: %v", ng)
	}
	// A nodegroup ARN is :nodegroup/{cluster}/{nodegroup}/{uuid} — the trailing
	// identifier is why this is a contains rather than a suffix check.
	if want := ":nodegroup/" + cluster + "/" + name + "/"; !strings.Contains(arn, want) {
		return fmt.Errorf("eks CreateNodegroup: nodegroupArn = %q, want one containing %q", arn, want)
	}
	t.Set("eks_nodegroup_arn", arn)
	if got, _ := ng["clusterName"].(string); got != cluster {
		return fmt.Errorf("eks CreateNodegroup: clusterName = %q, want %q", got, cluster)
	}

	// Roundtrip through the matching describe, checking the members the
	// request carried rather than only that the call succeeded.
	described, err := g.describeNodegroup(t, cluster, name)
	if err != nil {
		return fmt.Errorf("eks CreateNodegroup: describe-nodegroup after create failed: %w", err)
	}
	if got, _ := described["nodeRole"].(string); got != eksNodeRoleARN {
		return fmt.Errorf("eks CreateNodegroup: nodeRole = %q, want %q", got, eksNodeRoleARN)
	}
	types, err := stringList(described, "instanceTypes", "eks describe-nodegroup")
	if err != nil {
		return fmt.Errorf("eks CreateNodegroup: %w", err)
	}
	if len(types) != 1 || types[0] != "t3.medium" {
		return fmt.Errorf("eks CreateNodegroup: instanceTypes = %v, want [t3.medium]", types)
	}
	scaling, _ := described["scalingConfig"].(map[string]any)
	if desired, _ := scaling["desiredSize"].(float64); desired != 2 {
		return fmt.Errorf("eks CreateNodegroup: scalingConfig.desiredSize = %v, want 2", scaling["desiredSize"])
	}
	return nil
}

func (g *eksGroup) DescribeNodegroup(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ng_cluster"), t.GetString("eks_nodegroup")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks DescribeNodegroup: no nodegroup from CreateNodegroup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-nodegroup", "--cluster-name", cluster, "--nodegroup-name", name)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeNodegroup: the modeled URI answered HTTP %d, want 200", status)
	}

	ng, err := g.describeNodegroup(t, cluster, name)
	if err != nil {
		return err
	}
	if got, _ := ng["nodegroupName"].(string); got != name {
		return fmt.Errorf("eks DescribeNodegroup: nodegroupName = %q, want %q", got, name)
	}
	if got, _ := ng["nodegroupArn"].(string); got != t.GetString("eks_nodegroup_arn") {
		return fmt.Errorf("eks DescribeNodegroup: nodegroupArn = %q, want the created %q", got, t.GetString("eks_nodegroup_arn"))
	}
	if got, _ := ng["amiType"].(string); got != "AL2_x86_64" {
		return fmt.Errorf("eks DescribeNodegroup: amiType = %q, want AL2_x86_64", got)
	}
	labels, _ := ng["labels"].(map[string]any)
	if got, _ := labels["role"].(string); got != "compat" {
		return fmt.Errorf("eks DescribeNodegroup: labels = %v, want role=compat from the create request", ng["labels"])
	}
	return nil
}

// ListNodegroups covers GET /clusters/{name}/node-groups, the nested
// collection URI most likely to be bound one segment off.
func (g *eksGroup) ListNodegroups(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ng_cluster"), t.GetString("eks_nodegroup")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks ListNodegroups: no nodegroup from CreateNodegroup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-nodegroups", "--cluster-name", cluster)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListNodegroups: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	names, err := g.nodegroupNames(t, cluster)
	if err != nil {
		return err
	}
	if !containsString(names, name) {
		return fmt.Errorf("eks ListNodegroups: %q missing from %v", name, names)
	}
	return nil
}

// UpdateNodegroupConfig covers POST /clusters/{name}/node-groups/{nodegroupName}/update-config.
func (g *eksGroup) UpdateNodegroupConfig(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ng_cluster"), t.GetString("eks_nodegroup")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks UpdateNodegroupConfig: no nodegroup from CreateNodegroup")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "update-nodegroup-config",
		"--cluster-name", cluster, "--nodegroup-name", name,
		"--scaling-config", `{"minSize":2,"maxSize":5,"desiredSize":3}`,
	)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks UpdateNodegroupConfig: no update in response: %v", out)
	}
	if got, _ := update["id"].(string); got == "" {
		return fmt.Errorf("eks UpdateNodegroupConfig: update.id is empty: %v", update)
	}

	ng, err := g.describeNodegroup(t, cluster, name)
	if err != nil {
		return fmt.Errorf("eks UpdateNodegroupConfig: describe-nodegroup after update failed: %w", err)
	}
	scaling, _ := ng["scalingConfig"].(map[string]any)
	if desired, _ := scaling["desiredSize"].(float64); desired != 3 {
		return fmt.Errorf("eks UpdateNodegroupConfig: scalingConfig.desiredSize = %v after the update, want 3", scaling["desiredSize"])
	}
	if maxSize, _ := scaling["maxSize"].(float64); maxSize != 5 {
		return fmt.Errorf("eks UpdateNodegroupConfig: scalingConfig.maxSize = %v after the update, want 5", scaling["maxSize"])
	}
	return nil
}

// UpdateNodegroupVersion covers POST /clusters/{name}/node-groups/{nodegroupName}/update-version.
// No body member is required, so --release-version alone is a legal call.
func (g *eksGroup) UpdateNodegroupVersion(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ng_cluster"), t.GetString("eks_nodegroup")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks UpdateNodegroupVersion: no nodegroup from CreateNodegroup")
	}
	const releaseVersion = "1.30.0-20240703"
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "update-nodegroup-version",
		"--cluster-name", cluster, "--nodegroup-name", name,
		"--release-version", releaseVersion,
	)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks UpdateNodegroupVersion: no update in response: %v", out)
	}
	if got, _ := update["id"].(string); got == "" {
		return fmt.Errorf("eks UpdateNodegroupVersion: update.id is empty: %v", update)
	}

	ng, err := g.describeNodegroup(t, cluster, name)
	if err != nil {
		return fmt.Errorf("eks UpdateNodegroupVersion: describe-nodegroup after update failed: %w", err)
	}
	if got, _ := ng["releaseVersion"].(string); got != releaseVersion {
		return fmt.Errorf("eks UpdateNodegroupVersion: releaseVersion = %q after the update, want %q", got, releaseVersion)
	}
	return nil
}

func (g *eksGroup) CreateNodegroupAlreadyExists(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ng_cluster"), t.GetString("eks_nodegroup")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks CreateNodegroupAlreadyExists: no nodegroup from CreateNodegroup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "create-nodegroup",
		"--cluster-name", cluster, "--nodegroup-name", name,
		"--node-role", eksNodeRoleARN,
		"--subnets", "subnet-0a1b2c3d",
	)
	return assertEKSError(status, err, "CreateNodegroup", "ResourceInUseException", 409)
}

func (g *eksGroup) DescribeNodegroupNotFound(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_ng_cluster")
	if cluster == "" {
		return fmt.Errorf("eks DescribeNodegroupNotFound: no eks_ng_cluster from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-nodegroup",
		"--cluster-name", cluster, "--nodegroup-name", eksNodegroupNamer.Name(t)+"-nope")
	return assertEKSError(status, err, "DescribeNodegroup", "ResourceNotFoundException", 404)
}

// ListNodegroupsClusterNotFound asks the nested collection for a cluster that
// does not exist. A route that reads the wrong path label — or a handler that
// scans the whole store rather than one cluster's slice — answers 200 with an
// empty list instead of the modeled not-found error.
func (g *eksGroup) ListNodegroupsClusterNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-nodegroups", "--cluster-name", eksNodegroupNamer.Name(t)+"-nope")
	return assertEKSError(status, err, "ListNodegroups", "ResourceNotFoundException", 404)
}

func (g *eksGroup) DeleteNodegroup(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_ng_cluster")
	if cluster == "" {
		return fmt.Errorf("eks DeleteNodegroup: no eks_ng_cluster from setup")
	}
	name := eksNodegroupNamer.Name(t) + "-del"
	t.Set("eks_ng_delete", name)
	if _, err := g.createNodegroup(t, cluster, name); err != nil {
		return fmt.Errorf("eks DeleteNodegroup: create failed: %w", err)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "delete-nodegroup", "--cluster-name", cluster, "--nodegroup-name", name)
	if err != nil {
		return err
	}
	ng, _ := out["nodegroup"].(map[string]any)
	if ng == nil {
		return fmt.Errorf("eks DeleteNodegroup: no nodegroup in response: %v", out)
	}
	if got, _ := ng["status"].(string); got != "DELETING" {
		return fmt.Errorf("eks DeleteNodegroup: status = %q, want DELETING", got)
	}

	var lastStatus int
	var lastErr error
	gone := false
	for attempt := 0; attempt < eksPollAttempts && !gone; attempt++ {
		lastStatus, lastErr = awscli.RunStatusSigned(t.Endpoint, t.Region,
			"eks", "describe-nodegroup", "--cluster-name", cluster, "--nodegroup-name", name)
		gone = lastErr != nil
	}
	if !gone {
		return fmt.Errorf("eks DeleteNodegroup: describe-nodegroup still returns %q after %d polls", name, eksPollAttempts)
	}
	if err := assertEKSError(lastStatus, lastErr, "DescribeNodegroup after delete", "ResourceNotFoundException", 404); err != nil {
		return err
	}

	names, err := g.nodegroupNames(t, cluster)
	if err != nil {
		return fmt.Errorf("eks DeleteNodegroup: list-nodegroups after delete failed: %w", err)
	}
	if containsString(names, name) {
		return fmt.Errorf("eks DeleteNodegroup: %q still listed after delete: %v", name, names)
	}
	return nil
}

// ─── eks-fargate-profiles ─────────────────────────────────────────────────────

func (g *eksGroup) setupFargateProfiles(_ context.Context, t *harness.TestContext) error {
	return g.createClusterFor(t, eksFargateNamer.Name(t)+"-c", "eks_fg_cluster", "eks_fg_cluster_arn")
}

func (g *eksGroup) teardownFargateProfiles(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_fg_cluster")
	for _, key := range []string{"eks_fg_delete", "eks_fg_profile"} {
		if name := t.GetString(key); name != "" && cluster != "" {
			awscli.RunSigned(t.Endpoint, t.Region, //nolint:errcheck
				"eks", "delete-fargate-profile", "--cluster-name", cluster, "--fargate-profile-name", name)
		}
	}
	g.deleteClusterQuietly(t, "eks_fg_cluster")
	return nil
}

func (g *eksGroup) createFargateProfile(t *harness.TestContext, cluster, name string) (map[string]any, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "create-fargate-profile",
		"--cluster-name", cluster,
		"--fargate-profile-name", name,
		"--pod-execution-role-arn", eksPodExecRoleARN,
		"--subnets", "subnet-0a1b2c3d",
		"--selectors", `[{"namespace":"compat","labels":{"app":"web"}}]`,
	)
	if err != nil {
		return nil, err
	}
	fp, _ := out["fargateProfile"].(map[string]any)
	if fp == nil {
		return nil, fmt.Errorf("eks create-fargate-profile %q: no fargateProfile in response: %v", name, out)
	}
	return fp, nil
}

func (g *eksGroup) fargateProfileNames(t *harness.TestContext, cluster string) ([]string, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-fargate-profiles", "--cluster-name", cluster)
	if err != nil {
		return nil, err
	}
	return stringList(out, "fargateProfileNames", "eks list-fargate-profiles")
}

func (g *eksGroup) CreateFargateProfile(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_fg_cluster")
	if cluster == "" {
		return fmt.Errorf("eks CreateFargateProfile: no eks_fg_cluster from setup")
	}
	name := eksFargateNamer.Name(t)
	t.Set("eks_fg_profile", name)

	fp, err := g.createFargateProfile(t, cluster, name)
	if err != nil {
		return err
	}
	if got, _ := fp["fargateProfileName"].(string); got != name {
		return fmt.Errorf("eks CreateFargateProfile: fargateProfileName = %q, want %q", got, name)
	}
	arn, _ := fp["fargateProfileArn"].(string)
	if arn == "" {
		return fmt.Errorf("eks CreateFargateProfile: fargateProfileArn is empty: %v", fp)
	}
	t.Set("eks_fg_profile_arn", arn)

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-fargate-profile", "--cluster-name", cluster, "--fargate-profile-name", name)
	if err != nil {
		return fmt.Errorf("eks CreateFargateProfile: describe-fargate-profile after create failed: %w", err)
	}
	described, _ := out["fargateProfile"].(map[string]any)
	if described == nil {
		return fmt.Errorf("eks CreateFargateProfile: no fargateProfile in describe response: %v", out)
	}
	if got, _ := described["podExecutionRoleArn"].(string); got != eksPodExecRoleARN {
		return fmt.Errorf("eks CreateFargateProfile: podExecutionRoleArn = %q, want %q", got, eksPodExecRoleARN)
	}
	selectors, _ := described["selectors"].([]any)
	if len(selectors) != 1 {
		return fmt.Errorf("eks CreateFargateProfile: selectors = %v, want the one selector the create request sent", described["selectors"])
	}
	selector, _ := selectors[0].(map[string]any)
	if got, _ := selector["namespace"].(string); got != "compat" {
		return fmt.Errorf("eks CreateFargateProfile: selectors[0].namespace = %q, want compat", got)
	}
	return nil
}

func (g *eksGroup) DescribeFargateProfile(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_fg_cluster"), t.GetString("eks_fg_profile")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks DescribeFargateProfile: no profile from CreateFargateProfile")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-fargate-profile", "--cluster-name", cluster, "--fargate-profile-name", name)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeFargateProfile: the modeled URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-fargate-profile", "--cluster-name", cluster, "--fargate-profile-name", name)
	if err != nil {
		return err
	}
	fp, _ := out["fargateProfile"].(map[string]any)
	if fp == nil {
		return fmt.Errorf("eks DescribeFargateProfile: no fargateProfile in response: %v", out)
	}
	if got, _ := fp["fargateProfileName"].(string); got != name {
		return fmt.Errorf("eks DescribeFargateProfile: fargateProfileName = %q, want %q", got, name)
	}
	if got, _ := fp["fargateProfileArn"].(string); got != t.GetString("eks_fg_profile_arn") {
		return fmt.Errorf("eks DescribeFargateProfile: fargateProfileArn = %q, want the created %q", got, t.GetString("eks_fg_profile_arn"))
	}
	if got, _ := fp["clusterName"].(string); got != cluster {
		return fmt.Errorf("eks DescribeFargateProfile: clusterName = %q, want %q", got, cluster)
	}
	return nil
}

// ListFargateProfiles covers GET /clusters/{name}/fargate-profiles.
func (g *eksGroup) ListFargateProfiles(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_fg_cluster"), t.GetString("eks_fg_profile")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks ListFargateProfiles: no profile from CreateFargateProfile")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-fargate-profiles", "--cluster-name", cluster)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListFargateProfiles: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	names, err := g.fargateProfileNames(t, cluster)
	if err != nil {
		return err
	}
	if !containsString(names, name) {
		return fmt.Errorf("eks ListFargateProfiles: %q missing from %v", name, names)
	}
	return nil
}

func (g *eksGroup) DescribeFargateProfileNotFound(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_fg_cluster")
	if cluster == "" {
		return fmt.Errorf("eks DescribeFargateProfileNotFound: no eks_fg_cluster from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-fargate-profile",
		"--cluster-name", cluster, "--fargate-profile-name", eksFargateNamer.Name(t)+"-nope")
	return assertEKSError(status, err, "DescribeFargateProfile", "ResourceNotFoundException", 404)
}

func (g *eksGroup) DeleteFargateProfile(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_fg_cluster")
	if cluster == "" {
		return fmt.Errorf("eks DeleteFargateProfile: no eks_fg_cluster from setup")
	}
	name := eksFargateNamer.Name(t) + "-del"
	t.Set("eks_fg_delete", name)
	if _, err := g.createFargateProfile(t, cluster, name); err != nil {
		return fmt.Errorf("eks DeleteFargateProfile: create failed: %w", err)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "delete-fargate-profile", "--cluster-name", cluster, "--fargate-profile-name", name)
	if err != nil {
		return err
	}
	fp, _ := out["fargateProfile"].(map[string]any)
	if fp == nil {
		return fmt.Errorf("eks DeleteFargateProfile: no fargateProfile in response: %v", out)
	}
	if got, _ := fp["status"].(string); got != "DELETING" {
		return fmt.Errorf("eks DeleteFargateProfile: status = %q, want DELETING", got)
	}

	var lastStatus int
	var lastErr error
	gone := false
	for attempt := 0; attempt < eksPollAttempts && !gone; attempt++ {
		lastStatus, lastErr = awscli.RunStatusSigned(t.Endpoint, t.Region,
			"eks", "describe-fargate-profile", "--cluster-name", cluster, "--fargate-profile-name", name)
		gone = lastErr != nil
	}
	if !gone {
		return fmt.Errorf("eks DeleteFargateProfile: describe-fargate-profile still returns %q after %d polls", name, eksPollAttempts)
	}
	if err := assertEKSError(lastStatus, lastErr, "DescribeFargateProfile after delete", "ResourceNotFoundException", 404); err != nil {
		return err
	}

	names, err := g.fargateProfileNames(t, cluster)
	if err != nil {
		return fmt.Errorf("eks DeleteFargateProfile: list-fargate-profiles after delete failed: %w", err)
	}
	if containsString(names, name) {
		return fmt.Errorf("eks DeleteFargateProfile: %q still listed after delete: %v", name, names)
	}
	return nil
}

// ─── eks-addons ───────────────────────────────────────────────────────────────

func (g *eksGroup) setupAddons(_ context.Context, t *harness.TestContext) error {
	return g.createClusterFor(t, eksAddonNamer.Name(t)+"-c", "eks_addon_cluster", "eks_addon_cluster_arn")
}

func (g *eksGroup) teardownAddons(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_addon_cluster")
	for _, key := range []string{"eks_addon_delete", "eks_addon"} {
		if name := t.GetString(key); name != "" && cluster != "" {
			awscli.RunSigned(t.Endpoint, t.Region, //nolint:errcheck
				"eks", "delete-addon", "--cluster-name", cluster, "--addon-name", name)
		}
	}
	g.deleteClusterQuietly(t, "eks_addon_cluster")
	return nil
}

func (g *eksGroup) describeAddon(t *harness.TestContext, cluster, name string) (map[string]any, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-addon", "--cluster-name", cluster, "--addon-name", name)
	if err != nil {
		return nil, err
	}
	addon, _ := out["addon"].(map[string]any)
	if addon == nil {
		return nil, fmt.Errorf("eks describe-addon %q: no addon in response: %v", name, out)
	}
	return addon, nil
}

func (g *eksGroup) addonNames(t *harness.TestContext, cluster string) ([]string, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-addons", "--cluster-name", cluster)
	if err != nil {
		return nil, err
	}
	return stringList(out, "addons", "eks list-addons")
}

func (g *eksGroup) CreateAddon(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_addon_cluster")
	if cluster == "" {
		return fmt.Errorf("eks CreateAddon: no eks_addon_cluster from setup")
	}
	t.Set("eks_addon", eksAddonName)

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "create-addon",
		"--cluster-name", cluster,
		"--addon-name", eksAddonName,
		"--addon-version", eksAddonVersion,
		"--configuration-values", `{"env":{"AWS_VPC_K8S_CNI_LOGLEVEL":"DEBUG"}}`,
	)
	if err != nil {
		return err
	}
	addon, _ := out["addon"].(map[string]any)
	if addon == nil {
		return fmt.Errorf("eks CreateAddon: no addon in response: %v", out)
	}
	if got, _ := addon["addonName"].(string); got != eksAddonName {
		return fmt.Errorf("eks CreateAddon: addonName = %q, want %q", got, eksAddonName)
	}
	arn, _ := addon["addonArn"].(string)
	if arn == "" {
		return fmt.Errorf("eks CreateAddon: addonArn is empty: %v", addon)
	}
	t.Set("eks_addon_arn", arn)

	described, err := g.describeAddon(t, cluster, eksAddonName)
	if err != nil {
		return fmt.Errorf("eks CreateAddon: describe-addon after create failed: %w", err)
	}
	if got, _ := described["addonVersion"].(string); got != eksAddonVersion {
		return fmt.Errorf("eks CreateAddon: addonVersion = %q, want %q", got, eksAddonVersion)
	}
	if got, _ := described["clusterName"].(string); got != cluster {
		return fmt.Errorf("eks CreateAddon: clusterName = %q, want %q", got, cluster)
	}
	return nil
}

func (g *eksGroup) DescribeAddon(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_addon_cluster"), t.GetString("eks_addon")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks DescribeAddon: no addon from CreateAddon")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-addon", "--cluster-name", cluster, "--addon-name", name)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeAddon: the modeled URI answered HTTP %d, want 200", status)
	}

	addon, err := g.describeAddon(t, cluster, name)
	if err != nil {
		return err
	}
	if got, _ := addon["addonName"].(string); got != name {
		return fmt.Errorf("eks DescribeAddon: addonName = %q, want %q", got, name)
	}
	if got, _ := addon["addonArn"].(string); got != t.GetString("eks_addon_arn") {
		return fmt.Errorf("eks DescribeAddon: addonArn = %q, want the created %q", got, t.GetString("eks_addon_arn"))
	}
	if got, _ := addon["configurationValues"].(string); !strings.Contains(got, "AWS_VPC_K8S_CNI_LOGLEVEL") {
		return fmt.Errorf("eks DescribeAddon: configurationValues = %q, want the document the create request sent", got)
	}
	return nil
}

// ListAddons covers GET /clusters/{name}/addons — the nested collection whose
// prefix /addons is also a root path of its own, for the two add-on catalog
// operations.
func (g *eksGroup) ListAddons(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_addon_cluster"), t.GetString("eks_addon")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks ListAddons: no addon from CreateAddon")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-addons", "--cluster-name", cluster)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListAddons: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	names, err := g.addonNames(t, cluster)
	if err != nil {
		return err
	}
	if !containsString(names, name) {
		return fmt.Errorf("eks ListAddons: %q missing from %v", name, names)
	}
	return nil
}

// UpdateAddon covers POST /clusters/{name}/addons/{addonName}/update, the one
// add-on URI with a verb segment after the resource.
func (g *eksGroup) UpdateAddon(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_addon_cluster"), t.GetString("eks_addon")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks UpdateAddon: no addon from CreateAddon")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "update-addon",
		"--cluster-name", cluster, "--addon-name", name,
		"--addon-version", eksAddonNewVersion,
		"--resolve-conflicts", "OVERWRITE",
	)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks UpdateAddon: no update in response: %v", out)
	}
	if got, _ := update["id"].(string); got == "" {
		return fmt.Errorf("eks UpdateAddon: update.id is empty: %v", update)
	}
	if got, _ := update["type"].(string); got != "AddonUpdate" {
		return fmt.Errorf("eks UpdateAddon: update.type = %q, want AddonUpdate", got)
	}

	addon, err := g.describeAddon(t, cluster, name)
	if err != nil {
		return fmt.Errorf("eks UpdateAddon: describe-addon after update failed: %w", err)
	}
	if got, _ := addon["addonVersion"].(string); got != eksAddonNewVersion {
		return fmt.Errorf("eks UpdateAddon: addonVersion = %q after the update, want %q", got, eksAddonNewVersion)
	}
	return nil
}

// DescribeAddonVersions covers GET /addons/supported-versions, a root
// collection whose every input is a query parameter — including the add-on
// name, which most services would have made a path segment. A service reading
// addonName from anywhere else answers with the add-ons it was asked to
// exclude, so the filter is asserted in both directions.
func (g *eksGroup) DescribeAddonVersions(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region, "eks", "describe-addon-versions")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeAddonVersions: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	all, err := g.addonCatalog(t)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return fmt.Errorf("eks DescribeAddonVersions: the add-on catalog is empty")
	}
	if _, ok := all[eksAddonName]; !ok {
		names := make([]string, 0, len(all))
		for n := range all {
			names = append(names, n)
		}
		return fmt.Errorf("eks DescribeAddonVersions: %q missing from the catalog %v", eksAddonName, names)
	}
	if len(all[eksAddonName]) == 0 {
		return fmt.Errorf("eks DescribeAddonVersions: %q has no addonVersions", eksAddonName)
	}

	filtered, err := g.addonCatalog(t, "--addon-name", eksAddonName)
	if err != nil {
		return err
	}
	if len(filtered) != 1 {
		names := make([]string, 0, len(filtered))
		for n := range filtered {
			names = append(names, n)
		}
		return fmt.Errorf("eks DescribeAddonVersions --addon-name %s: got %v, want only %q — the addonName query member is not being applied",
			eksAddonName, names, eksAddonName)
	}
	if !containsString(filtered[eksAddonName], eksAddonVersion) {
		return fmt.Errorf("eks DescribeAddonVersions --addon-name %s: versions = %v, want one to be %q",
			eksAddonName, filtered[eksAddonName], eksAddonVersion)
	}
	return nil
}

// addonCatalog returns DescribeAddonVersions' answer as addon name → versions.
func (g *eksGroup) addonCatalog(t *harness.TestContext, extra ...string) (map[string][]string, error) {
	args := append([]string{"eks", "describe-addon-versions"}, extra...)
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, err
	}
	entries, ok := out["addons"].([]any)
	if !ok {
		return nil, fmt.Errorf("eks describe-addon-versions: addons is %T, want a list: %v", out["addons"], out)
	}
	catalog := make(map[string][]string, len(entries))
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		name, _ := entry["addonName"].(string)
		if name == "" {
			return nil, fmt.Errorf("eks describe-addon-versions: an entry has no addonName: %v", entry)
		}
		versions, _ := entry["addonVersions"].([]any)
		list := make([]string, 0, len(versions))
		for _, v := range versions {
			info, _ := v.(map[string]any)
			version, _ := info["addonVersion"].(string)
			if version != "" {
				list = append(list, version)
			}
		}
		catalog[name] = list
	}
	return catalog, nil
}

// DescribeAddonConfiguration covers GET /addons/configuration-schemas, the
// second root add-on URI. Both of its inputs are @required query members.
func (g *eksGroup) DescribeAddonConfiguration(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-addon-configuration",
		"--addon-name", eksAddonName, "--addon-version", eksAddonVersion)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeAddonConfiguration: the modeled URI answered HTTP %d, want 200", status)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-addon-configuration",
		"--addon-name", eksAddonName, "--addon-version", eksAddonVersion)
	if err != nil {
		return err
	}
	if got, _ := out["addonName"].(string); got != eksAddonName {
		return fmt.Errorf("eks DescribeAddonConfiguration: addonName = %q, want %q", got, eksAddonName)
	}
	if got, _ := out["addonVersion"].(string); got != eksAddonVersion {
		return fmt.Errorf("eks DescribeAddonConfiguration: addonVersion = %q, want %q", got, eksAddonVersion)
	}
	schema, _ := out["configurationSchema"].(string)
	if !strings.Contains(schema, "$schema") {
		return fmt.Errorf("eks DescribeAddonConfiguration: configurationSchema = %q, want a JSON Schema document", schema)
	}
	return nil
}

func (g *eksGroup) DescribeAddonNotFound(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_addon_cluster")
	if cluster == "" {
		return fmt.Errorf("eks DescribeAddonNotFound: no eks_addon_cluster from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-addon", "--cluster-name", cluster, "--addon-name", eksSecondAddonName)
	return assertEKSError(status, err, "DescribeAddon", "ResourceNotFoundException", 404)
}

func (g *eksGroup) DeleteAddon(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_addon_cluster")
	if cluster == "" {
		return fmt.Errorf("eks DeleteAddon: no eks_addon_cluster from setup")
	}
	t.Set("eks_addon_delete", eksSecondAddonName)
	if err := awscli.RunSigned(t.Endpoint, t.Region,
		"eks", "create-addon", "--cluster-name", cluster, "--addon-name", eksSecondAddonName,
	); err != nil {
		return fmt.Errorf("eks DeleteAddon: create failed: %w", err)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "delete-addon", "--cluster-name", cluster, "--addon-name", eksSecondAddonName)
	if err != nil {
		return err
	}
	addon, _ := out["addon"].(map[string]any)
	if addon == nil {
		return fmt.Errorf("eks DeleteAddon: no addon in response: %v", out)
	}
	if got, _ := addon["status"].(string); got != "DELETING" {
		return fmt.Errorf("eks DeleteAddon: status = %q, want DELETING", got)
	}

	var lastStatus int
	var lastErr error
	gone := false
	for attempt := 0; attempt < eksPollAttempts && !gone; attempt++ {
		lastStatus, lastErr = awscli.RunStatusSigned(t.Endpoint, t.Region,
			"eks", "describe-addon", "--cluster-name", cluster, "--addon-name", eksSecondAddonName)
		gone = lastErr != nil
	}
	if !gone {
		return fmt.Errorf("eks DeleteAddon: describe-addon still returns %q after %d polls", eksSecondAddonName, eksPollAttempts)
	}
	if err := assertEKSError(lastStatus, lastErr, "DescribeAddon after delete", "ResourceNotFoundException", 404); err != nil {
		return err
	}

	names, err := g.addonNames(t, cluster)
	if err != nil {
		return fmt.Errorf("eks DeleteAddon: list-addons after delete failed: %w", err)
	}
	if containsString(names, eksSecondAddonName) {
		return fmt.Errorf("eks DeleteAddon: %q still listed after delete: %v", eksSecondAddonName, names)
	}
	return nil
}

// ─── eks-cluster-access ───────────────────────────────────────────────────────

func (g *eksGroup) setupClusterAccess(_ context.Context, t *harness.TestContext) error {
	if err := g.createClusterFor(t, eksAccessNamer.Name(t)+"-c", "eks_ac_cluster", "eks_ac_cluster_arn"); err != nil {
		return err
	}
	t.Set("eks_ac_principal", "arn:aws:iam::000000000000:role/"+eksAccessNamer.Name(t))
	t.Set("eks_ac_idp", eksAccessNamer.Name(t)+"-oidc")
	return nil
}

func (g *eksGroup) teardownClusterAccess(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("eks_ac_cluster")
	principal := t.GetString("eks_ac_principal")
	if cluster != "" && principal != "" {
		// Reverse creation order: the policy association before the entry it
		// hangs off, then the entry, then the cluster.
		awscli.RunSigned(t.Endpoint, t.Region, //nolint:errcheck
			"eks", "disassociate-access-policy",
			"--cluster-name", cluster, "--principal-arn", principal, "--policy-arn", eksViewPolicyARN)
		awscli.RunSigned(t.Endpoint, t.Region, //nolint:errcheck
			"eks", "delete-access-entry", "--cluster-name", cluster, "--principal-arn", principal)
	}
	if name := t.GetString("eks_ac_idp"); name != "" && cluster != "" {
		awscli.RunSigned(t.Endpoint, t.Region, //nolint:errcheck
			"eks", "disassociate-identity-provider-config",
			"--cluster-name", cluster, "--identity-provider-config", "type=oidc,name="+name)
	}
	g.deleteClusterQuietly(t, "eks_ac_cluster")
	return nil
}

func (g *eksGroup) describeAccessEntry(t *harness.TestContext, cluster, principal string) (map[string]any, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-access-entry", "--cluster-name", cluster, "--principal-arn", principal)
	if err != nil {
		return nil, err
	}
	entry, _ := out["accessEntry"].(map[string]any)
	if entry == nil {
		return nil, fmt.Errorf("eks describe-access-entry %q: no accessEntry in response: %v", principal, out)
	}
	return entry, nil
}

func (g *eksGroup) accessEntryARNs(t *harness.TestContext, cluster string) ([]string, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-access-entries", "--cluster-name", cluster)
	if err != nil {
		return nil, err
	}
	return stringList(out, "accessEntries", "eks list-access-entries")
}

// CreateAccessEntry creates the entry the rest of the group reads. Its
// principal ARN becomes a path label on every later call, URL-escaped into a
// single segment, which is a second way this URI space can be got wrong.
func (g *eksGroup) CreateAccessEntry(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks CreateAccessEntry: no cluster or principal from setup")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "create-access-entry",
		"--cluster-name", cluster,
		"--principal-arn", principal,
		"--username", "compat-user",
		"--kubernetes-groups", "compat-group",
		"--type", "STANDARD",
	)
	if err != nil {
		return err
	}
	entry, _ := out["accessEntry"].(map[string]any)
	if entry == nil {
		return fmt.Errorf("eks CreateAccessEntry: no accessEntry in response: %v", out)
	}
	if got, _ := entry["principalArn"].(string); got != principal {
		return fmt.Errorf("eks CreateAccessEntry: principalArn = %q, want %q", got, principal)
	}
	if got, _ := entry["type"].(string); got != "STANDARD" {
		return fmt.Errorf("eks CreateAccessEntry: type = %q, want STANDARD", got)
	}

	described, err := g.describeAccessEntry(t, cluster, principal)
	if err != nil {
		return fmt.Errorf("eks CreateAccessEntry: describe-access-entry after create failed: %w", err)
	}
	if got, _ := described["username"].(string); got != "compat-user" {
		return fmt.Errorf("eks CreateAccessEntry: username = %q, want compat-user", got)
	}
	groups, err := stringList(described, "kubernetesGroups", "eks describe-access-entry")
	if err != nil {
		return fmt.Errorf("eks CreateAccessEntry: %w", err)
	}
	if !containsString(groups, "compat-group") {
		return fmt.Errorf("eks CreateAccessEntry: kubernetesGroups = %v, want compat-group", groups)
	}
	return nil
}

func (g *eksGroup) DescribeAccessEntry(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks DescribeAccessEntry: no cluster or principal from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-access-entry", "--cluster-name", cluster, "--principal-arn", principal)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks DescribeAccessEntry: the modeled URI answered HTTP %d, want 200", status)
	}

	entry, err := g.describeAccessEntry(t, cluster, principal)
	if err != nil {
		return err
	}
	if got, _ := entry["principalArn"].(string); got != principal {
		return fmt.Errorf("eks DescribeAccessEntry: principalArn = %q, want %q", got, principal)
	}
	if got, _ := entry["clusterName"].(string); got != cluster {
		return fmt.Errorf("eks DescribeAccessEntry: clusterName = %q, want %q", got, cluster)
	}
	return nil
}

// ListAccessEntries covers GET /clusters/{name}/access-entries.
func (g *eksGroup) ListAccessEntries(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks ListAccessEntries: no cluster or principal from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-access-entries", "--cluster-name", cluster)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListAccessEntries: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	arns, err := g.accessEntryARNs(t, cluster)
	if err != nil {
		return err
	}
	if !containsString(arns, principal) {
		return fmt.Errorf("eks ListAccessEntries: %q missing from %v", principal, arns)
	}
	return nil
}

// UpdateAccessEntry is a POST to the same URI DescribeAccessEntry reads with a
// GET and DeleteAccessEntry removes with a DELETE — three methods on one path,
// which is where a route registered for one method quietly hides.
func (g *eksGroup) UpdateAccessEntry(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks UpdateAccessEntry: no cluster or principal from setup")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "update-access-entry",
		"--cluster-name", cluster, "--principal-arn", principal,
		"--username", "compat-user-updated",
		"--kubernetes-groups", "compat-group", "compat-admins",
	)
	if err != nil {
		return err
	}
	if entry, _ := out["accessEntry"].(map[string]any); entry == nil {
		return fmt.Errorf("eks UpdateAccessEntry: no accessEntry in response: %v", out)
	}

	described, err := g.describeAccessEntry(t, cluster, principal)
	if err != nil {
		return fmt.Errorf("eks UpdateAccessEntry: describe-access-entry after update failed: %w", err)
	}
	if got, _ := described["username"].(string); got != "compat-user-updated" {
		return fmt.Errorf("eks UpdateAccessEntry: username = %q after the update, want compat-user-updated", got)
	}
	groups, err := stringList(described, "kubernetesGroups", "eks describe-access-entry")
	if err != nil {
		return fmt.Errorf("eks UpdateAccessEntry: %w", err)
	}
	if !containsString(groups, "compat-admins") {
		return fmt.Errorf("eks UpdateAccessEntry: kubernetesGroups = %v after the update, want compat-admins among them", groups)
	}
	return nil
}

// AssociateAccessPolicy covers the deepest URI in the EKS model:
// POST /clusters/{name}/access-entries/{principalArn}/access-policies.
func (g *eksGroup) AssociateAccessPolicy(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks AssociateAccessPolicy: no cluster or principal from setup")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "associate-access-policy",
		"--cluster-name", cluster, "--principal-arn", principal,
		"--policy-arn", eksViewPolicyARN,
		"--access-scope", `{"type":"cluster"}`,
	)
	if err != nil {
		return err
	}
	assoc, _ := out["associatedAccessPolicy"].(map[string]any)
	if assoc == nil {
		return fmt.Errorf("eks AssociateAccessPolicy: no associatedAccessPolicy in response: %v", out)
	}
	if got, _ := assoc["policyArn"].(string); got != eksViewPolicyARN {
		return fmt.Errorf("eks AssociateAccessPolicy: policyArn = %q, want %q", got, eksViewPolicyARN)
	}

	policies, err := g.associatedPolicyARNs(t, cluster, principal)
	if err != nil {
		return fmt.Errorf("eks AssociateAccessPolicy: list-associated-access-policies after associate failed: %w", err)
	}
	if !containsString(policies, eksViewPolicyARN) {
		return fmt.Errorf("eks AssociateAccessPolicy: %q missing from %v after the associate", eksViewPolicyARN, policies)
	}
	return nil
}

// associatedPolicyARNs returns the policy ARNs associated with one principal.
func (g *eksGroup) associatedPolicyARNs(t *harness.TestContext, cluster, principal string) ([]string, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-associated-access-policies",
		"--cluster-name", cluster, "--principal-arn", principal)
	if err != nil {
		return nil, err
	}
	entries, ok := out["associatedAccessPolicies"].([]any)
	if !ok && out["associatedAccessPolicies"] != nil {
		return nil, fmt.Errorf("eks list-associated-access-policies: associatedAccessPolicies is %T, want a list: %v",
			out["associatedAccessPolicies"], out)
	}
	arns := make([]string, 0, len(entries))
	for _, e := range entries {
		policy, _ := e.(map[string]any)
		arn, _ := policy["policyArn"].(string)
		if arn == "" {
			return nil, fmt.Errorf("eks list-associated-access-policies: an entry has no policyArn: %v", policy)
		}
		arns = append(arns, arn)
	}
	return arns, nil
}

// ListAssociatedAccessPolicies covers the GET on the deepest collection URI —
// the same path AssociateAccessPolicy posts to.
func (g *eksGroup) ListAssociatedAccessPolicies(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks ListAssociatedAccessPolicies: no cluster or principal from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-associated-access-policies",
		"--cluster-name", cluster, "--principal-arn", principal)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListAssociatedAccessPolicies: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-associated-access-policies",
		"--cluster-name", cluster, "--principal-arn", principal)
	if err != nil {
		return err
	}
	entries, _ := out["associatedAccessPolicies"].([]any)
	var found map[string]any
	for _, e := range entries {
		policy, _ := e.(map[string]any)
		if arn, _ := policy["policyArn"].(string); arn == eksViewPolicyARN {
			found = policy
		}
	}
	if found == nil {
		return fmt.Errorf("eks ListAssociatedAccessPolicies: %q missing from %v", eksViewPolicyARN, out["associatedAccessPolicies"])
	}
	scope, _ := found["accessScope"].(map[string]any)
	if got, _ := scope["type"].(string); got != "cluster" {
		return fmt.Errorf("eks ListAssociatedAccessPolicies: accessScope.type = %q, want the cluster scope the associate sent", got)
	}
	return nil
}

func (g *eksGroup) DisassociateAccessPolicy(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks DisassociateAccessPolicy: no cluster or principal from setup")
	}
	if err := awscli.RunSigned(t.Endpoint, t.Region,
		"eks", "disassociate-access-policy",
		"--cluster-name", cluster, "--principal-arn", principal, "--policy-arn", eksViewPolicyARN,
	); err != nil {
		return err
	}
	policies, err := g.associatedPolicyARNs(t, cluster, principal)
	if err != nil {
		return fmt.Errorf("eks DisassociateAccessPolicy: list-associated-access-policies after disassociate failed: %w", err)
	}
	if containsString(policies, eksViewPolicyARN) {
		return fmt.Errorf("eks DisassociateAccessPolicy: %q still associated after the disassociate: %v", eksViewPolicyARN, policies)
	}
	return nil
}

// ListAccessPolicies covers GET /access-policies, a root collection URI that
// belongs to no cluster and takes no input.
func (g *eksGroup) ListAccessPolicies(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region, "eks", "list-access-policies")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListAccessPolicies: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "eks", "list-access-policies")
	if err != nil {
		return err
	}
	entries, ok := out["accessPolicies"].([]any)
	if !ok {
		return fmt.Errorf("eks ListAccessPolicies: accessPolicies is %T, want a list: %v", out["accessPolicies"], out)
	}
	if len(entries) == 0 {
		return fmt.Errorf("eks ListAccessPolicies: no managed access policies returned")
	}
	for _, e := range entries {
		policy, _ := e.(map[string]any)
		if name, _ := policy["name"].(string); name == "" {
			return fmt.Errorf("eks ListAccessPolicies: an entry has no name: %v", policy)
		}
		if arn, _ := policy["arn"].(string); arn == "" {
			return fmt.Errorf("eks ListAccessPolicies: an entry has no arn: %v", policy)
		}
	}
	return nil
}

func (g *eksGroup) DeleteAccessEntry(_ context.Context, t *harness.TestContext) error {
	cluster, principal := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_principal")
	if cluster == "" || principal == "" {
		return fmt.Errorf("eks DeleteAccessEntry: no cluster or principal from setup")
	}
	if err := awscli.RunSigned(t.Endpoint, t.Region,
		"eks", "delete-access-entry", "--cluster-name", cluster, "--principal-arn", principal,
	); err != nil {
		return err
	}

	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "describe-access-entry", "--cluster-name", cluster, "--principal-arn", principal)
	if err := assertEKSError(status, err, "DescribeAccessEntry after delete", "ResourceNotFoundException", 404); err != nil {
		return err
	}

	arns, err := g.accessEntryARNs(t, cluster)
	if err != nil {
		return fmt.Errorf("eks DeleteAccessEntry: list-access-entries after delete failed: %w", err)
	}
	if containsString(arns, principal) {
		return fmt.Errorf("eks DeleteAccessEntry: %q still listed after delete: %v", principal, arns)
	}
	return nil
}

// AssociateIdentityProviderConfig covers
// POST /clusters/{name}/identity-provider-configs/associate — a verb segment
// under a collection, and one of three sibling paths (associate, describe,
// disassociate) that differ only in their last segment.
func (g *eksGroup) AssociateIdentityProviderConfig(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_idp")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks AssociateIdentityProviderConfig: no cluster or config name from setup")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "associate-identity-provider-config",
		"--cluster-name", cluster,
		"--oidc", fmt.Sprintf(`{"identityProviderConfigName":%q,"issuerUrl":"https://oidc.compat.example","clientId":"compat-client"}`, name),
	)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks AssociateIdentityProviderConfig: no update in response: %v", out)
	}
	if got, _ := update["id"].(string); got == "" {
		return fmt.Errorf("eks AssociateIdentityProviderConfig: update.id is empty: %v", update)
	}
	if got, _ := update["type"].(string); got != "AssociateIdentityProviderConfig" {
		return fmt.Errorf("eks AssociateIdentityProviderConfig: update.type = %q, want AssociateIdentityProviderConfig", got)
	}

	configs, err := g.identityProviderConfigNames(t, cluster)
	if err != nil {
		return fmt.Errorf("eks AssociateIdentityProviderConfig: list-identity-provider-configs after associate failed: %w", err)
	}
	if !containsString(configs, name) {
		return fmt.Errorf("eks AssociateIdentityProviderConfig: %q missing from %v after the associate", name, configs)
	}
	return nil
}

// identityProviderConfigNames returns the names of a cluster's oidc configs.
func (g *eksGroup) identityProviderConfigNames(t *harness.TestContext, cluster string) ([]string, error) {
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "list-identity-provider-configs", "--cluster-name", cluster)
	if err != nil {
		return nil, err
	}
	entries, ok := out["identityProviderConfigs"].([]any)
	if !ok && out["identityProviderConfigs"] != nil {
		return nil, fmt.Errorf("eks list-identity-provider-configs: identityProviderConfigs is %T, want a list: %v",
			out["identityProviderConfigs"], out)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		cfg, _ := e.(map[string]any)
		name, _ := cfg["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("eks list-identity-provider-configs: an entry has no name: %v", cfg)
		}
		if got, _ := cfg["type"].(string); got != "oidc" {
			return nil, fmt.Errorf("eks list-identity-provider-configs: entry %q has type %q, want oidc", name, got)
		}
		names = append(names, name)
	}
	return names, nil
}

// ListIdentityProviderConfigs covers GET /clusters/{name}/identity-provider-configs.
func (g *eksGroup) ListIdentityProviderConfigs(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_idp")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks ListIdentityProviderConfigs: no cluster or config name from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-identity-provider-configs", "--cluster-name", cluster)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListIdentityProviderConfigs: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	names, err := g.identityProviderConfigNames(t, cluster)
	if err != nil {
		return err
	}
	if !containsString(names, name) {
		return fmt.Errorf("eks ListIdentityProviderConfigs: %q missing from %v", name, names)
	}
	return nil
}

// DescribeIdentityProviderConfig is a POST, not a GET, and identifies the
// config by a body member rather than a path label — the config is returned
// nested under its provider type rather than flat.
func (g *eksGroup) DescribeIdentityProviderConfig(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_idp")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks DescribeIdentityProviderConfig: no cluster or config name from setup")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "describe-identity-provider-config",
		"--cluster-name", cluster,
		"--identity-provider-config", "type=oidc,name="+name,
	)
	if err != nil {
		return err
	}
	cfg, _ := out["identityProviderConfig"].(map[string]any)
	if cfg == nil {
		return fmt.Errorf("eks DescribeIdentityProviderConfig: no identityProviderConfig in response: %v", out)
	}
	oidc, _ := cfg["oidc"].(map[string]any)
	if oidc == nil {
		return fmt.Errorf("eks DescribeIdentityProviderConfig: identityProviderConfig has no oidc member: %v", cfg)
	}
	if got, _ := oidc["identityProviderConfigName"].(string); got != name {
		return fmt.Errorf("eks DescribeIdentityProviderConfig: identityProviderConfigName = %q, want %q", got, name)
	}
	if got, _ := oidc["issuerUrl"].(string); got != "https://oidc.compat.example" {
		return fmt.Errorf("eks DescribeIdentityProviderConfig: issuerUrl = %q, want the one the associate sent", got)
	}
	if got, _ := oidc["clientId"].(string); got != "compat-client" {
		return fmt.Errorf("eks DescribeIdentityProviderConfig: clientId = %q, want compat-client", got)
	}
	if got, _ := oidc["identityProviderConfigArn"].(string); got == "" {
		return fmt.Errorf("eks DescribeIdentityProviderConfig: identityProviderConfigArn is empty: %v", oidc)
	}
	return nil
}

func (g *eksGroup) DisassociateIdentityProviderConfig(_ context.Context, t *harness.TestContext) error {
	cluster, name := t.GetString("eks_ac_cluster"), t.GetString("eks_ac_idp")
	if cluster == "" || name == "" {
		return fmt.Errorf("eks DisassociateIdentityProviderConfig: no cluster or config name from setup")
	}
	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region,
		"eks", "disassociate-identity-provider-config",
		"--cluster-name", cluster,
		"--identity-provider-config", "type=oidc,name="+name,
	)
	if err != nil {
		return err
	}
	update, _ := out["update"].(map[string]any)
	if update == nil {
		return fmt.Errorf("eks DisassociateIdentityProviderConfig: no update in response: %v", out)
	}
	if got, _ := update["type"].(string); got != "DisassociateIdentityProviderConfig" {
		return fmt.Errorf("eks DisassociateIdentityProviderConfig: update.type = %q, want DisassociateIdentityProviderConfig", got)
	}

	names, err := g.identityProviderConfigNames(t, cluster)
	if err != nil {
		return fmt.Errorf("eks DisassociateIdentityProviderConfig: list-identity-provider-configs after disassociate failed: %w", err)
	}
	if containsString(names, name) {
		return fmt.Errorf("eks DisassociateIdentityProviderConfig: %q still listed after the disassociate: %v", name, names)
	}
	return nil
}

// ─── eks-tags ─────────────────────────────────────────────────────────────────

func (g *eksGroup) setupTags(_ context.Context, t *harness.TestContext) error {
	return g.createClusterFor(t, eksTagNamer.Name(t), "eks_tag_cluster", "eks_tag_arn")
}

func (g *eksGroup) teardownTags(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order. DeleteCluster drops the cluster's tags with it.
	g.deleteClusterQuietly(t, "eks_inline_tag_cluster")
	g.deleteClusterQuietly(t, "eks_tag_cluster")
	return nil
}

// TagResource covers POST /tags/{resourceArn}. Six groups declare a test of
// this name, so the implementation key is group-qualified; a bare key claimed
// by two groups is refused by every suite loader.
//
// The /tags path space is shared — Pipes, Scheduler, AppConfig and API Gateway
// all bind it — so Overcast dispatches it on the ARN's own service prefix
// rather than on the route. That makes the tag ARN the thing under test as
// much as the URI: an EKS ARN reaching another service's tag store would show
// up here as a tag that does not read back.
func (g *eksGroup) TagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("eks_tag_arn")
	if arn == "" {
		return fmt.Errorf("eks TagResource: no eks_tag_arn from setup")
	}
	if err := awscli.RunSigned(t.Endpoint, t.Region,
		"eks", "tag-resource", "--resource-arn", arn, "--tags", `{"team":"platform","env":"compat"}`,
	); err != nil {
		return err
	}
	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("eks TagResource: list-tags-for-resource after tag failed: %w", err)
	}
	if tags["team"] != "platform" {
		return fmt.Errorf("eks TagResource: tag team = %q, want platform (tags: %v)", tags["team"], tags)
	}
	if tags["env"] != "compat" {
		return fmt.Errorf("eks TagResource: tag env = %q, want compat (tags: %v)", tags["env"], tags)
	}
	return nil
}

// ListTagsForResource covers GET /tags/{resourceArn}.
func (g *eksGroup) ListTagsForResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("eks_tag_arn")
	if arn == "" {
		return fmt.Errorf("eks ListTagsForResource: no eks_tag_arn from setup")
	}
	status, err := awscli.RunStatusSigned(t.Endpoint, t.Region,
		"eks", "list-tags-for-resource", "--resource-arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("eks ListTagsForResource: the tags URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return err
	}
	if len(tags) != 2 || tags["team"] != "platform" || tags["env"] != "compat" {
		return fmt.Errorf("eks ListTagsForResource: tags = %v, want team=platform and env=compat from TagResource", tags)
	}
	return nil
}

// UntagResource drops one key and leaves the other, so a handler that
// clears the whole tag set fails here rather than passing on "no error".
func (g *eksGroup) UntagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("eks_tag_arn")
	if arn == "" {
		return fmt.Errorf("eks UntagResource: no eks_tag_arn from setup")
	}
	if err := awscli.RunSigned(t.Endpoint, t.Region,
		"eks", "untag-resource", "--resource-arn", arn, "--tag-keys", "env",
	); err != nil {
		return err
	}
	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("eks UntagResource: list-tags-for-resource after untag failed: %w", err)
	}
	if _, still := tags["env"]; still {
		return fmt.Errorf("eks UntagResource: tag env is still present after removal (tags: %v)", tags)
	}
	if tags["team"] != "platform" {
		return fmt.Errorf("eks UntagResource: tag team = %q, want platform — removing env took the other tag with it (tags: %v)",
			tags["team"], tags)
	}
	return nil
}

// CreateClusterWithTags covers the tags member CreateCluster accepts inline,
// which is a second, separate path into the tag store from TagResource. The
// tags must be readable both through the tagging API and inline on the
// cluster document.
func (g *eksGroup) CreateClusterWithTags(_ context.Context, t *harness.TestContext) error {
	name := eksInlineTagNamer.Name(t)
	t.Set("eks_inline_tag_cluster", name)

	cluster, err := g.createCluster(t, name, map[string]string{"owner": "compat"})
	if err != nil {
		return err
	}
	arn, _ := cluster["arn"].(string)
	if arn == "" {
		return fmt.Errorf("eks CreateClusterWithTags: no arn in response: %v", cluster)
	}

	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("eks CreateClusterWithTags: list-tags-for-resource failed: %w", err)
	}
	if tags["owner"] != "compat" {
		return fmt.Errorf("eks CreateClusterWithTags: tag owner = %q, want compat — the creation-time tags were not applied (tags: %v)",
			tags["owner"], tags)
	}

	described, err := g.describeCluster(t, name)
	if err != nil {
		return fmt.Errorf("eks CreateClusterWithTags: describe-cluster failed: %w", err)
	}
	inline, _ := described["tags"].(map[string]any)
	if got, _ := inline["owner"].(string); got != "compat" {
		return fmt.Errorf("eks CreateClusterWithTags: describe-cluster tags = %v, want owner=compat inline on the cluster", described["tags"])
	}
	return nil
}
