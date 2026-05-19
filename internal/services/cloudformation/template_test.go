package cloudformation

import (
	"encoding/json"
	"testing"
)

// TestParseTemplate_JSON verifies the existing JSON path still works after the
// format-detection refactor.
func TestParseTemplate_JSON(t *testing.T) {
	body := `{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": {
			"MyQueue": {
				"Type": "AWS::SQS::Queue",
				"Properties": { "QueueName": "test-queue" }
			}
		}
	}`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := tmpl.Resources["MyQueue"]; !ok {
		t.Error("expected MyQueue resource")
	}
}

// TestParseTemplate_YAML_plain verifies that a plain YAML template (no tag
// aliases) parses to the same structure as its JSON equivalent.
func TestParseTemplate_YAML_plain(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Description: plain yaml test
Parameters:
  Env:
    Type: String
    Default: dev
Resources:
  MyQueue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: test-queue
      VisibilityTimeout: 30
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := tmpl.Resources["MyQueue"]; !ok {
		t.Error("expected MyQueue resource")
	}
	if tmpl.Description != "plain yaml test" {
		t.Errorf("expected description 'plain yaml test', got %q", tmpl.Description)
	}
	if p, ok := tmpl.Parameters["Env"]; !ok || p.Default != "dev" {
		t.Errorf("expected parameter Env with default 'dev'")
	}
}

// TestParseTemplate_YAML_refTag verifies !Ref is converted to {"Ref": ...}.
func TestParseTemplate_YAML_refTag(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  MySubscription:
    Type: AWS::SNS::Subscription
    Properties:
      TopicArn: !Ref MyTopic
      Protocol: sqs
      Endpoint: arn:aws:sqs:us-east-1:000000000000:q
  MyTopic:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: my-topic
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub := tmpl.Resources["MySubscription"]
	topicArn := sub.Properties["TopicArn"]
	m, ok := topicArn.(map[string]any)
	if !ok {
		t.Fatalf("expected TopicArn to be map, got %T: %v", topicArn, topicArn)
	}
	if m["Ref"] != "MyTopic" {
		t.Errorf("expected Ref=MyTopic, got %v", m)
	}
}

// TestParseTemplate_YAML_getAttTag verifies !GetAtt Foo.Bar and !GetAtt [Foo, Bar].
func TestParseTemplate_YAML_getAttTag(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Consumer:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: consumer
      QueueArn: !GetAtt MyQueue.Arn
  MyQueue:
    Type: AWS::SQS::Queue
    Properties: {}
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	props := tmpl.Resources["Consumer"].Properties
	v, ok := props["QueueArn"].(map[string]any)
	if !ok {
		t.Fatalf("expected QueueArn to be map, got %T", props["QueueArn"])
	}
	arr, ok := v["Fn::GetAtt"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "MyQueue" || arr[1] != "Arn" {
		t.Errorf("expected Fn::GetAtt=[MyQueue, Arn], got %v", v["Fn::GetAtt"])
	}
}

// TestParseTemplate_YAML_subTag verifies !Sub string and !Sub [string, vars].
func TestParseTemplate_YAML_subTag(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Param:
    Type: AWS::SSM::Parameter
    Properties:
      Name: !Sub "/app/${AWS::StackName}/config"
      Type: String
      Value: hello
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	props := tmpl.Resources["Param"].Properties
	v, ok := props["Name"].(map[string]any)
	if !ok {
		t.Fatalf("expected Name to be map, got %T", props["Name"])
	}
	if v["Fn::Sub"] != "/app/${AWS::StackName}/config" {
		t.Errorf("expected Fn::Sub value, got %v", v)
	}
}

// TestParseTemplate_YAML_joinTag verifies !Join [delim, [values]].
func TestParseTemplate_YAML_joinTag(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Bucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Join
        - "-"
        - - my
          - bucket
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	props := tmpl.Resources["Bucket"].Properties
	v, ok := props["BucketName"].(map[string]any)
	if !ok {
		t.Fatalf("expected BucketName to be map, got %T: %v", props["BucketName"], props["BucketName"])
	}
	arr, ok := v["Fn::Join"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("expected Fn::Join=[delim, [values]], got %v", v)
	}
	if arr[0] != "-" {
		t.Errorf("expected delimiter '-', got %v", arr[0])
	}
}

// TestParseTemplate_YAML_ifTag verifies !If [cond, true, false].
func TestParseTemplate_YAML_ifTag(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Conditions:
  IsProd: !Equals [prod, prod]
Resources:
  Bucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !If [IsProd, prod-bucket, dev-bucket]
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	props := tmpl.Resources["Bucket"].Properties
	v, ok := props["BucketName"].(map[string]any)
	if !ok {
		t.Fatalf("expected BucketName to be map, got %T", props["BucketName"])
	}
	arr, ok := v["Fn::If"].([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("expected Fn::If=[cond,a,b], got %v", v)
	}
	if arr[0] != "IsProd" {
		t.Errorf("expected condition IsProd, got %v", arr[0])
	}
}

// TestParseTemplate_YAML_implicitDepsFromRef verifies that YAML-parsed
// templates still produce correct implicit dependency ordering via topoSort.
func TestParseTemplate_YAML_implicitDepsFromRef(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Subscription:
    Type: AWS::SNS::Subscription
    Properties:
      TopicArn: !Ref Topic
      Protocol: sqs
      Endpoint: arn:aws:sqs:us-east-1:000000000000:q
  Topic:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: my-topic
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	order, err := topoSort(tmpl.Resources)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	assertBefore(t, order, "Topic", "Subscription")
}

// TestParseTemplate_YAML_roundTrip verifies that a moderately complex YAML
// template round-trips cleanly through the JSON normalisation step.
func TestParseTemplate_YAML_roundTrip(t *testing.T) {
	body := `
AWSTemplateFormatVersion: "2010-09-09"
Description: round-trip test
Parameters:
  Env:
    Type: String
    Default: dev
Resources:
  OrderQueue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "orders-${Env}"
      VisibilityTimeout: 60
  NotifyTopic:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: !Sub "notify-${Env}"
  Subscription:
    Type: AWS::SNS::Subscription
    Properties:
      TopicArn: !Ref NotifyTopic
      Protocol: sqs
      Endpoint: !GetAtt OrderQueue.Arn
`
	tmpl, err := parseTemplate(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tmpl.Resources) != 3 {
		t.Fatalf("expected 3 resources, got %d", len(tmpl.Resources))
	}

	// Confirm Fn::Sub is preserved.
	queueProps := tmpl.Resources["OrderQueue"].Properties
	nameVal, _ := json.Marshal(queueProps["QueueName"])
	if string(nameVal) != `{"Fn::Sub":"orders-${Env}"}` {
		t.Errorf("unexpected QueueName: %s", nameVal)
	}

	// Confirm ordering: Subscription must come after both its refs.
	order, err := topoSort(tmpl.Resources)
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	assertBefore(t, order, "NotifyTopic", "Subscription")
	assertBefore(t, order, "OrderQueue", "Subscription")
}
