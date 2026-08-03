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
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
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

	// Outputs resolve too — a stack output feeds other stacks and the console.
	outputs := describeStackOutputs(t, srv, "dynref-stack")
	if got := outputs["ResolvedUser"]; got != "appuser" {
		t.Errorf("ResolvedUser output = %q, want %q", got, "appuser")
	}
	if got := outputs["ResolvedDBName"]; got != "newowners" {
		t.Errorf("ResolvedDBName output = %q, want %q", got, "newowners")
	}
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
