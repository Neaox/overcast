package cloudformation_test

// generate_secret_string_test.go — AWS::SecretsManager::Secret's
// GenerateSecretString property.
//
// The CloudFormation handler forwarded only SecretString to CreateSecret and
// dropped GenerateSecretString on the floor. A secret declared the CDK way —
// `new Secret(this, 'DatabaseSecret', { generateSecretString: { ... } })`, which
// synthesises to GenerateSecretString and no SecretString — was therefore
// created with a version labelled AWSCURRENT holding nothing at all. The secret
// existed, ListSecrets and DescribeSecret showed it, and GetSecretValue answered
// ResourceNotFoundException naming that version's ID, because a staged version
// with no value is exactly what an in-flight rotation looks like.
//
// Downstream that surfaced as an empty string rather than an error: an ECS task
// resolving `secrets: [{ valueFrom: <arn>:password:: }]` got nothing and the
// container failed its own validation ("WORDPRESS_PASSWORD must be set").

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const generateSecretStringTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "DatabaseSecret": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "gen/database/credentials",
		"KmsKeyId": "alias/cfn-secret",
		"Tags": [
		  { "Key": "environment", "Value": "test" },
		  { "Key": "owner", "Value": "cloudformation" }
		],
        "GenerateSecretString": {
          "SecretStringTemplate": "{\"username\":\"admin\"}",
          "GenerateStringKey": "password",
          "PasswordLength": 24,
          "ExcludeCharacters": "\"@/\\"
        }
      }
    },
    "ApiKeySecret": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "gen/api/key",
        "GenerateSecretString": { "PasswordLength": 12, "ExcludePunctuation": true }
      }
    }
  }
}`

func secretsJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, "secretsmanager.", action, "application/x-amz-json-1.1", body)
}

// getSecretString reads a secret's AWSCURRENT value the way an application
// does: GetSecretValue with no VersionId and no VersionStage.
func getSecretString(t *testing.T, srv *helpers.TestServer, secretID string) string {
	t.Helper()
	resp := secretsJSONCall(t, srv, "GetSecretValue", map[string]any{"SecretId": secretID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		SecretString string `json:"SecretString"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.SecretString
}

func TestCreateStack_generateSecretStringPopulatesAwsCurrent(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           []string{"generate-secret-stack"},
		"TemplateBody":        []string{generateSecretStringTemplate},
		"Tags.member.1.Key":   []string{"application"},
		"Tags.member.1.Value": []string{"overcast"},
		"Tags.member.2.Key":   []string{"owner"},
		"Tags.member.2.Value": []string{"stack-default"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "generate-secret-stack", "CREATE_COMPLETE")

	// The templated secret: the template's own fields survive, and the
	// generated password lands under GenerateStringKey.
	var creds map[string]any
	raw := getSecretString(t, srv, "gen/database/credentials")
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		t.Fatalf("SecretString is not JSON: %v\nvalue: %s", err, raw)
	}
	if got, _ := creds["username"].(string); got != "admin" {
		t.Errorf("username = %q, want %q (SecretStringTemplate fields must survive)", got, "admin")
	}
	password, _ := creds["password"].(string)
	if len(password) != 24 {
		t.Errorf("password length = %d, want 24 (PasswordLength)", len(password))
	}
	if i := strings.IndexAny(password, `"@/\`); i >= 0 {
		t.Errorf("password %q contains excluded character %q", password, password[i])
	}

	// Without a template the whole secret value is the generated password.
	apiKey := getSecretString(t, srv, "gen/api/key")
	if len(apiKey) != 12 {
		t.Errorf("api key length = %d, want 12", len(apiKey))
	}
	if i := strings.IndexAny(apiKey, `!"#$%&'()*+,-./:;<=>?@[\]^_`+"`"+`{|}~`); i >= 0 {
		t.Errorf("api key %q contains punctuation despite ExcludePunctuation", apiKey)
	}
	// Two secrets generated from the same template do not share a value.
	if password == apiKey {
		t.Error("both secrets got the same generated value")
	}

	// CloudFormation forwards metadata properties through the service API.
	metadata := describeSecretMetadata(t, srv, "gen/database/credentials")
	if metadata.KMSKeyID != "alias/cfn-secret" {
		t.Errorf("KmsKeyId = %q, want alias/cfn-secret", metadata.KMSKeyID)
	}
	wantTags := map[string]string{"application": "overcast", "environment": "test", "owner": "cloudformation"}
	for _, tag := range metadata.Tags {
		if want, exists := wantTags[tag.Key]; exists && want == tag.Value {
			delete(wantTags, tag.Key)
		}
	}
	if len(wantTags) != 0 {
		t.Errorf("missing CloudFormation tags: %v (got %+v)", wantTags, metadata.Tags)
	}

	// Updating metadata uses UpdateSecret/TagResource and does not replace or
	// regenerate the stored value when GenerateSecretString is unchanged.
	updatedTemplate := strings.Replace(generateSecretStringTemplate, "alias/cfn-secret", "alias/cfn-secret-updated", 1)
	updatedTemplate = strings.Replace(updatedTemplate, `"owner", "Value": "cloudformation"`, `"owner", "Value": "platform"`, 1)
	resp = cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"generate-secret-stack"},
		"TemplateBody": []string{updatedTemplate},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	waitForStackStatus(t, srv, "generate-secret-stack", "UPDATE_COMPLETE")
	if updatedValue := getSecretString(t, srv, "gen/database/credentials"); updatedValue != raw {
		t.Errorf("metadata-only update changed SecretString: got %q, want %q", updatedValue, raw)
	}
	updatedMetadata := describeSecretMetadata(t, srv, "gen/database/credentials")
	if updatedMetadata.KMSKeyID != "alias/cfn-secret-updated" {
		t.Errorf("updated KmsKeyId = %q, want alias/cfn-secret-updated", updatedMetadata.KMSKeyID)
	}
	owner := ""
	for _, tag := range updatedMetadata.Tags {
		if tag.Key == "owner" {
			owner = tag.Value
		}
	}
	if owner != "platform" {
		t.Errorf("updated owner tag = %q, want platform", owner)
	}
}

type secretMetadata struct {
	KMSKeyID string `json:"KmsKeyId"`
	Tags     []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

func describeSecretMetadata(t *testing.T, srv *helpers.TestServer, secretID string) secretMetadata {
	t.Helper()
	resp := secretsJSONCall(t, srv, "DescribeSecret", map[string]any{"SecretId": secretID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var metadata secretMetadata
	helpers.DecodeJSON(t, resp, &metadata)
	return metadata
}

func TestCreateStack_generateSecretStringRejectsHalfATemplate(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// GenerateStringKey without SecretStringTemplate: AWS rejects the pair as
	// incomplete rather than guessing what the JSON should look like.
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Broken": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "gen/broken",
        "GenerateSecretString": { "GenerateStringKey": "password" }
      }
    }
  }
}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"generate-secret-broken-stack"},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "generate-secret-broken-stack", "ROLLBACK_COMPLETE")

	// Validation happens before CreateSecret, so rollback has no partially
	// created secret to clean up.
	describe := secretsJSONCall(t, srv, "DescribeSecret", map[string]any{"SecretId": "gen/broken"})
	defer describe.Body.Close()
	helpers.AssertStatus(t, describe, http.StatusBadRequest)
	helpers.AssertJSONError(t, describe, "ResourceNotFoundException")
}
