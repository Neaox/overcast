package groups

// pipes.go — EventBridge Pipes groups for the Go SDK suite.
//
// pipes-wiring covers pipe CRUD, the loud rejection of a source/target
// combination the emulator cannot run, and an end-to-end delivery from a
// DynamoDB stream source to an SQS target.
//
// The group provisions its own table and queue (issue #388), and the delivery
// assertion matches on this group's own record rather than "the first message
// received", so a shared resource can never make it pass by accident.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/pipes"
	pipestypes "github.com/aws/aws-sdk-go-v2/service/pipes/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

// pipeRoleARN is a required CreatePipe member. Overcast does not evaluate it —
// it is not a security boundary — but AWS rejects the call without it.
const pipeRoleARN = "arn:aws:iam::000000000000:role/oc-compat-pipe"

// Pipes returns the EventBridge Pipes service group.
func Pipes(c *clients.Clients) ServiceGroup {
	g := &pipesGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreatePipe":                         g.CreatePipe,
			"DescribePipe":                       g.DescribePipe,
			"ListPipes":                          g.ListPipes,
			"CreatePipeRejectsUnsupportedTarget": g.CreatePipeRejectsUnsupportedTarget,
			"PipeDeliversToTarget":               g.PipeDeliversToTarget,
			"UpdatePipe":                         g.UpdatePipe,
			"DeletePipe":                         g.DeletePipe,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"pipes-wiring": g.setupWiring,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"pipes-wiring": g.teardownWiring,
		},
	}
}

type pipesGroup struct{ c *clients.Clients }

func (g *pipesGroup) pipeName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-pipe", t.RunID)
}

func (g *pipesGroup) setupWiring(ctx context.Context, t *harness.TestContext) error {
	table := fmt.Sprintf("oc-%s-pipe-src", t.RunID)
	ddb := g.c.DynamoDB()
	if _, err := ddb.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String(table),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
		StreamSpecification: &ddbtypes.StreamSpecification{
			StreamEnabled:  aws.Bool(true),
			StreamViewType: ddbtypes.StreamViewTypeNewAndOldImages,
		},
	}); err != nil {
		return err
	}
	desc, err := ddb.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		return err
	}
	if desc.Table == nil || aws.ToString(desc.Table.LatestStreamArn) == "" {
		return fmt.Errorf("pipes setup: table %q has no LatestStreamArn", table)
	}
	t.Set("pipe_table", table)
	t.Set("pipe_source_arn", aws.ToString(desc.Table.LatestStreamArn))

	queue := fmt.Sprintf("oc-%s-pipe-dst", t.RunID)
	created, err := g.c.SQS().CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(queue)})
	if err != nil {
		return err
	}
	attrs, err := g.c.SQS().GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       created.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return err
	}
	arn := attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	if arn == "" {
		return fmt.Errorf("pipes setup: missing QueueArn for %s", queue)
	}
	t.Set("pipe_queue_url", aws.ToString(created.QueueUrl))
	t.Set("pipe_target_arn", arn)
	return nil
}

func (g *pipesGroup) teardownWiring(ctx context.Context, t *harness.TestContext) error {
	g.c.Pipes().DeletePipe(ctx, &pipes.DeletePipeInput{Name: aws.String(g.pipeName(t))}) //nolint:errcheck
	if url := t.GetString("pipe_queue_url"); url != "" {
		g.c.SQS().DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(url)}) //nolint:errcheck
	}
	if table := t.GetString("pipe_table"); table != "" {
		g.c.DynamoDB().DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)}) //nolint:errcheck
	}
	return nil
}

func (g *pipesGroup) CreatePipe(ctx context.Context, t *harness.TestContext) error {
	out, err := g.c.Pipes().CreatePipe(ctx, &pipes.CreatePipeInput{
		Name:    aws.String(g.pipeName(t)),
		Source:  aws.String(t.GetString("pipe_source_arn")),
		Target:  aws.String(t.GetString("pipe_target_arn")),
		RoleArn: aws.String(pipeRoleARN),
	})
	if err != nil {
		return err
	}
	if aws.ToString(out.Arn) == "" {
		return fmt.Errorf("pipes CreatePipe: missing Arn")
	}
	if out.DesiredState != pipestypes.RequestedPipeStateRunning {
		return fmt.Errorf("pipes CreatePipe: DesiredState = %q, want RUNNING", out.DesiredState)
	}
	return nil
}

func (g *pipesGroup) DescribePipe(ctx context.Context, t *harness.TestContext) error {
	out, err := g.c.Pipes().DescribePipe(ctx, &pipes.DescribePipeInput{Name: aws.String(g.pipeName(t))})
	if err != nil {
		return err
	}
	if aws.ToString(out.Source) != t.GetString("pipe_source_arn") {
		return fmt.Errorf("pipes DescribePipe: Source = %q", aws.ToString(out.Source))
	}
	if aws.ToString(out.Target) != t.GetString("pipe_target_arn") {
		return fmt.Errorf("pipes DescribePipe: Target = %q", aws.ToString(out.Target))
	}
	if aws.ToString(out.RoleArn) != pipeRoleARN {
		return fmt.Errorf("pipes DescribePipe: RoleArn = %q, did not round-trip", aws.ToString(out.RoleArn))
	}
	return nil
}

func (g *pipesGroup) ListPipes(ctx context.Context, t *harness.TestContext) error {
	out, err := g.c.Pipes().ListPipes(ctx, &pipes.ListPipesInput{NamePrefix: aws.String(t.RunID)})
	if err != nil {
		return err
	}
	for _, p := range out.Pipes {
		if aws.ToString(p.Name) == g.pipeName(t) {
			return nil
		}
	}
	return fmt.Errorf("pipes ListPipes: %q not found", g.pipeName(t))
}

// CreatePipeRejectsUnsupportedTarget pins the §2.1 behaviour: a well-formed ARN
// naming a service the emulator cannot deliver to fails the call rather than
// being stored and silently ignored.
func (g *pipesGroup) CreatePipeRejectsUnsupportedTarget(ctx context.Context, t *harness.TestContext) error {
	name := g.pipeName(t) + "-bad"
	_, err := g.c.Pipes().CreatePipe(ctx, &pipes.CreatePipeInput{
		Name:    aws.String(name),
		Source:  aws.String(t.GetString("pipe_source_arn")),
		Target:  aws.String("arn:aws:logs:us-east-1:000000000000:log-group:/aws/pipes/compat"),
		RoleArn: aws.String(pipeRoleARN),
	})
	if err == nil {
		g.c.Pipes().DeletePipe(ctx, &pipes.DeletePipeInput{Name: aws.String(name)}) //nolint:errcheck
		return fmt.Errorf("pipes CreatePipe accepted a target the emulator cannot deliver to")
	}
	var validation *pipestypes.ValidationException
	if errors.As(err, &validation) {
		return nil
	}
	if strings.Contains(err.Error(), "ValidationException") {
		return nil
	}
	return fmt.Errorf("pipes CreatePipe: error %v, want ValidationException", err)
}

// PipeDeliversToTarget writes to the source table and waits for this group's
// own record to reach the target queue.
func (g *pipesGroup) PipeDeliversToTarget(ctx context.Context, t *harness.TestContext) error {
	marker := fmt.Sprintf("%s-delivery", t.RunID)

	// The pipe reaches RUNNING asynchronously.
	for i := 0; i < 20; i++ {
		out, err := g.c.Pipes().DescribePipe(ctx, &pipes.DescribePipeInput{Name: aws.String(g.pipeName(t))})
		if err == nil && out.CurrentState == pipestypes.PipeStateRunning {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := g.c.DynamoDB().PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(t.GetString("pipe_table")),
		Item:      map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: marker}},
	}); err != nil {
		return err
	}

	url := t.GetString("pipe_queue_url")
	for i := 0; i < 20; i++ {
		out, err := g.c.SQS().ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(url),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		if err != nil {
			return err
		}
		for _, msg := range out.Messages {
			body := aws.ToString(msg.Body)
			g.c.SQS().DeleteMessage(ctx, &sqs.DeleteMessageInput{ //nolint:errcheck
				QueueUrl: aws.String(url), ReceiptHandle: msg.ReceiptHandle,
			})
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

func (g *pipesGroup) UpdatePipe(ctx context.Context, t *harness.TestContext) error {
	out, err := g.c.Pipes().UpdatePipe(ctx, &pipes.UpdatePipeInput{
		Name:         aws.String(g.pipeName(t)),
		RoleArn:      aws.String(pipeRoleARN),
		DesiredState: pipestypes.RequestedPipeStateStopped,
		Description:  aws.String("compat"),
	})
	if err != nil {
		return err
	}
	if out.DesiredState != pipestypes.RequestedPipeStateStopped {
		return fmt.Errorf("pipes UpdatePipe: DesiredState = %q, want STOPPED", out.DesiredState)
	}
	return nil
}

func (g *pipesGroup) DeletePipe(ctx context.Context, t *harness.TestContext) error {
	out, err := g.c.Pipes().DeletePipe(ctx, &pipes.DeletePipeInput{Name: aws.String(g.pipeName(t))})
	if err != nil {
		return err
	}
	if out.CurrentState != pipestypes.PipeStateDeleting {
		return fmt.Errorf("pipes DeletePipe: CurrentState = %q, want DELETING", out.CurrentState)
	}
	return nil
}
