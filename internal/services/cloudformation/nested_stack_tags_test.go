package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"go.uber.org/zap"
)

type nestedTagRouter struct {
	template string
	requests []capturedRequest
}

func (r *nestedTagRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet && req.URL.Path == "/child-template" {
		_, _ = w.Write([]byte(r.template))
		return
	}
	raw, _ := io.ReadAll(req.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	r.requests = append(r.requests, capturedRequest{method: req.Method, path: req.URL.Path, query: req.URL.RawQuery, body: body})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"FunctionArn": "arn:aws:lambda:us-east-1:000000000000:function:child-function",
	})
}

func TestMergeNestedStackTags_ResourceTagsOverrideParent(t *testing.T) {
	got := mergeNestedStackTags(
		[]Tag{{Key: "owner", Value: "parent"}, {Key: "stage", Value: "test"}},
		[]any{map[string]any{"Key": "owner", "Value": "child"}},
	)
	want := []Tag{{Key: "owner", Value: "child"}, {Key: "stage", Value: "test"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged tags = %#v, want %#v", got, want)
	}
}

func TestNestedStackTags_PropagateThroughCreateUpdateRemovalAndRollback(t *testing.T) {
	store, clk := newTestCFNStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	router := &nestedTagRouter{template: `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"child-function","Runtime":"nodejs22.x","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/r","Code":{"ZipFile":"exports.handler=()=>1"}}}}}`}
	p := newProvisioner(cfg, store, clk, serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation"))
	p.initRouter(router)
	h := &nestedStackHandler{p: p}
	parentTags := []Tag{{Key: "stage", Value: "parent"}}
	oldProps := map[string]any{
		"TemplateURL": "http://overcast/child-template",
		"Tags":        []any{map[string]any{"Key": "owner", "Value": "child"}},
	}
	rCtx := &resolveContext{
		Region: "us-east-1", AccountID: cfg.AccountID, StackName: "parent",
		StackID: "arn:aws:cloudformation:us-east-1:000000000000:stack/parent/id", StackTags: parentTags,
	}

	physicalID, _, err := h.Create(context.Background(), router, cfg, oldProps, rCtx)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	create := router.requests[len(router.requests)-1]
	if got := create.body["Tags"]; !reflect.DeepEqual(got, map[string]any{"owner": "child", "stage": "parent"}) {
		t.Fatalf("create Tags = %#v", got)
	}

	router.requests = nil
	updatedProps := map[string]any{
		"TemplateURL": "http://overcast/child-template",
		"Tags":        []any{map[string]any{"Key": "owner", "Value": "updated"}},
	}
	if _, _, err := h.Update(context.Background(), router, cfg, physicalID, updatedProps, oldProps, rCtx); err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertNestedTagRequest(t, router.requests, http.MethodPost, "", map[string]any{"owner": "updated"})

	router.requests = nil
	removedProps := map[string]any{"TemplateURL": "http://overcast/child-template"}
	if _, _, err := h.Update(context.Background(), router, cfg, physicalID, removedProps, updatedProps, rCtx); err != nil {
		t.Fatalf("Remove tags: %v", err)
	}
	assertNestedTagRequest(t, router.requests, http.MethodDelete, "tagKeys=owner", nil)

	// Parent rollback calls the same updater in reverse with old/new properties.
	router.requests = nil
	if _, _, err := h.Update(context.Background(), router, cfg, physicalID, updatedProps, removedProps, rCtx); err != nil {
		t.Fatalf("Rollback tags: %v", err)
	}
	assertNestedTagRequest(t, router.requests, http.MethodPost, "", map[string]any{"owner": "updated"})
}

func assertNestedTagRequest(t *testing.T, requests []capturedRequest, method, query string, tags map[string]any) {
	t.Helper()
	for _, request := range requests {
		if request.path != "/2017-03-31/tags/arn:aws:lambda:us-east-1:000000000000:function:child-function" {
			continue
		}
		if request.method != method || request.query != query {
			t.Fatalf("tag request = %s ?%s, want %s ?%s", request.method, request.query, method, query)
		}
		if tags != nil && !reflect.DeepEqual(request.body["Tags"], tags) {
			t.Fatalf("tag body = %#v, want %#v", request.body["Tags"], tags)
		}
		return
	}
	t.Fatalf("no Lambda tag request in %#v", requests)
}

// blockedDeleteRouter answers every IAM DeleteRole with AWS's DeleteConflict and
// serves the child template for the nested stack under test.
type blockedDeleteRouter struct {
	template string
	refuse   bool
}

func (r *blockedDeleteRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet && req.URL.Path == "/child-template" {
		_, _ = w.Write([]byte(r.template))
		return
	}
	raw, _ := io.ReadAll(req.Body)
	if r.refuse && strings.Contains(string(raw), "Action=DeleteRole") {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`<ErrorResponse><Error><Type>Sender</Type><Code>DeleteConflict</Code>` +
			`<Message>Cannot delete entity, must detach all policies first.</Message></Error>` +
			`<RequestId>req-1</RequestId></ErrorResponse>`))
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	_, _ = w.Write([]byte(`<DeleteRoleResponse/>`))
}

// TestNestedStackDelete_childRefusalFailsTheParent covers the nested-stack half
// of the teardown contract. A child that cannot be torn down used to be reported
// as deleted, so a parent stack reached DELETE_COMPLETE over resources the child
// was still holding.
func TestNestedStackDelete_childRefusalFailsTheParent(t *testing.T) {
	newParentAndChild := func(t *testing.T, refuse bool) (*nestedStackHandler, *blockedDeleteRouter, *cfnStore, *resolveContext, string) {
		t.Helper()
		store, clk := newTestCFNStore()
		cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
		router := &blockedDeleteRouter{refuse: refuse}
		p := newProvisioner(cfg, store, clk, serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation"))
		p.initRouter(router)

		childARN := "arn:aws:cloudformation:us-east-1:000000000000:stack/child/uuid"
		child := &Stack{
			StackName: "child",
			StackID:   childARN,
			Region:    "us-east-1",
			Status:    StatusCreateComplete,
			Resources: []StackResource{{
				LogicalID:  "AppRole",
				PhysicalID: "child-role",
				Type:       "AWS::IAM::Role",
				Status:     ResourceCreateComplete,
			}},
		}
		if err := store.putStack(p.regionCtx("us-east-1"), child); err != nil {
			t.Fatalf("seed child stack: %v", err)
		}
		rCtx := &resolveContext{
			Region: "us-east-1", AccountID: cfg.AccountID, StackName: "parent",
			StackID: "arn:aws:cloudformation:us-east-1:000000000000:stack/parent/id",
		}
		return &nestedStackHandler{p: p}, router, store, rCtx, childARN
	}

	t.Run("child refusal blocks the parent", func(t *testing.T) {
		h, router, store, rCtx, childARN := newParentAndChild(t, true)

		err := h.Delete(context.Background(), router, nil, childARN, rCtx)
		if !errors.Is(err, errDeletionBlocked) {
			t.Fatalf("Delete = %v, want it to wrap errDeletionBlocked", err)
		}
		if !strings.Contains(err.Error(), "Cannot delete entity, must detach all policies first.") {
			t.Errorf("Delete = %v, want it to carry IAM's refusal message", err)
		}
		child, _ := store.getStack(h.p.regionCtx("us-east-1"), "child")
		if child == nil || child.Status != StatusDeleteFailed {
			t.Fatalf("child stack status = %v, want DELETE_FAILED", child)
		}
	})

	t.Run("child that tears down cleanly does not", func(t *testing.T) {
		h, router, _, rCtx, childARN := newParentAndChild(t, false)

		if err := h.Delete(context.Background(), router, nil, childARN, rCtx); err != nil {
			t.Fatalf("Delete = %v, want nil", err)
		}
	})
}
