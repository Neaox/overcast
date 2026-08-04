package cloudformation_test

// rds_update_test.go — updating an AWS::RDS::DBInstance's master credentials.
//
// The CloudFormation handler forced replacement for DBInstanceIdentifier and
// Engine but silently ignored MasterUsername: it was not compared for
// replacement and was not passed to ModifyDBInstance either, so the handler
// returned success having changed nothing. A stack that fixed a wrong master
// username deployed clean and left the instance exactly as it was — which is
// how an instance kept an unresolved "{{resolve:secretsmanager:...}}" username
// across redeploys even after dynamic references started resolving.
//
// On AWS a MasterUsername change carries "Update requires: Replacement".

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// rdsInstanceTemplate builds a one-instance template. An empty id leaves the
// instance unnamed, which is what makes CloudFormation generate a physical name
// and so what makes replacement possible at all.
func rdsInstanceTemplate(id, masterUsername string) string {
	idProp := ""
	if id != "" {
		idProp = fmt.Sprintf(`"DBInstanceIdentifier": %q,`, id)
	}
	return fmt.Sprintf(`{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Database": {
      "Type": "AWS::RDS::DBInstance",
      "Properties": {
        %s
        "Engine": "mysql",
        "DBInstanceClass": "db.t3.micro",
        "AllocatedStorage": "20",
        "MasterUsername": %q,
        "MasterUserPassword": "correct-horse-battery"
      }
    }
  }
}`, idProp, masterUsername)
}

// rdsMasterUsernames returns every DB instance's identifier and master username.
func rdsMasterUsernames(t *testing.T, srv *helpers.TestServer) map[string]string {
	t.Helper()
	params := url.Values{
		"Action":  []string{"DescribeDBInstances"},
		"Version": []string{"2014-10-31"},
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("build DescribeDBInstances request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	body := readBody(t, resp)
	var result struct {
		Instances []struct {
			ID             string `xml:"DBInstanceIdentifier"`
			MasterUsername string `xml:"MasterUsername"`
		} `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeDBInstancesResponse: %v\nbody: %s", err, body)
	}
	out := make(map[string]string, len(result.Instances))
	for _, i := range result.Instances {
		out[i.ID] = i.MasterUsername
	}
	return out
}

// Changing MasterUsername must actually change the master username. The
// instance is unnamed so CloudFormation can replace it, which is what AWS does
// for this property.
func TestUpdateStack_rdsMasterUsernameChangeIsApplied(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "rds-master-user-stack"

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsInstanceTemplate("", "olduser")},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	before := rdsMasterUsernames(t, srv)
	if len(before) != 1 {
		t.Fatalf("expected exactly one DB instance after create, got %v", before)
	}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsInstanceTemplate("", "newuser")},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	// Exact-match: "UPDATE_COMPLETE" is a substring of
	// "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS", and the instance the replacement
	// superseded is deleted during that cleanup phase.
	waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE")

	// The instance the replacement superseded is deleted asynchronously, so
	// settle before judging: what matters is that the stack ends up owning one
	// instance and that it carries the new username.
	var final map[string]string
	helpers.Eventually(t, 10*time.Second, 50*time.Millisecond, func() bool {
		final = rdsMasterUsernames(t, srv)
		if len(final) != 1 {
			return false
		}
		for _, user := range final {
			if user != "newuser" {
				return false
			}
		}
		return true
	}, fmt.Sprintf("stack never settled on a single instance with the new master username; last saw %v", final))
}

// With the identifier pinned in the template, replacement cannot succeed — the
// new instance collides with the one still standing, exactly as on AWS. The
// stack must roll back rather than report success over an unapplied change.
func TestUpdateStack_rdsMasterUsernameChangeOnPinnedNameRollsBack(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "rds-pinned-master-user-stack"
	const instanceID = "pinned-db"

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsInstanceTemplate(instanceID, "olduser")},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsInstanceTemplate(instanceID, "newuser")},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)

	got := waitForStackStatusIn(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_COMPLETE")
	if got == "UPDATE_COMPLETE" {
		t.Fatalf("stack reported %s for a replacement that cannot succeed on a pinned identifier", got)
	}

	// The original instance must survive the failed update untouched.
	after := rdsMasterUsernames(t, srv)
	if user, ok := after[instanceID]; !ok {
		t.Errorf("original instance %s is gone after a rolled-back update; instances: %v", instanceID, after)
	} else if user != "olduser" {
		t.Errorf("original instance master username = %q, want it left at %q", user, "olduser")
	}
}
