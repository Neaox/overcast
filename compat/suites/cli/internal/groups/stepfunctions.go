package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// StepFunctions returns the Step Functions service group.
func StepFunctions() ServiceGroup {
	g := &sfnCliGroup{}
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
