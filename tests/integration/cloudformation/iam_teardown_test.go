package cloudformation_test

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// iamQuery performs an IAM Query-protocol request against the test server.
func iamQuery(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2010-05-08")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("iamQuery: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("iamQuery: do: %v", err)
	}
	return resp
}

// TestDeleteStack_iamRoleWithInlinePolicy exercises the teardown order IAM's
// DeleteConflict now demands: AWS::IAM::Policy writes an inline policy onto the
// role, and AWS refuses DeleteRole while that document is still there. The
// AWS::IAM::Policy resource Refs the role, so CloudFormation deletes the policy
// first — and its provider has to actually remove the document, not assume the
// role delete will take it along.
func TestDeleteStack_iamRoleWithInlinePolicy(t *testing.T) {
	// Given: a stack with a role and an inline policy attached to it
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-teardown-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "lambda.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "AppPolicy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "cfn-teardown-policy",
        "Roles": [{"Ref": "AppRole"}],
        "PolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]
        }
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-teardown-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-teardown-stack", "CREATE_COMPLETE")

	// And: the inline policy really is on the role
	pol := iamQuery(t, srv, "GetRolePolicy", url.Values{
		"RoleName": []string{"cfn-teardown-role"}, "PolicyName": []string{"cfn-teardown-policy"},
	})
	pol.Body.Close()
	helpers.AssertStatus(t, pol, http.StatusOK)

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"iam-teardown-stack"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: teardown completes rather than stalling on a DeleteConflict
	waitForStackStatus(t, srv, "iam-teardown-stack", "DELETE_COMPLETE")

	// And: the role is gone
	role := iamQuery(t, srv, "GetRole", url.Values{"RoleName": []string{"cfn-teardown-role"}})
	defer role.Body.Close()
	helpers.AssertStatus(t, role, http.StatusNotFound)
}

// TestDeleteStack_iamRoleInInstanceProfile covers the other ordering IAM now
// enforces: a role that is still in an instance profile cannot be deleted. The
// profile Refs the role, so CloudFormation removes the profile first.
func TestDeleteStack_iamRoleInInstanceProfile(t *testing.T) {
	// Given: a stack with a role and an instance profile holding it
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-profile-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "ec2.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "AppProfile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "cfn-profile",
        "Roles": [{"Ref": "AppRole"}]
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-profile-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-profile-stack", "CREATE_COMPLETE")

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"iam-profile-stack"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: teardown completes and the role is gone
	waitForStackStatus(t, srv, "iam-profile-stack", "DELETE_COMPLETE")
	role := iamQuery(t, srv, "GetRole", url.Values{"RoleName": []string{"cfn-profile-role"}})
	defer role.Body.Close()
	helpers.AssertStatus(t, role, http.StatusNotFound)
}

// TestDeleteStack_iamRolePolicyAndInstanceProfile is the combined shape the two
// tests above cover one edge of each: a role that is both written to by an
// inline policy and held by an instance profile. Both blocks have to be cleared,
// and only the teardown order CloudFormation derives from the Refs clears them —
// which is what makes it safe for a refusal that survives to fail the stack.
func TestDeleteStack_iamRolePolicyAndInstanceProfile(t *testing.T) {
	// Given: a stack whose role is held by both an inline policy and a profile
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-combined-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "ec2.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "AppPolicy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "cfn-combined-policy",
        "Roles": [{"Ref": "AppRole"}],
        "PolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]
        }
      }
    },
    "AppProfile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "cfn-combined-profile",
        "Roles": [{"Ref": "AppRole"}]
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-combined-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-combined-stack", "CREATE_COMPLETE")

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"iam-combined-stack"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: teardown completes — both blocks were cleared in order
	waitForStackStatus(t, srv, "iam-combined-stack", "DELETE_COMPLETE")

	// And: nothing the stack owned is left standing
	for _, probe := range []struct {
		action string
		params url.Values
	}{
		{"GetRole", url.Values{"RoleName": []string{"cfn-combined-role"}}},
		{"GetInstanceProfile", url.Values{"InstanceProfileName": []string{"cfn-combined-profile"}}},
	} {
		resp := iamQuery(t, srv, probe.action, probe.params)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s after teardown: status = %d, want 404", probe.action, resp.StatusCode)
		}
	}
}

// TestDeleteStack_iamRoleBlockedByExternalDependency is the case the stack
// cannot clear for itself: something outside the stack attached a managed policy
// to the role, so IAM answers DeleteConflict and no amount of teardown ordering
// helps. Real CloudFormation fails the stack — it does not report DELETE_COMPLETE
// over a role that is still there.
func TestDeleteStack_iamRoleBlockedByExternalDependency(t *testing.T) {
	// Given: a stack owning a single role
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-blocked-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "lambda.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-blocked-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-blocked-stack", "CREATE_COMPLETE")

	// And: something outside the stack attaches a managed policy to the role
	pol := iamQuery(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     []string{"outside-the-stack"},
		"PolicyDocument": []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
	})
	polBody := readBody(t, pol)
	pol.Body.Close()
	helpers.AssertStatus(t, pol, http.StatusOK)
	policyARN := xmlTagValue(string(polBody), "Arn")
	if policyARN == "" {
		t.Fatalf("CreatePolicy returned no Arn: %s", polBody)
	}
	attach := iamQuery(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  []string{"cfn-blocked-role"},
		"PolicyArn": []string{policyARN},
	})
	attach.Body.Close()
	helpers.AssertStatus(t, attach, http.StatusOK)

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"iam-blocked-stack"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: the stack fails rather than claiming the role is gone
	waitForStackStatus(t, srv, "iam-blocked-stack", "DELETE_FAILED")

	// And: the role really is still there
	role := iamQuery(t, srv, "GetRole", url.Values{"RoleName": []string{"cfn-blocked-role"}})
	role.Body.Close()
	helpers.AssertStatus(t, role, http.StatusOK)

	// And: AWS's own refusal message reached the stack event
	const wantMessage = "Cannot delete entity, must detach all policies first."
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"iam-blocked-stack"},
	})
	eventsBody := string(readBody(t, events))
	events.Body.Close()
	if !strings.Contains(eventsBody, wantMessage) {
		t.Errorf("stack events do not carry IAM's refusal message %q:\n%s", wantMessage, eventsBody)
	}
	if !strings.Contains(eventsBody, "DELETE_FAILED") {
		t.Errorf("stack events have no DELETE_FAILED event:\n%s", eventsBody)
	}

	// And: the resource stays listed as DELETE_FAILED, with the reason, so a
	// retry knows what is still standing and why.
	status, reason := listedStackResource(t, srv, "iam-blocked-stack", "AppRole")
	if status != "DELETE_FAILED" {
		t.Errorf("ListStackResources AppRole status = %q, want DELETE_FAILED", status)
	}
	if !strings.Contains(reason, wantMessage) {
		t.Errorf("ListStackResources AppRole reason = %q, want it to contain %q", reason, wantMessage)
	}

	// And: DescribeStackResources reports the reason too, as AWS does.
	descStatus, descReason := describedStackResource(t, srv, "iam-blocked-stack", "AppRole")
	if descStatus != "DELETE_FAILED" {
		t.Errorf("DescribeStackResources AppRole status = %q, want DELETE_FAILED", descStatus)
	}
	if !strings.Contains(descReason, wantMessage) {
		t.Errorf("DescribeStackResources AppRole reason = %q, want it to contain %q", descReason, wantMessage)
	}
}

// TestDeleteStack_iamUserBlockedByExternalAccessKey is the same refusal reached
// through DeleteUser: an access key created outside the stack is a dependency
// the teardown cannot clear, and AWS refuses the delete.
func TestDeleteStack_iamUserBlockedByExternalAccessKey(t *testing.T) {
	// Given: a stack owning a single user
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppUser": {
      "Type": "AWS::IAM::User",
      "Properties": {"UserName": "cfn-blocked-user"}
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-user-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-user-stack", "CREATE_COMPLETE")

	// And: an access key minted outside the stack
	key := iamQuery(t, srv, "CreateAccessKey", url.Values{"UserName": []string{"cfn-blocked-user"}})
	key.Body.Close()
	helpers.AssertStatus(t, key, http.StatusOK)

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"iam-user-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: the stack fails and the user survives
	waitForStackStatus(t, srv, "iam-user-stack", "DELETE_FAILED")
	user := iamQuery(t, srv, "GetUser", url.Values{"UserName": []string{"cfn-blocked-user"}})
	user.Body.Close()
	helpers.AssertStatus(t, user, http.StatusOK)

	status, reason := listedStackResource(t, srv, "iam-user-stack", "AppUser")
	if status != "DELETE_FAILED" {
		t.Errorf("ListStackResources AppUser status = %q, want DELETE_FAILED", status)
	}
	if !strings.Contains(reason, "Cannot delete entity, must delete access keys first.") {
		t.Errorf("ListStackResources AppUser reason = %q, want IAM's access-key refusal", reason)
	}
}

// TestDeleteStack_iamManagedPolicyBlockedByExternalAttachment covers the third
// entity whose delete IAM refuses: a managed policy still attached to something.
func TestDeleteStack_iamManagedPolicyBlockedByExternalAttachment(t *testing.T) {
	// Given: a stack owning a managed policy
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppPolicy": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "cfn-blocked-managed-policy",
        "PolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]
        }
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-managed-policy-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-managed-policy-stack", "CREATE_COMPLETE")

	policyARN := listedStackResourcePhysicalID(t, srv, "iam-managed-policy-stack", "AppPolicy")

	// And: a role outside the stack attaches it
	roleResp := iamQuery(t, srv, "CreateRole", url.Values{
		"RoleName":                 []string{"outside-the-stack-role"},
		"AssumeRolePolicyDocument": []string{`{"Version":"2012-10-17","Statement":[]}`},
	})
	roleResp.Body.Close()
	helpers.AssertStatus(t, roleResp, http.StatusOK)
	attach := iamQuery(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  []string{"outside-the-stack-role"},
		"PolicyArn": []string{policyARN},
	})
	attach.Body.Close()
	helpers.AssertStatus(t, attach, http.StatusOK)

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"iam-managed-policy-stack"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: the stack fails and the policy survives
	waitForStackStatus(t, srv, "iam-managed-policy-stack", "DELETE_FAILED")
	status, reason := listedStackResource(t, srv, "iam-managed-policy-stack", "AppPolicy")
	if status != "DELETE_FAILED" {
		t.Errorf("ListStackResources AppPolicy status = %q, want DELETE_FAILED", status)
	}
	if !strings.Contains(reason, "Cannot delete a policy attached to entities.") {
		t.Errorf("ListStackResources AppPolicy reason = %q, want IAM's attachment refusal", reason)
	}
}

// TestDeleteStack_iamRoleAlreadyGoneStillCompletes is the other half of the
// contract deleteResource keeps: a refusal is fatal, but an entity that is
// already gone is not. Deleting the role out from under the stack must still
// leave the teardown able to finish.
func TestDeleteStack_iamRoleAlreadyGoneStillCompletes(t *testing.T) {
	// Given: a stack owning a role
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-vanished-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "lambda.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-vanished-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-vanished-stack", "CREATE_COMPLETE")

	// And: the role is deleted behind the stack's back
	gone := iamQuery(t, srv, "DeleteRole", url.Values{"RoleName": []string{"cfn-vanished-role"}})
	gone.Body.Close()
	helpers.AssertStatus(t, gone, http.StatusOK)

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"iam-vanished-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: teardown still completes — NoSuchEntity is a success, not a block
	waitForStackStatus(t, srv, "iam-vanished-stack", "DELETE_COMPLETE")
}

// TestUpdateStack_removedIamRoleAndProfileDeleteInOrder covers the other place
// resources are torn down: UpdateStack's cleanup phase, which removes what the
// new template dropped. It used to walk a map, so the order was random — and
// with IAM enforcing DeleteConflict, deleting the role before the instance
// profile holding it leaves the role standing behind an UPDATE_COMPLETE.
func TestUpdateStack_removedIamRoleAndProfileDeleteInOrder(t *testing.T) {
	// Given: a stack with a queue it keeps, plus a role held by an instance profile
	srv := helpers.NewTestServer(t)
	const stackName = "iam-update-cleanup-stack"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": []string{stackName},
		"TemplateBody": []string{`{
  "Resources": {
    "Keeper": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "iam-update-cleanup-queue"}
    },
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-update-cleanup-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "ec2.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "AppProfile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "cfn-update-cleanup-profile",
        "Roles": [{"Ref": "AppRole"}]
      }
    }
  }
}`},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: an update drops both the role and the profile
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": []string{stackName},
		"TemplateBody": []string{`{
  "Resources": {
    "Keeper": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "iam-update-cleanup-queue"}
    }
  }
}`},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: both really are gone — the profile was removed before the role
	for _, probe := range []struct {
		action string
		params url.Values
	}{
		{"GetInstanceProfile", url.Values{"InstanceProfileName": []string{"cfn-update-cleanup-profile"}}},
		{"GetRole", url.Values{"RoleName": []string{"cfn-update-cleanup-role"}}},
	} {
		resp := iamQuery(t, srv, probe.action, probe.params)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s after update cleanup: status = %d, want 404", probe.action, resp.StatusCode)
		}
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// xmlTagValue pulls the text of the first occurrence of a simple XML element.
func xmlTagValue(body, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(body, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(body[i:], close)
	if j < 0 {
		return ""
	}
	return body[i : i+j]
}

type stackResourceRow struct {
	LogicalResourceID    string `xml:"LogicalResourceId"`
	PhysicalResourceID   string `xml:"PhysicalResourceId"`
	ResourceStatus       string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason"`
}

// listedStackResource returns the status and reason ListStackResources reports
// for one logical ID.
func listedStackResource(t *testing.T, srv *helpers.TestServer, stackName, logicalID string) (string, string) {
	t.Helper()
	row := stackResourceRowFor(t, srv, "ListStackResources", "StackResourceSummaries", stackName, logicalID)
	return row.ResourceStatus, row.ResourceStatusReason
}

// describedStackResource is the same lookup through DescribeStackResources.
func describedStackResource(t *testing.T, srv *helpers.TestServer, stackName, logicalID string) (string, string) {
	t.Helper()
	row := stackResourceRowFor(t, srv, "DescribeStackResources", "StackResources", stackName, logicalID)
	return row.ResourceStatus, row.ResourceStatusReason
}

func listedStackResourcePhysicalID(t *testing.T, srv *helpers.TestServer, stackName, logicalID string) string {
	t.Helper()
	row := stackResourceRowFor(t, srv, "ListStackResources", "StackResourceSummaries", stackName, logicalID)
	return row.PhysicalResourceID
}

func stackResourceRowFor(t *testing.T, srv *helpers.TestServer, action, collection, stackName, logicalID string) stackResourceRow {
	t.Helper()
	resp := cfnQuery(t, srv, action, url.Values{"StackName": []string{stackName}})
	body := readBody(t, resp)
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// The two actions wrap their rows in differently named collections, so the
	// walk is driven by the collection name rather than by a per-action struct.
	var rows []stackResourceRow
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	var inCollection bool
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch {
			case el.Name.Local == collection:
				inCollection = true
			case inCollection && el.Name.Local == "member":
				var row stackResourceRow
				if err := dec.DecodeElement(&row, &el); err != nil {
					t.Fatalf("%s: decode member: %v\n%s", action, err, body)
				}
				rows = append(rows, row)
			}
		case xml.EndElement:
			if el.Name.Local == collection {
				inCollection = false
			}
		}
	}
	for _, row := range rows {
		if row.LogicalResourceID == logicalID {
			return row
		}
	}
	t.Fatalf("%s: no resource %q in stack %q:\n%s", action, logicalID, stackName, body)
	return stackResourceRow{}
}
