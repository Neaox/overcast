package cloudformation_test

// ecr_properties_test.go — Issue #530: ImageTagMutability, and the properties
// listed alongside it, dropped between CDK and the emulator.
//
// ecrRepositoryHandler.Create sent only repositoryName, then applied
// RepositoryPolicyText and LifecyclePolicy as follow-ups. ImageTagMutability
// was dropped even though CreateRepository already accepted and persisted it
// on both wire paths and DescribeRepositories already echoed it — a CDK
// repository with `imageTagMutability: TagMutability.IMMUTABLE` provisioned
// as mutable. ImageScanningConfiguration, EncryptionConfiguration, and Tags
// were dropped the same way.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func ecrJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, "AmazonEC2ContainerRegistry_V20150921.", action, "application/x-amz-json-1.1", body)
}

type ecrDescribedRepository struct {
	RepositoryName             string `json:"repositoryName"`
	RepositoryArn              string `json:"repositoryArn"`
	ImageTagMutability         string `json:"imageTagMutability"`
	ImageScanningConfiguration struct {
		ScanOnPush bool `json:"scanOnPush"`
	} `json:"imageScanningConfiguration"`
	EncryptionConfiguration struct {
		EncryptionType string `json:"encryptionType"`
		KmsKey         string `json:"kmsKey"`
	} `json:"encryptionConfiguration"`
}

func ecrDescribeRepository(t *testing.T, srv *helpers.TestServer, name string) ecrDescribedRepository {
	t.Helper()
	resp := ecrJSONCall(t, srv, "DescribeRepositories", map[string]any{"repositoryNames": []string{name}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Repositories []ecrDescribedRepository `json:"repositories"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Repositories) != 1 {
		t.Fatalf("DescribeRepositories(%q) returned %d repositories, want 1", name, len(result.Repositories))
	}
	return result.Repositories[0]
}

func ecrListTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := ecrJSONCall(t, srv, "ListTagsForResource", map[string]any{"resourceArn": arn})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"tags"`
	}
	helpers.DecodeJSON(t, resp, &result)
	out := make(map[string]string, len(result.Tags))
	for _, tag := range result.Tags {
		out[tag.Key] = tag.Value
	}
	return out
}

const ecrPropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Repo": {
      "Type": "AWS::ECR::Repository",
      "Properties": {
        "RepositoryName": "cfn-props-repo",
        "ImageTagMutability": "IMMUTABLE",
        "ImageScanningConfiguration": {"ScanOnPush": true},
        "EncryptionConfiguration": {"EncryptionType": "KMS", "KmsKey": "alias/ecr-key"},
        "Tags": [
          {"Key": "team", "Value": "platform"},
          {"Key": "tier", "Value": "registry"}
        ]
      }
    }
  }
}`

// A CDK-shaped repository carrying ImageTagMutability, ImageScanningConfiguration,
// EncryptionConfiguration, and Tags must come out the other side with all four —
// not just the RepositoryPolicyText/LifecyclePolicy follow-ups that already worked.
func TestCreateStack_ECRRepositoryPropertiesRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"ecr-props-stack"},
		"TemplateBody": {ecrPropertiesTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "ecr-props-stack", "CREATE_COMPLETE")

	repo := ecrDescribeRepository(t, srv, "cfn-props-repo")
	if repo.ImageTagMutability != "IMMUTABLE" {
		t.Errorf("imageTagMutability = %q, want IMMUTABLE", repo.ImageTagMutability)
	}
	if !repo.ImageScanningConfiguration.ScanOnPush {
		t.Errorf("imageScanningConfiguration.scanOnPush = false, want true")
	}
	if repo.EncryptionConfiguration.EncryptionType != "KMS" {
		t.Errorf("encryptionConfiguration.encryptionType = %q, want KMS", repo.EncryptionConfiguration.EncryptionType)
	}
	if repo.EncryptionConfiguration.KmsKey != "alias/ecr-key" {
		t.Errorf("encryptionConfiguration.kmsKey = %q, want alias/ecr-key", repo.EncryptionConfiguration.KmsKey)
	}

	tags := ecrListTags(t, srv, repo.RepositoryArn)
	want := map[string]string{"team": "platform", "tier": "registry"}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tags[%q] = %q, want %q (full: %#v)", k, tags[k], v, tags)
		}
	}
}

// The Definition of Done in #530 calls for ImageTagMutability to be reconciled
// on Update, not just forwarded on Create.
func TestUpdateStack_ECRRepositoryImageTagMutabilityReconciled(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := func(mutability string) string {
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Repo": {
      "Type": "AWS::ECR::Repository",
      "Properties": {
        "RepositoryName": "cfn-mutability-repo",
        "ImageTagMutability": "` + mutability + `"
      }
    }
  }
}`
	}

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"ecr-mutability-stack"},
		"TemplateBody": {template("MUTABLE")},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "ecr-mutability-stack", "CREATE_COMPLETE")

	before := ecrDescribeRepository(t, srv, "cfn-mutability-repo")
	if before.ImageTagMutability != "MUTABLE" {
		t.Fatalf("imageTagMutability after create = %q, want MUTABLE", before.ImageTagMutability)
	}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"ecr-mutability-stack"},
		"TemplateBody": {template("IMMUTABLE")},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, "ecr-mutability-stack", "UPDATE_COMPLETE")

	after := ecrDescribeRepository(t, srv, "cfn-mutability-repo")
	if after.ImageTagMutability != "IMMUTABLE" {
		t.Errorf("imageTagMutability after update = %q, want IMMUTABLE", after.ImageTagMutability)
	}
}

// Tags must be reconciled — added, changed, and removed — on Update, the same
// as the Lambda/LogGroup/SecretsManager/SQS tag-reconciliation pattern.
func TestUpdateStack_ECRRepositoryTagsReconciled(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := func(tags string) string {
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Repo": {
      "Type": "AWS::ECR::Repository",
      "Properties": {
        "RepositoryName": "cfn-tags-reconcile-repo",
        "Tags": ` + tags + `
      }
    }
  }
}`
	}

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"ecr-tags-reconcile-stack"},
		"TemplateBody": {template(`[{"Key": "keep", "Value": "same"}, {"Key": "drop", "Value": "gone-after-update"}]`)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "ecr-tags-reconcile-stack", "CREATE_COMPLETE")

	repo := ecrDescribeRepository(t, srv, "cfn-tags-reconcile-repo")
	before := ecrListTags(t, srv, repo.RepositoryArn)
	if before["keep"] != "same" || before["drop"] != "gone-after-update" {
		t.Fatalf("tags after create = %#v, want keep=same and drop=gone-after-update", before)
	}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"ecr-tags-reconcile-stack"},
		"TemplateBody": {template(`[{"Key": "keep", "Value": "same"}, {"Key": "add", "Value": "new-after-update"}]`)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, "ecr-tags-reconcile-stack", "UPDATE_COMPLETE")

	after := ecrListTags(t, srv, repo.RepositoryArn)
	if after["keep"] != "same" {
		t.Errorf("tags[keep] = %q, want same (unchanged key must survive)", after["keep"])
	}
	if after["add"] != "new-after-update" {
		t.Errorf("tags[add] = %q, want new-after-update", after["add"])
	}
	if _, stillPresent := after["drop"]; stillPresent {
		t.Errorf("tags still contain %q after it was removed from the template: %#v", "drop", after)
	}
}

// Real ECR fixes EncryptionConfiguration at CreateRepository — there is no
// Put* to change it later — so a template that changes it must replace the
// repository, not silently keep the old encryption. The repository is left
// unnamed so CloudFormation can replace it.
func TestUpdateStack_ECRRepositoryEncryptionConfigurationRequiresReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := func(encryption string) string {
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Repo": {
      "Type": "AWS::ECR::Repository",
      "Properties": {
        "EncryptionConfiguration": ` + encryption + `
      }
    }
  },
  "Outputs": {
    "RepoName": {"Value": {"Fn::GetAtt": ["Repo", "RepositoryName"]}}
  }
}`
	}

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"ecr-encryption-replace-stack"},
		"TemplateBody": {template(`{"EncryptionType": "AES256"}`)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "ecr-encryption-replace-stack", "CREATE_COMPLETE")

	firstName := extractBetween(string(readBody(t, cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {"ecr-encryption-replace-stack"}}))), "<OutputValue>", "</OutputValue>")
	if firstName == "" {
		t.Fatalf("could not read RepoName output after create")
	}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"ecr-encryption-replace-stack"},
		"TemplateBody": {template(`{"EncryptionType": "KMS", "KmsKey": "alias/ecr-key"}`)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatusIn(t, srv, "ecr-encryption-replace-stack", "UPDATE_COMPLETE")

	secondName := extractBetween(string(readBody(t, cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {"ecr-encryption-replace-stack"}}))), "<OutputValue>", "</OutputValue>")
	if secondName == "" {
		t.Fatalf("could not read RepoName output after update")
	}
	if secondName == firstName {
		t.Fatalf("repository name unchanged (%q) after an EncryptionConfiguration update — replacement did not happen", firstName)
	}

	repo := ecrDescribeRepository(t, srv, secondName)
	if repo.EncryptionConfiguration.EncryptionType != "KMS" {
		t.Errorf("encryptionConfiguration.encryptionType = %q, want KMS", repo.EncryptionConfiguration.EncryptionType)
	}
	if repo.EncryptionConfiguration.KmsKey != "alias/ecr-key" {
		t.Errorf("encryptionConfiguration.kmsKey = %q, want alias/ecr-key", repo.EncryptionConfiguration.KmsKey)
	}
}
