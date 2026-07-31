package lambda_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// Wire shapes for FileSystemConfigs (kept local to the test, mirroring the
// convention of lambda_test.go's request/response structs).
type fileSystemConfig struct {
	Arn            string `json:"Arn"`
	LocalMountPath string `json:"LocalMountPath"`
}

type createFunctionWithEFSReq struct {
	FunctionName      string             `json:"FunctionName"`
	Runtime           string             `json:"Runtime"`
	Handler           string             `json:"Handler"`
	Role              string             `json:"Role"`
	Code              *lambdaCode        `json:"Code,omitempty"`
	FileSystemConfigs []fileSystemConfig `json:"FileSystemConfigs,omitempty"`
}

type functionConfigurationWithEFS struct {
	FunctionName      string             `json:"FunctionName"`
	FileSystemConfigs []fileSystemConfig `json:"FileSystemConfigs"`
}

const testAccessPointARN = "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-0123456789abcdef0"

func createWithEFS(t *testing.T, srv *helpers.TestServer, name string, configs []fileSystemConfig) *http.Response {
	t.Helper()
	return doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionWithEFSReq{
		FunctionName:      name,
		Runtime:           "nodejs20.x",
		Handler:           "index.handler",
		Role:              "arn:aws:iam::000000000000:role/lambda-role",
		Code:              &lambdaCode{},
		FileSystemConfigs: configs,
	})
}

func TestCreateFunction_fileSystemConfigsRoundTrip(t *testing.T) {
	// Given a fresh server; When creating a function with a FileSystemConfig
	srv := helpers.NewTestServer(t)
	resp := createWithEFS(t, srv, "efs-fn", []fileSystemConfig{
		{Arn: testAccessPointARN, LocalMountPath: "/mnt/shared"},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var created functionConfigurationWithEFS
	decodeJSON(t, resp, &created)

	// Then the config is echoed on create and on GetFunctionConfiguration.
	if len(created.FileSystemConfigs) != 1 || created.FileSystemConfigs[0].LocalMountPath != "/mnt/shared" {
		t.Fatalf("expected FileSystemConfigs echoed on create, got %#v", created.FileSystemConfigs)
	}
	getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/efs-fn/configuration"), nil)
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var got functionConfigurationWithEFS
	decodeJSON(t, getResp, &got)
	if len(got.FileSystemConfigs) != 1 || got.FileSystemConfigs[0].Arn != testAccessPointARN {
		t.Fatalf("expected FileSystemConfigs on get, got %#v", got.FileSystemConfigs)
	}
}

func TestCreateFunction_fileSystemConfigsValidation(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cases := []struct {
		name    string
		configs []fileSystemConfig
	}{
		{"more than one config", []fileSystemConfig{
			{Arn: testAccessPointARN, LocalMountPath: "/mnt/a"},
			{Arn: testAccessPointARN, LocalMountPath: "/mnt/b"},
		}},
		{"non access-point arn", []fileSystemConfig{
			{Arn: "arn:aws:elasticfilesystem:us-east-1:000000000000:file-system/fs-0123456789abcdef0", LocalMountPath: "/mnt/a"},
		}},
		{"mount path outside /mnt", []fileSystemConfig{
			{Arn: testAccessPointARN, LocalMountPath: "/var/data"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := createWithEFS(t, srv, "efs-invalid", tc.configs)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", resp.StatusCode)
			}
			var body map[string]any
			decodeJSON(t, resp, &body)
			if body["__type"] != "InvalidParameterValueException" {
				t.Fatalf("expected InvalidParameterValueException, got %#v", body)
			}
		})
	}
}

func TestUpdateFunctionConfiguration_fileSystemConfigsSetAndClear(t *testing.T) {
	// Given a function without mounts
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "efs-upd")

	// When setting a FileSystemConfig via UpdateFunctionConfiguration
	upd := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/efs-upd/configuration"), map[string]any{
		"FileSystemConfigs": []fileSystemConfig{{Arn: testAccessPointARN, LocalMountPath: "/mnt/upd"}},
	})
	helpers.AssertStatus(t, upd, http.StatusOK)
	var updated functionConfigurationWithEFS
	decodeJSON(t, upd, &updated)
	if len(updated.FileSystemConfigs) != 1 {
		t.Fatalf("expected mount after update, got %#v", updated.FileSystemConfigs)
	}

	// Then an explicit empty list clears it.
	clear := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/efs-upd/configuration"), map[string]any{
		"FileSystemConfigs": []fileSystemConfig{},
	})
	helpers.AssertStatus(t, clear, http.StatusOK)
	var cleared functionConfigurationWithEFS
	decodeJSON(t, clear, &cleared)
	if len(cleared.FileSystemConfigs) != 0 {
		t.Fatalf("expected mounts cleared, got %#v", cleared.FileSystemConfigs)
	}
}
