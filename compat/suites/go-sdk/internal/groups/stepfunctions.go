package groups

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

func StepFunctions(c *clients.Clients) ServiceGroup {
	g := &sfnGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateStateMachine":   g.CreateStateMachine,
			"DescribeStateMachine": g.DescribeStateMachine,
			"ListStateMachines":    g.ListStateMachines,
			"StartExecution":       g.StartExecution,
			"DeleteStateMachine":   g.DeleteStateMachine,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"sfn-statemachines": g.setupSFN,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"sfn-statemachines": g.teardownSFN,
		},
	}
}

type sfnGroup struct{ c *clients.Clients }

func (g *sfnGroup) cl() *sfn.Client { return g.c.SFN() }

func (g *sfnGroup) setupSFN(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *sfnGroup) teardownSFN(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sfn_sm_arn"); arn != "" {
		g.cl().DeleteStateMachine(ctx, &sfn.DeleteStateMachineInput{StateMachineArn: aws.String(arn)}) //nolint:errcheck
	}
	return nil
}

func (g *sfnGroup) CreateStateMachine(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	def, _ := json.Marshal(map[string]interface{}{
		"Comment": "compat test",
		"StartAt": "Pass",
		"States": map[string]interface{}{
			"Pass": map[string]interface{}{"Type": "Pass", "End": true},
		},
	})
	roleArn := fmt.Sprintf("arn:aws:iam::000000000000:role/compat-sfn-%s", t.RunID)
	resp, err := g.cl().CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
		Name:       aws.String(name),
		Type:       sfntypes.StateMachineTypeExpress,
		Definition: aws.String(string(def)),
		RoleArn:    aws.String(roleArn),
	})
	if err != nil {
		return err
	}
	if resp.StateMachineArn == nil {
		return fmt.Errorf("CreateStateMachine: missing stateMachineArn")
	}
	t.Set("sfn_sm_arn", *resp.StateMachineArn)
	return nil
}

func (g *sfnGroup) DescribeStateMachine(ctx context.Context, t *harness.TestContext) error {
	smArn := t.GetString("sfn_sm_arn")
	if smArn == "" {
		return fmt.Errorf("DescribeStateMachine: no state machine from CreateStateMachine")
	}
	resp, err := g.cl().DescribeStateMachine(ctx, &sfn.DescribeStateMachineInput{
		StateMachineArn: aws.String(smArn),
	})
	if err != nil {
		return err
	}
	if resp.StateMachineArn == nil {
		return fmt.Errorf("DescribeStateMachine: missing stateMachineArn")
	}
	return nil
}

func (g *sfnGroup) ListStateMachines(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListStateMachines(ctx, &sfn.ListStateMachinesInput{})
	return err
}

func (g *sfnGroup) StartExecution(ctx context.Context, t *harness.TestContext) error {
	smArn := t.GetString("sfn_sm_arn")
	if smArn == "" {
		return fmt.Errorf("StartExecution: no state machine from CreateStateMachine")
	}
	input, _ := json.Marshal(map[string]interface{}{"key": "value"})
	resp, err := g.cl().StartExecution(ctx, &sfn.StartExecutionInput{
		StateMachineArn: aws.String(smArn),
		Input:           aws.String(string(input)),
	})
	if err != nil {
		return err
	}
	if resp.ExecutionArn == nil {
		return fmt.Errorf("StartExecution: missing executionArn")
	}
	return nil
}

func (g *sfnGroup) DeleteStateMachine(ctx context.Context, t *harness.TestContext) error {
	smArn := t.GetString("sfn_sm_arn")
	if smArn == "" {
		return nil
	}
	_, err := g.cl().DeleteStateMachine(ctx, &sfn.DeleteStateMachineInput{
		StateMachineArn: aws.String(smArn),
	})
	return err
}
