package groups

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

func StepFunctions(c *clients.Clients) ServiceGroup {
	g := &sfnGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateStateMachine":               g.CreateStateMachine,
			"DescribeStateMachine":             g.DescribeStateMachine,
			"ListStateMachines":                g.ListStateMachines,
			"sfn-statemachines:StartExecution": g.StartExecution,
			"DeleteStateMachine":               g.DeleteStateMachine,

			// sfn-executions — group-qualified so StartExecution does not
			// collide with the sfn-statemachines test of the same name. This
			// group provisions its own state machine in setup.
			"sfn-executions:StartExecution":      g.ExecStartExecution,
			"sfn-executions:DescribeExecution":   g.DescribeExecution,
			"sfn-executions:GetExecutionHistory": g.GetExecutionHistory,
			"sfn-executions:ListExecutions":      g.ListExecutions,
			"sfn-executions:StopExecution":       g.StopExecution,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"sfn-statemachines": g.setupSFN,
			"sfn-executions":    g.setupExecutions,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"sfn-statemachines": g.teardownSFN,
			"sfn-executions":    g.teardownExecutions,
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

// ─── sfn-executions ───────────────────────────────────────────────────────────
//
// The execution engine really interprets the definition, so these tests assert
// on what the execution produced rather than just that a call succeeded.

const sfnExecDefinition = `{"Comment":"compat executions","StartAt":"Hello","States":{"Hello":{"Type":"Pass","Result":{"greeting":"hello"},"End":true}}}`

// setupExecutions provisions this group's own state machine — groups must not
// depend on resources another group created.
func (g *sfnGroup) setupExecutions(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-exec-%s", t.RunID)
	resp, err := g.cl().CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
		Name:       aws.String(name),
		Type:       sfntypes.StateMachineTypeExpress,
		Definition: aws.String(sfnExecDefinition),
		RoleArn:    aws.String(fmt.Sprintf("arn:aws:iam::000000000000:role/compat-sfn-exec-%s", t.RunID)),
	})
	if err != nil {
		return err
	}
	if resp.StateMachineArn == nil {
		return fmt.Errorf("setupExecutions: missing stateMachineArn")
	}
	t.Set("sfn_exec_sm_arn", *resp.StateMachineArn)
	return nil
}

func (g *sfnGroup) teardownExecutions(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sfn_exec_sm_arn"); arn != "" {
		g.cl().DeleteStateMachine(ctx, &sfn.DeleteStateMachineInput{StateMachineArn: aws.String(arn)}) //nolint:errcheck
	}
	return nil
}

func (g *sfnGroup) ExecStartExecution(ctx context.Context, t *harness.TestContext) error {
	smArn := t.GetString("sfn_exec_sm_arn")
	if smArn == "" {
		return fmt.Errorf("StartExecution: no state machine from setup")
	}
	resp, err := g.cl().StartExecution(ctx, &sfn.StartExecutionInput{
		StateMachineArn: aws.String(smArn),
		Name:            aws.String(fmt.Sprintf("run-%s", t.RunID)),
		Input:           aws.String(`{"key":"value"}`),
	})
	if err != nil {
		return err
	}
	if resp.ExecutionArn == nil {
		return fmt.Errorf("StartExecution: missing executionArn")
	}
	t.Set("sfn_exec_arn", *resp.ExecutionArn)
	return nil
}

func (g *sfnGroup) DescribeExecution(ctx context.Context, t *harness.TestContext) error {
	execArn := t.GetString("sfn_exec_arn")
	if execArn == "" {
		return fmt.Errorf("DescribeExecution: no execution from StartExecution")
	}
	resp, err := g.cl().DescribeExecution(ctx, &sfn.DescribeExecutionInput{ExecutionArn: aws.String(execArn)})
	if err != nil {
		return err
	}
	if resp.Status == "" {
		return fmt.Errorf("DescribeExecution: missing status")
	}
	if resp.ExecutionArn == nil || *resp.ExecutionArn != execArn {
		return fmt.Errorf("DescribeExecution: executionArn did not round-trip")
	}
	return nil
}

func (g *sfnGroup) GetExecutionHistory(ctx context.Context, t *harness.TestContext) error {
	execArn := t.GetString("sfn_exec_arn")
	if execArn == "" {
		return fmt.Errorf("GetExecutionHistory: no execution from StartExecution")
	}
	resp, err := g.cl().GetExecutionHistory(ctx, &sfn.GetExecutionHistoryInput{ExecutionArn: aws.String(execArn)})
	if err != nil {
		return err
	}
	if len(resp.Events) == 0 {
		return fmt.Errorf("GetExecutionHistory: no events")
	}
	return nil
}

func (g *sfnGroup) ListExecutions(ctx context.Context, t *harness.TestContext) error {
	smArn := t.GetString("sfn_exec_sm_arn")
	if smArn == "" {
		return fmt.Errorf("ListExecutions: no state machine from setup")
	}
	resp, err := g.cl().ListExecutions(ctx, &sfn.ListExecutionsInput{StateMachineArn: aws.String(smArn)})
	if err != nil {
		return err
	}
	execArn := t.GetString("sfn_exec_arn")
	for _, item := range resp.Executions {
		if item.ExecutionArn != nil && *item.ExecutionArn == execArn {
			return nil
		}
	}
	return fmt.Errorf("ListExecutions: execution %s not listed", execArn)
}

func (g *sfnGroup) StopExecution(ctx context.Context, t *harness.TestContext) error {
	execArn := t.GetString("sfn_exec_arn")
	if execArn == "" {
		return fmt.Errorf("StopExecution: no execution from StartExecution")
	}
	_, err := g.cl().StopExecution(ctx, &sfn.StopExecutionInput{ExecutionArn: aws.String(execArn)})
	return err
}
