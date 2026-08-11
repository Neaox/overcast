package cloudformation_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// appconfigStackTemplate provisions the three AppConfig resource types the
// provisioner supports.
const appconfigStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "App": {
      "Type": "AWS::AppConfig::Application",
      "Properties": {"Name": "cfn-app", "Description": "from a stack"}
    },
    "Env": {
      "Type": "AWS::AppConfig::Environment",
      "Properties": {"ApplicationId": {"Ref": "App"}, "Name": "cfn-env"}
    },
    "Profile": {
      "Type": "AWS::AppConfig::ConfigurationProfile",
      "Properties": {
        "ApplicationId": {"Ref": "App"},
        "Name": "cfn-profile",
        "LocationUri": "hosted"
      }
    }
  },
  "Outputs": {
    "ApplicationId": {"Value": {"Ref": "App"}},
    "EnvironmentId": {"Value": {"Fn::GetAtt": ["Env", "Id"]}},
    "ProfileId": {"Value": {"Fn::GetAtt": ["Profile", "Id"]}}
  }
}`

// appconfigGet reads an AppConfig resource at its modeled binding, signed with
// AppConfig's credential scope — /applications is shared with Service Catalog
// AppRegistry and the scope is what tells the two apart.
func appconfigGet(t *testing.T, srv *helpers.TestServer, path string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20250101/us-east-1/appconfig/aws4_request, SignedHeaders=host, Signature=fake")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("AppConfig GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("AppConfig GET %s: decode: %v", path, err)
	}
	return resp.StatusCode, body
}

func TestCreateStack_AppConfigApplicationEnvironmentProfile(t *testing.T) {
	// Given: a stack declaring an AppConfig application, environment and
	// configuration profile.
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"appconfig-stack"},
		"TemplateBody": []string{appconfigStackTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: the stack completes.
	waitForStackStatus(t, srv, "appconfig-stack", "CREATE_COMPLETE")

	// Then: the resources exist in AppConfig, not in AppRegistry. Before #854
	// the provisioner dispatched over an invented `AppConfig.` X-Amz-Target
	// namespace; with that gone, an unscoped REST dispatch to /applications
	// would have created a Service Catalog application instead.
	status, apps := appconfigGet(t, srv, "/applications")
	if status != http.StatusOK {
		t.Fatalf("ListApplications: HTTP %d: %#v", status, apps)
	}
	items, _ := apps["Items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 AppConfig application, got %#v", apps)
	}
	app := items[0].(map[string]any)
	if app["Name"] != "cfn-app" || app["Description"] != "from a stack" {
		t.Fatalf("unexpected application: %#v", app)
	}
	appID, _ := app["Id"].(string)

	// And: the environment and the profile hang off it.
	status, envs := appconfigGet(t, srv, fmt.Sprintf("/applications/%s/environments", appID))
	if status != http.StatusOK {
		t.Fatalf("ListEnvironments: HTTP %d: %#v", status, envs)
	}
	envItems, _ := envs["Items"].([]any)
	if len(envItems) != 1 || envItems[0].(map[string]any)["Name"] != "cfn-env" {
		t.Fatalf("unexpected environments: %#v", envs)
	}

	status, profiles := appconfigGet(t, srv, fmt.Sprintf("/applications/%s/configurationprofiles", appID))
	if status != http.StatusOK {
		t.Fatalf("ListConfigurationProfiles: HTTP %d: %#v", status, profiles)
	}
	profItems, _ := profiles["Items"].([]any)
	if len(profItems) != 1 || profItems[0].(map[string]any)["Name"] != "cfn-profile" {
		t.Fatalf("unexpected configuration profiles: %#v", profiles)
	}

	// When: the stack is deleted.
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"appconfig-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)
	waitForStackStatus(t, srv, "appconfig-stack", "DELETE_COMPLETE")

	// Then: the application is gone, so the delete addressed it by the path
	// label rather than sending the ID somewhere nothing matched.
	status, after := appconfigGet(t, srv, "/applications")
	if status != http.StatusOK {
		t.Fatalf("ListApplications after delete: HTTP %d: %#v", status, after)
	}
	if remaining, _ := after["Items"].([]any); len(remaining) != 0 {
		t.Fatalf("expected the application to be deleted, got %#v", after)
	}
}
