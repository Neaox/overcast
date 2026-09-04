package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"cloudformation-stacks": g.setupStacks,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"cloudformation-stacks": g.teardownStacks,
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
