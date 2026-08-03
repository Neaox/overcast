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
		"StackName":    []string{"generate-secret-stack"},
		"TemplateBody": []string{generateSecretStringTemplate},
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
}
