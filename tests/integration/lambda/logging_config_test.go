package lambda_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func createFunctionWithLoggingConfig(t *testing.T, srv *helpers.TestServer, name string, logging map[string]any) *http.Response {
	t.Helper()
	body := map[string]any{
		"FunctionName": name,
		"Runtime":      "nodejs20.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/r",
		"Code":         map[string]any{"ZipFile": []byte("zip")},
	}
	if logging != nil {
		body["LoggingConfig"] = logging
	}
	return doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), body)
}

func loggingConfigFromResponse(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	decodeJSON(t, resp, &body)
	logging, ok := body["LoggingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("LoggingConfig = %#v", body["LoggingConfig"])
	}
	return logging
}

func TestCreateFunction_LoggingConfigDefaultsAndJSONUnsupported(t *testing.T) {
	// Given: no explicit logging configuration.
	srv := helpers.NewTestServer(t)

	// When: the function is created, Lambda's plain-text defaults are returned.
	resp := createFunctionWithLoggingConfig(t, srv, "text-log-defaults", nil)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	logging := loggingConfigFromResponse(t, resp)
	if logging["LogGroup"] != "/aws/lambda/text-log-defaults" || logging["LogFormat"] != "Text" {
		t.Fatalf("LoggingConfig = %#v", logging)
	}
	if _, ok := logging["ApplicationLogLevel"]; ok {
		t.Fatalf("plain-text config returned ApplicationLogLevel: %#v", logging)
	}
	if _, ok := logging["SystemLogLevel"]; ok {
		t.Fatalf("plain-text config returned SystemLogLevel: %#v", logging)
	}

	// JSON changes both application and platform record formatting. Until #660
	// implements that coherently, the valid AWS setting is rejected honestly.
	resp = createFunctionWithLoggingConfig(t, srv, "json-log-unsupported", map[string]any{"LogFormat": "JSON"})
	assertLambdaUnsupported(t, resp)
	missing := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/json-log-unsupported/configuration"), nil)
	helpers.AssertStatus(t, missing, http.StatusNotFound)
	missing.Body.Close()
}

func TestFunctionConfiguration_LoggingConfigValidationAndNonMutation(t *testing.T) {
	invalid := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{"log format", map[string]any{"LogFormat": "Yaml"}, "1 validation error detected: Value 'Yaml' at 'loggingConfig.logFormat' failed to satisfy constraint: Member must satisfy enum value set: [JSON, Text]"},
		{"application level", map[string]any{"LogFormat": "JSON", "ApplicationLogLevel": "NOTICE"}, "1 validation error detected: Value 'NOTICE' at 'loggingConfig.applicationLogLevel' failed to satisfy constraint: Member must satisfy enum value set: [TRACE, DEBUG, INFO, WARN, ERROR, FATAL]"},
		{"system level", map[string]any{"LogFormat": "JSON", "SystemLogLevel": "ERROR"}, "1 validation error detected: Value 'ERROR' at 'loggingConfig.systemLogLevel' failed to satisfy constraint: Member must satisfy enum value set: [DEBUG, INFO, WARN]"},
		{"empty group", map[string]any{"LogGroup": ""}, "1 validation error detected: Value '' at 'loggingConfig.logGroup' failed to satisfy constraint: Member must have length greater than or equal to 1"},
		{"long group", map[string]any{"LogGroup": strings.Repeat("a", 513)}, "1 validation error detected: Value '" + strings.Repeat("a", 513) + "' at 'loggingConfig.logGroup' failed to satisfy constraint: Member must have length less than or equal to 512"},
		{"group pattern", map[string]any{"LogGroup": "bad group"}, "1 validation error detected: Value 'bad group' at 'loggingConfig.logGroup' failed to satisfy constraint: Member must satisfy regular expression pattern: [\\.\\-_/#A-Za-z0-9]+"},
		{"text application level", map[string]any{"LogFormat": "Text", "ApplicationLogLevel": "INFO"}, "ApplicationLogLevel and SystemLogLevel can only be set for functions configured to use JSON log format."},
		{"implicit text system level", map[string]any{"SystemLogLevel": "WARN"}, "ApplicationLogLevel and SystemLogLevel can only be set for functions configured to use JSON log format."},
	}

	for _, tc := range invalid {
		t.Run("create/"+tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			name := "invalid-logging-" + strings.ReplaceAll(tc.name, " ", "-")
			assertLambdaValidationMessage(t, createFunctionWithLoggingConfig(t, srv, name, tc.config), tc.want)
			missing := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/"+name+"/configuration"), nil)
			helpers.AssertStatus(t, missing, http.StatusNotFound)
			missing.Body.Close()
		})
	}

	srv := helpers.NewTestServer(t)
	created := createFunctionWithLoggingConfig(t, srv, "logging-update-fence", map[string]any{
		"LogGroup": "/custom/original", "LogFormat": "Text",
	})
	helpers.AssertStatus(t, created, http.StatusCreated)
	before := loggingConfigFromResponse(t, created)
	for _, tc := range invalid {
		t.Run("update/"+tc.name, func(t *testing.T) {
			resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/logging-update-fence/configuration"), map[string]any{
				"Description": "must-not-stick", "LoggingConfig": tc.config,
			})
			assertLambdaValidationMessage(t, resp, tc.want)
			get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/logging-update-fence/configuration"), nil)
			helpers.AssertStatus(t, get, http.StatusOK)
			var current map[string]any
			decodeJSON(t, get, &current)
			if current["Description"] == "must-not-stick" {
				t.Fatal("rejected LoggingConfig update mutated Description")
			}
			if got := current["LoggingConfig"].(map[string]any); got["LogFormat"] != before["LogFormat"] ||
				got["LogGroup"] != before["LogGroup"] {
				t.Fatalf("LoggingConfig mutated: got %#v, want %#v", got, before)
			}
		})
	}
}

func TestUpdateFunctionConfiguration_LoggingConfigPresenceDefaults(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFunctionWithLoggingConfig(t, srv, "logging-presence", map[string]any{
		"LogGroup": "/custom/group", "LogFormat": "Text",
	})
	helpers.AssertStatus(t, created, http.StatusCreated)
	created.Body.Close()

	// An explicitly present empty object has uncaptured service semantics. Until
	// #660 captures them, reject it honestly without mutating the function.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/logging-presence/configuration"), map[string]any{
		"Description": "must-not-stick-empty", "LoggingConfig": map[string]any{},
	})
	assertLambdaUnsupported(t, resp)
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/logging-presence/configuration"), nil)
	var afterEmpty map[string]any
	decodeJSON(t, get, &afterEmpty)
	logging := afterEmpty["LoggingConfig"].(map[string]any)
	if logging["LogGroup"] != "/custom/group" || logging["LogFormat"] != "Text" {
		t.Fatalf("empty update mutated LoggingConfig: %#v", logging)
	}
	if afterEmpty["Description"] == "must-not-stick-empty" {
		t.Fatal("empty LoggingConfig update mutated Description")
	}

	// The maximum custom log-group length remains supported in Text mode.
	resp = doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/logging-presence/configuration"), map[string]any{
		"LoggingConfig": map[string]any{"LogGroup": strings.Repeat("g", 512), "LogFormat": "Text"},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	logging = loggingConfigFromResponse(t, resp)
	if logging["LogFormat"] != "Text" || logging["LogGroup"] != strings.Repeat("g", 512) {
		t.Fatalf("updated LoggingConfig = %#v", logging)
	}

	// Valid JSON defaults are modeled, but applying them remains unsupported
	// until #660 can reproduce JSON platform records and filtering end to end.
	resp = doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/logging-presence/configuration"), map[string]any{
		"Description": "must-not-stick", "LoggingConfig": map[string]any{"LogFormat": "JSON"},
	})
	assertLambdaUnsupported(t, resp)
	get = doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/logging-presence/configuration"), nil)
	var current map[string]any
	decodeJSON(t, get, &current)
	if current["Description"] == "must-not-stick" {
		t.Fatal("unsupported JSON logging update mutated Description")
	}
}

func TestFunctionConfiguration_LoggingUnsupportedWaitsForModeledValidation(t *testing.T) {
	// A valid-but-unsupported JSON logging request must not mask an earlier
	// modeled CreateFunction constraint failure.
	srv := helpers.NewTestServer(t)
	invalidCreate := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName": "mixed-invalid-logging", "Runtime": "nodejs20.x", "Handler": "index.handler",
		"Role": "arn:aws:iam::000000000000:role/r", "Code": map[string]any{"ZipFile": []byte("zip")},
		"Timeout": 0, "LoggingConfig": map[string]any{"LogFormat": "JSON"},
	})
	assertLambdaValidationMessage(t, invalidCreate, "1 validation error detected: Value '0' at 'timeout' failed to satisfy constraint: Member must have value greater than or equal to 1")
	missing := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/mixed-invalid-logging/configuration"), nil)
	helpers.AssertStatus(t, missing, http.StatusNotFound)
	missing.Body.Close()

	created := createFunctionWithLoggingConfig(t, srv, "mixed-invalid-update", map[string]any{"LogFormat": "Text"})
	helpers.AssertStatus(t, created, http.StatusCreated)
	created.Body.Close()

	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "json with invalid timeout",
			body: map[string]any{"Description": "must-not-stick", "Timeout": 0, "LoggingConfig": map[string]any{"LogFormat": "JSON"}},
			want: "1 validation error detected: Value '0' at 'timeout' failed to satisfy constraint: Member must have value greater than or equal to 1",
		},
		{
			name: "empty with invalid memory",
			body: map[string]any{"Description": "must-not-stick", "MemorySize": 127, "LoggingConfig": map[string]any{}},
			want: "1 validation error detected: Value '127' at 'memorySize' failed to satisfy constraint: Member must have value greater than or equal to 128",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/mixed-invalid-update/configuration"), tc.body)
			assertLambdaValidationMessage(t, resp, tc.want)
			get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/mixed-invalid-update/configuration"), nil)
			var current map[string]any
			decodeJSON(t, get, &current)
			if current["Description"] == "must-not-stick" {
				t.Fatal("mixed-invalid logging update mutated Description")
			}
			if current["Timeout"] != float64(3) || current["MemorySize"] != float64(128) {
				t.Fatalf("mixed-invalid logging update mutated limits: timeout=%v memory=%v", current["Timeout"], current["MemorySize"])
			}
			logging := current["LoggingConfig"].(map[string]any)
			if logging["LogFormat"] != "Text" {
				t.Fatalf("mixed-invalid update mutated LoggingConfig: %#v", logging)
			}
		})
	}
}
