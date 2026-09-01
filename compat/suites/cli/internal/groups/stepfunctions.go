package groups

import (
	"context"
	"fmt"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// StepFunctions returns the Step Functions service group.
func StepFunctions() ServiceGroup {
	g := &sfnCliGroup{}
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

type sfnCliGroup struct{}

const sfnDefinition = `{"Comment":"compat","StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`
const sfnRoleArn = "arn:aws:iam::000000000000:role/compat-sfn-role"

func (g *sfnCliGroup) setupSFN(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *sfnCliGroup) teardownSFN(_ context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sm_arn"); arn != "" {
		awscli.Run(t.Endpoint, t.Region, "stepfunctions", "delete-state-machine", "--state-machine-arn", arn) //nolint:errcheck
	}
	return nil
}

func (g *sfnCliGroup) CreateStateMachine(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "create-state-machine",
		"--name", name,
		"--definition", sfnDefinition,
		"--role-arn", sfnRoleArn,
		"--type", "EXPRESS",
	)
	if err != nil {
		return err
	}
	arn, _ := out["stateMachineArn"].(string)
	if arn == "" {
		return fmt.Errorf("CreateStateMachine: missing stateMachineArn")
	}
	t.Set("sm_arn", arn)
	return nil
}

func (g *sfnCliGroup) DescribeStateMachine(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("sm_arn")
	if arn == "" {
		return fmt.Errorf("DescribeStateMachine: no sm_arn from CreateStateMachine")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "describe-state-machine",
		"--state-machine-arn", arn,
	)
	if err != nil {
		return err
	}
	if out["stateMachineArn"] == nil {
		return fmt.Errorf("DescribeStateMachine: missing stateMachineArn")
	}
	return nil
}

func (g *sfnCliGroup) ListStateMachines(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "list-state-machines")
	return err
}

func (g *sfnCliGroup) StartExecution(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("sm_arn")
	if arn == "" {
		return fmt.Errorf("StartExecution: no sm_arn from CreateStateMachine")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "start-execution",
		"--state-machine-arn", arn,
		"--input", `{"compat":true}`,
	)
	if err != nil {
		return err
	}
	if out["executionArn"] == nil {
		return fmt.Errorf("StartExecution: missing executionArn")
	}
	return nil
}

func (g *sfnCliGroup) DeleteStateMachine(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-del-%s", t.RunID)
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "create-state-machine",
		"--name", name, "--definition", sfnDefinition, "--role-arn", sfnRoleArn, "--type", "EXPRESS",
	)
	arn, _ := out["stateMachineArn"].(string)
	if arn == "" {
		return fmt.Errorf("DeleteStateMachine: could not create state machine to delete")
	}
	return awscli.Run(t.Endpoint, t.Region, "stepfunctions", "delete-state-machine",
		"--state-machine-arn", arn,
	)
}

// ─── sfn-executions ───────────────────────────────────────────────────────────
//
// The execution engine really interprets the definition, so these tests assert
// on what the execution produced rather than just that a call succeeded.

const sfnExecDefinition = `{"Comment":"compat executions","StartAt":"Hello","States":{"Hello":{"Type":"Pass","Result":{"greeting":"hello"},"End":true}}}`

// setupExecutions provisions this group's own state machine — groups must not
// depend on resources another group created.
func (g *sfnCliGroup) setupExecutions(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-exec-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "create-state-machine",
		"--name", name,
		"--definition", sfnExecDefinition,
		"--role-arn", sfnRoleArn,
		"--type", "EXPRESS",
	)
	if err != nil {
		return err
	}
	arn, _ := out["stateMachineArn"].(string)
	if arn == "" {
		return fmt.Errorf("setupExecutions: missing stateMachineArn")
	}
	t.Set("exec_sm_arn", arn)
	return nil
}

func (g *sfnCliGroup) teardownExecutions(_ context.Context, t *harness.TestContext) error {
	if arn := t.GetString("exec_sm_arn"); arn != "" {
		awscli.Run(t.Endpoint, t.Region, "stepfunctions", "delete-state-machine", "--state-machine-arn", arn) //nolint:errcheck
	}
	return nil
}

func (g *sfnCliGroup) ExecStartExecution(_ context.Context, t *harness.TestContext) error {
	smArn := t.GetString("exec_sm_arn")
	if smArn == "" {
		return fmt.Errorf("StartExecution: no state machine from setup")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "start-execution",
		"--state-machine-arn", smArn,
		"--name", fmt.Sprintf("run-%s", t.RunID),
		"--input", `{"key":"value"}`,
	)
	if err != nil {
		return err
	}
	arn, _ := out["executionArn"].(string)
	if arn == "" {
		return fmt.Errorf("StartExecution: missing executionArn")
	}
	t.Set("exec_arn", arn)
	return nil
}

func (g *sfnCliGroup) DescribeExecution(_ context.Context, t *harness.TestContext) error {
	execArn := t.GetString("exec_arn")
	if execArn == "" {
		return fmt.Errorf("DescribeExecution: no execution from StartExecution")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "describe-execution",
		"--execution-arn", execArn,
	)
	if err != nil {
		return err
	}
	if out["status"] == nil {
		return fmt.Errorf("DescribeExecution: missing status")
	}
	if arn, _ := out["executionArn"].(string); arn != execArn {
		return fmt.Errorf("DescribeExecution: executionArn did not round-trip")
	}
	return nil
}

func (g *sfnCliGroup) GetExecutionHistory(_ context.Context, t *harness.TestContext) error {
	execArn := t.GetString("exec_arn")
	if execArn == "" {
		return fmt.Errorf("GetExecutionHistory: no execution from StartExecution")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "get-execution-history",
		"--execution-arn", execArn,
	)
	if err != nil {
		return err
	}
	events, _ := out["events"].([]interface{})
	if len(events) == 0 {
		return fmt.Errorf("GetExecutionHistory: no events")
	}
	return nil
}

func (g *sfnCliGroup) ListExecutions(_ context.Context, t *harness.TestContext) error {
	smArn := t.GetString("exec_sm_arn")
	if smArn == "" {
		return fmt.Errorf("ListExecutions: no state machine from setup")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "stepfunctions", "list-executions",
		"--state-machine-arn", smArn,
	)
	if err != nil {
		return err
	}
	execArn := t.GetString("exec_arn")
	items, _ := out["executions"].([]interface{})
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if arn, _ := item["executionArn"].(string); arn == execArn {
			return nil
		}
	}
	return fmt.Errorf("ListExecutions: execution %s not listed", execArn)
}

func (g *sfnCliGroup) StopExecution(_ context.Context, t *harness.TestContext) error {
	execArn := t.GetString("exec_arn")
	if execArn == "" {
		return fmt.Errorf("StopExecution: no execution from StartExecution")
	}
	return awscli.Run(t.Endpoint, t.Region, "stepfunctions", "stop-execution", "--execution-arn", execArn)
}
