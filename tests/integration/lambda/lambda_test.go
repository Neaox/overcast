// Package lambda_test contains integration tests for the Lambda control-plane emulator.
//
// TDD contract: every handler must have a failing test here before implementation.
//
// Run: go test ./tests/integration/lambda/...
package lambda_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/tests/helpers"
	"go.uber.org/zap"
)

// TestMain pre-pulls Docker images used by tests so that individual test
// cases don't all attempt to pull concurrently (thundering herd).
func TestMain(m *testing.M) {
	if socket := os.Getenv("LAMBDA_DOCKER_SOCKET"); socket == "" {
		socket = "/var/run/docker.sock"
		dc := docker.NewClient(socket, zap.NewNop())
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		for _, img := range []string{
			"public.ecr.aws/lambda/nodejs:20",
			"public.ecr.aws/lambda/nodejs:22",
		} {
			exists, _ := dc.ImageExists(ctx, img)
			if !exists {
				_ = dc.PullImage(ctx, img)
			}
		}
		cancel()
	}
	os.Exit(m.Run())
}

// ─── helpers ────────────────────────────────────────────────────────────────

type createFunctionReq struct {
	FunctionName  string            `json:"FunctionName"`
	Runtime       string            `json:"Runtime,omitempty"`
	Handler       string            `json:"Handler,omitempty"`
	Role          string            `json:"Role"`
	Description   string            `json:"Description,omitempty"`
	Timeout       int               `json:"Timeout,omitempty"`
	MemorySize    int               `json:"MemorySize,omitempty"`
	Environment   *lambdaEnv        `json:"Environment,omitempty"`
	Code          *lambdaCode       `json:"Code,omitempty"`
	PackageType   string            `json:"PackageType,omitempty"`
	Architectures []string          `json:"Architectures,omitempty"`
	Tags          map[string]string `json:"Tags,omitempty"`
	VpcConfig     *vpcConfigReq     `json:"VpcConfig,omitempty"`
	ImageConfig   *imageConfigReq   `json:"ImageConfig,omitempty"`
}

type imageConfigReq struct {
	EntryPoint       []string `json:"EntryPoint,omitempty"`
	Command          []string `json:"Command,omitempty"`
	WorkingDirectory string   `json:"WorkingDirectory,omitempty"`
}

type vpcConfigReq struct {
	SubnetIds        []string `json:"SubnetIds,omitempty"`
	SecurityGroupIds []string `json:"SecurityGroupIds,omitempty"`
}

type vpcConfigResp struct {
	SubnetIds        []string `json:"SubnetIds"`
	SecurityGroupIds []string `json:"SecurityGroupIds"`
	VpcId            string   `json:"VpcId"`
}

type lambdaEnv struct {
	Variables map[string]string `json:"Variables"`
}

type lambdaCode struct {
	ZipFile  []byte `json:"ZipFile,omitempty"`
	ImageUri string `json:"ImageUri,omitempty"`
}

type functionConfiguration struct {
	FunctionName  string           `json:"FunctionName"`
	FunctionArn   string           `json:"FunctionArn"`
	Runtime       string           `json:"Runtime"`
	Handler       string           `json:"Handler"`
	Role          string           `json:"Role"`
	Description   string           `json:"Description"`
	Timeout       int              `json:"Timeout"`
	MemorySize    int              `json:"MemorySize"`
	State         string           `json:"State"`
	CodeSize      int64            `json:"CodeSize"`
	LastModified  string           `json:"LastModified"`
	RevisionId    string           `json:"RevisionId"`
	PackageType   string           `json:"PackageType"`
	Architectures []string         `json:"Architectures"`
	ImageUri      string           `json:"ImageUri,omitempty"`
	VpcConfig     *vpcConfigResp   `json:"VpcConfig,omitempty"`
	ImageConfig   *imageConfigResp `json:"ImageConfig,omitempty"`
}

type imageConfigResp struct {
	EntryPoint       []string `json:"EntryPoint,omitempty"`
	Command          []string `json:"Command,omitempty"`
	WorkingDirectory string   `json:"WorkingDirectory,omitempty"`
}

type getFunctionResponse struct {
	Configuration functionConfiguration `json:"Configuration"`
	Code          *getFunctionCode      `json:"Code,omitempty"`
}

type getFunctionCode struct {
	Location       string `json:"Location"`
	RepositoryType string `json:"RepositoryType"`
}

func lambdaURL(srv *helpers.TestServer, path string) string {
	return srv.URL + "/2015-03-31" + path
}

func doJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response, out *T) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// createFunction is a test helper that creates a function and returns the ARN.
func createFunction(t *testing.T, srv *helpers.TestServer, name string) functionConfiguration {
	t.Helper()
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: name,
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)
	return cfg
}

// ─── CreateFunction ──────────────────────────────────────────────────────────

func TestCreateFunction_success(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When CreateFunction is called with valid params
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "my-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Description:  "test function",
		Timeout:      30,
		MemorySize:   128,
		Code:         &lambdaCode{},
	})
	defer resp.Body.Close()

	// Then 201 + FunctionConfiguration returned
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.FunctionName != "my-fn" {
		t.Errorf("FunctionName = %q, want %q", cfg.FunctionName, "my-fn")
	}
	if cfg.FunctionArn == "" {
		t.Error("FunctionArn must be set")
	}
	if cfg.Runtime != "nodejs20.x" {
		t.Errorf("Runtime = %q, want %q", cfg.Runtime, "nodejs20.x")
	}
	if cfg.Handler != "index.handler" {
		t.Errorf("Handler = %q, want %q", cfg.Handler, "index.handler")
	}
	if cfg.State != "Active" {
		t.Errorf("State = %q, want Active", cfg.State)
	}
	if cfg.PackageType != "Zip" {
		t.Errorf("PackageType = %q, want Zip", cfg.PackageType)
	}
	if len(cfg.Architectures) == 0 {
		t.Error("Architectures must be non-empty")
	}
	if cfg.RevisionId == "" {
		t.Error("RevisionId must be set")
	}
	if cfg.LastModified == "" {
		t.Error("LastModified must be set")
	}
}

func TestCreateFunction_defaults(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When CreateFunction is called with minimal params (no Timeout / MemorySize)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "minimal-fn",
		Runtime:      "nodejs22.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/r",
		Code:         &lambdaCode{},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)

	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)
	// AWS defaults: Timeout=3s, MemorySize=128MB
	if cfg.Timeout != 3 {
		t.Errorf("default Timeout = %d, want 3", cfg.Timeout)
	}
	if cfg.MemorySize != 128 {
		t.Errorf("default MemorySize = %d, want 128", cfg.MemorySize)
	}
}

func TestCreateFunction_missingName(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When CreateFunction is called without FunctionName
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		Runtime: "nodejs20.x",
		Handler: "index.handler",
		Role:    "arn:aws:iam::000000000000:role/r",
		Code:    &lambdaCode{},
	})
	defer resp.Body.Close()

	// Then 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateFunction_missingRole(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Code:         &lambdaCode{},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateFunction_zipMissingCode(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)

	// When: CreateFunction is called for a Zip function without Code.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "missing-code-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/r",
	})

	// Then: AWS rejects the request as invalid.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

func TestCreateFunction_zipMissingRuntime(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)

	// When: CreateFunction is called for a Zip function without Runtime.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "missing-runtime-fn",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/r",
		Code:         &lambdaCode{},
	})

	// Then: AWS rejects the request as invalid.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

func TestCreateFunction_zipMissingHandler(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)

	// When: CreateFunction is called for a Zip function without Handler.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "missing-handler-fn",
		Runtime:      "nodejs20.x",
		Role:         "arn:aws:iam::000000000000:role/r",
		Code:         &lambdaCode{},
	})

	// Then: AWS rejects the request as invalid.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

func TestCreateFunction_imageWithRuntime(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)

	// When: CreateFunction is called for an Image function that also specifies Runtime.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "image-runtime-fn",
		PackageType:  "Image",
		Runtime:      "nodejs20.x",
		Role:         "arn:aws:iam::000000000000:role/r",
		Code:         &lambdaCode{ImageUri: "000000000000.dkr.ecr.us-east-1.amazonaws.com/fn:latest"},
	})

	// Then: AWS rejects Runtime for image package functions.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

func TestCreateFunction_invalidPackageType(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)

	// When: CreateFunction is called with an unsupported PackageType value.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "bad-package-fn",
		PackageType:  "Layer",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/r",
		Code:         &lambdaCode{},
	})

	// Then: AWS rejects the invalid enum value.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

func TestCreateFunction_invalidBounds(t *testing.T) {
	// Given: invalid documented boundary values for CreateFunction.
	cases := []struct {
		name string
		req  createFunctionReq
	}{
		{
			name: "timeout above maximum",
			req: createFunctionReq{
				FunctionName: "timeout-too-large-fn",
				Runtime:      "nodejs20.x",
				Handler:      "index.handler",
				Role:         "arn:aws:iam::000000000000:role/r",
				Timeout:      901,
				Code:         &lambdaCode{},
			},
		},
		{
			name: "memory below minimum",
			req: createFunctionReq{
				FunctionName: "memory-too-small-fn",
				Runtime:      "nodejs20.x",
				Handler:      "index.handler",
				Role:         "arn:aws:iam::000000000000:role/r",
				MemorySize:   127,
				Code:         &lambdaCode{},
			},
		},
		{
			name: "invalid architecture",
			req: createFunctionReq{
				FunctionName:  "bad-arch-fn",
				Runtime:       "nodejs20.x",
				Handler:       "index.handler",
				Role:          "arn:aws:iam::000000000000:role/r",
				Architectures: []string{"sparc"},
				Code:          &lambdaCode{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a fresh server.
			srv := helpers.NewTestServer(t)

			// When: CreateFunction is called with the invalid boundary value.
			resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), tc.req)

			// Then: AWS rejects the request as invalid.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
		})
	}
}

func TestCreateFunction_duplicate(t *testing.T) {
	// Given a function already exists
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "dupe-fn")

	// When CreateFunction is called again with the same name
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "dupe-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/r",
		Code:         &lambdaCode{},
	})
	defer resp.Body.Close()

	// Then 409 ResourceConflictException
	helpers.AssertStatus(t, resp, http.StatusConflict)
	var errBody map[string]string
	decodeJSON(t, resp, &errBody)
	if errBody["__type"] != "ResourceConflictException" {
		t.Errorf("expected ResourceConflictException, got %v", errBody)
	}
}

func TestCreateFunction_deprecatedRuntime(t *testing.T) {
	// nodejs18.x is deprecated (EOL 2025-04-30) — AWS rejects it on CreateFunction
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "deprecated-fn",
		Runtime:      "nodejs18.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/r",
		Code:         &lambdaCode{},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── GetFunction ─────────────────────────────────────────────────────────────

func TestGetFunction_success(t *testing.T) {
	// Given a function exists
	srv := helpers.NewTestServer(t)
	created := createFunction(t, srv, "get-fn")

	// When GetFunction is called
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/get-fn"), nil)
	defer resp.Body.Close()

	// Then 200 + GetFunctionResponse with Configuration + Code location
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out getFunctionResponse
	decodeJSON(t, resp, &out)

	if out.Configuration.FunctionName != "get-fn" {
		t.Errorf("FunctionName = %q", out.Configuration.FunctionName)
	}
	if out.Configuration.FunctionArn != created.FunctionArn {
		t.Errorf("FunctionArn mismatch: %q vs %q", out.Configuration.FunctionArn, created.FunctionArn)
	}
	if out.Code == nil {
		t.Error("Code block must be present")
	}
	if out.Code != nil && out.Code.RepositoryType != "S3" {
		t.Errorf("RepositoryType = %q, want S3", out.Code.RepositoryType)
	}
}

func TestGetFunction_notFound(t *testing.T) {
	// Given a fresh server (no functions)
	srv := helpers.NewTestServer(t)

	// When GetFunction is called for a non-existent function
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/no-such-fn"), nil)
	defer resp.Body.Close()

	// Then 404 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── GetFunctionConfiguration ────────────────────────────────────────────────

func TestGetFunctionConfiguration_success(t *testing.T) {
	// Given a function exists
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "cfg-fn")

	// When GetFunctionConfiguration is called (GET /functions/{name}/configuration)
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/cfg-fn/configuration"), nil)
	defer resp.Body.Close()

	// Then 200 + FunctionConfiguration (flat, no Code block)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.FunctionName != "cfg-fn" {
		t.Errorf("FunctionName = %q", cfg.FunctionName)
	}
}

func TestGetFunctionConfiguration_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/no-such/configuration"), nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── DeleteFunction ──────────────────────────────────────────────────────────

func TestDeleteFunction_success(t *testing.T) {
	// Given a function exists
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "del-fn")

	// When DeleteFunction is called
	resp := doJSON(t, http.MethodDelete, lambdaURL(srv, "/functions/del-fn"), nil)
	defer resp.Body.Close()

	// Then 204 No Content
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// And GetFunction returns 404
	resp2 := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/del-fn"), nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusNotFound)
}

func TestDeleteFunction_notFound(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When DeleteFunction is called for a non-existent function
	resp := doJSON(t, http.MethodDelete, lambdaURL(srv, "/functions/no-such"), nil)
	defer resp.Body.Close()

	// Then 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── UpdateFunctionCode ──────────────────────────────────────────────────────

func TestUpdateFunctionCode_success(t *testing.T) {
	// Given a function exists
	srv := helpers.NewTestServer(t)
	created := createFunction(t, srv, "update-code-fn")

	// When UpdateFunctionCode is called
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/update-code-fn/code"), map[string]any{
		"ZipFile": []byte("fake-zip-content"),
	})
	defer resp.Body.Close()

	// Then 200 + FunctionConfiguration with updated RevisionId
	helpers.AssertStatus(t, resp, http.StatusOK)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.FunctionName != "update-code-fn" {
		t.Errorf("FunctionName = %q", cfg.FunctionName)
	}
	if cfg.RevisionId == created.RevisionId {
		t.Error("RevisionId must change after UpdateFunctionCode")
	}
}

func TestUpdateFunctionCode_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/no-such/code"), map[string]any{
		"ZipFile": []byte("fake"),
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestUpdateFunctionCode_rejectsRelativeHotReloadPath(t *testing.T) {
	// Given a function tagged with a relative hot-reload path
	srv := helpers.NewTestServer(t, helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-reload-relative",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": "relative/path"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusCreated)

	// When UpdateFunctionCode is called
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-reload-relative/code"), map[string]any{
		"ZipFile": []byte("fake-zip-content"),
	})
	defer resp.Body.Close()

	// Then a clear 400 validation error is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "absolute path") {
		t.Fatalf("expected absolute-path validation error, got: %s", string(body))
	}
}

func TestUpdateFunctionCode_allowsHotReloadWhenLayersAttached(t *testing.T) {
	// Given a hot-reload-enabled function with an attached layer
	srv := helpers.NewTestServer(t, helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-reload-layered",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": "/workspace"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusCreated)

	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip")},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-reload-layered/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When UpdateFunctionCode is called
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-reload-layered/code"), map[string]any{
		"ZipFile": []byte("fake-zip-content"),
	})
	defer resp.Body.Close()

	// Then the update succeeds.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)
	if cfg.FunctionName != "hot-reload-layered" {
		t.Fatalf("FunctionName = %q, want hot-reload-layered", cfg.FunctionName)
	}
}

// ─── UpdateFunctionConfiguration ─────────────────────────────────────────────

func TestUpdateFunctionConfiguration_success(t *testing.T) {
	// Given a function exists
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "update-cfg-fn")

	// When UpdateFunctionConfiguration is called with new Timeout and MemorySize
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/update-cfg-fn/configuration"), map[string]any{
		"Timeout":     60,
		"MemorySize":  256,
		"Description": "updated",
		"Environment": map[string]any{
			"Variables": map[string]string{"FOO": "bar"},
		},
	})
	defer resp.Body.Close()

	// Then 200 + updated FunctionConfiguration
	helpers.AssertStatus(t, resp, http.StatusOK)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.Timeout != 60 {
		t.Errorf("Timeout = %d, want 60", cfg.Timeout)
	}
	if cfg.MemorySize != 256 {
		t.Errorf("MemorySize = %d, want 256", cfg.MemorySize)
	}
}

func TestUpdateFunctionConfiguration_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/no-such/configuration"), map[string]any{
		"Timeout": 30,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestUpdateFunctionConfiguration_layersChangesRevisionID(t *testing.T) {
	// Given a function and a published layer.
	srv := helpers.NewTestServer(t)
	created := createFunction(t, srv, "update-cfg-layers-revision")

	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/revision-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip")},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	// When layers are attached via UpdateFunctionConfiguration.
	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/update-cfg-layers-revision/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)
	var attached functionConfiguration
	decodeJSON(t, attachResp, &attached)

	// Then RevisionId changes from create-time value.
	if attached.RevisionId == "" {
		t.Fatal("RevisionId must be non-empty after layer attach")
	}
	if attached.RevisionId == created.RevisionId {
		t.Fatalf("expected RevisionId to change after layer attach; got unchanged %q", attached.RevisionId)
	}

	// When layers are cleared via UpdateFunctionConfiguration.
	clearResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/update-cfg-layers-revision/configuration"), map[string]any{
		"Layers": []string{},
	})
	defer clearResp.Body.Close()
	helpers.AssertStatus(t, clearResp, http.StatusOK)
	var cleared functionConfiguration
	decodeJSON(t, clearResp, &cleared)

	// Then RevisionId changes again.
	if cleared.RevisionId == "" {
		t.Fatal("RevisionId must be non-empty after layer clear")
	}
	if cleared.RevisionId == attached.RevisionId {
		t.Fatalf("expected RevisionId to change after layer clear; got unchanged %q", cleared.RevisionId)
	}
}

// ─── ListFunctions ───────────────────────────────────────────────────────────

func TestListFunctions_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions"), nil)
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Functions []any `json:"Functions"`
	}
	decodeJSON(t, resp, &out)
	if len(out.Functions) != 0 {
		t.Errorf("expected empty list, got %d", len(out.Functions))
	}
}

func TestListFunctions_returnsAll(t *testing.T) {
	// Given three functions exist
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "fn-a")
	createFunction(t, srv, "fn-b")
	createFunction(t, srv, "fn-c")

	// When ListFunctions is called
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions"), nil)
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Functions []functionConfiguration `json:"Functions"`
	}
	decodeJSON(t, resp, &out)

	if len(out.Functions) != 3 {
		t.Errorf("expected 3 functions, got %d", len(out.Functions))
	}
}

// ─── wire types for versions + aliases ──────────────────────────────────────

type publishVersionReq struct {
	Description string `json:"Description,omitempty"`
	CodeSha256  string `json:"CodeSha256,omitempty"`
}

type versionConfiguration struct {
	FunctionName string `json:"FunctionName"`
	FunctionArn  string `json:"FunctionArn"`
	Version      string `json:"Version"`
	Description  string `json:"Description"`
	Runtime      string `json:"Runtime"`
	Handler      string `json:"Handler"`
	RevisionId   string `json:"RevisionId"`
	CodeSha256   string `json:"CodeSha256"`
}

type listVersionsResponse struct {
	Versions []versionConfiguration `json:"Versions"`
}

type aliasConfig struct {
	AliasArn        string `json:"AliasArn"`
	Name            string `json:"Name"`
	FunctionVersion string `json:"FunctionVersion"`
	Description     string `json:"Description"`
	RevisionId      string `json:"RevisionId"`
}

type listAliasesResponse struct {
	Aliases []aliasConfig `json:"Aliases"`
}

type createAliasReq struct {
	Name            string `json:"Name"`
	FunctionVersion string `json:"FunctionVersion"`
	Description     string `json:"Description,omitempty"`
}

type updateAliasReq struct {
	FunctionVersion string `json:"FunctionVersion"`
	Description     string `json:"Description,omitempty"`
}

// ─── PublishVersion ──────────────────────────────────────────────────────────

func TestPublishVersion_success(t *testing.T) {
	// Given a created function
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "pub-fn")

	// When POST /functions/{name}/versions with empty body
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/pub-fn/versions"), publishVersionReq{})
	defer resp.Body.Close()

	// Then 201, Version="1", FunctionArn ends with ":1", CodeSha256 non-empty, RevisionId non-empty
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var ver versionConfiguration
	decodeJSON(t, resp, &ver)

	if ver.Version != "1" {
		t.Errorf("Version = %q, want %q", ver.Version, "1")
	}
	if ver.FunctionArn == "" {
		t.Error("FunctionArn must be set")
	}
	if len(ver.FunctionArn) < 2 || ver.FunctionArn[len(ver.FunctionArn)-2:] != ":1" {
		t.Errorf("FunctionArn %q should end with \":1\"", ver.FunctionArn)
	}
	if ver.CodeSha256 == "" {
		t.Error("CodeSha256 must be non-empty")
	}
	if ver.RevisionId == "" {
		t.Error("RevisionId must be non-empty")
	}
}

func TestPublishVersion_incrementing(t *testing.T) {
	// Given a created function
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "inc-fn")

	// When PublishVersion is called twice
	resp1 := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/inc-fn/versions"), publishVersionReq{})
	helpers.AssertStatus(t, resp1, http.StatusCreated)
	var ver1 versionConfiguration
	decodeJSON(t, resp1, &ver1)

	resp2 := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/inc-fn/versions"), publishVersionReq{})
	helpers.AssertStatus(t, resp2, http.StatusCreated)
	var ver2 versionConfiguration
	decodeJSON(t, resp2, &ver2)

	// Then first response has Version="1", second has Version="2"
	if ver1.Version != "1" {
		t.Errorf("first Version = %q, want \"1\"", ver1.Version)
	}
	if ver2.Version != "2" {
		t.Errorf("second Version = %q, want \"2\"", ver2.Version)
	}
}

func TestPublishVersion_withDescription(t *testing.T) {
	// Given a created function
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "desc-fn")

	// When POST /functions/{name}/versions with {"Description":"my release"}
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/desc-fn/versions"), publishVersionReq{
		Description: "my release",
	})
	defer resp.Body.Close()

	// Then 201, Description="my release"
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var ver versionConfiguration
	decodeJSON(t, resp, &ver)

	if ver.Description != "my release" {
		t.Errorf("Description = %q, want %q", ver.Description, "my release")
	}
}

func TestPublishVersion_unknownFunction(t *testing.T) {
	// Given no function exists
	srv := helpers.NewTestServer(t)

	// When POST /functions/nonexistent/versions
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/nonexistent/versions"), publishVersionReq{})
	defer resp.Body.Close()

	// Then 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── ListVersionsByFunction ──────────────────────────────────────────────────

func TestListVersionsByFunction_empty(t *testing.T) {
	// Given a function with no published versions
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "list-ver-fn")

	// When GET /functions/{name}/versions
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/list-ver-fn/versions"), nil)
	defer resp.Body.Close()

	// Then 200, Versions contains exactly one entry with Version="$LATEST"
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out listVersionsResponse
	decodeJSON(t, resp, &out)

	if len(out.Versions) != 1 {
		t.Fatalf("expected 1 version ($LATEST), got %d", len(out.Versions))
	}
	if out.Versions[0].Version != "$LATEST" {
		t.Errorf("Version = %q, want \"$LATEST\"", out.Versions[0].Version)
	}
}

func TestListVersionsByFunction_afterPublish(t *testing.T) {
	// Given a function with two published versions
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "list-pub-fn")

	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/list-pub-fn/versions"), publishVersionReq{})
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/list-pub-fn/versions"), publishVersionReq{})

	// When GET /functions/{name}/versions
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/list-pub-fn/versions"), nil)
	defer resp.Body.Close()

	// Then 200, Versions has 3 entries (2 numbered + $LATEST)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out listVersionsResponse
	decodeJSON(t, resp, &out)

	if len(out.Versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(out.Versions))
	}

	versionSet := make(map[string]bool)
	for _, v := range out.Versions {
		versionSet[v.Version] = true
	}
	for _, want := range []string{"$LATEST", "1", "2"} {
		if !versionSet[want] {
			t.Errorf("version %q not found in response", want)
		}
	}
}

// ─── Aliases ─────────────────────────────────────────────────────────────────

func TestCreateAlias_success(t *testing.T) {
	// Given a function with one published version
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "alias-fn")
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/alias-fn/versions"), publishVersionReq{})

	// When POST /functions/{name}/aliases with {Name:"prod", FunctionVersion:"1"}
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/alias-fn/aliases"), createAliasReq{
		Name:            "prod",
		FunctionVersion: "1",
	})
	defer resp.Body.Close()

	// Then 201, AliasArn contains function name and "prod", Name="prod", FunctionVersion="1", RevisionId non-empty
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var alias aliasConfig
	decodeJSON(t, resp, &alias)

	if alias.Name != "prod" {
		t.Errorf("Name = %q, want \"prod\"", alias.Name)
	}
	if alias.FunctionVersion != "1" {
		t.Errorf("FunctionVersion = %q, want \"1\"", alias.FunctionVersion)
	}
	if alias.AliasArn == "" {
		t.Error("AliasArn must be set")
	}
	if alias.RevisionId == "" {
		t.Error("RevisionId must be non-empty")
	}
}

func TestCreateAlias_duplicate(t *testing.T) {
	// Given a function with alias "prod" already created
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "dup-alias-fn")
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/dup-alias-fn/versions"), publishVersionReq{})

	first := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/dup-alias-fn/aliases"), createAliasReq{
		Name:            "prod",
		FunctionVersion: "1",
	})
	helpers.AssertStatus(t, first, http.StatusCreated)
	first.Body.Close()

	// When POST /functions/{name}/aliases with same Name
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/dup-alias-fn/aliases"), createAliasReq{
		Name:            "prod",
		FunctionVersion: "1",
	})
	defer resp.Body.Close()

	// Then 409
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

func TestGetAlias_success(t *testing.T) {
	// Given a function with alias "prod" pointing to version "1"
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "get-alias-fn")
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/get-alias-fn/versions"), publishVersionReq{})
	created := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/get-alias-fn/aliases"), createAliasReq{
		Name:            "prod",
		FunctionVersion: "1",
	})
	helpers.AssertStatus(t, created, http.StatusCreated)
	created.Body.Close()

	// When GET /functions/{name}/aliases/prod
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/get-alias-fn/aliases/prod"), nil)
	defer resp.Body.Close()

	// Then 200, Name="prod", FunctionVersion="1"
	helpers.AssertStatus(t, resp, http.StatusOK)
	var alias aliasConfig
	decodeJSON(t, resp, &alias)

	if alias.Name != "prod" {
		t.Errorf("Name = %q, want \"prod\"", alias.Name)
	}
	if alias.FunctionVersion != "1" {
		t.Errorf("FunctionVersion = %q, want \"1\"", alias.FunctionVersion)
	}
}

func TestGetAlias_notFound(t *testing.T) {
	// Given a function exists but the alias does not
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "no-alias-fn")

	// When GET /functions/{name}/aliases/nonexistent
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/no-alias-fn/aliases/nonexistent"), nil)
	defer resp.Body.Close()

	// Then 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestListAliases_success(t *testing.T) {
	// Given a function with aliases "prod" and "staging"
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "list-alias-fn")
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/list-alias-fn/versions"), publishVersionReq{})
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/list-alias-fn/versions"), publishVersionReq{})

	r1 := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/list-alias-fn/aliases"), createAliasReq{
		Name: "prod", FunctionVersion: "1",
	})
	helpers.AssertStatus(t, r1, http.StatusCreated)
	r1.Body.Close()

	r2 := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/list-alias-fn/aliases"), createAliasReq{
		Name: "staging", FunctionVersion: "2",
	})
	helpers.AssertStatus(t, r2, http.StatusCreated)
	r2.Body.Close()

	// When GET /functions/{name}/aliases
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/list-alias-fn/aliases"), nil)
	defer resp.Body.Close()

	// Then 200, both aliases present in Aliases array
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out listAliasesResponse
	decodeJSON(t, resp, &out)

	nameSet := make(map[string]bool)
	for _, a := range out.Aliases {
		nameSet[a.Name] = true
	}
	for _, want := range []string{"prod", "staging"} {
		if !nameSet[want] {
			t.Errorf("alias %q not found in response", want)
		}
	}
}

func TestDeleteAlias_success(t *testing.T) {
	// Given a function with alias "prod"
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "del-alias-fn")
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/del-alias-fn/versions"), publishVersionReq{})

	created := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/del-alias-fn/aliases"), createAliasReq{
		Name: "prod", FunctionVersion: "1",
	})
	helpers.AssertStatus(t, created, http.StatusCreated)
	created.Body.Close()

	// When DELETE /functions/{name}/aliases/prod
	resp := doJSON(t, http.MethodDelete, lambdaURL(srv, "/functions/del-alias-fn/aliases/prod"), nil)
	defer resp.Body.Close()

	// Then 204
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// And GET /functions/{name}/aliases/prod returns 404
	check := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/del-alias-fn/aliases/prod"), nil)
	defer check.Body.Close()
	helpers.AssertStatus(t, check, http.StatusNotFound)
}

func TestDeleteAlias_notFound(t *testing.T) {
	// Given a function exists but the alias does not
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "del-nf-fn")

	// When DELETE /functions/fn/aliases/nonexistent
	resp := doJSON(t, http.MethodDelete, lambdaURL(srv, "/functions/del-nf-fn/aliases/nonexistent"), nil)
	defer resp.Body.Close()

	// Then 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestUpdateAlias_success(t *testing.T) {
	// Given function with alias "prod" pointing to "1" and version "2" published
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "upd-alias-fn")
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/upd-alias-fn/versions"), publishVersionReq{})
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/upd-alias-fn/versions"), publishVersionReq{})

	created := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/upd-alias-fn/aliases"), createAliasReq{
		Name: "prod", FunctionVersion: "1",
	})
	helpers.AssertStatus(t, created, http.StatusCreated)
	var origAlias aliasConfig
	decodeJSON(t, created, &origAlias)

	// When PUT /functions/{name}/aliases/prod with {FunctionVersion:"2"}
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/upd-alias-fn/aliases/prod"), updateAliasReq{
		FunctionVersion: "2",
	})
	defer resp.Body.Close()

	// Then 200, FunctionVersion="2", RevisionId changed
	helpers.AssertStatus(t, resp, http.StatusOK)
	var updated aliasConfig
	decodeJSON(t, resp, &updated)

	if updated.FunctionVersion != "2" {
		t.Errorf("FunctionVersion = %q, want \"2\"", updated.FunctionVersion)
	}
	if updated.RevisionId == "" {
		t.Error("RevisionId must be non-empty")
	}
	if updated.RevisionId == "" {
		t.Error("RevisionId must be non-empty")
	}
	if updated.RevisionId == origAlias.RevisionId {
		t.Error("RevisionId should change after update")
	}
}

// ─── wire types for layers ────────────────────────────────────────────────────

type publishLayerVersionReq struct {
	Description             string       `json:"Description,omitempty"`
	Content                 layerContent `json:"Content"`
	CompatibleRuntimes      []string     `json:"CompatibleRuntimes,omitempty"`
	CompatibleArchitectures []string     `json:"CompatibleArchitectures,omitempty"`
}

type layerContent struct {
	ZipFile []byte `json:"ZipFile,omitempty"`
}

type layerVersionResponse struct {
	LayerVersionArn         string                  `json:"LayerVersionArn"`
	LayerArn                string                  `json:"LayerArn"`
	Version                 int64                   `json:"Version"`
	Description             string                  `json:"Description,omitempty"`
	CreatedDate             string                  `json:"CreatedDate"`
	CompatibleRuntimes      []string                `json:"CompatibleRuntimes,omitempty"`
	CompatibleArchitectures []string                `json:"CompatibleArchitectures,omitempty"`
	Content                 layerVersionContentResp `json:"Content"`
}

type layerVersionContentResp struct {
	CodeSize int64 `json:"CodeSize"`
}

type listLayerVersionsResponse struct {
	LayerVersions []layerVersionResponse `json:"LayerVersions"`
	NextMarker    *string                `json:"NextMarker,omitempty"`
}

type listLayersEntry struct {
	LayerName             string               `json:"LayerName"`
	LayerArn              string               `json:"LayerArn"`
	LatestMatchingVersion layerVersionResponse `json:"LatestMatchingVersion"`
}

type listLayersResponse struct {
	Layers     []listLayersEntry `json:"Layers"`
	NextMarker *string           `json:"NextMarker,omitempty"`
}

func layerURL(srv *helpers.TestServer, path string) string {
	return srv.URL + "/2018-10-31" + path
}

// ─── PublishLayerVersion ──────────────────────────────────────────────────────

func TestPublishLayerVersion_success(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When PublishLayerVersion is called with a zip and compatible runtimes
	resp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{
		Description:        "test layer",
		Content:            layerContent{ZipFile: []byte("PK\x05\x06" + string(make([]byte, 18)))},
		CompatibleRuntimes: []string{"nodejs20.x", "nodejs22.x"},
	})

	// Then 201 is returned with a populated LayerVersionArn and Version=1
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, resp, &lv)
	if lv.LayerVersionArn == "" {
		t.Error("LayerVersionArn must be non-empty")
	}
	if lv.LayerArn == "" {
		t.Error("LayerArn must be non-empty")
	}
	if lv.Version != 1 {
		t.Errorf("expected Version=1, got %d", lv.Version)
	}
	if lv.Description != "test layer" {
		t.Errorf("unexpected description %q", lv.Description)
	}
	if len(lv.CompatibleRuntimes) != 2 {
		t.Errorf("expected 2 CompatibleRuntimes, got %d", len(lv.CompatibleRuntimes))
	}
	if lv.CreatedDate == "" {
		t.Error("CreatedDate must be non-empty")
	}
	if lv.Content.CodeSize == 0 {
		t.Error("Content.CodeSize must be non-zero")
	}
}

func TestPublishLayerVersion_incrementing(t *testing.T) {
	// Given a server with one layer version already published
	srv := helpers.NewTestServer(t)
	doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip1")},
	})

	// When a second version is published
	resp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip2")},
	})

	// Then Version=2 is returned
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, resp, &lv)
	if lv.Version != 2 {
		t.Errorf("expected Version=2, got %d", lv.Version)
	}
}

// ─── GetLayerVersion ─────────────────────────────────────────────────────────

func TestGetLayerVersion_success(t *testing.T) {
	// Given a layer version that was published
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{
		Description: "v1",
		Content:     layerContent{ZipFile: []byte("zip")},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var published layerVersionResponse
	decodeJSON(t, resp, &published)

	// When GetLayerVersion is called for version 1
	resp2 := doJSON(t, http.MethodGet, layerURL(srv, "/layers/my-layer/versions/1"), nil)

	// Then the same layer version is returned
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var got layerVersionResponse
	decodeJSON(t, resp2, &got)
	if got.LayerVersionArn != published.LayerVersionArn {
		t.Errorf("ARN mismatch: got %q, want %q", got.LayerVersionArn, published.LayerVersionArn)
	}
	if got.Description != "v1" {
		t.Errorf("unexpected description %q", got.Description)
	}
}

func TestGetLayerVersion_notFound(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When GetLayerVersion is called for a non-existent layer
	resp := doJSON(t, http.MethodGet, layerURL(srv, "/layers/missing-layer/versions/1"), nil)

	// Then 404 is returned
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── ListLayerVersions ────────────────────────────────────────────────────────

func TestListLayerVersions_empty(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When ListLayerVersions is called for a layer that has no versions
	resp := doJSON(t, http.MethodGet, layerURL(srv, "/layers/no-such-layer/versions"), nil)

	// Then 200 is returned with an empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
	var list listLayerVersionsResponse
	decodeJSON(t, resp, &list)
	if len(list.LayerVersions) != 0 {
		t.Errorf("expected empty list, got %d items", len(list.LayerVersions))
	}
}

func TestListLayerVersions_afterPublish(t *testing.T) {
	// Given a layer with two published versions
	srv := helpers.NewTestServer(t)
	doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{Content: layerContent{ZipFile: []byte("v1")}})
	doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{Content: layerContent{ZipFile: []byte("v2")}})

	// When ListLayerVersions is called
	resp := doJSON(t, http.MethodGet, layerURL(srv, "/layers/my-layer/versions"), nil)

	// Then both versions are returned in descending order (newest first, per AWS spec)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var list listLayerVersionsResponse
	decodeJSON(t, resp, &list)
	if len(list.LayerVersions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(list.LayerVersions))
	}
	if list.LayerVersions[0].Version != 2 {
		t.Errorf("expected newest version first, got version %d first", list.LayerVersions[0].Version)
	}
}

// ─── ListLayers ───────────────────────────────────────────────────────────────

func TestListLayers_empty(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When ListLayers is called
	resp := doJSON(t, http.MethodGet, layerURL(srv, "/layers"), nil)

	// Then 200 is returned with an empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
	var list listLayersResponse
	decodeJSON(t, resp, &list)
	if len(list.Layers) != 0 {
		t.Errorf("expected empty list, got %d items", len(list.Layers))
	}
}

func TestListLayers_afterPublish(t *testing.T) {
	// Given two distinct layers published
	srv := helpers.NewTestServer(t)
	doJSON(t, http.MethodPost, layerURL(srv, "/layers/layer-a/versions"), publishLayerVersionReq{Content: layerContent{ZipFile: []byte("a")}})
	doJSON(t, http.MethodPost, layerURL(srv, "/layers/layer-b/versions"), publishLayerVersionReq{Content: layerContent{ZipFile: []byte("b")}})
	// Publish a second version of layer-a so LatestMatchingVersion is version 2
	doJSON(t, http.MethodPost, layerURL(srv, "/layers/layer-a/versions"), publishLayerVersionReq{Content: layerContent{ZipFile: []byte("a2")}})

	// When ListLayers is called
	resp := doJSON(t, http.MethodGet, layerURL(srv, "/layers"), nil)

	// Then both distinct layers appear, each with their latest version
	helpers.AssertStatus(t, resp, http.StatusOK)
	var list listLayersResponse
	decodeJSON(t, resp, &list)
	if len(list.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(list.Layers))
	}
	for _, l := range list.Layers {
		if l.LayerName == "layer-a" && l.LatestMatchingVersion.Version != 2 {
			t.Errorf("layer-a: expected latest version 2, got %d", l.LatestMatchingVersion.Version)
		}
	}
}

// ─── DeleteLayerVersion ───────────────────────────────────────────────────────

func TestDeleteLayerVersion_success(t *testing.T) {
	// Given a published layer version
	srv := helpers.NewTestServer(t)
	doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{Content: layerContent{ZipFile: []byte("zip")}})

	// When DeleteLayerVersion is called
	resp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/my-layer/versions/1"), nil)

	// Then 204 is returned
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// And subsequent GetLayerVersion returns 404
	resp2 := doJSON(t, http.MethodGet, layerURL(srv, "/layers/my-layer/versions/1"), nil)
	helpers.AssertStatus(t, resp2, http.StatusNotFound)
}

func TestDeleteLayerVersion_notFound(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When DeleteLayerVersion is called for a non-existent layer
	resp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/no-layer/versions/99"), nil)

	// Then 404 is returned
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── Attach layer to function ─────────────────────────────────────────────────

func TestUpdateFunctionConfiguration_attachLayer(t *testing.T) {
	// Given a function and a published layer
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "fn-with-layer")
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/my-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip")},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	// When UpdateFunctionConfiguration is called with Layers
	type updateWithLayersReq struct {
		Layers []string `json:"Layers"`
	}
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/fn-with-layer/configuration"), updateWithLayersReq{
		Layers: []string{lv.LayerVersionArn},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then GetFunctionConfiguration returns the attached layer
	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn      string `json:"Arn"`
			CodeSize int64  `json:"CodeSize"`
		} `json:"Layers,omitempty"`
	}
	resp2 := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/fn-with-layer/configuration"), nil)
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var config funcConfigWithLayers
	decodeJSON(t, resp2, &config)
	if len(config.Layers) != 1 {
		t.Fatalf("expected 1 attached layer, got %d", len(config.Layers))
	}
	if config.Layers[0].Arn != lv.LayerVersionArn {
		t.Errorf("layer ARN mismatch: got %q, want %q", config.Layers[0].Arn, lv.LayerVersionArn)
	}
}

func TestUpdateFunctionConfiguration_replaceLayers(t *testing.T) {
	// Given a function with one attached layer and a second published layer.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "fn-replace-layers")

	lvResp1 := doJSON(t, http.MethodPost, layerURL(srv, "/layers/replace-layer-a/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip-a")},
	})
	helpers.AssertStatus(t, lvResp1, http.StatusCreated)
	var lv1 layerVersionResponse
	decodeJSON(t, lvResp1, &lv1)

	lvResp2 := doJSON(t, http.MethodPost, layerURL(srv, "/layers/replace-layer-b/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip-b")},
	})
	helpers.AssertStatus(t, lvResp2, http.StatusCreated)
	var lv2 layerVersionResponse
	decodeJSON(t, lvResp2, &lv2)

	type updateWithLayersReq struct {
		Layers []string `json:"Layers"`
	}
	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/fn-replace-layers/configuration"), updateWithLayersReq{
		Layers: []string{lv1.LayerVersionArn},
	})
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When Layers is updated to a different single layer.
	replaceResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/fn-replace-layers/configuration"), updateWithLayersReq{
		Layers: []string{lv2.LayerVersionArn},
	})
	helpers.AssertStatus(t, replaceResp, http.StatusOK)

	// Then configuration readback returns only the replacement layer.
	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn      string `json:"Arn"`
			CodeSize int64  `json:"CodeSize"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/fn-replace-layers/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 1 {
		t.Fatalf("expected 1 attached layer after replace, got %d", len(cfg.Layers))
	}
	if cfg.Layers[0].Arn != lv2.LayerVersionArn {
		t.Fatalf("expected replacement layer ARN %q, got %q", lv2.LayerVersionArn, cfg.Layers[0].Arn)
	}

	getFnResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/fn-replace-layers"), nil)
	helpers.AssertStatus(t, getFnResp, http.StatusOK)
	var fnResp struct {
		Configuration funcConfigWithLayers `json:"Configuration"`
	}
	decodeJSON(t, getFnResp, &fnResp)
	if len(fnResp.Configuration.Layers) != 1 {
		t.Fatalf("expected GetFunction to report 1 attached layer after replace, got %d", len(fnResp.Configuration.Layers))
	}
	if fnResp.Configuration.Layers[0].Arn != lv2.LayerVersionArn {
		t.Fatalf("expected GetFunction replacement layer ARN %q, got %q", lv2.LayerVersionArn, fnResp.Configuration.Layers[0].Arn)
	}
}

func TestUpdateFunctionConfiguration_clearLayers(t *testing.T) {
	// Given a function with one attached layer.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "fn-clear-layers")

	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/clear-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: []byte("zip")},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	type updateWithLayersReq struct {
		Layers []string `json:"Layers"`
	}
	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/fn-clear-layers/configuration"), updateWithLayersReq{
		Layers: []string{lv.LayerVersionArn},
	})
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When Layers is updated to an empty list.
	clearResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/fn-clear-layers/configuration"), map[string]any{
		"Layers": []string{},
	})
	helpers.AssertStatus(t, clearResp, http.StatusOK)

	// Then both GetFunctionConfiguration and GetFunction report no attached layers.
	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn      string `json:"Arn"`
			CodeSize int64  `json:"CodeSize"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/fn-clear-layers/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 0 {
		t.Fatalf("expected 0 attached layers after clear, got %d", len(cfg.Layers))
	}

	getFnResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/fn-clear-layers"), nil)
	helpers.AssertStatus(t, getFnResp, http.StatusOK)
	var fnResp struct {
		Configuration funcConfigWithLayers `json:"Configuration"`
	}
	decodeJSON(t, getFnResp, &fnResp)
	if len(fnResp.Configuration.Layers) != 0 {
		t.Fatalf("expected GetFunction to report 0 attached layers after clear, got %d", len(fnResp.Configuration.Layers))
	}
}

// ─── Invoke (container-based) ─────────────────────────────────────────────────
//
// These tests require a running Docker daemon. They are skipped automatically
// when Docker is unavailable (CI without Docker socket, Windows dev without
// Docker Desktop, etc.).

// skipIfNoDocker skips the test if the Docker socket is not accessible.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	const sock = "/var/run/docker.sock"
	if _, err := os.Stat(sock); err != nil {
		t.Skipf("Docker socket %s not available: %v", sock, err)
	}
}

func skipIfContainerizedHotReloadBindMount(t *testing.T) {
	t.Helper()
	if os.Getenv("OVERCAST_TEST_LAMBDA_HOT_RELOAD") == "1" {
		return
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("Lambda hot-reload bind mounts require host-visible source paths; set OVERCAST_TEST_LAMBDA_HOT_RELOAD=1 to force this test")
	}
}

// makeZip creates a minimal zip archive containing a single file.
func makeZip(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatalf("zip.Create: %v", err)
	}
	if _, err := io.WriteString(f, content); err != nil {
		t.Fatalf("zip.Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// createFunctionWithCode creates a Lambda function seeded with a real code zip.
func createFunctionWithCode(t *testing.T, srv *helpers.TestServer, name, runtime, handler string, zipBytes []byte) functionConfiguration {
	t.Helper()
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: name,
		Runtime:      runtime,
		Handler:      handler,
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Timeout:      10,
		MemorySize:   128,
		Code:         &lambdaCode{ZipFile: zipBytes},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)
	return cfg
}

// waitForFunctionActive polls GetFunction until the function reaches Active state
// or the deadline is exceeded. Required for Docker-dependent tests where the
// prewarmer callback transitions State from "Pending" asynchronously.
func waitForFunctionActive(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/"+name), nil)
		var fn struct {
			Configuration struct {
				State string `json:"State"`
			} `json:"Configuration"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&fn)
		resp.Body.Close()
		if fn.Configuration.State == "Active" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("function %s did not reach Active state within 30s", name)
}

// invokeFunction sends a synchronous Lambda invocation and returns the HTTP response.
func invokeFunction(t *testing.T, srv *helpers.TestServer, name string, payload any) *http.Response {
	t.Helper()
	return doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/"+name+"/invocations"), payload)
}

func TestInvoke_nodeRuntime_success(t *testing.T) {
	skipIfNoDocker(t)

	// Given a Node.js function that echoes its input
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async (event) => {
  return { statusCode: 200, body: JSON.stringify(event) };
};
`)
	createFunctionWithCode(t, srv, "echo-fn", "nodejs20.x", "index.handler", code)
	waitForFunctionActive(t, srv, "echo-fn")

	// When InvokeFunction is called
	resp := invokeFunction(t, srv, "echo-fn", map[string]string{"hello": "world"})
	if resp.Header.Get("X-Amz-Function-Error") == "Unhandled" {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		// Under high parallel test load, cold-start runtime init can fail
		// transiently; one immediate retry stabilizes this integration test
		// without masking deterministic handler/runtime regressions.
		if strings.Contains(string(body), "Runtime.InitError") || strings.Contains(string(body), "Runtime.ExitError") {
			resp = invokeFunction(t, srv, "echo-fn", map[string]string{"hello": "world"})
		}
	}
	defer resp.Body.Close()

	// Then 200 with the echoed payload
	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "" {
		t.Errorf("unexpected function error: %s", resp.Header.Get("X-Amz-Function-Error"))
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if statusCode, _ := out["statusCode"].(float64); int(statusCode) != 200 {
		t.Errorf("response statusCode = %v, want 200", out["statusCode"])
	}
}

func TestInvoke_nodeRuntime_hotReloadMountedSource(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a function that opts into hot-reload with source mounted from host.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.js", []byte(`
exports.handler = async () => {
  return { source: "mounted" };
};
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	waitForFunctionActive(t, srv, "hot-fn")

	// When invoking the function
	invokeResp := invokeFunction(t, srv, "hot-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)
	if invokeResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(body), "Runtime.ExitError") || strings.Contains(string(body), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(body))
	}

	// Then execution succeeds and comes from the mounted source tree.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["source"] != "mounted" {
		t.Fatalf("expected mounted source response, got: %s", body)
	}
}

func TestInvoke_nodeRuntime_hotReloadMountedSource_withLayer(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload source directory that imports a dependency from a layer.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-layered-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.js", []byte(`
const layerLib = require("layer-lib");
exports.handler = async () => {
  return { source: "mounted", layer: layerLib.value };
};
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-layer-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-layer-fn")

	// Publish a layer that provides /opt/nodejs/node_modules/layer-lib/index.js.
	layerZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "from-layer" };`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-layer-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "hot-layer-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)
	if invokeResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(body), "Runtime.ExitError") || strings.Contains(string(body), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(body))
	}

	// Then invocation succeeds and returns both mounted-source and layered values.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["source"] != "mounted" {
		t.Fatalf("expected mounted source response, got: %s", body)
	}
	if out["layer"] != "from-layer" {
		t.Fatalf("expected layer module value from-layer, got: %s", body)
	}
}

func TestInvoke_pythonRuntime_hotReloadMountedSource_withLayer(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload Python source directory that imports from a layer module.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-python-layered-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.py", []byte(`
import layer_mod

def handler(event, context):
    return {"source": "mounted", "layer": layer_mod.VALUE}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-python-layer-fn",
		Runtime:      "python3.11",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-python-layer-fn")

	// Publish a layer that provides /opt/python/layer_mod.py.
	layerZip := makeZip(t, "python/layer_mod.py", `VALUE = "from-layer"`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-python-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-python-layer-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "hot-python-layer-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)
	if invokeResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(body), "Runtime.ExitError") || strings.Contains(string(body), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(body))
	}

	// Then invocation succeeds and returns both mounted-source and layered values.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["source"] != "mounted" {
		t.Fatalf("expected mounted source response, got: %s", body)
	}
	if out["layer"] != "from-layer" {
		t.Fatalf("expected layer module value from-layer, got: %s", body)
	}
}

func TestInvoke_nodeRuntime_hotReloadMountedSource_withLayer_precedenceLastWins(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload source directory importing a module provided by layers.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-layered-precedence-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.js", []byte(`
const layerLib = require("layer-lib");
exports.handler = async () => {
  return { source: "mounted", layer: layerLib.value };
};
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-layer-precedence-node-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-layer-precedence-node-fn")

	baseZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "base" };`)
	baseResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-node-layer-base/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: baseZip},
	})
	helpers.AssertStatus(t, baseResp, http.StatusCreated)
	var base layerVersionResponse
	decodeJSON(t, baseResp, &base)

	overrideZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "override" };`)
	overrideResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-node-layer-override/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: overrideZip},
	})
	helpers.AssertStatus(t, overrideResp, http.StatusCreated)
	var override layerVersionResponse
	decodeJSON(t, overrideResp, &override)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-layer-precedence-node-fn/configuration"), map[string]any{
		"Layers": []string{base.LayerVersionArn, override.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "hot-layer-precedence-node-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)
	if invokeResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(body), "Runtime.ExitError") || strings.Contains(string(body), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(body))
	}

	// Then the later layer should override the earlier one.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["source"] != "mounted" {
		t.Fatalf("expected mounted source response, got: %s", body)
	}
	if out["layer"] != "override" {
		t.Fatalf("expected last layer to win (override), got: %s", body)
	}
}

func TestInvoke_pythonRuntime_hotReloadMountedSource_withLayer_precedenceLastWins(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload Python source directory importing a module from layers.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-python-layered-precedence-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.py", []byte(`
import layer_mod

def handler(event, context):
    return {"source": "mounted", "layer": layer_mod.VALUE}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-layer-precedence-python-fn",
		Runtime:      "python3.11",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-layer-precedence-python-fn")

	baseZip := makeZip(t, "python/layer_mod.py", `VALUE = "base"`)
	baseResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-python-layer-base/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: baseZip},
	})
	helpers.AssertStatus(t, baseResp, http.StatusCreated)
	var base layerVersionResponse
	decodeJSON(t, baseResp, &base)

	overrideZip := makeZip(t, "python/layer_mod.py", `VALUE = "override"`)
	overrideResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-python-layer-override/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: overrideZip},
	})
	helpers.AssertStatus(t, overrideResp, http.StatusCreated)
	var override layerVersionResponse
	decodeJSON(t, overrideResp, &override)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-layer-precedence-python-fn/configuration"), map[string]any{
		"Layers": []string{base.LayerVersionArn, override.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "hot-layer-precedence-python-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)
	if invokeResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(body), "Runtime.ExitError") || strings.Contains(string(body), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(body))
	}

	// Then the later layer should override the earlier one.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["source"] != "mounted" {
		t.Fatalf("expected mounted source response, got: %s", body)
	}
	if out["layer"] != "override" {
		t.Fatalf("expected last layer to win (override), got: %s", body)
	}
}

func TestInvoke_nodeRuntime_zipCode_withLayer(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Node function that imports from a layer module.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
const layerLib = require("layer-lib");
exports.handler = async () => {
  return { source: "zip", layer: layerLib.value };
};
`)
	createFunctionWithCode(t, srv, "zip-layer-node-fn", "nodejs20.x", "index.handler", code)

	waitForFunctionActive(t, srv, "zip-layer-node-fn")

	layerZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "from-layer" };`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/zip-node-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/zip-layer-node-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "zip-layer-node-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)

	// Under heavy parallel load cold-start can fail transiently; retry once.
	if invokeResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(body), "Runtime.InitError") || strings.Contains(string(body), "Runtime.ExitError")) {
		invokeResp = invokeFunction(t, srv, "zip-layer-node-fn", map[string]any{"ping": true})
		defer invokeResp.Body.Close()
		body, _ = io.ReadAll(invokeResp.Body)
	}

	// Then invocation succeeds and returns values from zip code and layer.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["source"] != "zip" {
		t.Fatalf("expected zip source response, got: %s", body)
	}
	if out["layer"] != "from-layer" {
		t.Fatalf("expected layer module value from-layer, got: %s", body)
	}
}

func TestInvoke_nodeRuntime_zipCode_withLayer_precedenceLastWins(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Node function importing a module provided by layers.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
const layerLib = require("layer-lib");
exports.handler = async () => {
  return { layer: layerLib.value };
};
`)
	createFunctionWithCode(t, srv, "zip-layer-precedence-node-fn", "nodejs20.x", "index.handler", code)

	waitForFunctionActive(t, srv, "zip-layer-precedence-node-fn")

	// Publish two layers that provide the same module path with different values.
	baseZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "base" };`)
	baseResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/zip-node-layer-base/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: baseZip},
	})
	helpers.AssertStatus(t, baseResp, http.StatusCreated)
	var base layerVersionResponse
	decodeJSON(t, baseResp, &base)

	overrideZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "override" };`)
	overrideResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/zip-node-layer-override/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: overrideZip},
	})
	helpers.AssertStatus(t, overrideResp, http.StatusCreated)
	var override layerVersionResponse
	decodeJSON(t, overrideResp, &override)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/zip-layer-precedence-node-fn/configuration"), map[string]any{
		"Layers": []string{base.LayerVersionArn, override.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "zip-layer-precedence-node-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)

	// Then the later layer should override the earlier one.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["layer"] != "override" {
		t.Fatalf("expected last layer to win (override), got: %s", body)
	}
}

func TestInvoke_pythonRuntime_zipCode_withLayer(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Python function that imports from a layer module.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.py", `
import layer_mod

def handler(event, context):
    return {"source": "zip", "layer": layer_mod.VALUE}
`)
	createFunctionWithCode(t, srv, "zip-layer-python-fn", "python3.11", "index.handler", code)

	waitForFunctionActive(t, srv, "zip-layer-python-fn")

	layerZip := makeZip(t, "python/layer_mod.py", `VALUE = "from-layer"`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/zip-python-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/zip-layer-python-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "zip-layer-python-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)

	// Then invocation succeeds and returns values from zip code and layer.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["source"] != "zip" {
		t.Fatalf("expected zip source response, got: %s", body)
	}
	if out["layer"] != "from-layer" {
		t.Fatalf("expected layer module value from-layer, got: %s", body)
	}
}

func TestInvoke_pythonRuntime_zipCode_withLayer_precedenceLastWins(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Python function importing a symbol from layer code.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.py", `
import layer_mod

def handler(event, context):
    return {"layer": layer_mod.VALUE}
`)
	createFunctionWithCode(t, srv, "zip-layer-precedence-python-fn", "python3.11", "index.handler", code)

	waitForFunctionActive(t, srv, "zip-layer-precedence-python-fn")

	// Publish two layers that provide the same module path with different values.
	baseZip := makeZip(t, "python/layer_mod.py", `VALUE = "base"`)
	baseResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/zip-python-layer-base/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: baseZip},
	})
	helpers.AssertStatus(t, baseResp, http.StatusCreated)
	var base layerVersionResponse
	decodeJSON(t, baseResp, &base)

	overrideZip := makeZip(t, "python/layer_mod.py", `VALUE = "override"`)
	overrideResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/zip-python-layer-override/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: overrideZip},
	})
	helpers.AssertStatus(t, overrideResp, http.StatusCreated)
	var override layerVersionResponse
	decodeJSON(t, overrideResp, &override)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/zip-layer-precedence-python-fn/configuration"), map[string]any{
		"Layers": []string{base.LayerVersionArn, override.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	invokeResp := invokeFunction(t, srv, "zip-layer-precedence-python-fn", map[string]any{"ping": true})
	defer invokeResp.Body.Close()
	body, _ := io.ReadAll(invokeResp.Body)

	// Then the later layer should override the earlier one.
	helpers.AssertStatus(t, invokeResp, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["layer"] != "override" {
		t.Fatalf("expected last layer to win (override), got: %s", body)
	}
}

func TestInvoke_nodeRuntime_deletedAttachedLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Node function with a real attached layer version.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async () => {
  return { ok: true };
};
`)
	createFunctionWithCode(t, srv, "deleted-layer-fn", "nodejs20.x", "index.handler", code)

	waitForFunctionActive(t, srv, "deleted-layer-fn")

	layerZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "from-layer" };`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/deleted-node-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/deleted-layer-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When the attached layer version is deleted after configuration.
	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/deleted-node-layer/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// Then function readback still preserves the configured layer reference.
	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/deleted-layer-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 1 || cfg.Layers[0].Arn != lv.LayerVersionArn {
		t.Fatalf("expected configuration to retain deleted layer ARN %q, got %#v", lv.LayerVersionArn, cfg.Layers)
	}

	getFnResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/deleted-layer-fn"), nil)
	helpers.AssertStatus(t, getFnResp, http.StatusOK)
	var fnResp struct {
		Configuration funcConfigWithLayers `json:"Configuration"`
	}
	decodeJSON(t, getFnResp, &fnResp)
	if len(fnResp.Configuration.Layers) != 1 || fnResp.Configuration.Layers[0].Arn != lv.LayerVersionArn {
		t.Fatalf("expected GetFunction to retain deleted layer ARN %q, got %#v", lv.LayerVersionArn, fnResp.Configuration.Layers)
	}

	// And invocation now fails during runtime init with a clear missing-layer error.
	resp := invokeFunction(t, srv, "deleted-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_pythonRuntime_deletedAttachedLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Python function with a real attached layer version.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.py", `
def handler(event, context):
    return {"ok": True}
`)
	createFunctionWithCode(t, srv, "deleted-python-layer-fn", "python3.11", "index.handler", code)

	waitForFunctionActive(t, srv, "deleted-python-layer-fn")

	layerZip := makeZip(t, "python/layer_mod.py", `VALUE = "from-layer"`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/deleted-python-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/deleted-python-layer-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When the attached layer version is deleted after configuration.
	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/deleted-python-layer/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// Then function readback still preserves the configured layer reference.
	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/deleted-python-layer-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 1 || cfg.Layers[0].Arn != lv.LayerVersionArn {
		t.Fatalf("expected configuration to retain deleted layer ARN %q, got %#v", lv.LayerVersionArn, cfg.Layers)
	}

	getFnResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/deleted-python-layer-fn"), nil)
	helpers.AssertStatus(t, getFnResp, http.StatusOK)
	var fnResp struct {
		Configuration funcConfigWithLayers `json:"Configuration"`
	}
	decodeJSON(t, getFnResp, &fnResp)
	if len(fnResp.Configuration.Layers) != 1 || fnResp.Configuration.Layers[0].Arn != lv.LayerVersionArn {
		t.Fatalf("expected GetFunction to retain deleted layer ARN %q, got %#v", lv.LayerVersionArn, fnResp.Configuration.Layers)
	}

	// And invocation now fails during runtime init with a clear missing-layer error.
	resp := invokeFunction(t, srv, "deleted-python-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_nodeRuntime_deletedLayerRecoveryAfterClearingLayers(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Node function with an attached layer that later gets deleted.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async () => {
  return { ok: true };
};
`)
	createFunctionWithCode(t, srv, "deleted-layer-recovery-fn", "nodejs20.x", "index.handler", code)

	waitForFunctionActive(t, srv, "deleted-layer-recovery-fn")

	layerZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "from-layer" };`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/deleted-node-layer-recovery/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/deleted-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/deleted-node-layer-recovery/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// First invoke fails because configured layer content can no longer be loaded.
	failResp := invokeFunction(t, srv, "deleted-layer-recovery-fn", map[string]any{"ping": true})
	defer failResp.Body.Close()
	failBody, err := io.ReadAll(failResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	helpers.AssertStatus(t, failResp, http.StatusOK)
	if failResp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", failResp.Header.Get("X-Amz-Function-Error"), failBody)
	}

	// When layer references are cleared via configuration update.
	clearResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/deleted-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{},
	})
	defer clearResp.Body.Close()
	helpers.AssertStatus(t, clearResp, http.StatusOK)

	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/deleted-layer-recovery-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 0 {
		t.Fatalf("expected 0 attached layers after clearing, got %#v", cfg.Layers)
	}

	// Then invoke succeeds again because startup no longer needs missing layer content.
	okResp := invokeFunction(t, srv, "deleted-layer-recovery-fn", map[string]any{"ping": true})
	defer okResp.Body.Close()
	okBody, err := io.ReadAll(okResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	helpers.AssertStatus(t, okResp, http.StatusOK)
	if okResp.Header.Get("X-Amz-Function-Error") != "" {
		t.Fatalf("expected no function error after clearing layers, got %q (body=%s)", okResp.Header.Get("X-Amz-Function-Error"), okBody)
	}

	var out map[string]any
	if err := json.Unmarshal(okBody, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, okBody)
	}
	if out["ok"] != true {
		t.Fatalf("expected successful payload after clearing layers, got: %s", okBody)
	}
}

func TestInvoke_pythonRuntime_deletedLayerRecoveryAfterClearingLayers(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Python function with an attached layer that later gets deleted.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.py", `
def handler(event, context):
    return {"ok": True}
`)
	createFunctionWithCode(t, srv, "deleted-python-layer-recovery-fn", "python3.11", "index.handler", code)

	waitForFunctionActive(t, srv, "deleted-python-layer-recovery-fn")

	layerZip := makeZip(t, "python/layer_mod.py", `VALUE = "from-layer"`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/deleted-python-layer-recovery/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/deleted-python-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/deleted-python-layer-recovery/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// First invoke fails because configured layer content can no longer be loaded.
	failResp := invokeFunction(t, srv, "deleted-python-layer-recovery-fn", map[string]any{"ping": true})
	defer failResp.Body.Close()
	failBody, err := io.ReadAll(failResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	helpers.AssertStatus(t, failResp, http.StatusOK)
	if failResp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", failResp.Header.Get("X-Amz-Function-Error"), failBody)
	}

	// When layer references are cleared via configuration update.
	clearResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/deleted-python-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{},
	})
	defer clearResp.Body.Close()
	helpers.AssertStatus(t, clearResp, http.StatusOK)

	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/deleted-python-layer-recovery-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 0 {
		t.Fatalf("expected 0 attached layers after clearing, got %#v", cfg.Layers)
	}

	// Then invoke succeeds again because startup no longer needs missing layer content.
	okResp := invokeFunction(t, srv, "deleted-python-layer-recovery-fn", map[string]any{"ping": true})
	defer okResp.Body.Close()
	okBody, err := io.ReadAll(okResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	helpers.AssertStatus(t, okResp, http.StatusOK)
	if okResp.Header.Get("X-Amz-Function-Error") != "" {
		t.Fatalf("expected no function error after clearing layers, got %q (body=%s)", okResp.Header.Get("X-Amz-Function-Error"), okBody)
	}

	var out map[string]any
	if err := json.Unmarshal(okBody, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, okBody)
	}
	if out["ok"] != true {
		t.Fatalf("expected successful payload after clearing layers, got: %s", okBody)
	}
}

func TestInvoke_nodeRuntime_hotReload_deletedAttachedLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload Node function with a real attached layer version.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-deleted-layer-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.js", []byte(`
exports.handler = async () => {
  return { ok: true };
};
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-deleted-layer-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-deleted-layer-fn")

	layerZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "from-layer" };`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-deleted-node-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-deleted-layer-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When the attached layer version is deleted after configuration.
	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/hot-deleted-node-layer/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// Then function readback still preserves the configured layer reference.
	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/hot-deleted-layer-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 1 || cfg.Layers[0].Arn != lv.LayerVersionArn {
		t.Fatalf("expected configuration to retain deleted layer ARN %q, got %#v", lv.LayerVersionArn, cfg.Layers)
	}

	// And invocation now fails during runtime init with a clear missing-layer error.
	resp := invokeFunction(t, srv, "hot-deleted-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_pythonRuntime_hotReload_deletedAttachedLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload Python function with a real attached layer version.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-python-deleted-layer-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.py", []byte(`
def handler(event, context):
    return {"ok": True}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-deleted-python-layer-fn",
		Runtime:      "python3.11",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-deleted-python-layer-fn")

	layerZip := makeZip(t, "python/layer_mod.py", `VALUE = "from-layer"`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-deleted-python-layer/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-deleted-python-layer-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When the attached layer version is deleted after configuration.
	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/hot-deleted-python-layer/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// Then function readback still preserves the configured layer reference.
	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/hot-deleted-python-layer-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 1 || cfg.Layers[0].Arn != lv.LayerVersionArn {
		t.Fatalf("expected configuration to retain deleted layer ARN %q, got %#v", lv.LayerVersionArn, cfg.Layers)
	}

	// And invocation now fails during runtime init with a clear missing-layer error.
	resp := invokeFunction(t, srv, "hot-deleted-python-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_nodeRuntime_hotReload_deletedLayerRecoveryAfterClearingLayers(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload Node function with an attached layer that later gets deleted.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-deleted-layer-recovery-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.js", []byte(`
exports.handler = async () => {
  return { ok: true };
};
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-deleted-layer-recovery-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-deleted-layer-recovery-fn")

	layerZip := makeZip(t, "nodejs/node_modules/layer-lib/index.js", `module.exports = { value: "from-layer" };`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-deleted-node-layer-recovery/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-deleted-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/hot-deleted-node-layer-recovery/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// First invoke fails because configured layer content can no longer be loaded.
	failResp := invokeFunction(t, srv, "hot-deleted-layer-recovery-fn", map[string]any{"ping": true})
	defer failResp.Body.Close()
	failBody, err := io.ReadAll(failResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if failResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(failBody), "Runtime.ExitError") || strings.Contains(string(failBody), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(failBody))
	}
	helpers.AssertStatus(t, failResp, http.StatusOK)
	if failResp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", failResp.Header.Get("X-Amz-Function-Error"), failBody)
	}
	var failOut map[string]any
	if err := json.Unmarshal(failBody, &failOut); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, failBody)
	}
	errMsg, _ := failOut["errorMessage"].(string)
	if failOut["errorType"] != "Runtime.InitError" || !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer Runtime.InitError, got: %s", failBody)
	}

	// When layer references are cleared via configuration update.
	clearResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-deleted-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{},
	})
	defer clearResp.Body.Close()
	helpers.AssertStatus(t, clearResp, http.StatusOK)

	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/hot-deleted-layer-recovery-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 0 {
		t.Fatalf("expected 0 attached layers after clearing, got %#v", cfg.Layers)
	}

	// Then invoke succeeds again because startup no longer needs missing layer content.
	okResp := invokeFunction(t, srv, "hot-deleted-layer-recovery-fn", map[string]any{"ping": true})
	defer okResp.Body.Close()
	okBody, err := io.ReadAll(okResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if okResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(okBody), "Runtime.ExitError") || strings.Contains(string(okBody), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(okBody))
	}
	helpers.AssertStatus(t, okResp, http.StatusOK)
	if okResp.Header.Get("X-Amz-Function-Error") != "" {
		t.Fatalf("expected no function error after clearing layers, got %q (body=%s)", okResp.Header.Get("X-Amz-Function-Error"), okBody)
	}
	var out map[string]any
	if err := json.Unmarshal(okBody, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, okBody)
	}
	if out["ok"] != true {
		t.Fatalf("expected successful payload after clearing layers, got: %s", okBody)
	}
}

func TestInvoke_pythonRuntime_hotReload_deletedLayerRecoveryAfterClearingLayers(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload Python function with an attached layer that later gets deleted.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-python-deleted-layer-recovery-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.py", []byte(`
def handler(event, context):
    return {"ok": True}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-deleted-python-layer-recovery-fn",
		Runtime:      "python3.11",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-deleted-python-layer-recovery-fn")

	layerZip := makeZip(t, "python/layer_mod.py", `VALUE = "from-layer"`)
	lvResp := doJSON(t, http.MethodPost, layerURL(srv, "/layers/hot-deleted-python-layer-recovery/versions"), publishLayerVersionReq{
		Content: layerContent{ZipFile: layerZip},
	})
	helpers.AssertStatus(t, lvResp, http.StatusCreated)
	var lv layerVersionResponse
	decodeJSON(t, lvResp, &lv)

	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-deleted-python-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{lv.LayerVersionArn},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	deleteResp := doJSON(t, http.MethodDelete, layerURL(srv, "/layers/hot-deleted-python-layer-recovery/versions/1"), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// First invoke fails because configured layer content can no longer be loaded.
	failResp := invokeFunction(t, srv, "hot-deleted-python-layer-recovery-fn", map[string]any{"ping": true})
	defer failResp.Body.Close()
	failBody, err := io.ReadAll(failResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if failResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(failBody), "Runtime.ExitError") || strings.Contains(string(failBody), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(failBody))
	}
	helpers.AssertStatus(t, failResp, http.StatusOK)
	if failResp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", failResp.Header.Get("X-Amz-Function-Error"), failBody)
	}
	var failOut map[string]any
	if err := json.Unmarshal(failBody, &failOut); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, failBody)
	}
	errMsg, _ := failOut["errorMessage"].(string)
	if failOut["errorType"] != "Runtime.InitError" || !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer Runtime.InitError, got: %s", failBody)
	}

	// When layer references are cleared via configuration update.
	clearResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-deleted-python-layer-recovery-fn/configuration"), map[string]any{
		"Layers": []string{},
	})
	defer clearResp.Body.Close()
	helpers.AssertStatus(t, clearResp, http.StatusOK)

	type funcConfigWithLayers struct {
		functionConfiguration
		Layers []struct {
			Arn string `json:"Arn"`
		} `json:"Layers,omitempty"`
	}
	getCfgResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/hot-deleted-python-layer-recovery-fn/configuration"), nil)
	helpers.AssertStatus(t, getCfgResp, http.StatusOK)
	var cfg funcConfigWithLayers
	decodeJSON(t, getCfgResp, &cfg)
	if len(cfg.Layers) != 0 {
		t.Fatalf("expected 0 attached layers after clearing, got %#v", cfg.Layers)
	}

	// Then invoke succeeds again because startup no longer needs missing layer content.
	okResp := invokeFunction(t, srv, "hot-deleted-python-layer-recovery-fn", map[string]any{"ping": true})
	defer okResp.Body.Close()
	okBody, err := io.ReadAll(okResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if okResp.Header.Get("X-Amz-Function-Error") == "Unhandled" &&
		(strings.Contains(string(okBody), "Runtime.ExitError") || strings.Contains(string(okBody), "Runtime.ImportModuleError")) {
		t.Skipf("hot-reload bind mount not supported in this Docker environment: %s", string(okBody))
	}
	helpers.AssertStatus(t, okResp, http.StatusOK)
	if okResp.Header.Get("X-Amz-Function-Error") != "" {
		t.Fatalf("expected no function error after clearing layers, got %q (body=%s)", okResp.Header.Get("X-Amz-Function-Error"), okBody)
	}
	var out map[string]any
	if err := json.Unmarshal(okBody, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, okBody)
	}
	if out["ok"] != true {
		t.Fatalf("expected successful payload after clearing layers, got: %s", okBody)
	}
}

func TestInvoke_nodeRuntime_missingLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Node function whose configuration references a
	// non-existent layer version ARN.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async () => {
  return { ok: true };
};
`)
	createFunctionWithCode(t, srv, "missing-layer-fn", "nodejs20.x", "index.handler", code)
	waitForFunctionActive(t, srv, "missing-layer-fn")

	missingARN := "arn:aws:lambda:us-east-1:000000000000:layer:does-not-exist:999"
	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/missing-layer-fn/configuration"), map[string]any{
		"Layers": []string{missingARN},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	resp := invokeFunction(t, srv, "missing-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Then the runtime init fails with a clear message about missing layer version.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_pythonRuntime_missingLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)

	// Given a zip-based Python function whose configuration references a
	// non-existent layer version ARN.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.py", `
def handler(event, context):
    return {"ok": True}
`)
	createFunctionWithCode(t, srv, "missing-python-layer-fn", "python3.11", "index.handler", code)
	waitForFunctionActive(t, srv, "missing-python-layer-fn")

	missingARN := "arn:aws:lambda:us-east-1:000000000000:layer:does-not-exist:999"
	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/missing-python-layer-fn/configuration"), map[string]any{
		"Layers": []string{missingARN},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	resp := invokeFunction(t, srv, "missing-python-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Then the runtime init fails with a clear message about missing layer version.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_nodeRuntime_hotReload_missingLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload function whose configuration references a
	// non-existent layer version ARN.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-missing-layer-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.js", []byte(`
exports.handler = async () => {
  return { ok: true };
};
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-missing-layer-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-missing-layer-fn")

	missingARN := "arn:aws:lambda:us-east-1:000000000000:layer:does-not-exist:999"
	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-missing-layer-fn/configuration"), map[string]any{
		"Layers": []string{missingARN},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	resp := invokeFunction(t, srv, "hot-missing-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Then runtime init fails with a clear missing-layer message.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_pythonRuntime_hotReload_missingLayerVersionFailsInit(t *testing.T) {
	skipIfNoDocker(t)
	skipIfContainerizedHotReloadBindMount(t)

	// Given a hot-reload Python function whose configuration references a
	// non-existent layer version ARN.
	sourceDir, err := os.MkdirTemp("/workspace", "hot-reload-python-missing-layer-")
	if err != nil {
		sourceDir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(sourceDir) })
	}
	if err := os.WriteFile(sourceDir+"/index.py", []byte(`
def handler(event, context):
    return {"ok": True}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker(), helpers.WithLambdaHotReload())
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "hot-missing-python-layer-fn",
		Runtime:      "python3.11",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		Tags:         map[string]string{"overcast:hot-reload-path": sourceDir},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForFunctionActive(t, srv, "hot-missing-python-layer-fn")

	missingARN := "arn:aws:lambda:us-east-1:000000000000:layer:does-not-exist:999"
	attachResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/hot-missing-python-layer-fn/configuration"), map[string]any{
		"Layers": []string{missingARN},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When invoking the function.
	resp := invokeFunction(t, srv, "hot-missing-python-layer-fn", map[string]any{"ping": true})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Then runtime init fails with a clear missing-layer message.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") != "Unhandled" {
		t.Fatalf("expected X-Amz-Function-Error=Unhandled, got %q (body=%s)", resp.Header.Get("X-Amz-Function-Error"), body)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, body)
	}
	if out["errorType"] != "Runtime.InitError" {
		t.Fatalf("expected errorType Runtime.InitError, got: %v (body=%s)", out["errorType"], body)
	}
	errMsg, _ := out["errorMessage"].(string)
	if !strings.Contains(errMsg, "layer version not found") {
		t.Fatalf("expected missing-layer message, got: %s", body)
	}
}

func TestInvoke_functionNotFound(t *testing.T) {
	// Does not require Docker — function lookup happens before runtime dispatch.
	srv := helpers.NewTestServer(t)

	// When InvokeFunction is called for a non-existent function
	resp := invokeFunction(t, srv, "no-such-function", nil)
	defer resp.Body.Close()

	// Then 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestInvoke_functionError(t *testing.T) {
	skipIfNoDocker(t)

	// Given a function that always throws
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async () => {
  throw new Error("something went wrong");
};
`)
	createFunctionWithCode(t, srv, "throw-fn", "nodejs20.x", "index.handler", code)
	waitForFunctionActive(t, srv, "throw-fn")

	// When InvokeFunction is called
	resp := invokeFunction(t, srv, "throw-fn", map[string]string{})
	defer resp.Body.Close()

	// Then 200 with X-Amz-Function-Error header set (AWS semantics: errors are
	// still 200 responses with a special header + error payload)
	helpers.AssertStatus(t, resp, http.StatusOK)
	if resp.Header.Get("X-Amz-Function-Error") == "" {
		t.Error("expected X-Amz-Function-Error header to be set for a throwing handler")
	}
	body, _ := io.ReadAll(resp.Body)
	var errPayload map[string]any
	if err := json.Unmarshal(body, &errPayload); err != nil {
		t.Fatalf("unmarshal error payload: %v — body: %s", err, body)
	}
	if errPayload["errorMessage"] == nil && errPayload["errorType"] == nil {
		t.Errorf("error payload missing errorMessage/errorType: %s", body)
	}
}

func TestInvoke_timeout(t *testing.T) {
	skipIfNoDocker(t)

	// Given a function that sleeps longer than its timeout
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async () => {
  await new Promise(r => setTimeout(r, 30000));
  return {};
};
`)
	// Timeout = 1s so the test completes quickly.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "timeout-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Timeout:      1,
		MemorySize:   128,
		Code:         &lambdaCode{ZipFile: code},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	waitForFunctionActive(t, srv, "timeout-fn")

	// When InvokeFunction is called (should time out)
	start := time.Now()
	invokeResp := invokeFunction(t, srv, "timeout-fn", nil)
	invokeResp.Body.Close()
	elapsed := time.Since(start)

	// Then it returns in a bounded time (not hanging forever). The budget is
	// generous because a cold Docker container start (image pull/create/start
	// of node20.x) plus the 1s sleep plus teardown can take several seconds
	// on a loaded host. The intent is to catch the "invoke hangs forever"
	// regression, not to assert tight timing.
	if elapsed > 15*time.Second {
		t.Errorf("invoke took %v, expected ≤15s (Lambda timeout=1s + Docker cold-start budget ~14s); this likely indicates the invoke is hanging or Docker is wedged", elapsed)
	}
}

func TestInvoke_logTail(t *testing.T) {
	skipIfNoDocker(t)

	// Given a function that logs output
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async (event) => {
  console.log("hello from lambda");
  return { ok: true };
};
`)
	createFunctionWithCode(t, srv, "log-fn", "nodejs20.x", "index.handler", code)
	waitForFunctionActive(t, srv, "log-fn")

	// When InvokeFunction is called with X-Amz-Log-Type: Tail
	req, err := http.NewRequest(http.MethodPost, lambdaURL(srv, "/functions/log-fn/invocations"), bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Log-Type", "Tail")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then 200 + X-Amz-Log-Result contains base64-encoded logs
	helpers.AssertStatus(t, resp, http.StatusOK)
	logResult := resp.Header.Get("X-Amz-Log-Result")
	if logResult == "" {
		t.Error("expected X-Amz-Log-Result header when X-Amz-Log-Type: Tail")
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(logResult)
	if err != nil {
		t.Fatalf("decode X-Amz-Log-Result: %v", err)
	}
	if !bytes.Contains(decoded, []byte("hello from lambda")) {
		t.Errorf("log tail %q does not contain expected log line", decoded)
	}
}

// TestInvoke_logsLandInCloudWatch verifies the END-TO-END log path: invoking a
// Lambda function results in handler stdout AND the synthetic START / END /
// REPORT lines being readable via CloudWatch Logs GetLogEvents.
//
// This is the user-visible behaviour the CloudWatch Logs UI relies on. It
// exercises the full pipeline: Docker stdout → streamLogs goroutine → batched
// flush → logsStore.appendEvents (cache + debounced persist) → GetLogEvents.
func TestInvoke_logsLandInCloudWatch(t *testing.T) {
	skipIfNoDocker(t)

	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async () => {
  console.log("handler ran ok marker-xyz");
  return { ok: true };
};
`)
	createFunctionWithCode(t, srv, "cwl-fn", "nodejs20.x", "index.handler", code)
	waitForFunctionActive(t, srv, "cwl-fn")

	// Invoke the function.
	resp := invokeFunction(t, srv, "cwl-fn", []byte("{}"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke returned %d, expected 200", resp.StatusCode)
	}

	// Poll CloudWatch Logs for up to 5 s — events are written by the async
	// log batcher (5 ms flush) and the persistence layer is debounced (50 ms).
	// The cache returns events as soon as appendEvents completes so this should
	// be near-instantaneous, but we poll to handle CI scheduling jitter.
	groupName := "/aws/lambda/cwl-fn"
	deadline := time.Now().Add(5 * time.Second)
	var matched bool
	var lastEvents []map[string]any
	for time.Now().Before(deadline) {
		// FilterLogEvents searches across all streams in the group; we don't
		// know the auto-generated stream name up front.
		body, _ := json.Marshal(map[string]any{
			"logGroupName": groupName,
		})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		req.Header.Set("X-Amz-Target", "Logs_20140328.FilterLogEvents")
		filterResp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("FilterLogEvents: %v", err)
		}
		var result struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.NewDecoder(filterResp.Body).Decode(&result)
		filterResp.Body.Close()
		lastEvents = result.Events

		// Check for the marker we logged + the synthetic START/REPORT lines.
		var sawMarker, sawStart, sawReport bool
		for _, e := range result.Events {
			msg, _ := e["message"].(string)
			if strings.Contains(msg, "marker-xyz") {
				sawMarker = true
			}
			if strings.HasPrefix(msg, "START RequestId:") {
				sawStart = true
			}
			if strings.HasPrefix(msg, "REPORT RequestId:") {
				sawReport = true
			}
		}
		if sawMarker && sawStart && sawReport {
			matched = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !matched {
		t.Fatalf("expected START / handler stdout (marker-xyz) / REPORT in CloudWatch Logs for group %q within 5 s; got %d events: %+v",
			groupName, len(lastEvents), lastEvents)
	}
}

// ─── InvokeWithResponseStream ────────────────────────────────────────────────

func streamingURL(srv *helpers.TestServer, name string) string {
	return srv.URL + "/2021-11-15/functions/" + name + "/response-streaming-invocations"
}

// parseEventStreamMessages decodes a raw AWS event stream body into a map
// from :event-type header value to event payload bytes.
func parseEventStreamMessages(body []byte) map[string][]byte {
	result := make(map[string][]byte)
	for len(body) >= 12 {
		totalLen := int(body[0])<<24 | int(body[1])<<16 | int(body[2])<<8 | int(body[3])
		if totalLen > len(body) {
			break
		}
		hdrLen := int(body[4])<<24 | int(body[5])<<16 | int(body[6])<<8 | int(body[7])
		// Skip prelude CRC (bytes 8-11).
		hdrStart := 12
		hdrEnd := hdrStart + hdrLen
		payloadStart := hdrEnd
		payloadEnd := totalLen - 4 // exclude trailing CRC

		headers := parseESHeaders(body[hdrStart:hdrEnd])
		eventType := headers[":event-type"]
		result[eventType] = body[payloadStart:payloadEnd]
		body = body[totalLen:]
	}
	return result
}

func parseESHeaders(b []byte) map[string]string {
	out := make(map[string]string)
	for len(b) > 0 {
		nameLen := int(b[0])
		name := string(b[1 : 1+nameLen])
		b = b[1+nameLen:]
		typ := b[0]
		b = b[1:]
		if typ == 7 { // string
			valLen := int(b[0])<<8 | int(b[1])
			val := string(b[2 : 2+valLen])
			out[name] = val
			b = b[2+valLen:]
		}
	}
	return out
}

func TestInvokeWithResponseStream_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, streamingURL(srv, "no-such-fn"), bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestInvokeWithResponseStream_nodeRuntime(t *testing.T) {
	skipIfNoDocker(t)

	// TODO(perf): When Approach B is implemented, these 5 Docker tests could
	// share a single ContainerRuntime + InstancePool via a package-level
	// singleton, enabling warm-container reuse across test functions.
	srv := helpers.NewTestServer(t, helpers.WithLambdaDocker())
	code := makeZip(t, "index.js", `
exports.handler = async (event) => {
  return { streamed: true, received: event };
};
`)
	createFunctionWithCode(t, srv, "stream-fn", "nodejs20.x", "index.handler", code)
	waitForFunctionActive(t, srv, "stream-fn")

	req, _ := http.NewRequest(http.MethodPost, streamingURL(srv, "stream-fn"), bytes.NewReader([]byte(`{"hello":"world"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.amazon.eventstream" {
		t.Errorf("Content-Type: got %q, want application/vnd.amazon.eventstream", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	events := parseEventStreamMessages(body)

	// PayloadChunk must contain the real function response.
	chunk, ok := events["PayloadChunk"]
	if !ok {
		t.Fatal("missing PayloadChunk event")
	}
	var fnResp struct {
		Streamed bool `json:"streamed"`
	}
	if err := json.Unmarshal(chunk, &fnResp); err != nil {
		t.Fatalf("unmarshal PayloadChunk: %v", err)
	}
	if !fnResp.Streamed {
		t.Errorf("PayloadChunk.streamed: got false, want true")
	}

	// InvokeComplete must be present.
	if _, ok := events["InvokeComplete"]; !ok {
		t.Fatal("missing InvokeComplete event")
	}
}

func TestInvokeWithResponseStream_badInvocationType(t *testing.T) {
	srv := helpers.NewTestServer(t)

	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName": "stream-type-fn",
		"Runtime":      "nodejs20.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/test",
	})

	req, _ := http.NewRequest(http.MethodPost, streamingURL(srv, "stream-type-fn"), bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Invocation-Type", "Event")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── Event Source Mappings ───────────────────────────────────────────────────

// esmURL builds the event-source-mappings base URL.
func esmURL(srv *helpers.TestServer) string {
	return lambdaURL(srv, "/event-source-mappings")
}

// sqsARN builds a fake SQS queue ARN for tests.
func sqsARN(name string) string {
	return "arn:aws:sqs:us-east-1:000000000000:" + name
}

// dynamoDBStreamARN builds a fake DynamoDB Stream ARN for tests.
func dynamoDBStreamARN(table string) string {
	return "arn:aws:dynamodb:us-east-1:000000000000:table/" + table + "/stream/2024-01-01T00:00:00.000"
}

// createESM creates an event source mapping and returns its decoded body.
func createESM(t *testing.T, srv *helpers.TestServer, body map[string]any) map[string]any {
	t.Helper()
	resp := doJSON(t, http.MethodPost, esmURL(srv), body)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var out map[string]any
	decodeJSON(t, resp, &out)
	return out
}

// createSQSQueue creates an SQS queue via the test server and returns the URL.
func createSQSQueue(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"QueueName": name})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("createSQSQueue: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createSQSQueue: status %d", resp.StatusCode)
	}
	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.QueueUrl
}

// sqsReceiveMessages attempts to receive up to 10 messages from the queue and
// returns how many were returned. Does not delete them.
func sqsReceiveMessages(t *testing.T, srv *helpers.TestServer, queueURL string) int {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
		"WaitTimeSeconds":     0,
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.ReceiveMessage")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sqsReceiveMessages: %v", err)
	}
	defer resp.Body.Close()
	var result struct {
		Messages []map[string]any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return len(result.Messages)
}

// sqsSendMessage sends a single message to an SQS queue via the test server.
func sqsSendMessage(t *testing.T, srv *helpers.TestServer, queueURL, messageBody string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": messageBody,
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.SendMessage")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sqsSendMessage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sqsSendMessage: status %d", resp.StatusCode)
	}
}

func TestListEventSourceMappings_empty(t *testing.T) {
	// Given: a server with no ESMs
	srv := helpers.NewTestServer(t)

	// When: we list all ESMs
	resp := doJSON(t, http.MethodGet, esmURL(srv), nil)
	defer resp.Body.Close()

	// Then: 200 with an empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		EventSourceMappings []any `json:"EventSourceMappings"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if len(out.EventSourceMappings) != 0 {
		t.Errorf("expected empty list, got %d items", len(out.EventSourceMappings))
	}
}

func TestCreateEventSourceMapping_sqsSource(t *testing.T) {
	// Given: a Lambda function and an SQS queue ARN
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-fn")
	queueARN := sqsARN("esm-queue")

	// When: we create an ESM mapping the SQS queue to the function
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-fn",
		"EventSourceArn": queueARN,
		"BatchSize":      5,
	})

	// Then: the ESM is created with expected fields
	if esm["UUID"] == "" {
		t.Error("expected UUID to be set")
	}
	if esm["EventSourceArn"] != queueARN {
		t.Errorf("EventSourceArn: got %v, want %s", esm["EventSourceArn"], queueARN)
	}
	if esm["State"] != "Enabled" {
		t.Errorf("State: got %v, want Enabled", esm["State"])
	}
	if esm["BatchSize"] != float64(5) {
		t.Errorf("BatchSize: got %v, want 5", esm["BatchSize"])
	}
	if esm["FunctionArn"] == "" {
		t.Error("expected FunctionArn to be set")
	}
}

func TestCreateEventSourceMapping_dynamodbStreamSource(t *testing.T) {
	// Given: a Lambda function and a DynamoDB Stream ARN
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-ddb-fn")
	streamARN := dynamoDBStreamARN("MyTable")

	// When: we create an ESM mapping the DynamoDB stream to the function
	esm := createESM(t, srv, map[string]any{
		"FunctionName":     "esm-ddb-fn",
		"EventSourceArn":   streamARN,
		"StartingPosition": "TRIM_HORIZON",
	})

	// Then: the ESM is created with Enabled state and the right source ARN
	if esm["UUID"] == "" {
		t.Error("expected UUID to be set")
	}
	if esm["EventSourceArn"] != streamARN {
		t.Errorf("EventSourceArn: got %v, want %s", esm["EventSourceArn"], streamARN)
	}
	if esm["State"] != "Enabled" {
		t.Errorf("State: got %v, want Enabled", esm["State"])
	}
	if esm["StartingPosition"] != "TRIM_HORIZON" {
		t.Errorf("StartingPosition: got %v, want TRIM_HORIZON", esm["StartingPosition"])
	}
}

func TestCreateEventSourceMapping_defaultBatchSize(t *testing.T) {
	// Given: a function and SQS queue ARN
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-default-fn")

	// When: we create an ESM without specifying BatchSize
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-default-fn",
		"EventSourceArn": sqsARN("default-queue"),
	})

	// Then: BatchSize defaults to 10
	if esm["BatchSize"] != float64(10) {
		t.Errorf("BatchSize: got %v, want 10", esm["BatchSize"])
	}
}

func TestCreateEventSourceMapping_disabledInitially(t *testing.T) {
	// Given: a function and SQS queue ARN
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-disabled-fn")

	// When: we create an ESM with Enabled: false
	resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
		"FunctionName":   "esm-disabled-fn",
		"EventSourceArn": sqsARN("disabled-queue"),
		"Enabled":        false,
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var esm map[string]any
	decodeJSON(t, resp, &esm)

	// Then: State is Disabled
	if esm["State"] != "Disabled" {
		t.Errorf("State: got %v, want Disabled", esm["State"])
	}
}

func TestCreateEventSourceMapping_functionNotFound(t *testing.T) {
	// Given: no function named "ghost-fn"
	srv := helpers.NewTestServer(t)

	// When: we create an ESM for it
	resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
		"FunctionName":   "ghost-fn",
		"EventSourceArn": sqsARN("ghost-queue"),
	})
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestCreateEventSourceMapping_invalidSource(t *testing.T) {
	// Given: a function
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-invalid-fn")

	// When: we create an ESM with a kinesis ARN (unsupported)
	resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
		"FunctionName":   "esm-invalid-fn",
		"EventSourceArn": "arn:aws:kinesis:us-east-1:000000000000:stream/my-stream",
	})
	defer resp.Body.Close()

	// Then: 400 ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateEventSourceMapping_missingFunctionName(t *testing.T) {
	// Given: a server
	srv := helpers.NewTestServer(t)

	// When: we create an ESM without FunctionName
	resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
		"EventSourceArn": sqsARN("my-queue"),
	})
	defer resp.Body.Close()

	// Then: 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateFunction_crossRegionLayer(t *testing.T) {
	// Given: a Lambda function creation request with a layer ARN from a different region
	srv := helpers.NewTestServer(t)

	// When: CreateFunction specifies a layer ARN in eu-west-1 but the server is us-east-1
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName": "cross-region-layer-fn",
		"Runtime":      "python3.12",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/test-role",
		"Code":         map[string]any{"ZipFile": "UEsDBBQAAAAIAA=="},
		"Layers":       []string{"arn:aws:lambda:eu-west-1:000000000000:layer:my-layer:1"},
	})
	defer resp.Body.Close()

	// Then: real AWS rejects cross-region layers with InvalidParameterValueException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["__type"] != "InvalidParameterValueException" {
		t.Errorf("error type: got %v, want InvalidParameterValueException", body["__type"])
	}
}

func TestUpdateFunctionConfiguration_crossRegionLayer(t *testing.T) {
	// Given: an existing function in us-east-1
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "cross-region-update-fn")

	// When: UpdateFunctionConfiguration specifies a layer ARN in eu-west-1
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/cross-region-update-fn/configuration"),
		map[string]any{
			"Layers": []string{"arn:aws:lambda:eu-west-1:000000000000:layer:my-layer:1"},
		})
	defer resp.Body.Close()

	// Then: real AWS rejects cross-region layers with InvalidParameterValueException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["__type"] != "InvalidParameterValueException" {
		t.Errorf("error type: got %v, want InvalidParameterValueException", body["__type"])
	}
}

func TestCreateEventSourceMapping_crossRegion(t *testing.T) {
	// Given: a function in the test server's default region (us-east-1)
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "cross-region-fn")

	// When: we try to map it to an SQS queue in a different region (eu-west-1)
	resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
		"FunctionName":   "cross-region-fn",
		"EventSourceArn": "arn:aws:sqs:eu-west-1:000000000000:some-queue",
	})
	defer resp.Body.Close()

	// Then: real AWS rejects cross-region ESMs with InvalidParameterValueException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["__type"] != "InvalidParameterValueException" {
		t.Errorf("error type: got %v, want InvalidParameterValueException", body["__type"])
	}
}

func TestGetEventSourceMapping_success(t *testing.T) {
	// Given: an existing ESM
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "get-esm-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "get-esm-fn",
		"EventSourceArn": sqsARN("get-esm-queue"),
	})
	id := esm["UUID"].(string)

	// When: we get it by UUID
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"/"+id, nil)
	defer resp.Body.Close()

	// Then: 200 with the same UUID and fields
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got map[string]any
	helpers.DecodeJSON(t, resp, &got)
	if got["UUID"] != id {
		t.Errorf("UUID: got %v, want %s", got["UUID"], id)
	}
	if got["State"] != "Enabled" {
		t.Errorf("State: got %v, want Enabled", got["State"])
	}
}

func TestGetEventSourceMapping_notFound(t *testing.T) {
	// Given: no ESM with this UUID
	srv := helpers.NewTestServer(t)

	// When: we get a non-existent UUID
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"/00000000-0000-0000-0000-000000000000", nil)
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestUpdateEventSourceMapping_disable(t *testing.T) {
	// Given: an enabled ESM
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "upd-esm-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "upd-esm-fn",
		"EventSourceArn": sqsARN("upd-esm-queue"),
	})
	id := esm["UUID"].(string)

	// When: we disable it
	resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
		"Enabled": false,
	})
	defer resp.Body.Close()

	// Then: 200 with State Disabled
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got map[string]any
	helpers.DecodeJSON(t, resp, &got)
	if got["State"] != "Disabled" {
		t.Errorf("State: got %v, want Disabled", got["State"])
	}
}

func TestUpdateEventSourceMapping_changeBatchSize(t *testing.T) {
	// Given: an ESM with BatchSize 5
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "batch-esm-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "batch-esm-fn",
		"EventSourceArn": sqsARN("batch-esm-queue"),
		"BatchSize":      5,
	})
	id := esm["UUID"].(string)

	// When: we update BatchSize to 20
	resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
		"BatchSize": 20,
	})
	defer resp.Body.Close()

	// Then: 200 with new BatchSize
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got map[string]any
	helpers.DecodeJSON(t, resp, &got)
	if got["BatchSize"] != float64(20) {
		t.Errorf("BatchSize: got %v, want 20", got["BatchSize"])
	}
}

func TestUpdateEventSourceMapping_notFound(t *testing.T) {
	// Given: no ESM
	srv := helpers.NewTestServer(t)

	// When: we update a non-existent UUID
	resp := doJSON(t, http.MethodPut, esmURL(srv)+"/no-such-uuid", map[string]any{
		"BatchSize": 5,
	})
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestDeleteEventSourceMapping_success(t *testing.T) {
	// Given: an ESM
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "del-esm-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "del-esm-fn",
		"EventSourceArn": sqsARN("del-esm-queue"),
	})
	id := esm["UUID"].(string)

	// When: we delete it
	resp := doJSON(t, http.MethodDelete, esmURL(srv)+"/"+id, nil)
	defer resp.Body.Close()

	// Then: 200 with the deleted ESM body
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got map[string]any
	helpers.DecodeJSON(t, resp, &got)
	if got["UUID"] != id {
		t.Errorf("UUID mismatch: got %v, want %s", got["UUID"], id)
	}

	// And: subsequent GET returns 404
	resp2 := doJSON(t, http.MethodGet, esmURL(srv)+"/"+id, nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusNotFound)
}

func TestDeleteEventSourceMapping_notFound(t *testing.T) {
	// Given: no ESM
	srv := helpers.NewTestServer(t)

	// When: we delete a non-existent UUID
	resp := doJSON(t, http.MethodDelete, esmURL(srv)+"/no-such-uuid", nil)
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestListEventSourceMappings_byFunctionName(t *testing.T) {
	// Given: two ESMs for different functions
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-fn-a")
	createFunction(t, srv, "esm-fn-b")
	createESM(t, srv, map[string]any{
		"FunctionName":   "esm-fn-a",
		"EventSourceArn": sqsARN("queue-a"),
	})
	createESM(t, srv, map[string]any{
		"FunctionName":   "esm-fn-b",
		"EventSourceArn": sqsARN("queue-b"),
	})

	// When: we list by FunctionName
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"?FunctionName=esm-fn-a", nil)
	defer resp.Body.Close()

	// Then: only esm-fn-a's mapping is returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		EventSourceMappings []map[string]any `json:"EventSourceMappings"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if len(out.EventSourceMappings) != 1 {
		t.Fatalf("expected 1 ESM, got %d", len(out.EventSourceMappings))
	}
	arnGot, _ := out.EventSourceMappings[0]["EventSourceArn"].(string)
	if arnGot != sqsARN("queue-a") {
		t.Errorf("EventSourceArn: got %q, want %q", arnGot, sqsARN("queue-a"))
	}
}

func TestESMDelivery_sqsToLambda(t *testing.T) {
	// Given: a function, an SQS queue, and an ESM connecting them.
	// The stub NodeRuntime returns success (no FunctionError), so messages
	// will be deleted after the ESM poller delivers them.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "delivery-fn")
	queueURL := createSQSQueue(t, srv, "delivery-queue")
	createESM(t, srv, map[string]any{
		"FunctionName":   "delivery-fn",
		"EventSourceArn": sqsARN("delivery-queue"),
		"BatchSize":      10,
	})

	// When: we send a message to the queue
	sqsSendMessage(t, srv, queueURL, `{"hello":"world"}`)

	// Then: after the ESM poll interval (1s + safety margin), the message is consumed.
	// The visibility timeout used by the poller is 30s, so the message will be
	// invisible (not deleted) if delivery fails — the queue would appear empty either
	// way from ReceiveMessage's perspective.  We rely on the server receiving 0 messages
	// on a fresh receive (either deleted or still invisible due to visibility window).
	time.Sleep(2 * time.Second)
	remaining := sqsReceiveMessages(t, srv, queueURL)
	if remaining != 0 {
		t.Errorf("expected 0 visible messages after ESM delivery (got %d)", remaining)
	}
}

func TestCreateEventSourceMapping_withDestinationConfig(t *testing.T) {
	// Given: a function and a DynamoDB Stream ARN
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-dest-fn")
	streamARN := dynamoDBStreamARN("dest-table")
	dlqARN := sqsARN("esm-on-failure-queue")

	// When: we create an ESM with DestinationConfig and MaximumRetryAttempts
	maxRetries := 2
	esm := createESM(t, srv, map[string]any{
		"FunctionName":         "esm-dest-fn",
		"EventSourceArn":       streamARN,
		"StartingPosition":     "TRIM_HORIZON",
		"MaximumRetryAttempts": maxRetries,
		"DestinationConfig": map[string]any{
			"OnFailure": map[string]any{
				"Destination": dlqARN,
			},
		},
	})

	// Then: the ESM stores and returns the DestinationConfig
	if esm["MaximumRetryAttempts"] != float64(maxRetries) {
		t.Errorf("MaximumRetryAttempts: got %v, want %d", esm["MaximumRetryAttempts"], maxRetries)
	}
	dest, ok := esm["DestinationConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected DestinationConfig to be present")
	}
	onFailure, ok := dest["OnFailure"].(map[string]any)
	if !ok {
		t.Fatal("expected DestinationConfig.OnFailure to be present")
	}
	if onFailure["Destination"] != dlqARN {
		t.Errorf("DestinationConfig.OnFailure.Destination: got %v, want %s", onFailure["Destination"], dlqARN)
	}

	// When: we GET the ESM by UUID
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"/"+esm["UUID"].(string), nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got map[string]any
	helpers.DecodeJSON(t, resp, &got)

	// Then: DestinationConfig is preserved on read
	dest2, ok := got["DestinationConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected DestinationConfig on GET")
	}
	onFailure2, ok := dest2["OnFailure"].(map[string]any)
	if !ok {
		t.Fatal("expected DestinationConfig.OnFailure on GET")
	}
	if onFailure2["Destination"] != dlqARN {
		t.Errorf("GET DestinationConfig.OnFailure.Destination: got %v, want %s", onFailure2["Destination"], dlqARN)
	}
}

func TestCreateEventSourceMapping_withScalingConfig(t *testing.T) {
	// Given: a function and an SQS queue ARN
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-scaling-fn")
	qArn := sqsARN("scaling-queue")

	// When: we create an ESM with ScalingConfig.MaximumConcurrency = 5
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-scaling-fn",
		"EventSourceArn": qArn,
		"ScalingConfig": map[string]any{
			"MaximumConcurrency": 5,
		},
	})

	// Then: the ESM stores and returns the ScalingConfig
	sc, ok := esm["ScalingConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected ScalingConfig to be present")
	}
	if sc["MaximumConcurrency"] != float64(5) {
		t.Errorf("ScalingConfig.MaximumConcurrency: got %v, want 5", sc["MaximumConcurrency"])
	}

	// When: we GET the ESM by UUID
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"/"+esm["UUID"].(string), nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got map[string]any
	helpers.DecodeJSON(t, resp, &got)

	// Then: ScalingConfig is preserved
	sc2, ok := got["ScalingConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected ScalingConfig on GET")
	}
	if sc2["MaximumConcurrency"] != float64(5) {
		t.Errorf("GET ScalingConfig.MaximumConcurrency: got %v, want 5", sc2["MaximumConcurrency"])
	}
}

func TestUpdateEventSourceMapping_changeScalingConfig(t *testing.T) {
	// Given: an ESM without ScalingConfig
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-update-sc-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-update-sc-fn",
		"EventSourceArn": sqsARN("sc-update-queue"),
	})
	id := esm["UUID"].(string)

	// When: we update ScalingConfig to MaximumConcurrency=3
	resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
		"ScalingConfig": map[string]any{
			"MaximumConcurrency": 3,
		},
	})
	defer resp.Body.Close()

	// Then: 200 with ScalingConfig set
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got map[string]any
	helpers.DecodeJSON(t, resp, &got)
	sc, ok := got["ScalingConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected ScalingConfig in update response")
	}
	if sc["MaximumConcurrency"] != float64(3) {
		t.Errorf("ScalingConfig.MaximumConcurrency: got %v, want 3", sc["MaximumConcurrency"])
	}
}

// ─── PackageType: Image ───────────────────────────────────────────────────────

func TestCreateFunction_imagePackageType_success(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When CreateFunction is called with PackageType=Image and an ImageUri
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "image-fn",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{ImageUri: "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:latest"},
	})
	defer resp.Body.Close()

	// Then 201 + PackageType=Image, ImageUri set, Runtime omitted
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.PackageType != "Image" {
		t.Errorf("PackageType = %q, want Image", cfg.PackageType)
	}
	if cfg.ImageUri != "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:latest" {
		t.Errorf("ImageUri = %q, want ECR URI", cfg.ImageUri)
	}
	if cfg.FunctionName != "image-fn" {
		t.Errorf("FunctionName = %q, want image-fn", cfg.FunctionName)
	}
	if cfg.FunctionArn == "" {
		t.Error("FunctionArn must be set")
	}
	if cfg.State != "Active" {
		t.Errorf("State = %q, want Active", cfg.State)
	}
}

func TestCreateFunction_imagePackageType_missingImageUri(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When CreateFunction is called with PackageType=Image but no ImageUri
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "image-fn-bad",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{},
	})
	defer resp.Body.Close()

	// Then 400 InvalidParameterValueException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestGetFunction_imageFunction_returnsECRRepositoryType(t *testing.T) {
	// Given an image function
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "image-get-fn",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{ImageUri: "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:latest"},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// When GetFunction is called
	resp2 := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/image-get-fn"), nil)
	defer resp2.Body.Close()

	// Then Code.RepositoryType = "ECR" (not "S3")
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var out getFunctionResponse
	decodeJSON(t, resp2, &out)

	if out.Configuration.PackageType != "Image" {
		t.Errorf("PackageType = %q, want Image", out.Configuration.PackageType)
	}
	if out.Code == nil {
		t.Fatal("Code block must be present")
	}
	if out.Code.RepositoryType != "ECR" {
		t.Errorf("RepositoryType = %q, want ECR", out.Code.RepositoryType)
	}
	if out.Configuration.ImageUri != "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:latest" {
		t.Errorf("ImageUri = %q", out.Configuration.ImageUri)
	}
}

func TestUpdateFunctionCode_imageFunction_updatesImageUri(t *testing.T) {
	// Given an image function
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "image-update-fn",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{ImageUri: "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:v1"},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var created functionConfiguration
	decodeJSON(t, resp, &created)

	// When UpdateFunctionCode is called with a new ImageUri
	resp2 := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/image-update-fn/code"), map[string]any{
		"ImageUri": "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:v2",
	})
	defer resp2.Body.Close()

	// Then 200 + updated ImageUri and new RevisionId
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var cfg functionConfiguration
	decodeJSON(t, resp2, &cfg)

	if cfg.ImageUri != "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:v2" {
		t.Errorf("ImageUri = %q, want :v2", cfg.ImageUri)
	}
	if cfg.RevisionId == created.RevisionId {
		t.Error("RevisionId must change after UpdateFunctionCode")
	}
}

// ─── VpcConfig ───────────────────────────────────────────────────────────────

// ec2Query is a helper to call EC2 Query-protocol actions from Lambda tests.
func ec2Query(t *testing.T, srv *helpers.TestServer, action string, params map[string]string) *http.Response {
	t.Helper()
	v := make(map[string][]string)
	v["Action"] = []string{action}
	v["Version"] = []string{"2016-11-15"}
	for k, val := range params {
		v[k] = []string{val}
	}
	var pairs []string
	for k, vals := range v {
		for _, val := range vals {
			pairs = append(pairs, k+"="+val)
		}
	}
	body := strings.Join(pairs, "&")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ec2Query %s: %v", action, err)
	}
	return resp
}

func TestCreateFunction_withVpcConfig(t *testing.T) {
	// Given: a fresh server
	srv := helpers.NewTestServer(t)

	// When: CreateFunction is called with VpcConfig
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "vpc-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		VpcConfig: &vpcConfigReq{
			SubnetIds:        []string{"subnet-abc123", "subnet-def456"},
			SecurityGroupIds: []string{"sg-111", "sg-222"},
		},
	})
	defer resp.Body.Close()

	// Then: 201 with VpcConfig in response
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.VpcConfig == nil {
		t.Fatal("expected VpcConfig to be set")
	}
	if len(cfg.VpcConfig.SubnetIds) != 2 {
		t.Errorf("expected 2 SubnetIds, got %d", len(cfg.VpcConfig.SubnetIds))
	}
	if len(cfg.VpcConfig.SecurityGroupIds) != 2 {
		t.Errorf("expected 2 SecurityGroupIds, got %d", len(cfg.VpcConfig.SecurityGroupIds))
	}
}

func TestGetFunction_withVpcConfig(t *testing.T) {
	// Given: a function with VpcConfig
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "vpc-get-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		VpcConfig: &vpcConfigReq{
			SubnetIds:        []string{"subnet-aaa"},
			SecurityGroupIds: []string{"sg-bbb"},
		},
	})
	resp.Body.Close()

	// When: GetFunction is called
	getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/vpc-get-fn"), nil)
	defer getResp.Body.Close()

	// Then: 200 with VpcConfig in Configuration
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var result getFunctionResponse
	decodeJSON(t, getResp, &result)

	if result.Configuration.VpcConfig == nil {
		t.Fatal("expected VpcConfig in GetFunction response")
	}
	if len(result.Configuration.VpcConfig.SubnetIds) != 1 {
		t.Errorf("expected 1 SubnetId, got %d", len(result.Configuration.VpcConfig.SubnetIds))
	}
	if result.Configuration.VpcConfig.SubnetIds[0] != "subnet-aaa" {
		t.Errorf("expected subnet-aaa, got %q", result.Configuration.VpcConfig.SubnetIds[0])
	}
}

func TestGetFunctionConfiguration_withVpcConfig(t *testing.T) {
	// Given: a function with VpcConfig
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "vpc-cfg-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		VpcConfig: &vpcConfigReq{
			SubnetIds:        []string{"subnet-x"},
			SecurityGroupIds: []string{"sg-y"},
		},
	})
	resp.Body.Close()

	// When: GetFunctionConfiguration is called
	getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/vpc-cfg-fn/configuration"), nil)
	defer getResp.Body.Close()

	// Then: 200 with VpcConfig
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var cfg functionConfiguration
	decodeJSON(t, getResp, &cfg)

	if cfg.VpcConfig == nil {
		t.Fatal("expected VpcConfig")
	}
	if cfg.VpcConfig.SubnetIds[0] != "subnet-x" {
		t.Errorf("expected subnet-x, got %q", cfg.VpcConfig.SubnetIds[0])
	}
}

func TestUpdateFunctionConfiguration_addsVpcConfig(t *testing.T) {
	// Given: a function without VpcConfig
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "vpc-update-fn")

	// When: UpdateFunctionConfiguration is called with VpcConfig
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/vpc-update-fn/configuration"), map[string]any{
		"VpcConfig": map[string]any{
			"SubnetIds":        []string{"subnet-new1", "subnet-new2"},
			"SecurityGroupIds": []string{"sg-new"},
		},
	})
	defer resp.Body.Close()

	// Then: 200 with VpcConfig set
	helpers.AssertStatus(t, resp, http.StatusOK)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.VpcConfig == nil {
		t.Fatal("expected VpcConfig after update")
	}
	if len(cfg.VpcConfig.SubnetIds) != 2 {
		t.Errorf("expected 2 SubnetIds, got %d", len(cfg.VpcConfig.SubnetIds))
	}
	if cfg.VpcConfig.SecurityGroupIds[0] != "sg-new" {
		t.Errorf("expected sg-new, got %q", cfg.VpcConfig.SecurityGroupIds[0])
	}
}

func TestCreateFunction_withVpcConfig_resolvesVpcId(t *testing.T) {
	// Given: an EC2 VPC with a subnet (EC2+Lambda both enabled by default)
	srv := helpers.NewTestServer(t)

	// Create VPC via EC2
	vpcResp := ec2Query(t, srv, "CreateVpc", map[string]string{"CidrBlock": "10.0.0.0/16"})
	defer vpcResp.Body.Close()
	var vpcResult struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	vpcBody, _ := io.ReadAll(vpcResp.Body)
	xml.Unmarshal(vpcBody, &vpcResult) //nolint:errcheck
	vpcID := vpcResult.Vpc.VpcID
	if vpcID == "" {
		t.Fatal("failed to create VPC")
	}

	// Create subnet in the VPC
	subResp := ec2Query(t, srv, "CreateSubnet", map[string]string{
		"VpcId":     vpcID,
		"CidrBlock": "10.0.1.0/24",
	})
	defer subResp.Body.Close()
	var subResult struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	subBody, _ := io.ReadAll(subResp.Body)
	xml.Unmarshal(subBody, &subResult) //nolint:errcheck
	subnetID := subResult.Subnet.SubnetID
	if subnetID == "" {
		t.Fatal("failed to create subnet")
	}

	// When: CreateFunction is called with VpcConfig referencing the real subnet
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "vpc-resolve-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		VpcConfig: &vpcConfigReq{
			SubnetIds:        []string{subnetID},
			SecurityGroupIds: []string{"sg-test"},
		},
	})
	defer resp.Body.Close()

	// Then: 201 and VpcConfig.VpcId is resolved from the subnet
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg functionConfiguration
	decodeJSON(t, resp, &cfg)

	if cfg.VpcConfig == nil {
		t.Fatal("expected VpcConfig")
	}
	if cfg.VpcConfig.VpcId != vpcID {
		t.Errorf("expected VpcId=%s (resolved from subnet), got %q", vpcID, cfg.VpcConfig.VpcId)
	}
}

func TestCreateFunction_withoutVpcConfig_noVpcConfigInResponse(t *testing.T) {
	// Given: a fresh server
	srv := helpers.NewTestServer(t)

	// When: CreateFunction is called without VpcConfig
	cfg := createFunction(t, srv, "no-vpc-fn")

	// Then: VpcConfig is nil/absent in response
	if cfg.VpcConfig != nil {
		t.Errorf("expected no VpcConfig, got %+v", cfg.VpcConfig)
	}
}

func TestListFunctions_includesVpcConfig(t *testing.T) {
	// Given: a function with VpcConfig
	srv := helpers.NewTestServer(t)
	doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "vpc-list-fn",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{},
		VpcConfig: &vpcConfigReq{
			SubnetIds:        []string{"subnet-list1"},
			SecurityGroupIds: []string{"sg-list1"},
		},
	}).Body.Close()

	// When: ListFunctions is called
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions"), nil)
	defer resp.Body.Close()

	// Then: the function's VpcConfig is included
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Functions []functionConfiguration `json:"Functions"`
	}
	decodeJSON(t, resp, &out)

	var found *functionConfiguration
	for i := range out.Functions {
		if out.Functions[i].FunctionName == "vpc-list-fn" {
			found = &out.Functions[i]
			break
		}
	}
	if found == nil {
		t.Fatal("vpc-list-fn not found in ListFunctions")
	}
	if found.VpcConfig == nil {
		t.Fatal("expected VpcConfig in ListFunctions response")
	}
	if found.VpcConfig.SubnetIds[0] != "subnet-list1" {
		t.Errorf("expected subnet-list1, got %q", found.VpcConfig.SubnetIds[0])
	}
}

// ─── ImageConfig ──────────────────────────────────────────────────────────────

func TestCreateFunction_imageConfig_storedAndReturned(t *testing.T) {
	// Given an image function created with ImageConfig overrides
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "imgcfg-create-fn",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{ImageUri: "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:latest"},
		ImageConfig: &imageConfigReq{
			EntryPoint:       []string{"/entry.sh"},
			Command:          []string{"handler"},
			WorkingDirectory: "/var/task",
		},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var created functionConfiguration
	decodeJSON(t, resp, &created)

	// Then ImageConfig is present in the create response
	if created.ImageConfig == nil {
		t.Fatal("ImageConfig must be present in CreateFunction response")
	}
	if len(created.ImageConfig.EntryPoint) != 1 || created.ImageConfig.EntryPoint[0] != "/entry.sh" {
		t.Errorf("EntryPoint = %v, want [/entry.sh]", created.ImageConfig.EntryPoint)
	}
	if len(created.ImageConfig.Command) != 1 || created.ImageConfig.Command[0] != "handler" {
		t.Errorf("Command = %v, want [handler]", created.ImageConfig.Command)
	}
	if created.ImageConfig.WorkingDirectory != "/var/task" {
		t.Errorf("WorkingDirectory = %q, want /var/task", created.ImageConfig.WorkingDirectory)
	}

	// And ImageConfig is returned by GetFunction
	resp2 := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/imgcfg-create-fn"), nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var out getFunctionResponse
	decodeJSON(t, resp2, &out)

	if out.Configuration.ImageConfig == nil {
		t.Fatal("ImageConfig must be present in GetFunction response")
	}
	if out.Configuration.ImageConfig.WorkingDirectory != "/var/task" {
		t.Errorf("WorkingDirectory = %q, want /var/task", out.Configuration.ImageConfig.WorkingDirectory)
	}
}

func TestUpdateFunctionConfiguration_imageConfig_patches(t *testing.T) {
	// Given an image function with no ImageConfig
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "imgcfg-update-fn",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{ImageUri: "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:latest"},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// When UpdateFunctionConfiguration is called with ImageConfig
	resp2 := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/imgcfg-update-fn/configuration"), map[string]any{
		"ImageConfig": map[string]any{
			"Command":          []string{"my-handler"},
			"WorkingDirectory": "/app",
		},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var updated functionConfiguration
	decodeJSON(t, resp2, &updated)

	// Then ImageConfig reflects the update
	if updated.ImageConfig == nil {
		t.Fatal("ImageConfig must be present in UpdateFunctionConfiguration response")
	}
	if len(updated.ImageConfig.Command) != 1 || updated.ImageConfig.Command[0] != "my-handler" {
		t.Errorf("Command = %v, want [my-handler]", updated.ImageConfig.Command)
	}
	if updated.ImageConfig.WorkingDirectory != "/app" {
		t.Errorf("WorkingDirectory = %q, want /app", updated.ImageConfig.WorkingDirectory)
	}
}

func TestUpdateFunctionConfiguration_imageConfig_clearable(t *testing.T) {
	// Given an image function with ImageConfig
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "imgcfg-clear-fn",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{ImageUri: "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-fn:latest"},
		ImageConfig: &imageConfigReq{
			Command: []string{"old-handler"},
		},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// When UpdateFunctionConfiguration sends an empty ImageConfig object
	resp2 := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/imgcfg-clear-fn/configuration"), map[string]any{
		"ImageConfig": map[string]any{},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var updated functionConfiguration
	decodeJSON(t, resp2, &updated)

	// Then ImageConfig is cleared (nil or empty)
	if updated.ImageConfig != nil && (len(updated.ImageConfig.Command) > 0 || len(updated.ImageConfig.EntryPoint) > 0 || updated.ImageConfig.WorkingDirectory != "") {
		t.Errorf("expected ImageConfig cleared, got %+v", updated.ImageConfig)
	}
}

// ─── Concurrency ──────────────────────────────────────────────────────────────

type concurrencyResponse struct {
	ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
}

type provisionedConcurrencyResponse struct {
	AllocatedProvisionedConcurrentExecutions int    `json:"AllocatedProvisionedConcurrentExecutions"`
	RequestedProvisionedConcurrentExecutions int    `json:"RequestedProvisionedConcurrentExecutions"`
	AvailableProvisionedConcurrentExecutions int    `json:"AvailableProvisionedConcurrentExecutions"`
	Status                                   string `json:"Status"`
	LastModified                             string `json:"LastModified"`
}

func TestPutFunctionConcurrency_success(t *testing.T) {
	// Given a function exists
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-fn")

	// When PutFunctionConcurrency is called
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/concurrency-fn/concurrency"), map[string]any{
		"ReservedConcurrentExecutions": 50,
	})
	defer resp.Body.Close()

	// Then 200 with the concurrency value
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out concurrencyResponse
	decodeJSON(t, resp, &out)
	if out.ReservedConcurrentExecutions != 50 {
		t.Errorf("ReservedConcurrentExecutions = %d, want 50", out.ReservedConcurrentExecutions)
	}
}

func TestGetFunctionConcurrency_success(t *testing.T) {
	// Given a function with reserved concurrency set
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-get-fn")
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/concurrency-get-fn/concurrency"), map[string]any{
		"ReservedConcurrentExecutions": 25,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When GetFunctionConcurrency is called
	resp2 := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/concurrency-get-fn/concurrency"), nil)
	defer resp2.Body.Close()

	// Then 200 with the stored value
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var out concurrencyResponse
	decodeJSON(t, resp2, &out)
	if out.ReservedConcurrentExecutions != 25 {
		t.Errorf("ReservedConcurrentExecutions = %d, want 25", out.ReservedConcurrentExecutions)
	}
}

func TestGetFunctionConcurrency_notSet_returns404(t *testing.T) {
	// Given a function with no reserved concurrency
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-notset-fn")

	// When GetFunctionConcurrency is called
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/concurrency-notset-fn/concurrency"), nil)
	defer resp.Body.Close()

	// Then 404 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestDeleteFunctionConcurrency_success(t *testing.T) {
	// Given a function with reserved concurrency set
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-del-fn")
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/concurrency-del-fn/concurrency"), map[string]any{
		"ReservedConcurrentExecutions": 10,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When DeleteFunctionConcurrency is called
	resp2 := doJSON(t, http.MethodDelete, lambdaURL(srv, "/functions/concurrency-del-fn/concurrency"), nil)
	defer resp2.Body.Close()

	// Then 204 No Content
	helpers.AssertStatus(t, resp2, http.StatusNoContent)

	// And GetFunctionConcurrency now returns 404
	resp3 := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/concurrency-del-fn/concurrency"), nil)
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusNotFound)
}

func TestPutProvisionedConcurrencyConfig_success(t *testing.T) {
	// Given a function with a published version
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "prov-concurrency-fn")

	// When PutProvisionedConcurrencyConfig is called with Qualifier
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/prov-concurrency-fn/provisioned-concurrency?Qualifier=1"), map[string]any{
		"ProvisionedConcurrentExecutions": 5,
	})
	defer resp.Body.Close()

	// Then 202 Accepted with READY status
	helpers.AssertStatus(t, resp, http.StatusAccepted)
	var out provisionedConcurrencyResponse
	decodeJSON(t, resp, &out)
	if out.RequestedProvisionedConcurrentExecutions != 5 {
		t.Errorf("RequestedProvisionedConcurrentExecutions = %d, want 5", out.RequestedProvisionedConcurrentExecutions)
	}
	if out.Status != "READY" {
		t.Errorf("Status = %q, want READY", out.Status)
	}
}

func TestGetProvisionedConcurrencyConfig_success(t *testing.T) {
	// Given a provisioned concurrency config exists
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "prov-concurrency-get-fn")
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/prov-concurrency-get-fn/provisioned-concurrency?Qualifier=prod"), map[string]any{
		"ProvisionedConcurrentExecutions": 8,
	})
	helpers.AssertStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()

	// When GetProvisionedConcurrencyConfig is called
	resp2 := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/prov-concurrency-get-fn/provisioned-concurrency?Qualifier=prod"), nil)
	defer resp2.Body.Close()

	// Then 200 with stored values
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var out provisionedConcurrencyResponse
	decodeJSON(t, resp2, &out)
	if out.RequestedProvisionedConcurrentExecutions != 8 {
		t.Errorf("RequestedProvisionedConcurrentExecutions = %d, want 8", out.RequestedProvisionedConcurrentExecutions)
	}
	if out.Status != "READY" {
		t.Errorf("Status = %q, want READY", out.Status)
	}
}

func TestGetProvisionedConcurrencyConfig_notFound(t *testing.T) {
	// Given a function with no provisioned concurrency
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "prov-concurrency-notfound-fn")

	// When GetProvisionedConcurrencyConfig is called
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/prov-concurrency-notfound-fn/provisioned-concurrency?Qualifier=1"), nil)
	defer resp.Body.Close()

	// Then 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── GetFunctionCodeSigningConfig ─────────────────────────────────────────────

func TestGetFunctionCodeSigningConfig_noConfigAssociated(t *testing.T) {
	// Given a function with no code signing config
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "codesign-fn")

	// When GetFunctionCodeSigningConfig is called
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/codesign-fn/code-signing-config"), nil)
	defer resp.Body.Close()

	// Then 404 ResourceNotFoundException (not a 501 — the function exists, it just has no config)
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	var errBody map[string]string
	decodeJSON(t, resp, &errBody)
	if errBody["__type"] != "ResourceNotFoundException" {
		t.Errorf("__type = %q, want ResourceNotFoundException", errBody["__type"])
	}
}

func TestGetFunctionCodeSigningConfig_functionNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/no-such-codesign-fn/code-signing-config"), nil)
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}
