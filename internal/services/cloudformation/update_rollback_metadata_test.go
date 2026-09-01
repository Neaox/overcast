package cloudformation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// failingCreateHandler provisions nothing: it is the resource an update adds to
// make that update fail after the stack record has already been overwritten
// with the attempted template and parameters.
type failingCreateHandler struct{}

func (failingCreateHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	return "", nil, errors.New("resource cannot be created")
}

func (failingCreateHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

// registerResourceHandler installs a handler under a type name unique to the
// running test and to kind, so neither a test's own resource types nor two
// tests' can collide.
func registerResourceHandler(t *testing.T, kind string, handler resourceHandler) string {
	t.Helper()
	resourceType := "Test::" + kind + "::" + t.Name()
	resourceHandlers[resourceType] = handler
	t.Cleanup(func() { delete(resourceHandlers, resourceType) })
	return resourceType
}

// rollbackMetadataFixture is one stack, its two template generations, and the
// metadata each generation carries.
type rollbackMetadataFixture struct {
	h            *Handler
	st           *cfnStore
	stackName    string
	baseTemplate string
	failTemplate string
}

func newRollbackMetadataFixture(t *testing.T) rollbackMetadataFixture {
	t.Helper()
	keepType := registerResourceHandler(t, "MetadataKeep", &stubResourceHandler{})
	failType := registerResourceHandler(t, "MetadataBroken", failingCreateHandler{})

	keepProps := map[string]any{"Name": "keep"}
	base := fmt.Sprintf(`{"Resources":{"Keep":{"Type":%q,"Properties":{"Name":"keep"}}}}`, keepType)
	failing := fmt.Sprintf(
		`{"Resources":{"Keep":{"Type":%q,"Properties":{"Name":"keep"}},"Broken":{"Type":%q,"Properties":{}}}}`,
		keepType, failType)

	h, st := newRollbackTestHandler(t)
	stack := &Stack{
		StackName:    "meta-rollback",
		StackID:      "arn:aws:cloudformation:us-east-1:000000000000:stack/meta-rollback/1111",
		Region:       "us-east-1",
		TemplateBody: base,
		Parameters:   []Parameter{{Key: "Stage", Value: "one"}},
		Tags:         []Tag{{Key: "stage", Value: "one"}},
		Outputs:      []Output{{Key: "Group", Value: "keep-id"}},
		Status:       StatusCreateComplete,
		Resources: []StackResource{{
			LogicalID:      "Keep",
			PhysicalID:     "keep-id",
			Type:           keepType,
			Status:         ResourceCreateComplete,
			PropertiesHash: hashResourceProperties(keepType, keepProps, nil),
			Properties:     keepProps,
		}},
	}
	if err := st.putStack(context.Background(), stack); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
	return rollbackMetadataFixture{h: h, st: st, stackName: stack.StackName, baseTemplate: base, failTemplate: failing}
}

// An update persists the template and parameters it was given before any
// resource is touched, so the record describes the attempt from the moment it
// starts. Rolling the resources back therefore has to hand the previous
// generation back as well — otherwise GetTemplate serves the template that
// failed, and the next update resolves parameters from an attempt that was
// undone, over resources that describe the generation before it.
//
// Every entry point that starts an update has to do this, not just the one the
// wire happens to take today: DispatchQuery prefers the typed operation for
// UpdateStack and falls back to the Query handler, and ExecuteChangeSet is
// still on the Query handler with a typed implementation waiting behind it.
func TestUpdateStack_rollbackRestoresSupersededMetadata(t *testing.T) {
	entryPoints := []struct {
		name  string
		start func(t *testing.T, f rollbackMetadataFixture)
	}{
		{
			name: "query UpdateStack",
			start: func(t *testing.T, f rollbackMetadataFixture) {
				t.Helper()
				rec := httptest.NewRecorder()
				f.h.dispatch(rec, cfnPost("UpdateStack", map[string]string{
					"StackName":                          f.stackName,
					"TemplateBody":                       f.failTemplate,
					"Parameters.member.1.ParameterKey":   "Stage",
					"Parameters.member.1.ParameterValue": "two",
					"Tags.member.1.Key":                  "stage",
					"Tags.member.1.Value":                "two",
				}))
				if rec.Code != http.StatusOK {
					t.Fatalf("UpdateStack status = %d, want 200; body: %s", rec.Code, rec.Body.String())
				}
			},
		},
		{
			name: "typed UpdateStack",
			start: func(t *testing.T, f rollbackMetadataFixture) {
				t.Helper()
				tags := []cfnTagMember{{Key: "stage", Value: "two"}}
				_, aerr := f.h.updateStackTyped(context.Background(), &updateStackReq{
					StackName:    f.stackName,
					TemplateBody: f.failTemplate,
					Parameters:   []cfnParamMember{{ParameterKey: "Stage", ParameterValue: "two"}},
					Tags:         &tags,
				})
				if aerr != nil {
					t.Fatalf("updateStackTyped: %v", aerr)
				}
			},
		},
		{
			name: "query ExecuteChangeSet",
			start: func(t *testing.T, f rollbackMetadataFixture) {
				t.Helper()
				seedUpdateChangeSet(t, f)
				rec := httptest.NewRecorder()
				f.h.dispatch(rec, cfnPost("ExecuteChangeSet", map[string]string{
					"StackName":     f.stackName,
					"ChangeSetName": "cs",
				}))
				if rec.Code != http.StatusOK {
					t.Fatalf("ExecuteChangeSet status = %d, want 200; body: %s", rec.Code, rec.Body.String())
				}
			},
		},
		{
			name: "typed ExecuteChangeSet",
			start: func(t *testing.T, f rollbackMetadataFixture) {
				t.Helper()
				seedUpdateChangeSet(t, f)
				if _, aerr := f.h.executeChangeSetTyped(context.Background(), &executeChangeSetReq{
					StackName:     f.stackName,
					ChangeSetName: "cs",
				}); aerr != nil {
					t.Fatalf("executeChangeSetTyped: %v", aerr)
				}
			},
		},
	}

	for _, entryPoint := range entryPoints {
		t.Run(entryPoint.name, func(t *testing.T) {
			// Given: a stack whose recorded template, parameters and tags describe
			// the generation its resources are actually in.
			f := newRollbackMetadataFixture(t)

			// When: an update carrying a new template, parameters and tags fails.
			entryPoint.start(t, f)

			// Then: the persisted stack describes the generation it rolled back to.
			got, aerr := f.st.getStack(context.Background(), f.stackName)
			if aerr != nil || got == nil {
				t.Fatalf("getStack: %v", aerr)
			}
			if got.Status != StatusUpdateRollbackComplete {
				t.Fatalf("stack status = %q (%s), want %q", got.Status, got.StatusReason, StatusUpdateRollbackComplete)
			}
			if got.TemplateBody != f.baseTemplate {
				t.Errorf("TemplateBody = %s\nwant %s", got.TemplateBody, f.baseTemplate)
			}
			if want := []Parameter{{Key: "Stage", Value: "one"}}; !reflect.DeepEqual(got.Parameters, want) {
				t.Errorf("Parameters = %+v, want %+v", got.Parameters, want)
			}
			if want := []Tag{{Key: "stage", Value: "one"}}; !reflect.DeepEqual(got.Tags, want) {
				t.Errorf("Tags = %+v, want %+v", got.Tags, want)
			}
			if want := []Output{{Key: "Group", Value: "keep-id"}}; !reflect.DeepEqual(got.Outputs, want) {
				t.Errorf("Outputs = %+v, want %+v", got.Outputs, want)
			}
			if len(got.Resources) != 1 || got.Resources[0].LogicalID != "Keep" {
				t.Errorf("Resources = %+v, want only the pre-update Keep record", got.Resources)
			}
		})
	}
}

// storeBreakingCreateHandler fails to provision, and takes the store's writes
// down with it — the shape of an update that fails because the persistent
// backend became unavailable underneath it.
type storeBreakingCreateHandler struct{ store *failingStore }

func (h storeBreakingCreateHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	h.store.failSet = true
	return "", nil, errors.New("resource cannot be created")
}

func (storeBreakingCreateHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

// A rollback that cannot write the restored generation back has not restored
// it: the stack still describes the attempt that failed. Reporting
// UPDATE_ROLLBACK_COMPLETE over that is the same lie as reporting it over
// resources that could not be deleted, and AWS reports UPDATE_ROLLBACK_FAILED
// for both.
func TestUpdateStack_metadataRestoreFailureLeavesRollbackFailed(t *testing.T) {
	// Given: a stack whose store stops accepting writes mid-update.
	keepType := registerResourceHandler(t, "MetadataKeep", &stubResourceHandler{})
	p := newProvisionerTestFixture(t)
	failing := &failingStore{Store: state.NewMemoryStore()}
	p.store = newCFNStore(failing, "us-east-1", p.clk)
	failType := registerResourceHandler(t, "MetadataBroken", storeBreakingCreateHandler{store: failing})

	keepProps := map[string]any{"Name": "keep"}
	stack := &Stack{
		StackName:    "meta-rollback-store-failure",
		StackID:      "arn:aws:cloudformation:us-east-1:000000000000:stack/meta-rollback-store-failure/1111",
		Region:       "us-east-1",
		TemplateBody: `{"Resources":{}}`,
		Parameters:   []Parameter{{Key: "Stage", Value: "one"}},
		Status:       StatusUpdateInProgress,
		Resources: []StackResource{{
			LogicalID:      "Keep",
			PhysicalID:     "keep-id",
			Type:           keepType,
			Status:         ResourceCreateComplete,
			PropertiesHash: hashResourceProperties(keepType, keepProps, nil),
			Properties:     keepProps,
		}},
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"Keep":   {Type: keepType, Properties: keepProps},
		"Broken": {Type: failType, Properties: map[string]any{}},
	}}

	// When: the update fails and the rollback cannot persist what it restored.
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the stack does not claim the rollback completed.
	if stack.Status != StatusUpdateRollbackFailed {
		t.Fatalf("stack status = %q (%s), want %q", stack.Status, stack.StatusReason, StatusUpdateRollbackFailed)
	}
	if !strings.Contains(stack.StatusReason, "restore stack metadata") {
		t.Errorf("stack status reason = %q, want it to name the failed metadata restore", stack.StatusReason)
	}
}

// childTemplateRouter serves one nested-stack template body and nothing else.
type childTemplateRouter struct{ template string }

func (r *childTemplateRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet && req.URL.Path == "/child-template" {
		_, _ = w.Write([]byte(r.template))
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// A nested stack's own record is overwritten with the attempted template and
// parameters before its resources are touched, exactly as the top-level stack's
// is. It is worse here: the parent only reverses an in-place update it recorded
// as successful, and a child whose update failed was never recorded as one — so
// nothing above the child will ever put its metadata back. The child's own
// rollback has to.
func TestNestedStackUpdate_failedChildUpdateRestoresChildMetadata(t *testing.T) {
	// Given: a provisioned child stack, and a new child template whose added
	// resource cannot be created.
	keepType := registerResourceHandler(t, "MetadataKeep", &stubResourceHandler{})
	failType := registerResourceHandler(t, "MetadataBroken", failingCreateHandler{})

	keepProps := map[string]any{"Name": "keep"}
	childBase := fmt.Sprintf(`{"Resources":{"Keep":{"Type":%q,"Properties":{"Name":"keep"}}}}`, keepType)
	childFailing := fmt.Sprintf(
		`{"Resources":{"Keep":{"Type":%q,"Properties":{"Name":"keep"}},"Broken":{"Type":%q,"Properties":{}}}}`,
		keepType, failType)

	p := newProvisionerTestFixture(t)
	p.initRouter(&childTemplateRouter{template: childFailing})
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	childARN := "arn:aws:cloudformation:us-east-1:000000000000:stack/child/uuid"
	child := &Stack{
		StackName:    "child",
		StackID:      childARN,
		Region:       "us-east-1",
		TemplateBody: childBase,
		Parameters:   []Parameter{{Key: "Size", Value: "small"}},
		Tags:         []Tag{{Key: "stage", Value: "one"}},
		Status:       StatusCreateComplete,
		Resources: []StackResource{{
			LogicalID:      "Keep",
			PhysicalID:     "keep-id",
			Type:           keepType,
			Status:         ResourceCreateComplete,
			PropertiesHash: hashResourceProperties(keepType, keepProps, nil),
			Properties:     keepProps,
		}},
	}
	storeCtx := p.regionCtx("us-east-1")
	if err := p.store.putStack(storeCtx, child); err != nil {
		t.Fatalf("seed child stack: %v", err)
	}
	h := &nestedStackHandler{p: p}
	rCtx := &resolveContext{
		Region: "us-east-1", AccountID: cfg.AccountID, StackName: "parent",
		StackID:           "arn:aws:cloudformation:us-east-1:000000000000:stack/parent/id",
		StackTags:         []Tag{{Key: "stage", Value: "two"}},
		PreviousStackTags: []Tag{{Key: "stage", Value: "one"}},
	}

	// When: the parent updates the nested stack and the child update fails.
	_, _, err := h.Update(context.Background(), p.router, cfg, childARN,
		map[string]any{
			"TemplateURL": "http://overcast/child-template",
			"Parameters":  map[string]any{"Size": "large"},
		},
		map[string]any{
			"TemplateURL": "http://overcast/child-template",
			"Parameters":  map[string]any{"Size": "small"},
		}, rCtx)

	// Then: the failure reaches the parent, and the child describes the
	// generation its own resources were rolled back to.
	if err == nil {
		t.Fatal("nested stack Update = nil, want the child's failure")
	}
	got, aerr := p.store.getStack(storeCtx, "child")
	if aerr != nil || got == nil {
		t.Fatalf("getStack child: %v", aerr)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Fatalf("child status = %q (%s), want %q", got.Status, got.StatusReason, StatusUpdateRollbackComplete)
	}
	if got.TemplateBody != childBase {
		t.Errorf("child TemplateBody = %s\nwant %s", got.TemplateBody, childBase)
	}
	if want := []Parameter{{Key: "Size", Value: "small"}}; !reflect.DeepEqual(got.Parameters, want) {
		t.Errorf("child Parameters = %+v, want %+v", got.Parameters, want)
	}
	if want := []Tag{{Key: "stage", Value: "one"}}; !reflect.DeepEqual(got.Tags, want) {
		t.Errorf("child Tags = %+v, want %+v", got.Tags, want)
	}
	if len(got.Resources) != 1 || got.Resources[0].LogicalID != "Keep" {
		t.Errorf("child Resources = %+v, want only the pre-update Keep record", got.Resources)
	}
}

func seedUpdateChangeSet(t *testing.T, f rollbackMetadataFixture) {
	t.Helper()
	cs := &ChangeSet{
		ChangeSetName:   "cs",
		ChangeSetID:     "arn:aws:cloudformation:us-east-1:000000000000:changeSet/cs/2222",
		StackName:       f.stackName,
		TemplateBody:    f.failTemplate,
		Parameters:      []Parameter{{Key: "Stage", Value: "two"}},
		Tags:            []Tag{{Key: "stage", Value: "two"}},
		TagsSet:         true,
		Status:          ChangeSetStatusCreateComplete,
		ChangeSetType:   "UPDATE",
		ExecutionStatus: ExecStatusAvailable,
	}
	if err := f.st.putChangeSet(context.Background(), cs); err != nil {
		t.Fatalf("seed change set: %v", err)
	}
}
