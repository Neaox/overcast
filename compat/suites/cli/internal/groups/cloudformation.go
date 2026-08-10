package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// CloudFormation returns the CloudFormation service group.
func CloudFormation() ServiceGroup {
	g := &cfnCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateStack":      g.CreateStack,
			"DescribeStacks":   g.DescribeStacks,
			"ListStacks":       g.ListStacks,
			"UpdateStack":      g.UpdateStack,
			"DeleteStack":      g.DeleteStack,
			"ValidateTemplate": g.ValidateTemplate,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"cloudformation-stacks": g.setupStacks,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"cloudformation-stacks": g.teardownStacks,
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
