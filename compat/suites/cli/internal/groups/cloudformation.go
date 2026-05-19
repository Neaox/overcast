package groups

import (
	"context"
	"encoding/json"
	"fmt"

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

func (g *cfnCliGroup) ListStacks(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"cloudformation", "list-stacks",
	)
	return err
}

func (g *cfnCliGroup) UpdateStack(_ context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("UpdateStack: no stack from CreateStack")
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
