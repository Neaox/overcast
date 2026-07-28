package cloudformation_test

// unnamed_resources_test.go — physical names for resources whose template does
// not name them.
//
// CloudFormation generates a physical ID from the logical ID when a template
// omits the resource's name, and CDK relies on that: it almost never sets
// FunctionName, QueueName and friends, leaving CloudFormation to name them. A
// fallback built only from the stack name gives every unnamed resource of a
// type the same name, so the second one collides with the first and the stack
// rolls back — which is most real CDK stacks, since they rarely contain only
// one Lambda.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const twoUnnamedFunctionsTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Alpha": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "Runtime": "nodejs20.x",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": { "ZipFile": "exports.handler = async () => ({ from: 'alpha' });" }
      }
    },
    "Beta": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "Runtime": "nodejs20.x",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": { "ZipFile": "exports.handler = async () => ({ from: 'beta' });" }
      }
    }
  },
  "Outputs": {
    "AlphaName": { "Value": { "Ref": "Alpha" } },
    "BetaName":  { "Value": { "Ref": "Beta" } }
  }
}`

func TestCreateStack_twoUnnamedLambdaFunctionsGetDistinctNames(t *testing.T) {
	// Given: a stack with two Lambda functions, neither naming itself — the
	// shape CDK synthesises
	srv := helpers.NewTestServer(t)
	stackName := "two-unnamed-fns"

	// When: the stack is created
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{twoUnnamedFunctionsTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: it completes rather than failing on a name collision
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// And: the two functions have distinct names, each derived from its logical ID
	outputs := describeStackOutputs(t, srv, stackName)
	alpha, beta := outputs["AlphaName"], outputs["BetaName"]
	if alpha == "" || beta == "" {
		t.Fatalf("expected both function names in outputs; got %#v", outputs)
	}
	if alpha == beta {
		t.Fatalf("both unnamed functions were named %q; each needs its own name", alpha)
	}
	for logicalID, got := range map[string]string{"Alpha": alpha, "Beta": beta} {
		if !containsLogicalID(got, logicalID) {
			t.Errorf("function name %q does not identify its logical ID %q", got, logicalID)
		}
	}
}

const twoUnnamedQueuesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "First":  { "Type": "AWS::SQS::Queue" },
    "Second": { "Type": "AWS::SQS::Queue" }
  },
  "Outputs": {
    "FirstUrl":  { "Value": { "Ref": "First" } },
    "SecondUrl": { "Value": { "Ref": "Second" } }
  }
}`

// The same fallback is used by every resource handler, so the defect is not
// Lambda's alone — two unnamed queues collided identically.
func TestCreateStack_twoUnnamedQueuesGetDistinctNames(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "two-unnamed-queues"

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{twoUnnamedQueuesTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	outputs := describeStackOutputs(t, srv, stackName)
	first, second := outputs["FirstUrl"], outputs["SecondUrl"]
	if first == "" || second == "" {
		t.Fatalf("expected both queue URLs in outputs; got %#v", outputs)
	}
	if first == second {
		t.Fatalf("both unnamed queues resolved to %q; each needs its own name", first)
	}
}

// containsLogicalID reports whether a generated physical name identifies the
// logical ID it came from. Case-insensitive: services differ on what casing a
// name may carry, so the check is about identity, not spelling.
func containsLogicalID(physicalName, logicalID string) bool {
	return len(physicalName) > 0 && len(logicalID) > 0 &&
		containsFold(physicalName, logicalID)
}

func containsFold(haystack, needle string) bool {
	h, n := []rune(haystack), []rune(needle)
	if len(n) > len(h) {
		return false
	}
	lower := func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if lower(h[i+j]) != lower(n[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
