package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// CloudFormation returns the CloudFormation service group.
func CloudFormation() ServiceGroup {
	g := &cfnCliGroup{}
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

type cfnCliGroup struct{}

func (g *cfnCliGroup) setupStacks(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *cfnCliGroup) teardownStacks(_ context.Context, t *harness.TestContext) error {
	if name := t.GetString("cfn_stack_name"); name != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudformation", "delete-stack", "--stack-name", name) //nolint:errcheck
	}
	return nil
}

func cfnTemplate() string {
	b, _ := json.Marshal(map[string]interface{}{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": map[string]interface{}{
			"DummyBucket": map[string]interface{}{"Type": "AWS::S3::Bucket"},
		},
	})
	return string(b)
}

func (g *cfnCliGroup) CreateStack(_ context.Context, t *harness.TestContext) error {
	stackName := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "create-stack",
		"--stack-name", stackName,
		"--template-body", cfnTemplate(),
	)
	if err != nil {
		return err
	}
	arn, _ := out["StackId"].(string)
	if arn == "" {
		return fmt.Errorf("CreateStack: missing StackId")
	}
	t.Set("cfn_stack_name", stackName)
	return nil
}

func (g *cfnCliGroup) DescribeStacks(_ context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("DescribeStacks: no stack from CreateStack")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "describe-stacks",
		"--stack-name", stackName,
	)
	if err != nil {
		return err
	}
	stacks, _ := out["Stacks"].([]interface{})
	if len(stacks) == 0 {
		return fmt.Errorf("DescribeStacks: no stacks returned")
	}
	return nil
}

// listStackNames returns the stack names in a list-stacks response, optionally
// filtered by status. It also checks the invariant a status filter has to
// hold: every summary returned is in one of the requested statuses.
func (g *cfnCliGroup) listStackNames(t *harness.TestContext, statuses ...string) ([]string, error) {
	args := []string{"cloudformation", "list-stacks"}
	if len(statuses) > 0 {
		args = append(args, "--stack-status-filter")
		args = append(args, statuses...)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, err
	}
	summaries, _ := out["StackSummaries"].([]interface{})
	names := make([]string, 0, len(summaries))
	for _, raw := range summaries {
		s, _ := raw.(map[string]interface{})
		name, _ := s["StackName"].(string)
		status, _ := s["StackStatus"].(string)
		if len(statuses) > 0 && !slices.Contains(statuses, status) {
			return nil, fmt.Errorf("list-stacks: %s returned with status %s, not in filter %v",
				name, status, statuses)
		}
		names = append(names, name)
	}
	return names, nil
}

// ListStacks covers --stack-status-filter in both directions: a filter naming
// the stack's status must include it, and one naming a status it cannot hold
// must exclude it. Only the second catches an implementation that accepts the
// parameter and ignores it.
//
// The statuses are the ones a stack created in this group can legitimately be
// in — the suite does not wait for CREATE_COMPLETE — and DELETE_FAILED is one
// it cannot reach, since nothing has tried to delete it yet.
func (g *cfnCliGroup) ListStacks(_ context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("ListStacks: no stack from CreateStack")
	}

	all, err := g.listStackNames(t)
	if err != nil {
		return err
	}
	if !slices.Contains(all, stackName) {
		return fmt.Errorf("list-stacks: %s not in unfiltered listing %v", stackName, all)
	}

	active, err := g.listStackNames(t, "CREATE_COMPLETE", "CREATE_IN_PROGRESS")
	if err != nil {
		return err
	}
	if !slices.Contains(active, stackName) {
		return fmt.Errorf("list-stacks: %s not in CREATE_COMPLETE/CREATE_IN_PROGRESS listing %v", stackName, active)
	}

	deleteFailed, err := g.listStackNames(t, "DELETE_FAILED")
	if err != nil {
		return err
	}
	if slices.Contains(deleteFailed, stackName) {
		return fmt.Errorf("list-stacks: %s returned by a DELETE_FAILED filter", stackName)
	}
	return nil
}

// UpdateStack waits for the create to finish before updating. create-stack is
// asynchronous — it returns a StackId with the stack still CREATE_IN_PROGRESS —
// and CloudFormation refuses an update to a stack that is mid-operation, so
// updating straight after creating is a race the suite would lose against real
// AWS as readily as against Overcast. The java-sdk suite waits the same way.
func (g *cfnCliGroup) UpdateStack(_ context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("UpdateStack: no stack from CreateStack")
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"cloudformation", "wait", "stack-create-complete",
		"--stack-name", stackName,
	); err != nil {
		return fmt.Errorf("UpdateStack: waiting for %s to finish creating: %w", stackName, err)
	}
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "update-stack",
		"--stack-name", stackName,
		"--template-body", cfnTemplate(),
		"--use-previous-template",
	)
	return err
}

func (g *cfnCliGroup) DeleteStack(_ context.Context, t *harness.TestContext) error {
	stackName := fmt.Sprintf("compat-del-%s", t.RunID)
	awscli.Run(t.Endpoint, t.Region, //nolint:errcheck
		"cloudformation", "create-stack",
		"--stack-name", stackName,
		"--template-body", cfnTemplate(),
	)
	return awscli.Run(t.Endpoint, t.Region,
		"cloudformation", "delete-stack",
		"--stack-name", stackName,
	)
}

func (g *cfnCliGroup) ValidateTemplate(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "validate-template",
		"--template-body", cfnTemplate(),
	)
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
// and describe-stack-resources — which is what a template author actually sees.
// The role, subnet and add-on names are the ones eks.go already uses.

// eksRefsTemplate wires the Nodegroup and the Addon to their cluster purely by
// {"Ref": "Cluster"} — no literal cluster name and no DependsOn. That is the
// point: CloudFormation must hand CreateNodegroup and CreateAddon the cluster
// *name*, so a Ref that resolves to the ARN fails the stack outright.
func eksRefsTemplate(clusterName, nodegroupName string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": map[string]interface{}{
			"Cluster": map[string]interface{}{
				"Type": "AWS::EKS::Cluster",
				"Properties": map[string]interface{}{
					"Name":               clusterName,
					"RoleArn":            eksClusterRoleARN,
					"ResourcesVpcConfig": map[string]interface{}{"SubnetIds": []string{eksSubnetA}},
				},
			},
			"Nodegroup": map[string]interface{}{
				"Type": "AWS::EKS::Nodegroup",
				"Properties": map[string]interface{}{
					"ClusterName":   map[string]interface{}{"Ref": "Cluster"},
					"NodegroupName": nodegroupName,
					"NodeRole":      eksNodeRoleARN,
					"Subnets":       []string{eksSubnetA},
				},
			},
			"Addon": map[string]interface{}{
				"Type": "AWS::EKS::Addon",
				"Properties": map[string]interface{}{
					"ClusterName": map[string]interface{}{"Ref": "Cluster"},
					"AddonName":   eksAddonName,
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
	return string(b)
}

// eksRefsExpectedIDs is the AWS-documented Ref value — and so the physical
// resource ID — of each resource in the template.
func eksRefsExpectedIDs(clusterName, nodegroupName string) map[string]string {
	return map[string]string{
		"Cluster":   clusterName,
		"Nodegroup": clusterName + "/" + nodegroupName,
		"Addon":     clusterName + "|" + eksAddonName,
	}
}

func (g *cfnCliGroup) setupEksRefs(_ context.Context, t *harness.TestContext) error {
	t.Set("cfn_eks_stack_name", "compat-eks-refs-"+t.RunID)
	t.Set("cfn_eks_cluster_name", "compat-eks-refs-cluster-"+t.RunID)
	t.Set("cfn_eks_nodegroup_name", "compat-eks-refs-ng-"+t.RunID)
	return nil
}

// teardownEksRefs deletes the stack, which cascades to the cluster, the
// nodegroup and the add-on: CloudFormation owns every resource this group
// creates, and deleting a stack deletes the resources it owns.
func (g *cfnCliGroup) teardownEksRefs(_ context.Context, t *harness.TestContext) error {
	if name := t.GetString("cfn_eks_stack_name"); name != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudformation", "delete-stack", "--stack-name", name) //nolint:errcheck
	}
	return nil
}

func (g *cfnCliGroup) CreateStackWithEksRefs(_ context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_eks_stack_name")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "create-stack",
		"--stack-name", stackName,
		"--template-body", eksRefsTemplate(
			t.GetString("cfn_eks_cluster_name"), t.GetString("cfn_eks_nodegroup_name")),
	)
	if err != nil {
		return err
	}
	stackID, _ := out["StackId"].(string)
	if stackID == "" {
		return fmt.Errorf("create-stack: missing StackId")
	}
	t.Set("cfn_eks_stack_id", stackID)

	// Outputs and physical resource IDs only exist once the stack settles.
	if err := awscli.Run(t.Endpoint, t.Region,
		"cloudformation", "wait", "stack-create-complete", "--stack-name", stackName,
	); err != nil {
		return fmt.Errorf("create-stack: waiting for %s: %w", stackName, err)
	}
	stack, err := g.describeEksRefsStack(t, stackName)
	if err != nil {
		return err
	}
	if status, _ := stack["StackStatus"].(string); status != "CREATE_COMPLETE" {
		reason, _ := stack["StackStatusReason"].(string)
		return fmt.Errorf("create-stack: %s is %s (%s), want CREATE_COMPLETE", stackName, status, reason)
	}
	return nil
}

// DescribeStackResourcesPhysicalIds asserts the physical ID CloudFormation
// recorded for each resource. The physical ID is what Ref resolves to, so this
// is the same contract the output assertions below check, seen through the API
// an operator reaches for when a stack misbehaves.
func (g *cfnCliGroup) DescribeStackResourcesPhysicalIds(_ context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_eks_stack_name")
	if stackName == "" {
		return fmt.Errorf("describe-stack-resources: no stack from CreateStackWithEksRefs")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "describe-stack-resources", "--stack-name", stackName,
	)
	if err != nil {
		return err
	}
	resources, _ := out["StackResources"].([]interface{})
	physicalIDs := make(map[string]string, len(resources))
	for _, raw := range resources {
		r, _ := raw.(map[string]interface{})
		logicalID, _ := r["LogicalResourceId"].(string)
		physicalID, _ := r["PhysicalResourceId"].(string)
		physicalIDs[logicalID] = physicalID
	}
	want := eksRefsExpectedIDs(t.GetString("cfn_eks_cluster_name"), t.GetString("cfn_eks_nodegroup_name"))
	for _, logicalID := range []string{"Cluster", "Nodegroup", "Addon"} {
		if got := physicalIDs[logicalID]; got != want[logicalID] {
			return fmt.Errorf("describe-stack-resources: %s PhysicalResourceId = %q, want %q",
				logicalID, got, want[logicalID])
		}
	}
	return nil
}

// GetAttClusterArn covers the divergence AWS documents for AWS::EKS::Cluster:
// Ref is the cluster name, and the ARN is reachable only as the Arn attribute.
// Asserting both together is what makes the pair meaningful — either value
// alone looks plausible on its own.
func (g *cfnCliGroup) GetAttClusterArn(_ context.Context, t *harness.TestContext) error {
	outputs, err := g.eksRefsOutputs(t)
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

func (g *cfnCliGroup) RefNodegroupIsClusterSlashName(_ context.Context, t *harness.TestContext) error {
	outputs, err := g.eksRefsOutputs(t)
	if err != nil {
		return err
	}
	want := eksRefsExpectedIDs(t.GetString("cfn_eks_cluster_name"), t.GetString("cfn_eks_nodegroup_name"))["Nodegroup"]
	if got := outputs["NodegroupRef"]; got != want {
		return fmt.Errorf("Ref on AWS::EKS::Nodegroup = %q, want %q", got, want)
	}
	return nil
}

func (g *cfnCliGroup) RefAddonIsClusterPipeAddon(_ context.Context, t *harness.TestContext) error {
	outputs, err := g.eksRefsOutputs(t)
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
func (g *cfnCliGroup) DeleteStackWithEksRefs(_ context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_eks_stack_name")
	stackID := t.GetString("cfn_eks_stack_id")
	if stackName == "" || stackID == "" {
		return fmt.Errorf("delete-stack: no stack from CreateStackWithEksRefs")
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"cloudformation", "delete-stack", "--stack-name", stackName,
	); err != nil {
		return err
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"cloudformation", "wait", "stack-delete-complete", "--stack-name", stackName,
	); err != nil {
		return fmt.Errorf("delete-stack: waiting for %s: %w", stackName, err)
	}
	stack, err := g.describeEksRefsStack(t, stackID)
	if err != nil {
		return err
	}
	if status, _ := stack["StackStatus"].(string); status != "DELETE_COMPLETE" {
		reason, _ := stack["StackStatusReason"].(string)
		return fmt.Errorf("delete-stack: %s is %s (%s), want DELETE_COMPLETE", stackName, status, reason)
	}
	return nil
}

func (g *cfnCliGroup) describeEksRefsStack(t *harness.TestContext, nameOrID string) (map[string]interface{}, error) {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "describe-stacks", "--stack-name", nameOrID,
	)
	if err != nil {
		return nil, err
	}
	stacks, _ := out["Stacks"].([]interface{})
	if len(stacks) == 0 {
		return nil, fmt.Errorf("describe-stacks: %s returned no stacks", nameOrID)
	}
	stack, _ := stacks[0].(map[string]interface{})
	return stack, nil
}

// eksRefsOutputs reads the stack's outputs afresh for each test rather than
// stashing them, so every assertion is made against a value the server just
// produced rather than one an earlier test cached.
func (g *cfnCliGroup) eksRefsOutputs(t *harness.TestContext) (map[string]string, error) {
	stackName := t.GetString("cfn_eks_stack_name")
	if stackName == "" {
		return nil, fmt.Errorf("describe-stacks: no stack from CreateStackWithEksRefs")
	}
	stack, err := g.describeEksRefsStack(t, stackName)
	if err != nil {
		return nil, err
	}
	items, _ := stack["Outputs"].([]interface{})
	outputs := make(map[string]string, len(items))
	for _, raw := range items {
		o, _ := raw.(map[string]interface{})
		key, _ := o["OutputKey"].(string)
		value, _ := o["OutputValue"].(string)
		outputs[key] = value
	}
	return outputs, nil
}

// accountFromStackID pulls the account ID out of a stack ARN
// ("arn:aws:cloudformation:<region>:<account>:stack/<name>/<uuid>"), so the
// expected EKS ARN can be built without reaching for a second AWS API.
func accountFromStackID(stackID string) (string, error) {
	parts := strings.Split(stackID, ":")
	if len(parts) < 6 || parts[4] == "" {
		return "", fmt.Errorf("stack ID %q is not a stack ARN, cannot read the account ID from it", stackID)
	}
	return parts[4], nil
}
