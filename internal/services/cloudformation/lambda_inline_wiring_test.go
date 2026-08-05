package cloudformation

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// captureRouter records every request the provisioner dispatches and replies
// with a minimal CreateFunction/UpdateFunctionCode response. Update sends the
// code and the configuration as separate requests, so keeping only the last one
// would silently assert against the wrong body.
type captureRouter struct {
	requests     []capturedRequest
	failPath     string
	failed       bool
	failPaths    map[string]int
	pathRequests map[string]int
	failAt       map[string]map[int]int
}

type capturedRequest struct {
	path string
	body map[string]any
}

func (c *captureRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	c.requests = append(c.requests, capturedRequest{path: r.URL.Path, body: body})
	if c.pathRequests == nil {
		c.pathRequests = make(map[string]int)
	}
	c.pathRequests[r.URL.Path]++
	if status := c.failAt[r.URL.Path][c.pathRequests[r.URL.Path]]; status != 0 {
		http.Error(w, "injected compensation failure", status)
		return
	}
	if c.failPaths[r.URL.Path] > 0 {
		c.failPaths[r.URL.Path]--
		http.Error(w, "injected failure", http.StatusInternalServerError)
		return
	}
	if r.URL.Path == c.failPath && !c.failed {
		c.failed = true
		http.Error(w, "injected failure", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"FunctionArn": "arn:aws:lambda:us-east-1:000000000000:function:Stack-Function",
	})
}

func TestLambdaProvisionerCreateCleanupFailureRetainsPhysicalIDForRetry(t *testing.T) {
	const name = "partial-function"
	router := &captureRouter{failPaths: map[string]int{
		"/2017-10-31/functions/" + name + "/concurrency": 1,
		"/2015-03-31/functions/" + name:                  1,
	}}
	h := &lambdaFunctionHandler{}
	props := map[string]any{
		"FunctionName": name, "Runtime": "nodejs22.x", "Handler": "index.handler", "Role": "arn:aws:iam::000000000000:role/r",
		"Code": map[string]any{"ZipFile": "exports.handler=()=>1"}, "ReservedConcurrentExecutions": 1,
	}
	physicalID, _, err := h.Create(context.Background(), router, nil, props, &resolveContext{Region: "us-east-1"})
	if err == nil || physicalID != name || !strings.Contains(err.Error(), "PutFunctionConcurrency") || !strings.Contains(err.Error(), "cleanup DeleteFunction") {
		t.Fatalf("Create = physicalID %q, err %v", physicalID, err)
	}
	if err := h.Delete(context.Background(), router, nil, physicalID, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("retry Delete: %v", err)
	}
}

func TestLambdaProvisionerDeletePropagatesNonNotFoundFailure(t *testing.T) {
	router := &captureRouter{failPaths: map[string]int{"/2015-03-31/functions/delete-fn": 1}}
	h := &lambdaFunctionHandler{}
	err := h.Delete(context.Background(), router, nil, "delete-fn", &resolveContext{Region: "us-east-1"})
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("Delete error = %v, want injected failure", err)
	}
	if err := h.Delete(context.Background(), router, nil, "delete-fn", &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("retry Delete: %v", err)
	}
}

func TestLambdaProvisionerUpdateCompensatesCompletedPhasesInReverseOrder(t *testing.T) {
	const name = "Stack-Function"
	configurationPath := "/2015-03-31/functions/" + name + "/configuration"
	codePath := "/2015-03-31/functions/" + name + "/code"
	concurrencyPath := "/2017-10-31/functions/" + name + "/concurrency"
	oldProps := map[string]any{
		"Runtime": "nodejs22.x", "Handler": "index.handler", "Role": "arn:aws:iam::000000000000:role/r",
		"Description": "old", "Code": map[string]any{"ZipFile": "exports.handler=()=>1"}, "ReservedConcurrentExecutions": 1,
	}
	newProps := map[string]any{
		"Runtime": "nodejs22.x", "Handler": "index.handler", "Role": "arn:aws:iam::000000000000:role/r",
		"Description": "new", "Code": map[string]any{"ZipFile": "exports.handler=()=>2"}, "ReservedConcurrentExecutions": 2,
	}

	tests := []struct {
		name      string
		failPath  string
		wantPaths []string
	}{
		{
			name:      "configuration failure does not compensate",
			failPath:  configurationPath,
			wantPaths: []string{configurationPath},
		},
		{
			name:      "code failure compensates configuration",
			failPath:  codePath,
			wantPaths: []string{configurationPath, codePath, configurationPath},
		},
		{
			name:     "concurrency failure compensates code then configuration",
			failPath: concurrencyPath,
			wantPaths: []string{
				configurationPath, codePath, concurrencyPath,
				codePath, configurationPath,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: the named Lambda update phase will fail before completing.
			router := &captureRouter{failPath: tc.failPath}

			// When: CloudFormation updates all three mutable phases.
			_, _, err := (&lambdaFunctionHandler{}).Update(
				context.Background(), router, nil, name, newProps, oldProps, &resolveContext{Region: "us-east-1"},
			)

			// Then: only completed phases are restored, in reverse order.
			if err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatalf("Update error = %v, want original injected failure", err)
			}
			if len(router.requests) != len(tc.wantPaths) {
				t.Fatalf("request paths = %v, want %v", capturedPaths(router.requests), tc.wantPaths)
			}
			for i, want := range tc.wantPaths {
				if router.requests[i].path != want {
					t.Errorf("request %d path = %q, want %q", i, router.requests[i].path, want)
				}
			}
			if got := router.requests[0].body["Description"]; got != "new" {
				t.Errorf("forward Description = %v, want new", got)
			}
			if tc.failPath != configurationPath {
				got := router.requests[len(router.requests)-1].body["Description"]
				if got != "old" {
					t.Errorf("restored Description = %v, want old", got)
				}
			}
		})
	}
}

func capturedPaths(requests []capturedRequest) []string {
	paths := make([]string, 0, len(requests))
	for _, request := range requests {
		paths = append(paths, request.path)
	}
	return paths
}

func TestLambdaProvisionerUpdateCompensationFailurePreservesBothErrors(t *testing.T) {
	const (
		name              = "Stack-Function"
		configurationPath = "/2015-03-31/functions/" + name + "/configuration"
		concurrencyPath   = "/2017-10-31/functions/" + name + "/concurrency"
	)
	router := &captureRouter{
		failAt: map[string]map[int]int{
			concurrencyPath:   {1: http.StatusBadRequest},
			configurationPath: {2: http.StatusInternalServerError},
		},
	}
	h := &lambdaFunctionHandler{}
	oldProps := map[string]any{
		"Runtime": "nodejs22.x", "Handler": "index.handler", "Role": "arn:aws:iam::000000000000:role/r",
		"Description": "old", "Code": map[string]any{"ZipFile": "exports.handler=()=>1"}, "ReservedConcurrentExecutions": 1,
	}
	newProps := map[string]any{
		"Runtime": "nodejs22.x", "Handler": "index.handler", "Role": "arn:aws:iam::000000000000:role/r",
		"Description": "new", "Code": map[string]any{"ZipFile": "exports.handler=()=>2"}, "ReservedConcurrentExecutions": 2,
	}

	_, _, err := h.Update(context.Background(), router, nil, name, newProps, oldProps, &resolveContext{Region: "us-east-1"})

	if err == nil {
		t.Fatal("Update error = nil, want original and compensation failures")
	}
	for _, want := range []string{
		"lambda PutFunctionConcurrency",
		"restore Lambda function after failed update",
		"lambda UpdateFunctionConfiguration",
		"injected compensation failure",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Update error = %q, want %q", err, want)
		}
	}
}

// carryingCode returns the body of the one request that carried a code payload.
func (c *captureRouter) carryingCode(t *testing.T) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, req := range c.requests {
		if _, ok := req.body["ZipFile"]; ok {
			found = append(found, req.body)
			continue
		}
		if code, ok := req.body["Code"].(map[string]any); ok {
			if _, ok := code["ZipFile"]; ok {
				found = append(found, req.body)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one request carrying code, got %d across %d requests",
			len(found), len(c.requests))
	}
	return found[0]
}

// codeZipFrom pulls Code.ZipFile (create) or ZipFile (update) out of a captured
// request and decodes it.
func codeZipFrom(t *testing.T, body map[string]any) []byte {
	t.Helper()
	encoded, ok := body["ZipFile"].(string)
	if !ok {
		code, _ := body["Code"].(map[string]any)
		encoded, ok = code["ZipFile"].(string)
		if !ok {
			t.Fatalf("no ZipFile in request body: %v", body)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("ZipFile is not valid base64: %v", err)
	}
	return decoded
}

func assertReadableArchive(t *testing.T, payload []byte, wantName, wantContent string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("dispatched code is not a readable zip (%v); this is what fails at invoke time "+
			"with \"build code tar: open zip: zip: not a valid zip file\"", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != wantName {
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Fatalf("archive contains %v, want exactly [%s]", names, wantName)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("open %s: %v", wantName, err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != wantContent {
		t.Errorf("archived content = %q, want %q", got, wantContent)
	}
}

// The provisioner must package inline template source before handing it to the
// Lambda API — the two Code.ZipFile fields do not mean the same thing.
func TestLambdaProvisionerPackagesInlineCode(t *testing.T) {
	const source = "import json\n\ndef handler(event, context):\n    return 1\n"

	t.Run("on create", func(t *testing.T) {
		router := &captureRouter{}
		h := &lambdaFunctionHandler{}
		props := map[string]any{
			"Runtime": "python3.12",
			"Handler": "index.handler",
			"Role":    "arn:aws:iam::000000000000:role/r",
			"Code":    map[string]any{"ZipFile": source},
		}

		// An unnamed function is named from its logical ID, as the provisioner
		// sets it before dispatching — two unnamed functions in one stack must
		// not end up with the same name.
		name, _, err := h.Create(context.Background(), router, nil, props, &resolveContext{
			StackName: "Stack",
			LogicalID: "Function",
			Region:    "us-east-1",
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		// Shaped like CloudFormation's own generated physical ID:
		// {StackName}-{LogicalID}-{RANDOM}. The random part is not predictable,
		// so assert the shape rather than an exact string.
		if !generatedNamePattern("Stack", "Function").MatchString(name) {
			t.Errorf("physical id = %q, want Stack-Function-{RANDOM}", name)
		}
		assertReadableArchive(t, codeZipFrom(t, router.carryingCode(t)), "index.py", source)
	})

	t.Run("on update", func(t *testing.T) {
		router := &captureRouter{}
		h := &lambdaFunctionHandler{}
		props := map[string]any{
			"Runtime": "nodejs22.x",
			"Handler": "index.handler",
			"Code":    map[string]any{"ZipFile": source},
		}

		_, _, err := h.Update(context.Background(), router, nil, "Stack-Function", props, nil, &resolveContext{
			StackName: "Stack",
			Region:    "us-east-1",
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertReadableArchive(t, codeZipFrom(t, router.carryingCode(t)), "index.js", source)
	})
}
