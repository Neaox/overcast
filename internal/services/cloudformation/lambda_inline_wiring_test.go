package cloudformation

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// captureRouter records every request the provisioner dispatches and replies
// with a minimal CreateFunction/UpdateFunctionCode response. Update sends the
// code and the configuration as separate requests, so keeping only the last one
// would silently assert against the wrong body.
type captureRouter struct {
	requests []capturedRequest
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"FunctionArn": "arn:aws:lambda:us-east-1:000000000000:function:Stack-Function",
	})
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
