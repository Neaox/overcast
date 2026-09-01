package cloudformation_test

// ecr_bootstrap_update_test.go — re-running `cdk bootstrap` after a CDK
// upgrade.
//
// The CDKToolkit stack pins its container-assets repository name
// ("cdk-{qualifier}-container-assets-{account}-{region}") and a CDK version
// bump changes other repository properties (RepositoryPolicyText, lifecycle
// rules), so the second bootstrap issues an UpdateStack that touches the
// repository. Real CloudFormation updates every AWS::ECR::Repository property
// except RepositoryName in place; a handler that answers "replace" instead
// re-creates the pinned name and dies with RepositoryAlreadyExistsException.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const ecrBootstrapTemplate = `{
  "Resources": {
    "ContainerAssetsRepository": {
      "Type": "AWS::ECR::Repository",
      "Properties": {
        "RepositoryName": "cdk-hnb659fds-container-assets-000000000000-us-east-1",
        "ImageTagMutability": "IMMUTABLE",
        "LifecyclePolicy": {
          "LifecyclePolicyText": "{\"rules\":[{\"rulePriority\":1,\"description\":\"Untagged images should not exist, but expire any older than one year\",\"selection\":{\"tagStatus\":\"untagged\",\"countType\":\"sinceImagePushed\",\"countUnit\":\"days\",\"countNumber\":365},\"action\":{\"type\":\"expire\"}}]}"
        },
        "RepositoryPolicyText": {
          "Version": "2012-10-17",
          "Statement": [{"Sid": "%s", "Effect": "Allow", "Principal": {"Service": ["lambda.amazonaws.com"]}, "Action": ["ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer"]}]
        }
      }
    }
  }
}`

func TestUpdateStack_ecrRepositoryPolicyChange_updatesInPlace(t *testing.T) {
	// Given: a bootstrapped toolkit stack with a pinned repository name
	srv := helpers.NewTestServer(t)
	stackName := "ecr-bootstrap-upd"
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(ecrBootstrapTemplate, "LambdaECRImageRetrievalPolicy")},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, 200)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	originalID := describeStackResourceIDs(t, srv, stackName)["ContainerAssetsRepository"]
	if originalID == "" {
		t.Fatalf("stack has no ContainerAssetsRepository after create")
	}

	// When: a newer CDK version re-bootstraps with a changed repository policy
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(ecrBootstrapTemplate, "LambdaECRImageRetrievalPolicy-v2")},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, 200)

	// Then: the update succeeds in place — no RepositoryAlreadyExistsException
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
	if got := describeStackResourceIDs(t, srv, stackName)["ContainerAssetsRepository"]; got != originalID {
		t.Errorf("repository was replaced: physical ID %q, want original %q", got, originalID)
	}

	// And: the new policy is what ECR now serves
	policyResp := awsJSONCall(t, srv, "AmazonEC2ContainerRegistry_V20150921.", "GetRepositoryPolicy", "application/x-amz-json-1.1", map[string]any{
		"repositoryName": "cdk-hnb659fds-container-assets-000000000000-us-east-1",
	})
	defer policyResp.Body.Close()
	helpers.AssertStatus(t, policyResp, http.StatusOK)
	var policy struct {
		PolicyText string `json:"policyText"`
	}
	if err := json.NewDecoder(policyResp.Body).Decode(&policy); err != nil {
		t.Fatalf("decode GetRepositoryPolicy: %v", err)
	}
	if !strings.Contains(policy.PolicyText, "LambdaECRImageRetrievalPolicy-v2") {
		t.Errorf("repository policy was not updated; policyText: %s", policy.PolicyText)
	}
}

func TestUpdateStack_ecrRepositoryNameChange_stillReplaces(t *testing.T) {
	// Given: a stack with a named repository
	srv := helpers.NewTestServer(t)
	stackName := "ecr-rename-upd"
	template := `{
  "Resources": {
    "Repo": {
      "Type": "AWS::ECR::Repository",
      "Properties": {"RepositoryName": "%s"}
    }
  }
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "ecr-rename-v1")},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, 200)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the repository name changes (Replacement: Yes on real AWS)
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "ecr-rename-v2")},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, 200)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the physical resource is the new repository
	if got := describeStackResourceIDs(t, srv, stackName)["Repo"]; !strings.HasSuffix(got, "/ecr-rename-v2") {
		t.Errorf("Repo physical ID = %q, want the replacement ecr-rename-v2", got)
	}
}
