package groups

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

func CloudFormation(c *clients.Clients) ServiceGroup {
	g := &cfnGroup{c: c}
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

func (g *cfnGroup) ListStacks(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListStacks(ctx, &cloudformation.ListStacksInput{
		StackStatusFilter: []cftypes.StackStatus{cftypes.StackStatusCreateComplete},
	})
	return err
}

func (g *cfnGroup) UpdateStack(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("UpdateStack: no stack from CreateStack")
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

func (g *cfnGroup) DeleteStack(ctx context.Context, t *harness.TestContext) error {
	stackName := t.GetString("cfn_stack_name")
	if stackName == "" {
		return fmt.Errorf("DeleteStack: no stack from CreateStack")
	}
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
