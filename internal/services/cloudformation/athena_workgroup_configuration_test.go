package cloudformation

// athena_workgroup_configuration_test.go — AWS::Athena::WorkGroup's schema
// names the configuration property WorkGroupConfiguration
// (https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-athena-workgroup.html);
// the CreateWorkGroup API member it maps onto is Configuration. The handler
// used to read the API member's name from the template, so a real template's
// WorkGroupConfiguration was silently dropped — the same
// template-property-vs-API-member confusion #1309 fixed for
// AWS::CloudTrail::Trail's TrailName.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

const athenaCreateWorkGroupTarget = "AmazonAthena.CreateWorkGroup"

// athenaRecordingRouter answers 200 to every dispatch and keeps each decoded
// request body keyed by X-Amz-Target, so a test can assert on exactly what
// the handler forwarded to Athena.
type athenaRecordingRouter struct {
	bodies map[string][]map[string]any
}

func (r *athenaRecordingRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	target := req.Header.Get("X-Amz-Target")
	body := map[string]any{}
	raw, _ := io.ReadAll(req.Body)
	_ = json.Unmarshal(raw, &body)
	if r.bodies == nil {
		r.bodies = make(map[string][]map[string]any)
	}
	r.bodies[target] = append(r.bodies[target], body)
	_ = json.NewEncoder(w).Encode(map[string]any{})
}

func (r *athenaRecordingRouter) onlyCreate(t *testing.T) map[string]any {
	t.Helper()
	creates := r.bodies[athenaCreateWorkGroupTarget]
	if len(creates) != 1 {
		t.Fatalf("CreateWorkGroup calls = %d, want 1 (bodies: %#v)", len(creates), r.bodies)
	}
	return creates[0]
}

func TestAthenaWorkGroupCreate_forwardsWorkGroupConfigurationAsConfiguration(t *testing.T) {
	// Given: a template using the real schema property, WorkGroupConfiguration
	router := &athenaRecordingRouter{}
	wantConfig := map[string]any{
		"EnforceWorkGroupConfiguration":   true,
		"PublishCloudWatchMetricsEnabled": false,
		"ResultConfiguration": map[string]any{
			"OutputLocation": "s3://athena-results/prefix/",
		},
	}
	props := map[string]any{
		"Name":                   "analytics",
		"WorkGroupConfiguration": wantConfig,
	}

	// When: CloudFormation creates the resource
	physicalID, _, err := (&athenaWorkGroupHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{Region: "us-east-1", StackName: "s"},
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if physicalID != "analytics" {
		t.Fatalf("physicalID = %q, want %q", physicalID, "analytics")
	}

	// Then: it reaches CreateWorkGroup under the API's member name, Configuration
	body := router.onlyCreate(t)
	got, ok := body["Configuration"]
	if !ok {
		t.Fatalf("CreateWorkGroup body has no Configuration; template's WorkGroupConfiguration was dropped (body: %#v)", body)
	}
	if !reflect.DeepEqual(got, wantConfig) {
		t.Fatalf("CreateWorkGroup Configuration = %#v, want %#v", got, wantConfig)
	}
}

// TestAthenaWorkGroupCreate_configurationIsNotAcceptedAsAnAlias pins the
// alpha no-shims policy: the API member's name, Configuration, is not a
// template property and is not honored as one. A template that sets it gets
// a workgroup with no configuration, exactly as any other unrecognised
// property is ignored (Overcast has no per-property diagnostic channel; see
// fidelity.go).
func TestAthenaWorkGroupCreate_configurationIsNotAcceptedAsAnAlias(t *testing.T) {
	router := &athenaRecordingRouter{}
	props := map[string]any{
		"Name": "analytics",
		"Configuration": map[string]any{
			"ResultConfiguration": map[string]any{"OutputLocation": "s3://wrong-key/"},
		},
	}

	if _, _, err := (&athenaWorkGroupHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{Region: "us-east-1", StackName: "s"},
	); err != nil {
		t.Fatalf("Create: %v", err)
	}

	body := router.onlyCreate(t)
	if _, ok := body["Configuration"]; ok {
		t.Fatalf("template key Configuration was honored as an alias for WorkGroupConfiguration: %#v", body["Configuration"])
	}
}
