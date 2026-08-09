package lambda_test

// tracing_storage_kms_test.go — TracingConfig, EphemeralStorage and KMSKeyArn.
//
// These three members were added to the CreateFunction/UpdateFunctionConfiguration
// request shapes as *rejected* fields, which turned a working CDK deploy into
// `Create Failed lambda CreateFunction: HTTP 501 NotImplemented` the moment the
// template enabled tracing. They are now stored and echoed, because accepting
// them claims nothing a user can observe as false:
//
//   - X-Ray is not emulated at all, so a tracing mode changes nothing either way.
//   - Ephemeral storage size is recorded, not enforced on the container.
//   - The KMS key is recorded; environment variables are not encrypted at rest.

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/internal/state"
	"github.com/Neaox/overcast/tests/helpers"
)

type tracingConfigResp struct {
	Mode string `json:"Mode"`
}

type ephemeralStorageResp struct {
	Size int `json:"Size"`
}

// advancedFunctionConfiguration decodes only the members these tests assert on.
type advancedFunctionConfiguration struct {
	FunctionName     string                `json:"FunctionName"`
	Description      string                `json:"Description"`
	MemorySize       int                   `json:"MemorySize"`
	Timeout          int                   `json:"Timeout"`
	Runtime          string                `json:"Runtime"`
	RevisionId       string                `json:"RevisionId"`
	TracingConfig    *tracingConfigResp    `json:"TracingConfig"`
	EphemeralStorage *ephemeralStorageResp `json:"EphemeralStorage"`
	KMSKeyArn        string                `json:"KMSKeyArn"`
}

const testKMSKeyArn = "arn:aws:kms:us-east-1:000000000000:key/00000000-0000-0000-0000-000000000000"

// cdkTracingCreateRequest is the request shape a CDK `NodejsFunction` with
// `tracing: Tracing.ACTIVE` emits — the exact body that regressed to 501.
func cdkTracingCreateRequest(name string) map[string]any {
	return map[string]any{
		"FunctionName":  name,
		"Runtime":       "nodejs22.x",
		"Handler":       "index.handler",
		"Role":          "arn:aws:iam::000000000000:role/lambda-role",
		"Code":          map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"MemorySize":    1024,
		"Timeout":       120,
		"Architectures": []string{"arm64"},
		"TracingConfig": map[string]any{"Mode": "Active"},
		"Environment": map[string]any{
			"Variables": map[string]string{"NODE_OPTIONS": "--enable-source-maps"},
		},
		"Layers": []string{
			"arn:aws:lambda:us-east-1:000000000000:layer:powertools:1",
		},
		"VpcConfig": map[string]any{
			"SubnetIds":        []string{"subnet-0123456789abcdef0"},
			"SecurityGroupIds": []string{"sg-0123456789abcdef0"},
		},
		"LoggingConfig": map[string]any{"LogGroup": "/aws/lambda/custom-" + name},
		"Tags":          map[string]string{"aws:cloudformation:stack-name": "MyStack"},
	}
}

// TestCreateFunction_cdkTracingActiveRequestShapeSucceeds reproduces the
// user-reported regression: a CDK-emitted CreateFunction carrying
// TracingConfig alongside layers, VPC config, environment, tags and a custom
// log group must create the function, not answer 501.
func TestCreateFunction_cdkTracingActiveRequestShapeSucceeds(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)

	// When: CDK's CreateFunction body is sent.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), cdkTracingCreateRequest("cdk-traced-fn"))

	// Then: the function is created and the response echoes the tracing mode.
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg advancedFunctionConfiguration
	decodeJSON(t, resp, &cfg)
	if cfg.TracingConfig == nil || cfg.TracingConfig.Mode != "Active" {
		t.Fatalf("CreateFunction TracingConfig = %+v, want Mode=Active", cfg.TracingConfig)
	}
	if cfg.MemorySize != 1024 || cfg.Timeout != 120 || cfg.Runtime != "nodejs22.x" {
		t.Fatalf("CreateFunction echoed = %+v, want MemorySize=1024 Timeout=120 Runtime=nodejs22.x", cfg)
	}
}

// TestFunctionAdvancedConfiguration_roundTripsThroughEveryReadPath pins that
// the three members survive every operation that returns a
// FunctionConfiguration.
func TestFunctionAdvancedConfiguration_roundTripsThroughEveryReadPath(t *testing.T) {
	// Given: a function created with all three members set.
	srv := helpers.NewTestServer(t)
	create := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName": "advanced-fn", "Runtime": "python3.12", "Handler": "index.handler",
		"Role":             "arn:aws:iam::000000000000:role/lambda-role",
		"Code":             map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"TracingConfig":    map[string]any{"Mode": "Active"},
		"EphemeralStorage": map[string]any{"Size": 2048},
		"KMSKeyArn":        testKMSKeyArn,
	})
	helpers.AssertStatus(t, create, http.StatusCreated)
	var created advancedFunctionConfiguration
	decodeJSON(t, create, &created)
	assertAdvancedConfiguration(t, "CreateFunction", created, "Active", 2048, testKMSKeyArn)

	// When/Then: GetFunctionConfiguration returns the same values.
	var config advancedFunctionConfiguration
	decodeJSON(t, doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/advanced-fn/configuration"), nil), &config)
	assertAdvancedConfiguration(t, "GetFunctionConfiguration", config, "Active", 2048, testKMSKeyArn)

	// And: GetFunction's nested Configuration too.
	var get struct {
		Configuration advancedFunctionConfiguration `json:"Configuration"`
	}
	decodeJSON(t, doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/advanced-fn"), nil), &get)
	assertAdvancedConfiguration(t, "GetFunction", get.Configuration, "Active", 2048, testKMSKeyArn)

	// And: UpdateFunctionConfiguration can change each of them.
	update := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/advanced-fn/configuration"), map[string]any{
		"TracingConfig":    map[string]any{"Mode": "PassThrough"},
		"EphemeralStorage": map[string]any{"Size": 10240},
		"KMSKeyArn":        "",
	})
	helpers.AssertStatus(t, update, http.StatusOK)
	var updated advancedFunctionConfiguration
	decodeJSON(t, update, &updated)
	assertAdvancedConfiguration(t, "UpdateFunctionConfiguration", updated, "PassThrough", 10240, "")
}

// TestFunctionAdvancedConfiguration_defaultsMatchAWS pins the values AWS
// reports for a function that set neither member.
func TestFunctionAdvancedConfiguration_defaultsMatchAWS(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "advanced-defaults-fn")

	var config advancedFunctionConfiguration
	decodeJSON(t, doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/advanced-defaults-fn/configuration"), nil), &config)
	assertAdvancedConfiguration(t, "GetFunctionConfiguration", config, "PassThrough", 512, "")
}

// TestFunctionAdvancedConfiguration_survivesRestart proves the members are
// persisted rather than held only in the handler's response. A second server
// over the same store is what a restart looks like from the service's side.
func TestFunctionAdvancedConfiguration_survivesRestart(t *testing.T) {
	// Given: a function created against one server.
	store := state.NewMemoryStore()
	first := helpers.NewTestServer(t, helpers.WithStore(store))
	create := doJSON(t, http.MethodPost, lambdaURL(first, "/functions"), map[string]any{
		"FunctionName": "advanced-restart-fn", "Runtime": "python3.12", "Handler": "index.handler",
		"Role":             "arn:aws:iam::000000000000:role/lambda-role",
		"Code":             map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"TracingConfig":    map[string]any{"Mode": "Active"},
		"EphemeralStorage": map[string]any{"Size": 3072},
		"KMSKeyArn":        testKMSKeyArn,
	})
	helpers.AssertStatus(t, create, http.StatusCreated)
	create.Body.Close()

	// When: a fresh server reads the same persisted state.
	second := helpers.NewTestServer(t, helpers.WithStore(store))
	var config advancedFunctionConfiguration
	decodeJSON(t, doJSON(t, http.MethodGet, lambdaURL(second, "/functions/advanced-restart-fn/configuration"), nil), &config)

	// Then: every member survived.
	assertAdvancedConfiguration(t, "after restart", config, "Active", 3072, testKMSKeyArn)
}

// TestFunctionAdvancedConfiguration_invalidValuesRejectedBeforeMutation checks
// each member's AWS-modeled constraint, on create (nothing is persisted) and on
// update (nothing is changed).
func TestFunctionAdvancedConfiguration_invalidValuesRejectedBeforeMutation(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value any
	}{
		{name: "tracing mode enum", field: "TracingConfig", value: map[string]any{"Mode": "Sampled"}},
		{name: "ephemeral storage below minimum", field: "EphemeralStorage", value: map[string]any{"Size": 511}},
		{name: "ephemeral storage above maximum", field: "EphemeralStorage", value: map[string]any{"Size": 10241}},
		{name: "ephemeral storage missing size", field: "EphemeralStorage", value: map[string]any{}},
		{name: "malformed kms key arn", field: "KMSKeyArn", value: "not-an-arn"},
	}

	for _, tc := range cases {
		t.Run("create/"+tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
				"FunctionName": "invalid-fn", "Runtime": "python3.12", "Handler": "index.handler",
				"Role":   "arn:aws:iam::000000000000:role/lambda-role",
				"Code":   map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
				tc.field: tc.value,
			})
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")

			// And: nothing was persisted.
			get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/invalid-fn/configuration"), nil)
			helpers.AssertStatus(t, get, http.StatusNotFound)
			get.Body.Close()
		})

		t.Run("update/"+tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			createFunction(t, srv, "invalid-update-fn")
			before := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/invalid-update-fn/configuration"), map[string]any{
				"Description": "before",
			})
			helpers.AssertStatus(t, before, http.StatusOK)
			before.Body.Close()

			resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/invalid-update-fn/configuration"), map[string]any{
				"Description": "mutated", tc.field: tc.value,
			})
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")

			// And: the rejected request changed nothing.
			var config advancedFunctionConfiguration
			decodeJSON(t, doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/invalid-update-fn/configuration"), nil), &config)
			if config.Description != "before" {
				t.Fatalf("Description = %q after rejected update, want before", config.Description)
			}
			assertAdvancedConfiguration(t, "after rejected update", config, "PassThrough", 512, "")
		})
	}
}

func assertAdvancedConfiguration(t *testing.T, op string, cfg advancedFunctionConfiguration, mode string, size int, kmsKeyArn string) {
	t.Helper()
	if cfg.TracingConfig == nil || cfg.TracingConfig.Mode != mode {
		t.Errorf("%s TracingConfig = %+v, want Mode=%s", op, cfg.TracingConfig, mode)
	}
	if cfg.EphemeralStorage == nil || cfg.EphemeralStorage.Size != size {
		t.Errorf("%s EphemeralStorage = %+v, want Size=%d", op, cfg.EphemeralStorage, size)
	}
	if cfg.KMSKeyArn != kmsKeyArn {
		t.Errorf("%s KMSKeyArn = %q, want %q", op, cfg.KMSKeyArn, kmsKeyArn)
	}
}
