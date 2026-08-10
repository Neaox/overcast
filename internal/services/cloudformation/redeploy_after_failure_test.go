package cloudformation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

// redeployHandler provisions a resource named by its Name property, failing for
// whichever name the test currently points it at. It records the name it was
// asked to create or update, and the physical ID it was asked to delete, so a
// test can say which of the three a redeploy chose.
type redeployHandler struct {
	failName string
	created  []string
	updated  []string
	deleted  []string
}

func (h *redeployHandler) Create(_ context.Context, _ http.Handler, _ *config.Config, props map[string]any, _ *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == h.failName {
		return "", nil, errors.New("cannot provision " + name)
	}
	h.created = append(h.created, name)
	return "phys-" + name, nil, nil
}

func (h *redeployHandler) Update(_ context.Context, _ http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, _ *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	h.updated = append(h.updated, name)
	return physicalID, nil, nil
}

func (h *redeployHandler) Delete(_ context.Context, _ http.Handler, _ *config.Config, physicalID string, _ *resolveContext) error {
	h.deleted = append(h.deleted, physicalID)
	return nil
}

// registerRedeployHandler installs the handler under a resource type unique to
// the calling test, so the shared resourceHandlers map stays test-local.
func registerRedeployHandler(t *testing.T, h *redeployHandler) string {
	t.Helper()
	resourceType := "Test::Redeploy::" + t.Name()
	resourceHandlers[resourceType] = h
	t.Cleanup(func() { delete(resourceHandlers, resourceType) })
	return resourceType
}

// redeployTemplate is a one-resource template whose resource is provisioned
// under the given name.
func redeployTemplate(resourceType, name string) string {
	return fmt.Sprintf(`{"Resources":{"Widget":{"Type":%q,"Properties":{"Name":%q}}}}`,
		resourceType, name)
}

// resourceRecords returns the stack's resource list as
// "<logicalId> <status> <reason>" strings, which is what these tests compare —
// a slice, not a map, because the defect being guarded is a second record for a
// logical ID that already has one.
func resourceRecords(t *testing.T, st *cfnStore, stackName string) []string {
	t.Helper()
	stack, err := st.getStack(context.Background(), stackName)
	if err != nil || stack == nil {
		t.Fatalf("getStack(%q) = %v, %v", stackName, stack, err)
	}
	records := make([]string, 0, len(stack.Resources))
	for _, r := range stack.Resources {
		records = append(records, fmt.Sprintf("%s %s %s", r.LogicalID, r.Status, r.StatusReason))
	}
	return records
}

// executeCreateChangeSet runs the CreateChangeSet + ExecuteChangeSet pair the
// CDK CLI issues for a stack it is creating.
func executeCreateChangeSet(t *testing.T, h *Handler, stackName, csName, templateBody string) {
	t.Helper()
	for _, call := range []struct {
		action string
		params map[string]string
	}{
		{"CreateChangeSet", map[string]string{
			"StackName": stackName, "ChangeSetName": csName,
			"ChangeSetType": "CREATE", "TemplateBody": templateBody,
		}},
		{"ExecuteChangeSet", map[string]string{
			"StackName": stackName, "ChangeSetName": csName,
		}},
	} {
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost(call.action, call.params))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body: %s", call.action, rec.Code, rec.Body.String())
		}
	}
}

// Deploying a stack that already failed once is the CDK workflow this most
// affects: the CLI executes a CREATE change set against the stored record,
// which still describes the attempt that rolled back. Provisioning appended to
// that record's resource list instead of replacing it, so the stack came out
// owning two entries for the same logical ID — and DescribeStackResources, the
// console banner and `cdk deploy`'s own output all answer with the first one,
// reporting the previous run's failure over the reason this run failed for.
func TestExecuteChangeSet_recreatingAFailedStackReplacesItsResourceRecords(t *testing.T) {
	// Given: a stack whose first deploy failed and rolled back
	handler := &redeployHandler{failName: "widget-v1"}
	resourceType := registerRedeployHandler(t, handler)
	h, st := newRollbackTestHandler(t)
	h.prov.initRouter(http.NotFoundHandler())

	executeCreateChangeSet(t, h, "app", "cs-1", redeployTemplate(resourceType, "widget-v1"))
	if got, want := resourceRecords(t, st, "app"),
		[]string{"Widget CREATE_FAILED cannot provision widget-v1"}; !slices.Equal(got, want) {
		t.Fatalf("after the first deploy, resources = %v, want %v", got, want)
	}

	// When: it is deployed again and fails for a different reason
	handler.failName = "widget-v2"
	executeCreateChangeSet(t, h, "app", "cs-2", redeployTemplate(resourceType, "widget-v2"))

	// Then: the stack holds one record for the resource, carrying this run's
	// reason and not the previous run's
	want := []string{"Widget CREATE_FAILED cannot provision widget-v2"}
	if got := resourceRecords(t, st, "app"); !slices.Equal(got, want) {
		t.Errorf("after the second deploy, resources = %v, want %v", got, want)
	}
}

// Outputs belong to the generation that resolved them. A re-create that fails
// never reaches resolveOutputs, so anything the record was still carrying would
// survive as the failed stack's outputs.
func TestProvisionStackResources_failedRecreateDropsThePreviousOutputs(t *testing.T) {
	// Given: a stack record left over from an earlier generation
	handler := &redeployHandler{failName: "widget"}
	resourceType := registerRedeployHandler(t, handler)
	p := newProvisionerTestFixture(t)
	stack := &Stack{
		StackName: "app",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/app/1111",
		Region:    "us-east-1",
		Outputs:   []Output{{Key: "QueueUrl", Value: "http://localhost:4566/000000000000/gone"}},
	}
	tmpl, err := parseTemplate(redeployTemplate(resourceType, "widget"))
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}

	// When: it is created again and the create fails
	p.provisionStackResources(stack, tmpl)

	// Then: the stack rolled back and reports no outputs of its own
	if stack.Status != StatusRollbackComplete {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusRollbackComplete)
	}
	if len(stack.Outputs) != 0 {
		t.Errorf("outputs = %v, want none", stack.Outputs)
	}
}

// A create that failed before naming anything leaves a record with no resource
// behind it. The next update matched that record by logical ID and treated it
// as an existing resource — and since a failed create records no property hash,
// resourcePropertiesMatch read the comparison as "unchanged" — so the update
// skipped the resource entirely and kept reporting the failed attempt's status
// and reason for a resource the stack had never created.
func TestUpdateStack_reprovisionsAResourceThePreviousCreateNeverMade(t *testing.T) {
	// Given: a stack holding only the record of a create that failed outright
	handler := &redeployHandler{}
	resourceType := registerRedeployHandler(t, handler)
	p := newProvisionerTestFixture(t)
	stack := &Stack{
		StackName: "app",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/app/1111",
		Region:    "us-east-1",
		Resources: []StackResource{{
			LogicalID:    "Widget",
			Type:         resourceType,
			Status:       ResourceCreateFailed,
			StatusReason: "cannot provision widget",
		}},
	}
	tmpl, err := parseTemplate(redeployTemplate(resourceType, "widget"))
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}

	// When: the stack is updated with the same template, and the resource can
	// now be provisioned
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the resource is created, and its record no longer carries the
	// failure of the attempt that never made it
	if stack.Status != StatusUpdateComplete {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateComplete)
	}
	if got, want := handler.created, []string{"widget"}; !slices.Equal(got, want) {
		t.Errorf("created = %v, want %v", got, want)
	}
	want := []string{"Widget CREATE_COMPLETE "}
	if got := resourceRecords(t, p.store, "app"); !slices.Equal(got, want) {
		t.Errorf("resources = %v, want %v", got, want)
	}
}

// The counterpart to the two tests above: narrowing what an update accepts as
// an existing resource must not narrow it past the resources that really are
// there. A record backed by a live resource is still diffed and changed in
// place — CloudFormation replaces a resource only when the property that
// changed requires it, and an update that re-created every resource instead
// would hand back new physical IDs and lose whatever they held.
func TestUpdateStack_updatesAResourceThatExistsInPlace(t *testing.T) {
	// Given: a stack holding a resource the previous deploy successfully created
	handler := &redeployHandler{}
	resourceType := registerRedeployHandler(t, handler)
	p := newProvisionerTestFixture(t)
	oldProps := map[string]any{"Name": "widget-v1"}
	stack := &Stack{
		StackName: "app",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/app/1111",
		Region:    "us-east-1",
		Resources: []StackResource{{
			LogicalID:      "Widget",
			PhysicalID:     "phys-widget-v1",
			Type:           resourceType,
			Status:         ResourceCreateComplete,
			PropertiesHash: hashResourceProperties(resourceType, oldProps, nil),
			Properties:     oldProps,
		}},
	}
	tmpl, err := parseTemplate(redeployTemplate(resourceType, "widget-v2"))
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}

	// When: the stack is updated with changed properties
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the resource is updated in place, keeping its physical ID, and is
	// neither re-created nor torn down
	if stack.Status != StatusUpdateComplete {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateComplete)
	}
	if got, want := handler.updated, []string{"widget-v2"}; !slices.Equal(got, want) {
		t.Errorf("updated = %v, want %v", got, want)
	}
	if len(handler.created) != 0 || len(handler.deleted) != 0 {
		t.Errorf("created = %v, deleted = %v, want neither — the update replaced a resource it could change",
			handler.created, handler.deleted)
	}
	if got := stack.Resources[0].PhysicalID; got != "phys-widget-v1" {
		t.Errorf("physical ID = %q, want it unchanged at phys-widget-v1", got)
	}
	want := []string{"Widget UPDATE_COMPLETE "}
	if got := resourceRecords(t, p.store, "app"); !slices.Equal(got, want) {
		t.Errorf("resources = %v, want %v", got, want)
	}
}

// A create rollback records what it deleted rather than dropping it, so the
// record outlives the resource and keeps its physical ID. An update that read
// that as an existing resource skipped it as unchanged; one that re-creates it
// must also not then delete it in the cleanup phase, which walks the pre-update
// list and would be deleting the resource it had just made.
func TestUpdateStack_reprovisionsAResourceTheRollbackDeleted(t *testing.T) {
	// Given: a stack holding the DELETE_COMPLETE record a create rollback left
	handler := &redeployHandler{}
	resourceType := registerRedeployHandler(t, handler)
	p := newProvisionerTestFixture(t)
	props := map[string]any{"Name": "widget"}
	stack := &Stack{
		StackName: "app",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/app/1111",
		Region:    "us-east-1",
		Resources: []StackResource{{
			LogicalID:      "Widget",
			PhysicalID:     "phys-widget",
			Type:           resourceType,
			Status:         ResourceDeleteComplete,
			PropertiesHash: hashResourceProperties(resourceType, props, nil),
			Properties:     props,
		}},
	}
	tmpl, err := parseTemplate(redeployTemplate(resourceType, "widget"))
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}

	// When: the stack is updated with the same template
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the resource is provisioned again and left standing
	if stack.Status != StatusUpdateComplete {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateComplete)
	}
	if got, want := handler.created, []string{"widget"}; !slices.Equal(got, want) {
		t.Errorf("created = %v, want %v", got, want)
	}
	if len(handler.deleted) != 0 {
		t.Errorf("deleted = %v, want none — the cleanup phase tore down what the update created", handler.deleted)
	}
	want := []string{"Widget CREATE_COMPLETE "}
	if got := resourceRecords(t, p.store, "app"); !slices.Equal(got, want) {
		t.Errorf("resources = %v, want %v", got, want)
	}
}
