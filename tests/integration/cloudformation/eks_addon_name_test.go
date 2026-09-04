package cloudformation_test

// eks_addon_name_test.go covers #1692: AWS::EKS::Addon's AddonName is
// Required: Yes, and CreateAddon only accepts a name DescribeAddonVersions
// publishes. The CloudFormation handler used to mint a placeholder when the
// template omitted AddonName and forward whatever name it was given
// unvalidated — both diverge from AWS, which rejects the first at template
// validation and the second with an InvalidParameterException.
//
// AWS::EKS::Cluster's Ref currently returns the cluster ARN rather than its
// name (a separate, concurrently-fixed bug — #1690), so these templates wire
// the add-on to its cluster with a literal ClusterName plus DependsOn, the
// same pattern generated_names_test.go uses for AWS::EKS::Nodegroup and
// AWS::EKS::FargateProfile, rather than {"Ref": "Cluster"}.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const eksAddonClusterProperties = `{"Name": "addon-name-test-cluster",
	"RoleArn": "arn:aws:iam::000000000000:role/eks",
	"ResourcesVpcConfig": {"SubnetIds": ["subnet-1"]}}`

func TestCreateStack_EKSAddonMissingNameFailsStack(t *testing.T) {
	// Given: a template whose AWS::EKS::Addon omits the required AddonName.
	srv := helpers.NewTestServer(t)
	stackName := "eks-addon-missing-name"
	template := `{"Resources":{
		"Cluster":{"Type":"AWS::EKS::Cluster","Properties":` + eksAddonClusterProperties + `},
		"Addon":{"Type":"AWS::EKS::Addon","DependsOn":"Cluster","Properties":{
			"ClusterName":"addon-name-test-cluster"}}
	}}`

	// When: the stack is created with rollback disabled, so the failed
	// resource's terminal status is observable directly.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":       []string{stackName},
		"TemplateBody":    []string{template},
		"DisableRollback": []string{"true"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: CloudFormation reports the same required-property shape it uses
	// for every other missing @required property, naming the resource and
	// the property — not a stack that completes over a synthetic add-on name.
	waitForStackStatus(t, srv, stackName, "CREATE_FAILED")

	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{stackName}})
	defer events.Body.Close()
	helpers.AssertStatus(t, events, http.StatusOK)
	body := string(readBody(t, events))
	const want = "Properties validation failed for resource Addon with message: #: required key [AddonName] not found"
	if !strings.Contains(body, want) {
		t.Fatalf("stack events do not contain %q:\n%s", want, body)
	}

	// And: no add-on was created against the cluster.
	listResp := eksCFNCall(t, srv, http.MethodGet, "/clusters/addon-name-test-cluster/addons")
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)
	listBody := string(readBody(t, listResp))
	if !strings.Contains(listBody, `"addons":[]`) && !strings.Contains(listBody, `"addons": []`) {
		t.Fatalf("expected no addons on the cluster after the failed create, got %s", listBody)
	}
}

func TestCreateStack_EKSAddonUnknownNameFailsStack(t *testing.T) {
	// Given: a template naming an add-on DescribeAddonVersions never publishes.
	srv := helpers.NewTestServer(t)
	stackName := "eks-addon-unknown-name"
	template := `{"Resources":{
		"Cluster":{"Type":"AWS::EKS::Cluster","Properties":` + eksAddonClusterProperties + `},
		"Addon":{"Type":"AWS::EKS::Addon","DependsOn":"Cluster","Properties":{
			"ClusterName":"addon-name-test-cluster",
			"AddonName":"totally-invented-addon"}}
	}}`

	// When: the stack is created with rollback disabled.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":       []string{stackName},
		"TemplateBody":    []string{template},
		"DisableRollback": []string{"true"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the resource fails, carrying EKS's own InvalidParameterException
	// rather than a stack that provisions an add-on no real AWS account
	// could ever have.
	waitForStackStatus(t, srv, stackName, "CREATE_FAILED")

	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{stackName}})
	defer events.Body.Close()
	helpers.AssertStatus(t, events, http.StatusOK)
	body := string(readBody(t, events))
	if !strings.Contains(body, "InvalidParameterException") {
		t.Fatalf("stack events do not mention InvalidParameterException:\n%s", body)
	}
	if !strings.Contains(body, "totally-invented-addon") {
		t.Fatalf("stack events do not name the rejected add-on:\n%s", body)
	}
}

func TestCreateStack_EKSAddonPublishedNameSucceeds(t *testing.T) {
	// Given: a template naming an add-on DescribeAddonVersions does publish.
	srv := helpers.NewTestServer(t)
	stackName := "eks-addon-published-name"
	template := `{"Resources":{
		"Cluster":{"Type":"AWS::EKS::Cluster","Properties":` + eksAddonClusterProperties + `},
		"Addon":{"Type":"AWS::EKS::Addon","DependsOn":"Cluster","Properties":{
			"ClusterName":"addon-name-test-cluster",
			"AddonName":"coredns"}}
	}}`

	// When: the stack is created.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: it completes, and the add-on exists on the cluster.
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	descResp := eksCFNCall(t, srv, http.MethodGet, "/clusters/addon-name-test-cluster/addons/coredns")
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
}

// eksCFNCall issues a plain REST-JSON request against the EKS surface the
// same server exposes, for assertions that read EKS state directly rather
// than through a CloudFormation action.
func eksCFNCall(t *testing.T, srv *helpers.TestServer, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build EKS request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("EKS %s %s: %v", method, path, err)
	}
	return resp
}
