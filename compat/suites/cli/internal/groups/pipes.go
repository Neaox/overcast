package groups

// pipes.go — EventBridge Pipes groups for the CLI suite.
//
// pipes-wiring covers pipe CRUD, the loud rejection of a source/target
// combination the emulator cannot run, and an end-to-end delivery from a
// DynamoDB stream source to an SQS target.
//
// The group provisions its own table and queue (issue #388) and the delivery
// assertion matches on this group's own record rather than "the first message
// received", so a shared resource can never make it pass by accident.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// Pipes returns the EventBridge Pipes service group.
func Pipes() ServiceGroup {
	g := &pipesGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"pipes-wiring:CreatePipe":                         g.CreatePipe,
			"pipes-wiring:DescribePipe":                       g.DescribePipe,
			"pipes-wiring:ListPipes":                          g.ListPipes,
			"pipes-wiring:CreatePipeRejectsUnsupportedTarget": g.CreatePipeRejectsUnsupportedTarget,
			"pipes-wiring:PipeDeliversToTarget":               g.PipeDeliversToTarget,
			"pipes-wiring:UpdatePipe":                         g.UpdatePipe,
			"pipes-wiring:DeletePipe":                         g.DeletePipe,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"pipes-wiring": g.setupWiring,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"pipes-wiring": g.teardownWiring,
		},
	}
}

type pipesGroup struct{}

var (
	pipeTableNamer = harness.NewNamer("pipe-src")
	pipeQueueNamer = harness.NewNamer("pipe-dst")
)

// pipeRoleARN is a required CreatePipe member. Overcast does not evaluate it —
// it is not a security boundary — but AWS rejects the call without it.
const pipeRoleARN = "arn:aws:iam::000000000000:role/oc-compat-pipe"

func (g *pipesGroup) pipeName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-pipe", t.RunID)
}

func (g *pipesGroup) setupWiring(_ context.Context, t *harness.TestContext) error {
	table := pipeTableNamer.Name(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "create-table",
		"--table-name", table,
		"--attribute-definitions", "AttributeName=id,AttributeType=S",
		"--key-schema", "AttributeName=id,KeyType=HASH",
		"--billing-mode", "PAY_PER_REQUEST",
		"--stream-specification", "StreamEnabled=true,StreamViewType=NEW_AND_OLD_IMAGES",
	); err != nil {
		return err
	}
	desc, err := awscli.RunOutput(t.Endpoint, t.Region, "dynamodb", "describe-table", "--table-name", table)
	if err != nil {
		return err
	}
	tableDesc, _ := desc["Table"].(map[string]any)
	streamARN, _ := tableDesc["LatestStreamArn"].(string)
	if streamARN == "" {
		return fmt.Errorf("pipes setup: table %q has no LatestStreamArn", table)
	}
	t.Set("pipe_table", table)
	t.Set("pipe_source_arn", streamARN)

	queue := pipeQueueNamer.Name(t)
	created, err := awscli.RunOutput(t.Endpoint, t.Region, "sqs", "create-queue", "--queue-name", queue)
	if err != nil {
		return err
	}
	url, _ := created["QueueUrl"].(string)
	attrs, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sqs", "get-queue-attributes", "--queue-url", url, "--attribute-names", "QueueArn")
	if err != nil {
		return err
	}
	attrMap, _ := attrs["Attributes"].(map[string]any)
	arn, _ := attrMap["QueueArn"].(string)
	if arn == "" {
		return fmt.Errorf("pipes setup: missing QueueArn for %s", queue)
	}
	t.Set("pipe_queue_url", url)
	t.Set("pipe_target_arn", arn)
	return nil
}

func (g *pipesGroup) teardownWiring(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "pipes", "delete-pipe", "--name", g.pipeName(t)) //nolint:errcheck
	if url := t.GetString("pipe_queue_url"); url != "" {
		awscli.Run(t.Endpoint, t.Region, "sqs", "delete-queue", "--queue-url", url) //nolint:errcheck
	}
	if table := t.GetString("pipe_table"); table != "" {
		awscli.Run(t.Endpoint, t.Region, "dynamodb", "delete-table", "--table-name", table) //nolint:errcheck
	}
	return nil
}

func (g *pipesGroup) CreatePipe(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"pipes", "create-pipe",
		"--name", g.pipeName(t),
		"--source", t.GetString("pipe_source_arn"),
		"--target", t.GetString("pipe_target_arn"),
		"--role-arn", pipeRoleARN,
	)
	if err != nil {
		return err
	}
	if arn, _ := out["Arn"].(string); arn == "" {
		return fmt.Errorf("pipes CreatePipe: missing Arn")
	}
	if state, _ := out["DesiredState"].(string); state != "RUNNING" {
		return fmt.Errorf("pipes CreatePipe: DesiredState = %q, want RUNNING", state)
	}
	return nil
}

func (g *pipesGroup) DescribePipe(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "pipes", "describe-pipe", "--name", g.pipeName(t))
	if err != nil {
		return err
	}
	if src, _ := out["Source"].(string); src != t.GetString("pipe_source_arn") {
		return fmt.Errorf("pipes DescribePipe: Source = %q", src)
	}
	if tgt, _ := out["Target"].(string); tgt != t.GetString("pipe_target_arn") {
		return fmt.Errorf("pipes DescribePipe: Target = %q", tgt)
	}
	if role, _ := out["RoleArn"].(string); role != pipeRoleARN {
		return fmt.Errorf("pipes DescribePipe: RoleArn = %q, did not round-trip", role)
	}
	return nil
}

func (g *pipesGroup) ListPipes(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "pipes", "list-pipes", "--name-prefix", t.RunID)
	if err != nil {
		return err
	}
	items, _ := out["Pipes"].([]any)
	for _, raw := range items {
		if m, ok := raw.(map[string]any); ok && m["Name"] == g.pipeName(t) {
			return nil
		}
	}
	return fmt.Errorf("pipes ListPipes: %q not found", g.pipeName(t))
}

// CreatePipeRejectsUnsupportedTarget pins the §2.1 behaviour: a well-formed ARN
// naming a service the emulator cannot deliver to fails the call rather than
// being stored and silently ignored.
func (g *pipesGroup) CreatePipeRejectsUnsupportedTarget(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"pipes", "create-pipe",
		"--name", g.pipeName(t)+"-bad",
		"--source", t.GetString("pipe_source_arn"),
		"--target", "arn:aws:logs:us-east-1:000000000000:log-group:/aws/pipes/compat",
		"--role-arn", pipeRoleARN,
	)
	if err == nil {
		awscli.Run(t.Endpoint, t.Region, "pipes", "delete-pipe", "--name", g.pipeName(t)+"-bad") //nolint:errcheck
		return fmt.Errorf("pipes CreatePipe accepted a target the emulator cannot deliver to")
	}
	if !strings.Contains(err.Error(), "ValidationException") {
		return fmt.Errorf("pipes CreatePipe: error %v, want ValidationException", err)
	}
	return nil
}

// PipeDeliversToTarget writes to the source table and waits for this group's
// own record to reach the target queue.
func (g *pipesGroup) PipeDeliversToTarget(_ context.Context, t *harness.TestContext) error {
	marker := fmt.Sprintf("%s-delivery", t.RunID)

	// The pipe reaches RUNNING asynchronously.
	for i := 0; i < 20; i++ {
		out, err := awscli.RunOutput(t.Endpoint, t.Region, "pipes", "describe-pipe", "--name", g.pipeName(t))
		if err == nil {
			if state, _ := out["CurrentState"].(string); state == "RUNNING" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	item := fmt.Sprintf(`{"id":{"S":"%s"}}`, marker)
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "put-item", "--table-name", t.GetString("pipe_table"), "--item", item); err != nil {
		return err
	}

	url := t.GetString("pipe_queue_url")
	for i := 0; i < 20; i++ {
		out, err := awscli.RunOutput(t.Endpoint, t.Region,
			"sqs", "receive-message", "--queue-url", url,
			"--max-number-of-messages", "10", "--wait-time-seconds", "1")
		if err != nil {
			return err
		}
		messages, _ := out["Messages"].([]any)
		for _, raw := range messages {
			m, _ := raw.(map[string]any)
			body, _ := m["Body"].(string)
			if handle, _ := m["ReceiptHandle"].(string); handle != "" {
				awscli.Run(t.Endpoint, t.Region, //nolint:errcheck
					"sqs", "delete-message", "--queue-url", url, "--receipt-handle", handle)
			}
			if !strings.Contains(body, marker) {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(body), &record); err != nil {
				return fmt.Errorf("pipes PipeDeliversToTarget: body is not JSON: %s", body)
			}
			if record["eventSource"] != "aws:dynamodb" {
				return fmt.Errorf("pipes PipeDeliversToTarget: not a DynamoDB record: %s", body)
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("pipes PipeDeliversToTarget: no delivery reached the target queue")
}

func (g *pipesGroup) UpdatePipe(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"pipes", "update-pipe",
		"--name", g.pipeName(t),
		"--role-arn", pipeRoleARN,
		"--desired-state", "STOPPED",
		"--description", "compat",
	)
	if err != nil {
		return err
	}
	if state, _ := out["DesiredState"].(string); state != "STOPPED" {
		return fmt.Errorf("pipes UpdatePipe: DesiredState = %q, want STOPPED", state)
	}
	return nil
}

func (g *pipesGroup) DeletePipe(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "pipes", "delete-pipe", "--name", g.pipeName(t))
	if err != nil {
		return err
	}
	if state, _ := out["CurrentState"].(string); state != "DELETING" {
		return fmt.Errorf("pipes DeletePipe: CurrentState = %q, want DELETING", state)
	}
	return nil
}
