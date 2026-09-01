package cloudformation_test

// Integration coverage for issue #534: sfnStateMachineHandler.Create sent
// only name, definition, roleArn and type to CreateStateMachine, so a state
// machine whose ASL used DefinitionSubstitutions placeholders
// (${LambdaArn}-style) was stored with the placeholders unresolved — inert
// today, but PR #504's ASL interpreter would execute the literal "${...}"
// string and fail at the first task state. LoggingConfiguration,
// TracingConfiguration and Tags were dropped on Create too (Tags entirely;
// Logging/Tracing were read on Update only), and StateMachineType changes on
// Update were silently ignored instead of forcing replacement.
//
// These tests provision through CloudFormation and read the result back
// through Step Functions' own DescribeStateMachine/ListTagsForResource, so
// template and service must agree.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func sfnCFNJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, "AWSStepFunctions.", action, "application/x-amz-json-1.0", body)
}

func describeSFNStateMachine(t *testing.T, srv *helpers.TestServer, arn string) map[string]any {
	t.Helper()
	resp := sfnCFNJSONCall(t, srv, "DescribeStateMachine", map[string]any{"stateMachineArn": arn})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out map[string]any
	helpers.DecodeJSON(t, resp, &out)
	return out
}

func listSFNTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := sfnCFNJSONCall(t, srv, "ListTagsForResource", map[string]any{"resourceArn": arn})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"tags"`
	}
	helpers.DecodeJSON(t, resp, &out)
	tags := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		tags[tag.Key] = tag.Value
	}
	return tags
}

// sfnStateMachineARN mirrors how the emulator builds the ARN
// (protocol.ARN(region, account, "states", "stateMachine:"+name)) so tests
// can address a resource without threading the CreateStack response through
// DescribeStackResources.
func sfnStateMachineARN(name string) string {
	return "arn:aws:states:us-east-1:000000000000:stateMachine:" + name
}

func sfnPropertiesTemplate(name, lambdaArn, loggingLevel, tracingEnabled, tag string) string {
	tmpl := `{
  "Resources": {
    "Workflow": {
      "Type": "AWS::StepFunctions::StateMachine",
      "Properties": {
        "StateMachineName": "%s",
        "DefinitionString": "{\"Comment\":\"cfn-properties\",\"StartAt\":\"Invoke\",\"States\":{\"Invoke\":{\"Type\":\"Task\",\"Resource\":\"${LambdaArn}\",\"Parameters\":{\"Note\":\"${Unmatched}\"},\"End\":true}}}",
        "DefinitionSubstitutions": {
          "LambdaArn": "%s"
        },
        "RoleArn": "arn:aws:iam::000000000000:role/sfn-role",
        "LoggingConfiguration": {
          "Level": "%s",
          "IncludeExecutionData": true
        },
        "TracingConfiguration": {"Enabled": %s},
        "Tags": [{"Key": "stage", "Value": "%s"}]
      }
    }
  }
}`
	return fmt.Sprintf(tmpl, name, lambdaArn, loggingLevel, tracingEnabled, tag)
}

// TestCreateStack_StepFunctionsStateMachine_definitionSubstitutionsResolved is
// the headline regression for #534: a matched ${key} placeholder must be
// replaced with its DefinitionSubstitutions value before the definition
// reaches Step Functions, and an unmatched placeholder must be left verbatim
// rather than failing the deploy.
func TestCreateStack_StepFunctionsStateMachine_definitionSubstitutionsResolved(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "sfn-definition-substitutions"
	lambdaArn := "arn:aws:lambda:us-east-1:000000000000:function:configured-fn"
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {sfnPropertiesTemplate("cfn-properties-workflow", lambdaArn, "ALL", "true", "created")},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	arn := sfnStateMachineARN("cfn-properties-workflow")
	described := describeSFNStateMachine(t, srv, arn)
	definition, _ := described["definition"].(string)

	if !strings.Contains(definition, lambdaArn) {
		t.Errorf("definition = %s, want it to contain the substituted %q", definition, lambdaArn)
	}
	if strings.Contains(definition, "${LambdaArn}") {
		t.Errorf("definition = %s, want no unresolved ${LambdaArn} placeholder", definition)
	}
	if !strings.Contains(definition, "${Unmatched}") {
		t.Errorf("definition = %s, want the unmatched ${Unmatched} placeholder left verbatim", definition)
	}
}

// TestCreateStack_StepFunctionsStateMachine_reconcilesDroppedProperties covers
// LoggingConfiguration, TracingConfiguration and Tags on Create, then their
// reconciliation (including DefinitionSubstitutions re-applied) on Update.
func TestCreateStack_StepFunctionsStateMachine_reconcilesDroppedProperties(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "sfn-properties"
	name := "cfn-properties-full"
	arn := sfnStateMachineARN(name)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {sfnPropertiesTemplate(name,
			"arn:aws:lambda:us-east-1:000000000000:function:created-fn", "ALL", "true", "created")},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	described := describeSFNStateMachine(t, srv, arn)
	logging, ok := described["loggingConfiguration"].(map[string]any)
	if !ok || logging["Level"] != "ALL" {
		t.Errorf("loggingConfiguration = %+v, want Level=ALL", described["loggingConfiguration"])
	}
	tracing, ok := described["tracingConfiguration"].(map[string]any)
	if !ok || tracing["Enabled"] != true {
		t.Errorf("tracingConfiguration = %+v, want Enabled=true", described["tracingConfiguration"])
	}
	tags := listSFNTags(t, srv, arn)
	if tags["stage"] != "created" {
		t.Errorf("tags = %+v, want stage=created", tags)
	}

	// When: the stack updates the substitution value, logging level, tracing
	// flag and tag in place (StateMachineName and StateMachineType are
	// unchanged, so no replacement is triggered).
	resp = cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {sfnPropertiesTemplate(name,
			"arn:aws:lambda:us-east-1:000000000000:function:updated-fn", "ERROR", "false", "updated")},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	described = describeSFNStateMachine(t, srv, arn)
	definition, _ := described["definition"].(string)
	if !strings.Contains(definition, "updated-fn") {
		t.Errorf("updated definition = %s, want the new substitution value", definition)
	}
	if strings.Contains(definition, "created-fn") {
		t.Errorf("updated definition = %s, want the old substitution value gone", definition)
	}
	logging, ok = described["loggingConfiguration"].(map[string]any)
	if !ok || logging["Level"] != "ERROR" {
		t.Errorf("updated loggingConfiguration = %+v, want Level=ERROR", described["loggingConfiguration"])
	}
	tracing, ok = described["tracingConfiguration"].(map[string]any)
	if !ok || tracing["Enabled"] != false {
		t.Errorf("updated tracingConfiguration = %+v, want Enabled=false", described["tracingConfiguration"])
	}
	tags = listSFNTags(t, srv, arn)
	if tags["stage"] != "updated" {
		t.Errorf("updated tags = %+v, want stage=updated", tags)
	}
	// And: the same ARN survived the update — an in-place property change
	// must not replace the resource.
	if described["stateMachineArn"] != arn {
		t.Errorf("stateMachineArn = %v, want unchanged %q (update should not replace)", described["stateMachineArn"], arn)
	}
}

// TestUpdateStack_StepFunctionsStateMachine_reconcilesTagRemoval covers the
// half of tag reconciliation a same-key update cannot: a key present in the
// old template and absent from the new one must be untagged, not left stale.
func TestUpdateStack_StepFunctionsStateMachine_reconcilesTagRemoval(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "sfn-tag-removal"
	name := "cfn-tag-removal-workflow"
	arn := sfnStateMachineARN(name)
	definition := `{\"StartAt\":\"Pass\",\"States\":{\"Pass\":{\"Type\":\"Pass\",\"End\":true}}}`

	withTags := fmt.Sprintf(`{
  "Resources": {
    "Workflow": {
      "Type": "AWS::StepFunctions::StateMachine",
      "Properties": {
        "StateMachineName": "%s",
        "DefinitionString": "%s",
        "RoleArn": "arn:aws:iam::000000000000:role/sfn-role",
        "Tags": [{"Key": "team", "Value": "platform"}, {"Key": "temp", "Value": "drop-me"}]
      }
    }
  }
}`, name, definition)
	withoutTemp := fmt.Sprintf(`{
  "Resources": {
    "Workflow": {
      "Type": "AWS::StepFunctions::StateMachine",
      "Properties": {
        "StateMachineName": "%s",
        "DefinitionString": "%s",
        "RoleArn": "arn:aws:iam::000000000000:role/sfn-role",
        "Tags": [{"Key": "team", "Value": "platform"}]
      }
    }
  }
}`, name, definition)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {withTags}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	tags := listSFNTags(t, srv, arn)
	if tags["team"] != "platform" || tags["temp"] != "drop-me" {
		t.Fatalf("initial tags = %+v", tags)
	}

	resp2 := cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {withoutTemp}})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	tags = listSFNTags(t, srv, arn)
	if tags["team"] != "platform" {
		t.Errorf("tags after removal = %+v, want team=platform kept", tags)
	}
	if _, present := tags["temp"]; present {
		t.Errorf("tags after removal = %+v, want temp removed", tags)
	}
}

// TestUpdateStack_StepFunctionsStateMachine_typeChangeRequiresReplacement
// guards against the pre-#534 bug where a StateMachineType change on Update
// was silently ignored (`_ = t`) instead of forcing replacement — real AWS
// documents StateMachineType as "Update requires: Replacement" and refuses
// to change it in place ("You cannot update the type of a state machine
// once it has been created").
func TestUpdateStack_StepFunctionsStateMachine_typeChangeRequiresReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "sfn-type-replacement"
	definition := `{\"StartAt\":\"Pass\",\"States\":{\"Pass\":{\"Type\":\"Pass\",\"End\":true}}}`
	template := func(smType string) string {
		return fmt.Sprintf(`{
  "Resources": {
    "Workflow": {
      "Type": "AWS::StepFunctions::StateMachine",
      "Properties": {
        "StateMachineType": "%s",
        "DefinitionString": "%s",
        "RoleArn": "arn:aws:iam::000000000000:role/sfn-role"
      }
    }
  }
}`, smType, definition)
	}

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template("STANDARD")}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	firstARN := stackResourceRowFor(t, srv, "DescribeStackResources", "StackResources", stackName, "Workflow").PhysicalResourceID

	resp2 := cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template("EXPRESS")}})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	secondARN := stackResourceRowFor(t, srv, "DescribeStackResources", "StackResources", stackName, "Workflow").PhysicalResourceID
	if secondARN == firstARN {
		t.Fatalf("StateMachineType change kept the same physical resource %q, want replacement", firstARN)
	}

	described := describeSFNStateMachine(t, srv, secondARN)
	if described["type"] != "EXPRESS" {
		t.Errorf("replaced state machine type = %v, want EXPRESS", described["type"])
	}
}
