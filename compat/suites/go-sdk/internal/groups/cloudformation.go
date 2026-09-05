package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

func CloudFormation(c *clients.Clients) ServiceGroup {
	g := &cfnGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"cloudformation-stacks:CreateStack":      g.CreateStack,
			"cloudformation-stacks:DescribeStacks":   g.DescribeStacks,
			"cloudformation-stacks:ListStacks":       g.ListStacks,
			"cloudformation-stacks:UpdateStack":      g.UpdateStack,
			"cloudformation-stacks:DeleteStack":      g.DeleteStack,
			"cloudformation-stacks:ValidateTemplate": g.ValidateTemplate,

			"cloudformation-eks-refs:CreateStackWithEksRefs":            g.CreateStackWithEksRefs,
			"cloudformation-eks-refs:DescribeStackResourcesPhysicalIds": g.DescribeStackResourcesPhysicalIds,
			"cloudformation-eks-refs:GetAttClusterArn":                  g.GetAttClusterArn,
			"cloudformation-eks-refs:RefNodegroupIsClusterSlashName":    g.RefNodegroupIsClusterSlashName,
			"cloudformation-eks-refs:RefAddonIsClusterPipeAddon":        g.RefAddonIsClusterPipeAddon,
			"cloudformation-eks-refs:DeleteStackWithEksRefs":            g.DeleteStackWithEksRefs,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"cloudformation-stacks":   g.setupStacks,
			"cloudformation-eks-refs": g.setupEksRefs,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"cloudformation-stacks":   g.teardownStacks,
			"cloudformation-eks-refs": g.teardownEksRefs,
		},
	}
}

type cfnGroup struct{ c *clients.Clients }

func (g *cfnGroup) cl() *cloudformation.Client { return g.c.CloudFormation() }

func (g *cfnGroup) setupStacks(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *cfnGroup) teardownStacks(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("cfn_stack_name"); name != "" {
		g.cl().DeleteStack(ctx, &cloudformation.DeleteStackInput{StackName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *cfnGroup) CreateStack(ctx context.Context, t *harness.TestContext) error {
	stackName := fmt.Sprintf("compat-%s", t.RunID)
	tpl, _ := json.Marshal(map[string]interface{}{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": map[string]interface{}{
			"DummyBucket": map[string]interface{}{"Type": "AWS::S3::Bucket"},
		},
	})
	resp, err := g.cl().CreateStack(ctx, &cloudformation.CreateStackInput{
		StackName:    aws.String(stackName),
		TemplateBody: aws.String(string(tpl)),
	})
	if err != nil {
		return err
	}
	if resp.StackId == nil {
		return fmt.Errorf("CreateStack: missing StackId")
	}
	t.Set("cfn_stack_name", stackName)
	return nil
}

func (g *cfnGroup) DescribeStacks(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("DescribeStacks: no stack from CreateStack")
	}
	resp, err := g.cl().DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		return err
	}
	if len(resp.Stacks) == 0 {
		return fmt.Errorf("DescribeStacks: no stacks returned")
	}
	return nil
}

// listStackNames returns the stack names in a ListStacks response, optionally
// filtered by status. It also checks the invariant a status filter has to
// hold: every summary returned is in one of the requested statuses.
func (g *cfnGroup) listStackNames(ctx context.Context, statuses ...cftypes.StackStatus) ([]string, error) {
	resp, err := g.cl().ListStacks(ctx, &cloudformation.ListStacksInput{StackStatusFilter: statuses})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.StackSummaries))
	for _, s := range resp.StackSummaries {
		if len(statuses) > 0 && !slices.Contains(statuses, s.StackStatus) {
			return nil, fmt.Errorf("ListStacks: %s returned with status %s, not in filter %v",
				aws.ToString(s.StackName), s.StackStatus, statuses)
		}
		names = append(names, aws.ToString(s.StackName))
	}
	return names, nil
}

// ListStacks covers StackStatusFilter in both directions: a filter naming the
// stack's status must include it, and one naming a status it cannot hold must
// exclude it. Only the second catches an implementation that accepts the
// parameter and ignores it.
//
// The statuses are the ones a stack created in this group can legitimately be
// in — the suite does not wait for CREATE_COMPLETE — and DELETE_FAILED is one
// it cannot reach, since nothing has tried to delete it yet.
func (g *cfnGroup) ListStacks(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("ListStacks: no stack from CreateStack")
	}

	all, err := g.listStackNames(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(all, stackName) {
		return fmt.Errorf("ListStacks: %s not in unfiltered listing %v", stackName, all)
	}

	active, err := g.listStackNames(ctx,
		cftypes.StackStatusCreateComplete, cftypes.StackStatusCreateInProgress)
	if err != nil {
		return err
	}
	if !slices.Contains(active, stackName) {
		return fmt.Errorf("ListStacks: %s not in CREATE_COMPLETE/CREATE_IN_PROGRESS listing %v", stackName, active)
	}

	deleteFailed, err := g.listStackNames(ctx, cftypes.StackStatusDeleteFailed)
	if err != nil {
		return err
	}
	if slices.Contains(deleteFailed, stackName) {
		return fmt.Errorf("ListStacks: %s returned by a DELETE_FAILED filter", stackName)
	}
	return nil
}

// UpdateStack waits for the create to finish before updating. CreateStack is
// asynchronous — it returns a StackId with the stack still CREATE_IN_PROGRESS —
// and CloudFormation refuses an update to a stack that is mid-operation, so
// updating straight after creating is a race the suite would lose against real
// AWS as readily as against Overcast. The java-sdk suite waits the same way.
func (g *cfnGroup) UpdateStack(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("UpdateStack: no stack from CreateStack")
	}
	if err := cloudformation.NewStackCreateCompleteWaiter(g.cl()).Wait(ctx,
		&cloudformation.DescribeStacksInput{StackName: aws.String(stackName)},
		60*time.Second); err != nil {
		return fmt.Errorf("UpdateStack: waiting for %s to finish creating: %w", stackName, err)
	}
	tpl, _ := json.Marshal(map[string]interface{}{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": map[string]interface{}{
			"DummyBucket": map[string]interface{}{"Type": "AWS::S3::Bucket"},
		},
	})
	_, err := g.cl().UpdateStack(ctx, &cloudformation.UpdateStackInput{
		StackName:    aws.String(stackName),
		TemplateBody: aws.String(string(tpl)),
	})
	return err
}

// DeleteStack exercises delete on its own stack rather than the group's
// shared one. UpdateStack runs against the shared stack and, as on AWS, a
// stack that has finished deleting cannot be updated — deleting the shared
// stack here would make the group's outcome depend on test order. Mirrors the
// cli suite's shape.
func (g *cfnGroup) DeleteStack(ctx context.Context, t *harness.TestContext) error {
	stackName := fmt.Sprintf("compat-del-%s", t.RunID)
	tpl, _ := json.Marshal(map[string]interface{}{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": map[string]interface{}{
			"DummyBucket": map[string]interface{}{"Type": "AWS::S3::Bucket"},
		},
	})
	// Create is scaffolding, not the operation under test; DeleteStack
	// succeeds on AWS even for a stack that failed to create.
	_, _ = g.cl().CreateStack(ctx, &cloudformation.CreateStackInput{
		StackName:    aws.String(stackName),
		TemplateBody: aws.String(string(tpl)),
	})
	_, err := g.cl().DeleteStack(ctx, &cloudformation.DeleteStackInput{
		StackName: aws.String(stackName),
	})
	return err
}

func (g *cfnGroup) ValidateTemplate(ctx context.Context, t *harness.TestContext) error {
	tpl, _ := json.Marshal(map[string]interface{}{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": map[string]interface{}{
			"Bucket": map[string]interface{}{"Type": "AWS::S3::Bucket"},
		},
	})
	_, err := g.cl().ValidateTemplate(ctx, &cloudformation.ValidateTemplateInput{
		TemplateBody: aws.String(string(tpl)),
	})
	return err
}

// ── cloudformation-eks-refs ───────────────────────────────────────────────

// CloudFormation documents Ref and Fn::GetAtt separately for every resource
// type, and the EKS family disagrees with itself in ways only a deployed stack
// reveals: Ref on a Cluster is its name and the ARN is a distinct Arn
// attribute, Ref on a Nodegroup is "<cluster>/<nodegroup>", and Ref on an Addon
// is "<cluster>|<addon>" — a pipe, not a slash.
//
//	https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-cluster.html#aws-resource-eks-cluster-return-values
//	https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-nodegroup.html#aws-resource-eks-nodegroup-return-values
//	https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-addon.html#aws-resource-eks-addon-return-values
//
// Overcast returned the cluster ARN from Ref (#1690) and separated the add-on
// with "/" (#1692). Both were wire-observable and neither was caught here, so
// this group asserts the values through CloudFormation alone — stack outputs
// and DescribeStackResources — which is what a template author actually sees.
const (
	// vpc-cni is one of the add-ons DescribeAddonVersions publishes. AWS
	// refuses an AddonName it does not know, so the name is not arbitrary.
	eksRefsAddonName = "vpc-cni"

	eksRefsClusterRoleARN = "arn:aws:iam::000000000000:role/compat-eks-refs-cluster"
	eksRefsNodeRoleARN    = "arn:aws:iam::000000000000:role/compat-eks-refs-node"
	eksRefsSubnetID       = "subnet-1"
)

// eksRefsTemplate wires the Nodegroup and the Addon to their cluster purely by
// {"Ref": "Cluster"} — no literal cluster name and no DependsOn. That is the
// point: CloudFormation must hand CreateNodegroup and CreateAddon the cluster
// *name*, so a Ref that resolves to the ARN fails the stack outright.
func eksRefsTemplate(clusterName, nodegroupName string) string {
	tpl, _ := json.Marshal(map[string]interface{}{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": map[string]interface{}{
			"Cluster": map[string]interface{}{
				"Type": "AWS::EKS::Cluster",
				"Properties": map[string]interface{}{
					"Name":               clusterName,
					"RoleArn":            eksRefsClusterRoleARN,
					"ResourcesVpcConfig": map[string]interface{}{"SubnetIds": []string{eksRefsSubnetID}},
				},
			},
			"Nodegroup": map[string]interface{}{
				"Type": "AWS::EKS::Nodegroup",
				"Properties": map[string]interface{}{
					"ClusterName":   map[string]interface{}{"Ref": "Cluster"},
					"NodegroupName": nodegroupName,
					"NodeRole":      eksRefsNodeRoleARN,
					"Subnets":       []string{eksRefsSubnetID},
				},
			},
			"Addon": map[string]interface{}{
				"Type": "AWS::EKS::Addon",
				"Properties": map[string]interface{}{
					"ClusterName": map[string]interface{}{"Ref": "Cluster"},
					"AddonName":   eksRefsAddonName,
				},
			},
		},
		"Outputs": map[string]interface{}{
			"ClusterRef":   map[string]interface{}{"Value": map[string]interface{}{"Ref": "Cluster"}},
			"ClusterArn":   map[string]interface{}{"Value": map[string]interface{}{"Fn::GetAtt": []string{"Cluster", "Arn"}}},
			"NodegroupRef": map[string]interface{}{"Value": map[string]interface{}{"Ref": "Nodegroup"}},
			"AddonRef":     map[string]interface{}{"Value": map[string]interface{}{"Ref": "Addon"}},
		},
	})
	return string(tpl)
}

func (g *cfnGroup) setupEksRefs(_ context.Context, t *harness.TestContext) error {
	t.Set("cfn_eks_stack_name", "compat-eks-refs-"+t.RunID)
	t.Set("cfn_eks_cluster_name", "compat-eks-refs-cluster-"+t.RunID)
	t.Set("cfn_eks_nodegroup_name", "compat-eks-refs-ng-"+t.RunID)
	return nil
}

// teardownEksRefs deletes the stack, which cascades to the cluster, the
// nodegroup and the add-on: CloudFormation owns every resource this group
// creates, and deleting a stack deletes the resources it owns.
func (g *cfnGroup) teardownEksRefs(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("cfn_eks_stack_name"); name != "" {
		g.cl().DeleteStack(ctx, &cloudformation.DeleteStackInput{StackName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *cfnGroup) CreateStackWithEksRefs(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_eks_stack_name")
	resp, err := g.cl().CreateStack(ctx, &cloudformation.CreateStackInput{
		StackName: aws.String(stackName),
		TemplateBody: aws.String(eksRefsTemplate(
			t.GetString("cfn_eks_cluster_name"), t.GetString("cfn_eks_nodegroup_name"))),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.StackId) == "" {
		return fmt.Errorf("CreateStack: missing StackId")
	}
	t.Set("cfn_eks_stack_id", aws.ToString(resp.StackId))

	// Outputs and physical resource IDs only exist once the stack settles.
	if err := cloudformation.NewStackCreateCompleteWaiter(g.cl()).Wait(ctx,
		&cloudformation.DescribeStacksInput{StackName: aws.String(stackName)},
		120*time.Second); err != nil {
		return fmt.Errorf("CreateStack: waiting for %s: %w", stackName, err)
	}
	stack, err := g.describeEksRefsStack(ctx, stackName)
	if err != nil {
		return err
	}
	if stack.StackStatus != cftypes.StackStatusCreateComplete {
		return fmt.Errorf("CreateStack: %s is %s (%s), want CREATE_COMPLETE",
			stackName, stack.StackStatus, aws.ToString(stack.StackStatusReason))
	}
	return nil
}

// DescribeStackResourcesPhysicalIds asserts the physical ID CloudFormation
// recorded for each resource. The physical ID is what Ref resolves to, so this
// is the same contract the output assertions below check, seen through the API
// an operator reaches for when a stack misbehaves.
func (g *cfnGroup) DescribeStackResourcesPhysicalIds(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_eks_stack_name")
	if stackName == "" {
		return fmt.Errorf("DescribeStackResources: no stack from CreateStackWithEksRefs")
	}
	resp, err := g.cl().DescribeStackResources(ctx, &cloudformation.DescribeStackResourcesInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		return err
	}
	physicalIDs := make(map[string]string, len(resp.StackResources))
	for _, r := range resp.StackResources {
		physicalIDs[aws.ToString(r.LogicalResourceId)] = aws.ToString(r.PhysicalResourceId)
	}
	want := eksRefsExpectedIDs(t.GetString("cfn_eks_cluster_name"), t.GetString("cfn_eks_nodegroup_name"))
	for _, logicalID := range []string{"Cluster", "Nodegroup", "Addon"} {
		if got := physicalIDs[logicalID]; got != want[logicalID] {
			return fmt.Errorf("DescribeStackResources: %s PhysicalResourceId = %q, want %q",
				logicalID, got, want[logicalID])
		}
	}
	return nil
}

// eksRefsExpectedIDs is the AWS-documented Ref value — and so the physical
// resource ID — of each resource in the template.
func eksRefsExpectedIDs(clusterName, nodegroupName string) map[string]string {
	return map[string]string{
		"Cluster":   clusterName,
		"Nodegroup": clusterName + "/" + nodegroupName,
		"Addon":     clusterName + "|" + eksRefsAddonName,
	}
}

// GetAttClusterArn covers the divergence AWS documents for AWS::EKS::Cluster:
// Ref is the cluster name, and the ARN is reachable only as the Arn attribute.
// Asserting both together is what makes the pair meaningful — either value
// alone looks plausible on its own.
func (g *cfnGroup) GetAttClusterArn(ctx context.Context, t *harness.TestContext) error {
	outputs, err := g.eksRefsOutputs(ctx, t)
	if err != nil {
		return err
	}
	cluster := t.GetString("cfn_eks_cluster_name")
	if got := outputs["ClusterRef"]; got != cluster {
		return fmt.Errorf("Ref on AWS::EKS::Cluster = %q, want the cluster name %q", got, cluster)
	}
	account, err := accountFromStackID(t.GetString("cfn_eks_stack_id"))
	if err != nil {
		return err
	}
	want := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", t.Region, account, cluster)
	if got := outputs["ClusterArn"]; got != want {
		return fmt.Errorf("Fn::GetAtt [Cluster, Arn] = %q, want %q", got, want)
	}
	return nil
}

func (g *cfnGroup) RefNodegroupIsClusterSlashName(ctx context.Context, t *harness.TestContext) error {
	outputs, err := g.eksRefsOutputs(ctx, t)
	if err != nil {
		return err
	}
	want := eksRefsExpectedIDs(t.GetString("cfn_eks_cluster_name"), t.GetString("cfn_eks_nodegroup_name"))["Nodegroup"]
	if got := outputs["NodegroupRef"]; got != want {
		return fmt.Errorf("Ref on AWS::EKS::Nodegroup = %q, want %q", got, want)
	}
	return nil
}

func (g *cfnGroup) RefAddonIsClusterPipeAddon(ctx context.Context, t *harness.TestContext) error {
	outputs, err := g.eksRefsOutputs(ctx, t)
	if err != nil {
		return err
	}
	want := eksRefsExpectedIDs(t.GetString("cfn_eks_cluster_name"), t.GetString("cfn_eks_nodegroup_name"))["Addon"]
	if got := outputs["AddonRef"]; got != want {
		return fmt.Errorf("Ref on AWS::EKS::Addon = %q, want %q (pipe-separated, not a slash)", got, want)
	}
	return nil
}

// DeleteStackWithEksRefs deletes the group's own stack. A deleted stack is
// readable by stack ID and gone by name — AWS documents that asymmetry — so the
// stack ID is what proves the delete finished rather than merely started.
func (g *cfnGroup) DeleteStackWithEksRefs(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_eks_stack_name")
	stackID := t.GetString("cfn_eks_stack_id")
	if stackName == "" || stackID == "" {
		return fmt.Errorf("DeleteStack: no stack from CreateStackWithEksRefs")
	}
	if _, err := g.cl().DeleteStack(ctx, &cloudformation.DeleteStackInput{
		StackName: aws.String(stackName),
	}); err != nil {
		return err
	}
	if err := cloudformation.NewStackDeleteCompleteWaiter(g.cl()).Wait(ctx,
		&cloudformation.DescribeStacksInput{StackName: aws.String(stackName)},
		120*time.Second); err != nil {
		return fmt.Errorf("DeleteStack: waiting for %s: %w", stackName, err)
	}
	stack, err := g.describeEksRefsStack(ctx, stackID)
	if err != nil {
		return err
	}
	if stack.StackStatus != cftypes.StackStatusDeleteComplete {
		return fmt.Errorf("DeleteStack: %s is %s (%s), want DELETE_COMPLETE",
			stackName, stack.StackStatus, aws.ToString(stack.StackStatusReason))
	}
	return nil
}

func (g *cfnGroup) describeEksRefsStack(ctx context.Context, nameOrID string) (cftypes.Stack, error) {
	resp, err := g.cl().DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(nameOrID),
	})
	if err != nil {
		return cftypes.Stack{}, err
	}
	if len(resp.Stacks) == 0 {
		return cftypes.Stack{}, fmt.Errorf("DescribeStacks: %s returned no stacks", nameOrID)
	}
	return resp.Stacks[0], nil
}

// eksRefsOutputs reads the stack's outputs afresh for each test rather than
// stashing them, so every assertion is made against a value the server just
// produced rather than one an earlier test cached.
func (g *cfnGroup) eksRefsOutputs(ctx context.Context, t *harness.TestContext) (map[string]string, error) {
	stackName := t.GetString("cfn_eks_stack_name")
	if stackName == "" {
		return nil, fmt.Errorf("DescribeStacks: no stack from CreateStackWithEksRefs")
	}
	stack, err := g.describeEksRefsStack(ctx, stackName)
	if err != nil {
		return nil, err
	}
	outputs := make(map[string]string, len(stack.Outputs))
	for _, o := range stack.Outputs {
		outputs[aws.ToString(o.OutputKey)] = aws.ToString(o.OutputValue)
	}
	return outputs, nil
}

// accountFromStackID pulls the account ID out of a stack ARN
// ("arn:aws:cloudformation:<region>:<account>:stack/<name>/<uuid>"), so the
// expected EKS ARN can be built without reaching for a second AWS client.
func accountFromStackID(stackID string) (string, error) {
	parts := strings.Split(stackID, ":")
	if len(parts) < 6 || parts[4] == "" {
		return "", fmt.Errorf("stack ID %q is not a stack ARN, cannot read the account ID from it", stackID)
	}
	return parts[4], nil
}
