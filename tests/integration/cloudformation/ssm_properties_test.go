package cloudformation_test

// Integration coverage for issue #532: ssmParameterHandler.Create sent only
// Name, Type and Value to PutParameter, so Description, Tags, Tier, DataType,
// AllowedPattern and Policies on AWS::SSM::Parameter provisioned successfully
// and then vanished. Tier, DataType and Policies were dropped on the SSM
// service's own PutParameter/DescribeParameters path too — the response
// fields existed but were hardcoded constants, so they read as configured
// when they were not. These tests provision through CloudFormation and read
// the result back through SSM's own operations, so template and service must
// agree.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func ssmJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, "AmazonSSM.", action, "application/x-amz-json-1.1", body)
}

func describeSSMParameter(t *testing.T, srv *helpers.TestServer, name string) map[string]any {
	t.Helper()
	resp := ssmJSONCall(t, srv, "DescribeParameters", map[string]any{
		"ParameterFilters": []map[string]any{
			{"Key": "Name", "Option": "Equals", "Values": []string{name}},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Parameters []map[string]any `json:"Parameters"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if len(out.Parameters) != 1 {
		t.Fatalf("DescribeParameters(%s): got %d parameters, want 1: %+v", name, len(out.Parameters), out.Parameters)
	}
	return out.Parameters[0]
}

func listSSMParameterTags(t *testing.T, srv *helpers.TestServer, name string) map[string]string {
	t.Helper()
	resp := ssmJSONCall(t, srv, "ListTagsForResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   name,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		TagList []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"TagList"`
	}
	helpers.DecodeJSON(t, resp, &out)
	tags := make(map[string]string, len(out.TagList))
	for _, tag := range out.TagList {
		tags[tag.Key] = tag.Value
	}
	return tags
}

func ssmParameterTemplate(description, tier, allowedPattern, policyType, stage string) string {
	tmpl := `{
  "Resources": {
    "Config": {
      "Type": "AWS::SSM::Parameter",
      "Properties": {
        "Name": "/cfn-properties/config",
        "Type": "String",
        "Value": "configured-value",
        "Description": "%s",
        "Tier": "%s",
        "DataType": "text",
        "AllowedPattern": "%s",
        "Policies": "[{\"Type\":\"%s\",\"Version\":\"1.0\",\"Attributes\":{\"Timestamp\":\"2026-01-01T00:00:00.000Z\"}}]",
        "Tags": {
          "stage": "%s"
        }
      }
    }
  }
}`
	return fmt.Sprintf(tmpl, description, tier, allowedPattern, policyType, stage)
}

func TestCreateStack_SSMParameter_reconcilesDroppedProperties(t *testing.T) {
	// Given: a parameter with every property #532 reported as dropped.
	srv := helpers.NewTestServer(t)
	stackName := "ssm-parameter-properties"
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {ssmParameterTemplate("created by CloudFormation", "Advanced", "^[a-zA-Z0-9-]+$", "Expiration", "created")},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: DescribeParameters echoes every scalar back, not a constant.
	described := describeSSMParameter(t, srv, "/cfn-properties/config")
	if described["Description"] != "created by CloudFormation" {
		t.Errorf("Description = %v, want %q", described["Description"], "created by CloudFormation")
	}
	if described["Tier"] != "Advanced" {
		t.Errorf("Tier = %v, want %q", described["Tier"], "Advanced")
	}
	if described["DataType"] != "text" {
		t.Errorf("DataType = %v, want %q", described["DataType"], "text")
	}
	if described["AllowedPattern"] != "^[a-zA-Z0-9-]+$" {
		t.Errorf("AllowedPattern = %v, want %q", described["AllowedPattern"], "^[a-zA-Z0-9-]+$")
	}
	policies, ok := described["Policies"].([]any)
	if !ok || len(policies) != 1 {
		t.Fatalf("Policies = %+v, want one entry", described["Policies"])
	}
	policy, ok := policies[0].(map[string]any)
	if !ok || policy["PolicyType"] != "Expiration" {
		t.Errorf("Policies[0] = %+v, want PolicyType Expiration", policies[0])
	}

	// And: the Tags property reached SSM's own tag store.
	tags := listSSMParameterTags(t, srv, "/cfn-properties/config")
	if tags["stage"] != "created" {
		t.Errorf("tags = %+v, want stage=created", tags)
	}

	// When: the stack updates every one of those properties in place.
	resp = cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {ssmParameterTemplate("updated by CloudFormation", "Standard", "^[0-9]+$", "NoChangeNotification", "updated")},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the new values replace the old ones.
	described = describeSSMParameter(t, srv, "/cfn-properties/config")
	if described["Description"] != "updated by CloudFormation" {
		t.Errorf("updated Description = %v, want %q", described["Description"], "updated by CloudFormation")
	}
	if described["Tier"] != "Standard" {
		t.Errorf("updated Tier = %v, want %q", described["Tier"], "Standard")
	}
	if described["AllowedPattern"] != "^[0-9]+$" {
		t.Errorf("updated AllowedPattern = %v, want %q", described["AllowedPattern"], "^[0-9]+$")
	}
	policies, ok = described["Policies"].([]any)
	if !ok || len(policies) != 1 {
		t.Fatalf("updated Policies = %+v, want one entry", described["Policies"])
	}
	policy, ok = policies[0].(map[string]any)
	if !ok || policy["PolicyType"] != "NoChangeNotification" {
		t.Errorf("updated Policies[0] = %+v, want PolicyType NoChangeNotification", policies[0])
	}

	// And: the tag set was reconciled — the old value is replaced, not merged.
	tags = listSSMParameterTags(t, srv, "/cfn-properties/config")
	if tags["stage"] != "updated" {
		t.Errorf("updated tags = %+v, want stage=updated", tags)
	}
}

// TestUpdateStack_SSMParameter_reconcilesTagRemoval covers the half of tag
// reconciliation a same-key update cannot: a key present in the old template
// and absent from the new one must be untagged, not merely left stale.
func TestUpdateStack_SSMParameter_reconcilesTagRemoval(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "ssm-parameter-tag-removal"
	withTags := `{
  "Resources": {
    "Config": {
      "Type": "AWS::SSM::Parameter",
      "Properties": {
        "Name": "/cfn-properties/tag-removal",
        "Type": "String",
        "Value": "v1",
        "Tags": {"team": "platform", "temp": "drop-me"}
      }
    }
  }
}`
	withoutTemp := `{
  "Resources": {
    "Config": {
      "Type": "AWS::SSM::Parameter",
      "Properties": {
        "Name": "/cfn-properties/tag-removal",
        "Type": "String",
        "Value": "v1",
        "Tags": {"team": "platform"}
      }
    }
  }
}`

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {withTags}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	tags := listSSMParameterTags(t, srv, "/cfn-properties/tag-removal")
	if tags["team"] != "platform" || tags["temp"] != "drop-me" {
		t.Fatalf("initial tags = %+v", tags)
	}

	resp2 := cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {withoutTemp}})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	tags = listSSMParameterTags(t, srv, "/cfn-properties/tag-removal")
	if tags["team"] != "platform" {
		t.Errorf("tags after removal = %+v, want team=platform kept", tags)
	}
	if _, present := tags["temp"]; present {
		t.Errorf("tags after removal = %+v, want temp removed", tags)
	}
}

// TestSSMParameterTemplate_policiesFieldIsValidJSON guards the fmt.Sprintf
// template above against an escaping mistake: Policies is a JSON-encoded
// string embedded inside a JSON document, and a bad edit there fails silently
// as a parameter with unparseable Policies rather than a template error.
func TestSSMParameterTemplate_policiesFieldIsValidJSON(t *testing.T) {
	tmpl := ssmParameterTemplate("d", "Standard", "^.*$", "Expiration", "s")
	var doc struct {
		Resources struct {
			Config struct {
				Properties struct {
					Policies string `json:"Policies"`
				} `json:"Properties"`
			} `json:"Config"`
		} `json:"Resources"`
	}
	if err := json.Unmarshal([]byte(tmpl), &doc); err != nil {
		t.Fatalf("template is not valid JSON: %v", err)
	}
	var policies []map[string]any
	if err := json.Unmarshal([]byte(doc.Resources.Config.Properties.Policies), &policies); err != nil {
		t.Fatalf("Policies is not a valid JSON array: %v", err)
	}
	if len(policies) != 1 || policies[0]["Type"] != "Expiration" {
		t.Fatalf("Policies = %+v", policies)
	}
}
