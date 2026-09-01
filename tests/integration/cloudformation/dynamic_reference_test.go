package cloudformation_test

// dynamic_reference_test.go — {{resolve:...}} dynamic references.
//
// A dynamic reference is plain text inside a property value, not an intrinsic
// function, so the intrinsic resolver walked past it and handed the literal
// reference to the service. The visible symptom was an RDS instance whose
// master username was
// "{{resolve:secretsmanager:arn:aws:secretsmanager:…:SecretString:username::}}"
// — displayed faithfully by the web UI, embedded in every connection string it
// generated, and rejected by the database engine, which caps usernames at 32
// characters.

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const dynamicRefTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "DBSecret": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "dynref-db-secret",
        "SecretString": "{\"username\":\"appuser\",\"password\":\"correct-horse-battery\"}"
      }
    },
    "DBNameParam": {
      "Type": "AWS::SSM::Parameter",
      "Properties": { "Name": "/dynref/db-name", "Type": "String", "Value": "newowners" }
    },
    "Database": {
      "Type": "AWS::RDS::DBInstance",
      "DependsOn": ["DBSecret", "DBNameParam"],
      "Properties": {
        "DBInstanceIdentifier": "dynref-db",
        "Engine": "mysql",
        "DBInstanceClass": "db.t3.micro",
        "AllocatedStorage": "20",
        "MasterUsername": "{{resolve:secretsmanager:dynref-db-secret:SecretString:username}}",
        "MasterUserPassword": "{{resolve:secretsmanager:dynref-db-secret:SecretString:password}}",
        "DBName": "{{resolve:ssm:/dynref/db-name}}"
      }
    }
  },
  "Outputs": {
    "ResolvedUser": { "Value": "{{resolve:secretsmanager:dynref-db-secret:SecretString:username}}" },
    "ResolvedDBName": { "Value": "{{resolve:ssm:/dynref/db-name}}" }
  }
}`

const dynamicRefParameterTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "SourceSecret": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "dynref-parameter-secret",
        "SecretString": "before"
      }
    },
    "TargetParameter": {
      "Type": "AWS::SSM::Parameter",
      "DependsOn": "SourceSecret",
      "Properties": {
        "Name": "/dynref/target",
        "Type": "String",
        "Value": "{{resolve:secretsmanager:dynref-parameter-secret}}",
        "Description": "before-update"
      }
    }
  }
}`

// rdsDescribeInstance returns one DB instance's master username and DB name as
// the RDS API reports them — the same values the web UI reads.
func rdsDescribeInstance(t *testing.T, srv *helpers.TestServer, id string) (masterUsername, dbName string) {
	t.Helper()
	params := url.Values{
		"Action":               []string{"DescribeDBInstances"},
		"Version":              []string{"2014-10-31"},
		"DBInstanceIdentifier": []string{id},
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
			MasterUsername string `xml:"MasterUsername"`
			DBName         string `xml:"DBName"`
		} `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeDBInstancesResponse: %v\nbody: %s", err, body)
	}
	if len(result.Instances) != 1 {
		t.Fatalf("DescribeDBInstances returned %d instances, want 1\nbody: %s", len(result.Instances), body)
	}
	return result.Instances[0].MasterUsername, result.Instances[0].DBName
}

func TestCreateStack_resolvesDynamicReferencesIntoResourceProperties(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"dynref-stack"},
		"TemplateBody": []string{dynamicRefTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "dynref-stack", "CREATE_COMPLETE")

	// The property the bug was reported against: a JSON key selected out of a
	// Secrets Manager secret.
	masterUsername, dbName := rdsDescribeInstance(t, srv, "dynref-db")
	if masterUsername != "appuser" {
		t.Errorf("MasterUsername = %q, want %q", masterUsername, "appuser")
	}
	if strings.Contains(masterUsername, "{{resolve:") {
		t.Errorf("MasterUsername still holds the literal reference text: %q", masterUsername)
	}
	// And an SSM parameter reference, the other half of the syntax.
	if dbName != "newowners" {
		t.Errorf("DBName = %q, want %q", dbName, "newowners")
	}

	// Outputs are the one place a reference is deliberately left alone. Real
	// CloudFormation returns the literal reference string from an output rather
	// than the secret behind it, and DescribeStacks redacts nothing — so
	// resolving here would publish the secret to everyone who can describe the
	// stack.
	const userRef = "{{resolve:secretsmanager:dynref-db-secret:SecretString:username}}"
	outputs := describeStackOutputs(t, srv, "dynref-stack")
	if got := outputs["ResolvedUser"]; got != userRef {
		t.Errorf("ResolvedUser output = %q, want the reference left literal (%q)", got, userRef)
	}
	if outputs["ResolvedUser"] == "appuser" {
		t.Error("a stack output resolved a Secrets Manager reference — DescribeStacks would publish the secret")
	}
}

// Rotating a secret must not, on its own, make CloudFormation think a resource
// changed. AWS compares the literal reference string: "Updating only the secret
// value in Secrets Manager doesn't automatically cause CloudFormation to
// retrieve the new value." Hashing the resolved value instead would make a
// rotation look like a property change — and since a MasterUsername change
// forces replacement, that would rebuild the database behind the user's back.
func TestUpdateStack_rotatingASecretDoesNotReplaceTheResource(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "dynref-rotation-stack"

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{dynamicRefTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	before, _ := rdsDescribeInstance(t, srv, "dynref-db")
	if before != "appuser" {
		t.Fatalf("MasterUsername = %q, want %q before rotation", before, "appuser")
	}

	// Rotate the secret behind the reference, leaving the template untouched.
	putSecret(t, srv, "dynref-db-secret", `{"username":"rotateduser","password":"a-new-password"}`)

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{dynamicRefTemplate},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED")

	// The instance is the one that was there before, untouched.
	after, _ := rdsDescribeInstance(t, srv, "dynref-db")
	if after != "appuser" {
		t.Errorf("MasterUsername = %q after rotating the secret; want it left at %q — "+
			"a rotation with an unchanged template must not re-provision the resource", after, "appuser")
	}
}

// CloudFormation retrieves a dynamic reference only when it creates or updates
// the resource containing that reference. An unchanged resource must therefore
// remain untouched even when its previously resolved secret no longer exists.
func TestUpdateStack_unchangedResourceDoesNotRetrieveDynamicReference(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "dynref-unchanged-stack"

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{dynamicRefTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	before, _ := rdsDescribeInstance(t, srv, "dynref-db")
	if before != "appuser" {
		t.Fatalf("MasterUsername = %q, want %q before deleting the secret", before, "appuser")
	}

	deleteSecret(t, srv, "dynref-db-secret")

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{dynamicRefTemplate},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	after, _ := rdsDescribeInstance(t, srv, "dynref-db")
	if after != "appuser" {
		t.Errorf("MasterUsername = %q after a no-op update; want the previously resolved value %q", after, "appuser")
	}
}

func TestUpdateStack_changedResourceRetrievesCurrentDynamicReference(t *testing.T) {
	// Given: a parameter whose value was resolved from AWSCURRENT when its
	// containing resource was created.
	srv := helpers.NewTestServer(t)
	const stackName = "dynref-changed-stack"
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{dynamicRefParameterTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	if got := getParameterValue(t, srv, "/dynref/target"); got != "before" {
		t.Fatalf("initial parameter value = %q, want before", got)
	}

	putSecret(t, srv, "dynref-parameter-secret", "after")
	updatedTemplate := strings.Replace(dynamicRefParameterTemplate, "before-update", "after-update", 1)

	// When: a literal property change selects the containing resource for an
	// update without changing the dynamic reference itself.
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{updatedTemplate},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: CloudFormation retrieves AWSCURRENT at resource-update time.
	if got := getParameterValue(t, srv, "/dynref/target"); got != "after" {
		t.Errorf("updated parameter value = %q, want after", got)
	}
}

// putSecret overwrites a secret's value through the Secrets Manager API.
func putSecret(t *testing.T, srv *helpers.TestServer, secretID, value string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"SecretId": secretID, "SecretString": value})
	if err != nil {
		t.Fatalf("marshal PutSecretValue body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build PutSecretValue request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.PutSecretValue")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func deleteSecret(t *testing.T, srv *helpers.TestServer, secretID string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"SecretId":                   secretID,
		"ForceDeleteWithoutRecovery": true,
	})
	if err != nil {
		t.Fatalf("marshal DeleteSecret body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build DeleteSecret request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.DeleteSecret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func getParameterValue(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"Name": name})
	if err != nil {
		t.Fatalf("marshal GetParameter body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build GetParameter request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonSSM.GetParameter")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Parameter struct {
			Value string `json:"Value"`
		} `json:"Parameter"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.Parameter.Value
}

const unresolvableRefTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Database": {
      "Type": "AWS::RDS::DBInstance",
      "Properties": {
        "DBInstanceIdentifier": "dynref-missing-db",
        "Engine": "mysql",
        "DBInstanceClass": "db.t3.micro",
        "AllocatedStorage": "20",
        "MasterUsername": "{{resolve:secretsmanager:no-such-secret:SecretString:username}}",
        "MasterUserPassword": "correct-horse-battery"
      }
    }
  }
}`

// A reference that cannot be resolved must fail the resource. Creating it
// anyway is what produced the original bug: an instance that exists, reports a
// username no engine will accept, and fails only later and somewhere else.
func TestCreateStack_unresolvableDynamicReferenceFailsTheResource(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"dynref-missing-stack"},
		"TemplateBody": []string{unresolvableRefTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	waitForStackStatus(t, srv, "dynref-missing-stack", "ROLLBACK_COMPLETE")

	// The failure reason has to name the reference — "resource Database failed"
	// alone sends the reader looking at RDS instead of at the missing secret.
	events := describeStackEventReasons(t, srv, "dynref-missing-stack")
	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, "{{resolve:secretsmanager:no-such-secret") {
		t.Errorf("no stack event names the unresolved reference:\n%s", joined)
	}
}

// describeStackEventReasons returns every non-empty ResourceStatusReason for a
// stack, newest first.
func describeStackEventReasons(t *testing.T, srv *helpers.TestServer, stackName string) []string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	body := readBody(t, resp)
	var result struct {
		Events []struct {
			Reason string `xml:"ResourceStatusReason"`
		} `xml:"DescribeStackEventsResult>StackEvents>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStackEventsResponse: %v\nbody: %s", err, body)
	}
	reasons := make([]string, 0, len(result.Events))
	for _, e := range result.Events {
		if e.Reason != "" {
			reasons = append(reasons, e.Reason)
		}
	}
	return reasons
}
