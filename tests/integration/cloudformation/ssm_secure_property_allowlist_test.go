package cloudformation_test

// ssm_secure_property_allowlist_test.go — issue #605.
//
// AWS accepts an {{resolve:ssm-secure:...}} reference only in the resource
// properties it enumerates; every other property is rejected. Overcast
// resolved one anywhere, so a template that deploys here failed on AWS — the
// expensive direction for an emulator.
//
// The other two reference kinds carry no such restriction: "The
// secretsmanager dynamic reference can be used in all resource properties",
// and plain ssm is a plaintext parameter with nothing to protect.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// putSecureParameter stores a SecureString parameter through SSM's own API.
// CloudFormation cannot create one — AWS::SSM::Parameter has no SecureString
// type — so a template that reads one always reads a parameter put here.
func putSecureParameter(t *testing.T, srv *helpers.TestServer, name, value string) {
	t.Helper()
	resp := ssmJSONCall(t, srv, "PutParameter", map[string]any{
		"Name":      name,
		"Type":      "SecureString",
		"Value":     value,
		"Overwrite": true,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// AWS::SQS::Queue has no property on the secure-string list, so QueueName is
// a property AWS refuses outright.
const ssmSecureDisallowedTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {
        "QueueName": "{{resolve:ssm-secure:/dynref/secure-queue-name}}"
      }
    }
  }
}`

// AWS::RDS::DBInstance MasterUserPassword is on the list, at the top level of
// the resource's properties.
const ssmSecureAllowedTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Database": {
      "Type": "AWS::RDS::DBInstance",
      "Properties": {
        "DBInstanceIdentifier": "ssmsecure-db",
        "Engine": "mysql",
        "DBInstanceClass": "db.t3.micro",
        "AllocatedStorage": "20",
        "MasterUsername": "appuser",
        "MasterUserPassword": "{{resolve:ssm-secure:/dynref/secure-db-password}}"
      }
    }
  }
}`

// AWS::KinesisFirehose::DeliveryStream is on the list through a nested
// property type: RedshiftDestinationConfiguration's Password, not a property
// of the resource itself.
const ssmSecureNestedAllowedTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Stream": {
      "Type": "AWS::KinesisFirehose::DeliveryStream",
      "Properties": {
        "DeliveryStreamName": "ssmsecure-stream",
        "DeliveryStreamType": "DirectPut",
        "RedshiftDestinationConfiguration": {
          "ClusterJDBCURL": "jdbc:redshift://example:5439/dev",
          "Username": "appuser",
          "Password": "{{resolve:ssm-secure:/dynref/secure-db-password}}"
        }
      }
    }
  }
}`

// The same QueueName the ssm-secure reference is refused in, read through the
// two unrestricted schemes instead.
const unrestrictedRefsInDisallowedPropertyTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "NameSecret": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "dynref-queue-name-secret",
        "SecretString": "ssmsecure-secret-named-queue"
      }
    },
    "NameParam": {
      "Type": "AWS::SSM::Parameter",
      "Properties": { "Name": "/dynref/plain-queue-name", "Type": "String", "Value": "ssmsecure-ssm-named-queue" }
    },
    "SecretNamedQueue": {
      "Type": "AWS::SQS::Queue",
      "DependsOn": "NameSecret",
      "Properties": { "QueueName": "{{resolve:secretsmanager:dynref-queue-name-secret}}" }
    },
    "ParamNamedQueue": {
      "Type": "AWS::SQS::Queue",
      "DependsOn": "NameParam",
      "Properties": { "QueueName": "{{resolve:ssm:/dynref/plain-queue-name}}" }
    }
  }
}`

func TestCreateStack_ssmSecureReferenceOutsideTheAllowlistFailsTheResource(t *testing.T) {
	// Given: a SecureString parameter, and a template reading it into a
	// property AWS does not allow secure strings in.
	srv := helpers.NewTestServer(t)
	putSecureParameter(t, srv, "/dynref/secure-queue-name", "ssmsecure-queue")

	// When: the stack is created.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ssmsecure-disallowed-stack"},
		"TemplateBody": []string{ssmSecureDisallowedTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the resource fails and the stack rolls back.
	waitForStackStatus(t, srv, "ssmsecure-disallowed-stack", "ROLLBACK_COMPLETE")

	// And: the reason names the reference and the property it was written in —
	// "resource Queue failed" alone sends the reader looking at SQS.
	joined := strings.Join(describeStackEventReasons(t, srv, "ssmsecure-disallowed-stack"), "\n")
	if !strings.Contains(joined, "{{resolve:ssm-secure:/dynref/secure-queue-name}}") {
		t.Errorf("no stack event names the refused reference:\n%s", joined)
	}
	if !strings.Contains(joined, "QueueName") {
		t.Errorf("no stack event names the property the reference was written in:\n%s", joined)
	}

	// And: the parameter was never read — a refused reference must not decrypt
	// the secure string on its way to being rejected.
	if strings.Contains(joined, "ssmsecure-queue") {
		t.Errorf("a stack event carries the decrypted parameter value:\n%s", joined)
	}
}

func TestCreateStack_ssmSecureReferenceInAnAllowedPropertyResolves(t *testing.T) {
	// Given: a SecureString parameter holding a database password.
	srv := helpers.NewTestServer(t)
	putSecureParameter(t, srv, "/dynref/secure-db-password", "correct-horse-battery")

	// When: it is read into AWS::RDS::DBInstance's MasterUserPassword, which is
	// on AWS's secure-string list.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ssmsecure-allowed-stack"},
		"TemplateBody": []string{ssmSecureAllowedTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stack completes — the restriction applies to the properties
	// AWS omits, not to every property.
	waitForStackStatus(t, srv, "ssmsecure-allowed-stack", "CREATE_COMPLETE")
}

func TestCreateStack_ssmSecureReferenceInAnAllowedNestedPropertyResolves(t *testing.T) {
	// Given: the same SecureString parameter.
	srv := helpers.NewTestServer(t)
	putSecureParameter(t, srv, "/dynref/secure-db-password", "correct-horse-battery")

	// When: it is read into a property of a nested property type —
	// RedshiftDestinationConfiguration's Password, not a property of the
	// delivery stream itself.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ssmsecure-nested-stack"},
		"TemplateBody": []string{ssmSecureNestedAllowedTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stack completes — the allowlist matches the whole property
	// path, not just the property name at the top of the resource.
	waitForStackStatus(t, srv, "ssmsecure-nested-stack", "CREATE_COMPLETE")
}

func TestCreateStack_secretsManagerAndPlainSSMReferencesStayUnrestricted(t *testing.T) {
	// Given: a template naming two queues from a secret and a plain parameter —
	// QueueName being exactly the property an ssm-secure reference is refused in.
	srv := helpers.NewTestServer(t)

	// When: the stack is created.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ssmsecure-unrestricted-stack"},
		"TemplateBody": []string{unrestrictedRefsInDisallowedPropertyTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: both resolve, and the queues carry the resolved names.
	waitForStackStatus(t, srv, "ssmsecure-unrestricted-stack", "CREATE_COMPLETE")

	resources := describeStackResources(t, srv, "ssmsecure-unrestricted-stack")
	if got := resources["SecretNamedQueue"].PhysicalID; !strings.HasSuffix(got, ":ssmsecure-secret-named-queue") {
		t.Errorf("SecretNamedQueue physical ID = %q, want a queue named from the secret", got)
	}
	if got := resources["ParamNamedQueue"].PhysicalID; !strings.HasSuffix(got, ":ssmsecure-ssm-named-queue") {
		t.Errorf("ParamNamedQueue physical ID = %q, want a queue named from the parameter", got)
	}
}
