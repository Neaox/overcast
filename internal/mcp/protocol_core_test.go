package mcp

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDefaultServerCapabilities_AdvertisesImplementedSet(t *testing.T) {
	caps := defaultServerCapabilities()
	if caps.Tools == nil || caps.Resources == nil || caps.Prompts == nil || caps.Completions == nil || caps.Logging == nil {
		t.Fatalf("expected implemented capability set, got %#v", caps)
	}
	if caps.Resources.Subscribe != true {
		t.Fatalf("resources.subscribe = %v, want true", caps.Resources.Subscribe)
	}
	if caps.Tasks != nil {
		t.Fatalf("tasks capability must remain unadvertised, got %#v", caps.Tasks)
	}
}

func TestValidateOptionalSessionHeader(t *testing.T) {
	srv := NewServer(nil, nil)
	srv.sessions["known"] = time.Now()

	if err := srv.validateOptionalSessionHeader(""); err != nil {
		t.Fatalf("empty session header error = %v, want nil", err)
	}
	if err := srv.validateOptionalSessionHeader(" known "); err != nil {
		t.Fatalf("known session header error = %v, want nil", err)
	}
	if err := srv.validateOptionalSessionHeader("missing"); err != errUnknownSessionHeader {
		t.Fatalf("missing session error = %v, want %v", err, errUnknownSessionHeader)
	}
}

func TestValidateProtocolVersionHeader(t *testing.T) {
	if err := validateProtocolVersionHeader(""); err != nil {
		t.Fatalf("empty header error = %#v, want nil", err)
	}
	if err := validateProtocolVersionHeader(ProtocolVersion); err != nil {
		t.Fatalf("supported header error = %#v, want nil", err)
	}
	err := validateProtocolVersionHeader("2023-01-01")
	if err == nil {
		t.Fatal("expected unsupported version error")
	}
	if err.Code != RPCInvalidParams {
		t.Fatalf("error code = %d, want %d", err.Code, RPCInvalidParams)
	}
	data, _ := err.Data.(map[string]any)
	if data["requested"] != "2023-01-01" {
		t.Fatalf("requested = %v, want 2023-01-01", data["requested"])
	}
}

func TestValidateLifecycle(t *testing.T) {
	if err := validateLifecycle(false, false); err == nil || err.Code != RPCInvalidRequest {
		t.Fatalf("expected invalid-request error before initialize, got %#v", err)
	}
	if err := validateLifecycle(true, false); err == nil || err.Code != RPCInvalidRequest {
		t.Fatalf("expected invalid-request error before initialized notification, got %#v", err)
	}
	if err := validateLifecycle(true, true); err != nil {
		t.Fatalf("ready lifecycle error = %#v, want nil", err)
	}
}

func TestPaginateRange(t *testing.T) {
	start, end, next, err := paginateRange(5, "", 2)
	if err != nil || start != 0 || end != 2 || next != "2" {
		t.Fatalf("first page = (%d,%d,%q,%#v), want (0,2,\"2\",nil)", start, end, next, err)
	}
	start, end, next, err = paginateRange(5, "2", 10)
	if err != nil || start != 2 || end != 5 || next != "" {
		t.Fatalf("second page = (%d,%d,%q,%#v), want (2,5,\"\",nil)", start, end, next, err)
	}
	if _, _, _, err = paginateRange(5, "-1", 1); err == nil || err.Code != RPCInvalidParams {
		t.Fatalf("expected invalid cursor error, got %#v", err)
	}
}

func TestDecodeListParams(t *testing.T) {
	params, err := decodeListParams(nil)
	if err != nil {
		t.Fatalf("nil params error = %#v, want nil", err)
	}
	if params.Cursor != "" || params.Limit != 0 {
		t.Fatalf("nil params decoded = %#v, want zero values", params)
	}

	params, err = decodeListParams(json.RawMessage(`{"cursor":"2","limit":3}`))
	if err != nil {
		t.Fatalf("decoded params error = %#v, want nil", err)
	}
	if params.Cursor != "2" || params.Limit != 3 {
		t.Fatalf("decoded params = %#v, want cursor=2 limit=3", params)
	}

	_, err = decodeListParams(json.RawMessage(`{"limit":`))
	if err == nil || err.Code != RPCInvalidParams {
		t.Fatalf("invalid params error = %#v, want invalid params", err)
	}
}

func TestDecodeOptionalParams(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
	}

	decoded, err := decodeOptionalParams[sample](nil)
	if err != nil {
		t.Fatalf("nil optional params error = %#v, want nil", err)
	}
	if decoded.Name != "" {
		t.Fatalf("nil optional params decoded = %#v, want zero value", decoded)
	}

	decoded, err = decodeOptionalParams[sample](json.RawMessage(`{"name":"ok"}`))
	if err != nil {
		t.Fatalf("valid optional params error = %#v, want nil", err)
	}
	if decoded.Name != "ok" {
		t.Fatalf("valid optional params decoded = %#v, want name=ok", decoded)
	}

	_, err = decodeOptionalParams[sample](json.RawMessage(`{"name":`))
	if err == nil || err.Code != RPCInvalidParams {
		t.Fatalf("invalid optional params error = %#v, want invalid params", err)
	}
}

func TestDecodeRequiredParams(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
	}

	_, err := decodeRequiredParams[sample](nil, "example/method")
	if err == nil || err.Code != RPCInvalidParams || err.Message != "example/method params required" {
		t.Fatalf("missing required params error = %#v, want method params required", err)
	}

	decoded, err := decodeRequiredParams[sample](json.RawMessage(`{"name":"ok"}`), "example/method")
	if err != nil {
		t.Fatalf("valid required params error = %#v, want nil", err)
	}
	if decoded.Name != "ok" {
		t.Fatalf("valid required params decoded = %#v, want name=ok", decoded)
	}

	_, err = decodeRequiredParams[sample](json.RawMessage(`{"name":`), "example/method")
	if err == nil || err.Code != RPCInvalidParams {
		t.Fatalf("invalid required params error = %#v, want invalid params", err)
	}
}

func TestDecodeRequiredURIParam(t *testing.T) {
	uri, err := decodeRequiredURIParam(json.RawMessage(`{"uri":"  file:///workspace/README.md  "}`), "resources/read")
	if err != nil {
		t.Fatalf("valid URI param error = %#v, want nil", err)
	}
	if uri != "file:///workspace/README.md" {
		t.Fatalf("normalized URI = %q, want file:///workspace/README.md", uri)
	}

	_, err = decodeRequiredURIParam(nil, "resources/read")
	if err == nil || err.Code != RPCInvalidParams || err.Message != "resources/read params required" {
		t.Fatalf("missing URI params error = %#v, want params required", err)
	}

	_, err = decodeRequiredURIParam(json.RawMessage(`{"uri":"   "}`), "resources/read")
	if err == nil || err.Code != RPCInvalidParams || err.Message != "resources/read uri required" {
		t.Fatalf("blank URI error = %#v, want uri required", err)
	}
}

func TestUniquePrefixMatches(t *testing.T) {
	values := uniquePrefixMatches([]string{"example", "example", "validate_next_step", ""}, "")
	if len(values) != 2 {
		t.Fatalf("values len = %d, want 2", len(values))
	}
	if values[0] != "example" || values[1] != "validate_next_step" {
		t.Fatalf("values = %#v, want [example validate_next_step]", values)
	}

	values = uniquePrefixMatches([]string{"example", "validate_next_step", "value"}, "val")
	if len(values) != 2 || values[0] != "validate_next_step" || values[1] != "value" {
		t.Fatalf("prefixed values = %#v, want [validate_next_step value]", values)
	}

	values = uniquePrefixMatches([]string{"example", "validate_next_step", "value"}, "next")
	if len(values) != 1 || values[0] != "validate_next_step" {
		t.Fatalf("contains fallback values = %#v, want [validate_next_step]", values)
	}

	values = uniquePrefixMatches([]string{"example", "validate_next_step", "value"}, "  VAL  ")
	if len(values) != 2 || values[0] != "validate_next_step" || values[1] != "value" {
		t.Fatalf("trimmed uppercase values = %#v, want [validate_next_step value]", values)
	}
}

func TestPaginatedListResult(t *testing.T) {
	result, err := paginatedListResult(json.RawMessage(`{"limit":1}`), "items", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("first page error = %#v, want nil", err)
	}
	items := result["items"].([]string)
	if len(items) != 1 || items[0] != "alpha" {
		t.Fatalf("first page items = %#v, want [alpha]", items)
	}
	if result["nextCursor"] != "1" {
		t.Fatalf("first page nextCursor = %v, want 1", result["nextCursor"])
	}

	result, err = paginatedListResult(json.RawMessage(`{"cursor":"1","limit":1}`), "items", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("second page error = %#v, want nil", err)
	}
	items = result["items"].([]string)
	if len(items) != 1 || items[0] != "beta" {
		t.Fatalf("second page items = %#v, want [beta]", items)
	}
	if _, ok := result["nextCursor"]; ok {
		t.Fatalf("second page nextCursor = %v, want omitted", result["nextCursor"])
	}
}

func TestNormalizeToolResult(t *testing.T) {
	execErr := errors.New("boom")
	errResult := normalizeToolResult(nil, execErr)
	if errResult.IsError != true {
		t.Fatalf("error result IsError = %v, want true", errResult.IsError)
	}
	errContent := errResult.Content[0]["text"]
	if errContent != "boom" {
		t.Fatalf("error result content text = %v, want boom", errContent)
	}
	errStructured, ok := errResult.StructuredContent.(map[string]any)
	if !ok || errStructured["error"] != "boom" {
		t.Fatalf("error result structuredContent = %#v, want error map", errResult.StructuredContent)
	}

	passthrough := ToolResult{Content: TextContent("ok"), StructuredContent: map[string]any{"k": "v"}}
	passResult := normalizeToolResult(passthrough, nil)
	if passResult.Content[0]["text"] != "ok" {
		t.Fatalf("passthrough content = %#v, want ok", passResult.Content)
	}

	normalized := normalizeToolResult(map[string]any{"ok": true}, nil)
	normContent := normalized.Content[0]["text"]
	if normContent == "" {
		t.Fatal("normalized content text must be non-empty")
	}
	normStructured, ok := normalized.StructuredContent.(map[string]any)
	if !ok || normStructured["ok"] != true {
		t.Fatalf("normalized structuredContent = %#v, want map[ok:true]", normalized.StructuredContent)
	}
}

func TestNormalizeLoggingLevel(t *testing.T) {
	level, err := normalizeLoggingLevel("  WARNING ")
	if err != nil {
		t.Fatalf("normalize warning error = %#v, want nil", err)
	}
	if level != "warning" {
		t.Fatalf("normalize warning = %q, want warning", level)
	}

	level, err = normalizeLoggingLevel("warn")
	if err != nil {
		t.Fatalf("normalize warn alias error = %#v, want nil", err)
	}
	if level != "warning" {
		t.Fatalf("normalize warn alias = %q, want warning", level)
	}

	_, err = normalizeLoggingLevel("verbose")
	if err == nil || err.Code != RPCInvalidParams {
		t.Fatalf("normalize verbose error = %#v, want invalid params", err)
	}
}

func TestLoggingLevelRank(t *testing.T) {
	if got := loggingLevelRank("debug"); got != 7 {
		t.Fatalf("debug rank = %d, want 7", got)
	}
	if got := loggingLevelRank("error"); got != 3 {
		t.Fatalf("error rank = %d, want 3", got)
	}
	if got := loggingLevelRank("unknown"); got != 6 {
		t.Fatalf("unknown rank = %d, want 6 (info)", got)
	}
}

func TestWriteJSONRPCError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeJSONRPCError(recorder, 42, &rpcError{Code: RPCInvalidParams, Message: "bad"})
	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if body := recorder.Body.String(); body == "" {
		t.Fatal("expected encoded JSON-RPC error body")
	}
}
