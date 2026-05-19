package cloudformation

import (
	"testing"
)

// TestTopoSortImplicitDeps verifies that Ref, Fn::GetAtt, and Fn::Sub inside
// resource Properties are treated as implicit dependency edges — matching real
// AWS CloudFormation behaviour where these intrinsics establish ordering without
// requiring an explicit DependsOn.
func TestTopoSortImplicitDeps(t *testing.T) {
	t.Run("Ref creates implicit dependency", func(t *testing.T) {
		// TopicSubscription references Topic via Ref — must be provisioned after Topic.
		resources := map[string]TemplateResource{
			"TopicSubscription": {
				Type: "AWS::SNS::Subscription",
				Properties: map[string]any{
					"TopicArn": map[string]any{"Ref": "Topic"},
					"Protocol": "sqs",
					"Endpoint": "arn:aws:sqs:us-east-1:000000000000:queue",
				},
			},
			"Topic": {
				Type: "AWS::SNS::Topic",
				Properties: map[string]any{
					"TopicName": "my-topic",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "Topic", "TopicSubscription")
	})

	t.Run("Fn::GetAtt array form creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"QueuePolicy": {
				Type: "AWS::SQS::QueuePolicy",
				Properties: map[string]any{
					"Queues": []any{
						map[string]any{"Fn::GetAtt": []any{"MyQueue", "QueueUrl"}},
					},
				},
			},
			"MyQueue": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": "my-queue",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "MyQueue", "QueuePolicy")
	})

	t.Run("Fn::GetAtt dot-string form creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"Consumer": {
				Type: "AWS::Lambda::Function",
				Properties: map[string]any{
					"Environment": map[string]any{
						"Variables": map[string]any{
							"QUEUE_ARN": map[string]any{"Fn::GetAtt": "SourceQueue.Arn"},
						},
					},
				},
			},
			"SourceQueue": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": "source-queue",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "SourceQueue", "Consumer")
	})

	t.Run("Fn::Sub string creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"StateMachine": {
				Type: "AWS::StepFunctions::StateMachine",
				Properties: map[string]any{
					"DefinitionString": map[string]any{
						"Fn::Sub": `{"StartAt":"Call","States":{"Call":{"Type":"Task","Resource":"${MyLambda.Arn}","End":true}}}`,
					},
				},
			},
			"MyLambda": {
				Type: "AWS::Lambda::Function",
				Properties: map[string]any{
					"FunctionName": "my-fn",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "MyLambda", "StateMachine")
	})

	t.Run("Fn::Sub array form creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"B": {
				Type: "AWS::SSM::Parameter",
				Properties: map[string]any{
					"Value": map[string]any{
						"Fn::Sub": []any{
							"arn:aws:sqs:${AWS::Region}:${AWS::AccountId}:${QueueName}",
							map[string]any{
								"QueueName": map[string]any{"Ref": "A"},
							},
						},
					},
				},
			},
			"A": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": "queue-a",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "A", "B")
	})

	t.Run("Explicit DependsOn still works", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"B": {
				Type:       "AWS::SQS::Queue",
				Properties: map[string]any{},
				DependsOn:  "A",
			},
			"A": {
				Type:       "AWS::SQS::Queue",
				Properties: map[string]any{},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "A", "B")
	})

	t.Run("Cycle detection still works", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"A": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": map[string]any{"Ref": "B"},
				},
			},
			"B": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": map[string]any{"Ref": "A"},
				},
			},
		}

		_, err := topoSort(resources)
		if err == nil {
			t.Fatal("expected cycle error, got nil")
		}
	})

	t.Run("Refs to pseudo-params and template params are not treated as deps", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"MyQueue": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": map[string]any{"Fn::Sub": "${AWS::StackName}-queue"},
					"Tags": []any{
						map[string]any{
							"Key":   "Region",
							"Value": map[string]any{"Ref": "AWS::Region"},
						},
					},
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(order) != 1 || order[0] != "MyQueue" {
			t.Fatalf("expected [MyQueue], got %v", order)
		}
	})
}

// assertBefore asserts that `a` appears before `b` in the ordering slice.
func assertBefore(t *testing.T, order []string, a, b string) {
	t.Helper()
	posA, posB := -1, -1
	for i, name := range order {
		if name == a {
			posA = i
		}
		if name == b {
			posB = i
		}
	}
	if posA < 0 {
		t.Fatalf("%q not found in order %v", a, order)
	}
	if posB < 0 {
		t.Fatalf("%q not found in order %v", b, order)
	}
	if posA >= posB {
		t.Fatalf("expected %q (pos %d) before %q (pos %d) in %v", a, posA, b, posB, order)
	}
}
