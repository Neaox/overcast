package cloudformation_test

// ecr_repository_uri_test.go — Fn::GetAtt Repo.RepositoryUri.
//
// Real AWS returns {account}.dkr.ecr.{region}.amazonaws.com/{name}, the
// registry a `docker push` targets. Overcast serves its own registry on a
// separate port and ECR mints repositoryUri on that address (see
// ecr.Service.registryEndpoint), but the CloudFormation resource handler
// rebuilt the amazonaws.com string instead of reading the response it had just
// received. A stack therefore published a registry nothing in this environment
// can push to, while `aws ecr describe-repositories` on the same repository
// returned the working one.
//
// Neither "dkr" nor "ecr" is a registered host-route label, so the stack-output
// re-hosting in output_rehost_test.go cannot correct this after the fact — it
// deliberately leaves unregistered hostnames alone. The value has to be right
// where it is minted.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const ecrRepositoryTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Repo": {
      "Type": "AWS::ECR::Repository",
      "Properties": { "RepositoryName": "cfn-uri-repo" }
    }
  },
  "Outputs": {
    "RepositoryUri": { "Value": { "Fn::GetAtt": ["Repo", "RepositoryUri"] } }
  }
}`

func TestGetAtt_ecrRepositoryUriIsTheRegistryOvercastServes(t *testing.T) {
	// Given: a stack with an ECR repository, on a configured hostname
	const hostname = "localhost.overcast.sh"
	srv := helpers.NewTestServer(t, helpers.WithHostname(hostname))
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ecr-uri-stack"},
		"TemplateBody": []string{ecrRepositoryTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "ecr-uri-stack", "CREATE_COMPLETE")

	// When: the stack is described
	desc := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"ecr-uri-stack"},
	})
	defer desc.Body.Close()
	body := string(readBody(t, desc))

	// Then: RepositoryUri names a registry in this environment, not real AWS
	if strings.Contains(body, "dkr.ecr.us-east-1.amazonaws.com") {
		t.Errorf("RepositoryUri points at real AWS; body:\n%s", body)
	}
	if !strings.Contains(body, "/cfn-uri-repo") {
		t.Errorf("RepositoryUri does not name the repository; body:\n%s", body)
	}
	if !strings.Contains(body, hostname) {
		t.Errorf("RepositoryUri is not on the configured hostname %q; body:\n%s", hostname, body)
	}
}
