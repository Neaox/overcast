package cloudformation_test

// eventbus_ref_test.go — what Ref returns for AWS::Events::EventBus.
//
// AWS documents it as the event bus *name*: "When you pass the logical ID of
// this resource to the intrinsic Ref function, Ref returns the event bus name."
// Overcast returned the ARN, and because every consumer builds an ARN as
// "arn:aws:events:<region>:<account>:event-bus/" + name, a Ref handed onward
// produced a doubled ARN:
//
//	arn:aws:events:us-east-1:000000000000:event-bus/arn:aws:events:us-east-1:000000000000:event-bus/my-bus
//
// It stayed hidden because the CDK compat suite could not deploy far enough to
// reach the check.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const eventBusTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Bus": {
      "Type": "AWS::Events::EventBus",
      "Properties": { "Name": "ref-probe-bus" }
    }
  },
  "Outputs": {
    "BusRef": { "Value": { "Ref": "Bus" } },
    "BusArn": { "Value": { "Fn::GetAtt": ["Bus", "Arn"] } }
  }
}`

func TestCreateStack_eventBusRefIsTheNameNotTheArn(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"eventbus-ref-stack"},
		"TemplateBody": []string{eventBusTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "eventbus-ref-stack", "CREATE_COMPLETE")

	outputs := describeStackOutputs(t, srv, "eventbus-ref-stack")

	if got := outputs["BusRef"]; got != "ref-probe-bus" {
		t.Errorf("Ref = %q, want the bus name %q", got, "ref-probe-bus")
	}
	// And the ARN is still available via GetAtt, exactly once.
	arn := outputs["BusArn"]
	if !strings.HasSuffix(arn, ":event-bus/ref-probe-bus") {
		t.Errorf("GetAtt Arn = %q, want it to end in :event-bus/ref-probe-bus", arn)
	}
	if strings.Count(arn, "arn:aws:events:") != 1 {
		t.Errorf("GetAtt Arn = %q, want a single ARN rather than a doubled one", arn)
	}
}
