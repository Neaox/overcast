package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoProviderRegistersExpectedTools(t *testing.T) {
	provider := NewRepoProvider(t.TempDir())
	have := make(map[string]bool)
	toolsByName := make(map[string]Tool)
	for _, tool := range provider.Tools() {
		have[tool.Name] = true
		toolsByName[tool.Name] = tool
	}
	for _, name := range []string{
		"repo_workspace_info",
		"repo_build_commands",
		"repo_find_todos",
		"repo_service_coverage",
		"repo_service_files",
		"repo_doc_coverage",
		"repo_cloudformation_links",
		"repo_service_manifest",
		"repo_operation_support",
		"repo_find_symbol",
		"repo_conventions_snapshot",
		"repo_topology_contributors",
		"repo_related_files",
		"repo_endpoint_map",
		"repo_change_impact",
		"repo_test_targets",
		"repo_compat_rerun_subset",
		"runtime_list_instances",
		"runtime_probe_instance",
		"runtime_refresh_probe_cache",
		"runtime_mcp_call",
		"runtime_list_services",
		"runtime_get_health",
		"runtime_get_config",
		"runtime_get_service_state",
		"runtime_get_recent_events",
		"runtime_probe_kv_store",
		"repo_list_files",
		"repo_read_file_snippet",
		"repo_service_capabilities",
	} {
		if !have[name] {
			t.Fatalf("expected tool %q to be registered", name)
		}
	}
	for _, name := range []string{"workspace_server_info", "repo_workspace_info", "repo_build_commands", "repo_find_todos", "repo_service_coverage", "repo_service_files", "repo_doc_coverage", "repo_cloudformation_links", "repo_service_manifest", "repo_operation_support", "repo_find_symbol", "repo_conventions_snapshot", "repo_topology_contributors", "repo_related_files", "repo_endpoint_map", "repo_change_impact", "repo_test_targets", "repo_compat_rerun_subset", "runtime_mcp_call", "repo_list_files", "repo_read_file_snippet", "repo_service_capabilities"} {
		if len(toolsByName[name].OutputSchema) == 0 {
			t.Fatalf("expected tool %q to advertise outputSchema", name)
		}
	}
	compatRerun := toolsByName["repo_compat_rerun_subset"]
	if compatRerun.Execution["readOnlyHint"] != false {
		t.Fatalf("repo_compat_rerun_subset execution.readOnlyHint = %v, want false", compatRerun.Execution["readOnlyHint"])
	}
	if compatRerun.Execution["openWorldHint"] != false {
		t.Fatalf("repo_compat_rerun_subset execution.openWorldHint = %v, want false", compatRerun.Execution["openWorldHint"])
	}
	if len(toolsByName["runtime_list_instances"].OutputSchema) == 0 {
		t.Fatal("expected runtime_list_instances to advertise outputSchema")
	}
	runtimeProbe := toolsByName["runtime_probe_instance"]
	if runtimeProbe.Execution["openWorldHint"] != true {
		t.Fatalf("runtime_probe_instance execution.openWorldHint = %v, want true", runtimeProbe.Execution["openWorldHint"])
	}
	if runtimeProbe.Execution["readOnlyHint"] != true {
		t.Fatalf("runtime_probe_instance execution.readOnlyHint = %v, want true", runtimeProbe.Execution["readOnlyHint"])
	}
	runtimeCall := toolsByName["runtime_mcp_call"]
	if runtimeCall.Annotations["readOnlyHint"] != false {
		t.Fatalf("runtime_mcp_call annotations.readOnlyHint = %v, want false", runtimeCall.Annotations["readOnlyHint"])
	}
	if runtimeCall.Execution["readOnlyHint"] != false {
		t.Fatalf("runtime_mcp_call execution.readOnlyHint = %v, want false", runtimeCall.Execution["readOnlyHint"])
	}
	if runtimeCall.Execution["destructiveHint"] != true {
		t.Fatalf("runtime_mcp_call execution.destructiveHint = %v, want true", runtimeCall.Execution["destructiveHint"])
	}
	if runtimeCall.Execution["openWorldHint"] != true {
		t.Fatalf("runtime_mcp_call execution.openWorldHint = %v, want true", runtimeCall.Execution["openWorldHint"])
	}
	refreshProbeCache := toolsByName["runtime_refresh_probe_cache"]
	if len(refreshProbeCache.OutputSchema) == 0 {
		t.Fatal("expected runtime_refresh_probe_cache to advertise outputSchema")
	}
	if refreshProbeCache.Execution["readOnlyHint"] != false {
		t.Fatalf("expected runtime_refresh_probe_cache execution.readOnlyHint=false, got %v", refreshProbeCache.Execution["readOnlyHint"])
	}
	if refreshProbeCache.Execution["openWorldHint"] != false {
		t.Fatalf("expected runtime_refresh_probe_cache execution.openWorldHint=false, got %v", refreshProbeCache.Execution["openWorldHint"])
	}
	for _, name := range []string{"runtime_list_services", "runtime_get_health", "runtime_get_config", "runtime_get_service_state", "runtime_get_recent_events", "runtime_probe_kv_store"} {
		tool := toolsByName[name]
		if len(tool.OutputSchema) == 0 {
			t.Fatalf("expected tool %q to advertise outputSchema", name)
		}
		if tool.Execution["readOnlyHint"] != true {
			t.Fatalf("expected %q execution.readOnlyHint=true, got %v", name, tool.Execution["readOnlyHint"])
		}
		if tool.Execution["openWorldHint"] != true {
			t.Fatalf("expected %q execution.openWorldHint=true, got %v", name, tool.Execution["openWorldHint"])
		}
	}
}

func TestRepoReadFileSnippetReturnsExpectedRange(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "docs/sample.txt", "line1\nline2\nline3\nline4\n")
	params, _ := json.Marshal(map[string]any{"path": "docs/sample.txt", "start_line": 2, "end_line": 3})
	out, err := provider.toolReadFileSnippet(context.Background(), params)
	if err != nil {
		t.Fatalf("toolReadFileSnippet() error = %v", err)
	}
	got := out.(map[string]any)
	if got["content"] != "line2\nline3" {
		t.Fatalf("unexpected content: %v", got["content"])
	}
	if got["line_count"] != 2 {
		t.Fatalf("unexpected line_count: %v", got["line_count"])
	}
}

func TestRepoReadFileSnippetRejectsPathOutsideWorkspace(t *testing.T) {
	provider := NewRepoProvider(t.TempDir())
	params, _ := json.Marshal(map[string]any{"path": "../outside.txt"})
	_, err := provider.toolReadFileSnippet(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for path outside workspace")
	}
}

func TestRepoBuildCommandsParsesMakefile(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "Makefile", "## build: compile the binary\n## test: run all tests\n")
	out, err := provider.toolBuildCommands(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolBuildCommands() error = %v", err)
	}
	toolResult, ok := out.(ToolResult)
	if !ok {
		t.Fatalf("toolBuildCommands() type = %T, want ToolResult", out)
	}
	got := toolResult.StructuredContent.(map[string]any)
	commands := got["commands"].([]commandEntry)
	if len(commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(commands))
	}
	if commands[0].Target != "build" || commands[0].Command != "make build" {
		t.Fatalf("unexpected first command: %#v", commands[0])
	}
	if len(toolResult.Content) == 0 {
		t.Fatal("expected text content summary")
	}
}

func TestRepoCompatRerunSubsetRejectsUnknownSuite(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "compat/suites/node-js-sdk/.keep", "")
	provider := NewRepoProvider(root)
	params, _ := json.Marshal(map[string]any{"suites": []string{"unknown-suite"}})
	_, err := provider.toolCompatRerunSubset(context.Background(), params)
	if err == nil {
		t.Fatal("expected allowlist validation error for unknown suite")
	}
}

func TestRepoCompatRerunSubsetRunsBoundedPreview(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "compat/suites/node-js-sdk/.keep", "")
	provider := NewRepoProvider(root)
	provider.compatRunner = func(_ context.Context, args []string) ([]byte, int, error) {
		if len(args) == 0 || args[0] != "run" {
			t.Fatalf("unexpected compat args: %#v", args)
		}
		if !strings.Contains(strings.Join(args, " "), "--suite node-js-sdk") {
			t.Fatalf("expected node-js-sdk suite in args, got %#v", args)
		}
		return []byte("line1\nline2\nline3\nline4\n"), 0, nil
	}

	params, _ := json.Marshal(map[string]any{
		"suites":           []string{"node-js-sdk"},
		"endpoint":         "http://localhost:4566",
		"max_output_lines": 2,
	})
	out, err := provider.toolCompatRerunSubset(context.Background(), params)
	if err != nil {
		t.Fatalf("toolCompatRerunSubset() error = %v", err)
	}
	got := out.(map[string]any)
	if got["success"] != true {
		t.Fatalf("expected success=true, got %#v", got)
	}
	if got["exit_code"].(int) != 0 {
		t.Fatalf("expected exit_code=0, got %#v", got["exit_code"])
	}
	preview := got["output_preview"].([]string)
	if len(preview) != 2 || preview[0] != "line1" || preview[1] != "line2" {
		t.Fatalf("unexpected output_preview: %#v", preview)
	}
	if got["truncated"] != true {
		t.Fatalf("expected truncated=true, got %#v", got["truncated"])
	}
}

func TestRepoFindTodosParsesAndGroupsEntries(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "internal/services/sqs/handler.go", `package sqs

// TODO(priority:P2): implement ChangeMessageVisibilityBatch
func handle() {}
`)
	writeTestFile(t, root, "tests/helpers/server.go", `package helpers

// TODO(perf): share a single Docker client
func helper() {}
`)
	writeTestFile(t, root, "docs/sdk-cli.md", `cfg, _ := config.LoadDefaultConfig(context.TODO(), x)`)

	out, err := provider.toolFindTodos(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolFindTodos() error = %v", err)
	}
	got := out.(map[string]any)
	todos := got["todos"].([]todoEntry)
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %#v", todos)
	}
	if got["total_matches"].(int) != 2 {
		t.Fatalf("unexpected total_matches: %v", got["total_matches"])
	}
	if todos[0].Service != "sqs" && todos[1].Service != "sqs" {
		t.Fatalf("expected one sqs todo, got %#v", todos)
	}
	byPriority := got["by_priority"].(map[string]int)
	if byPriority["P2"] != 1 {
		t.Fatalf("unexpected by_priority: %#v", byPriority)
	}
	byLocation := got["by_location"].(map[string]int)
	if byLocation["internal_service"] != 1 || byLocation["tests"] != 1 {
		t.Fatalf("unexpected by_location: %#v", byLocation)
	}
	byService := got["by_service"].(map[string]int)
	if byService["sqs"] != 1 {
		t.Fatalf("unexpected by_service: %#v", byService)
	}
	for _, todo := range todos {
		if strings.Contains(todo.Text, "context.TODO") {
			t.Fatalf("context.TODO() false positive was not filtered: %#v", todo)
		}
	}
}

func TestRepoFindTodosFiltersByServicePriorityAndLimit(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "internal/services/sqs/handler.go", `package sqs

// TODO(priority:P2): first item
// TODO(priority:P2): second item
`)
	writeTestFile(t, root, "internal/services/s3/handler.go", `package s3

// TODO(priority:P1): other service item
`)
	params, _ := json.Marshal(map[string]any{"service": "sqs", "priority": "P2", "max_results": 1})
	out, err := provider.toolFindTodos(context.Background(), params)
	if err != nil {
		t.Fatalf("toolFindTodos() error = %v", err)
	}
	got := out.(map[string]any)
	if got["count"].(int) != 1 {
		t.Fatalf("unexpected count: %v", got["count"])
	}
	if got["total_matches"].(int) != 2 {
		t.Fatalf("unexpected total_matches: %v", got["total_matches"])
	}
	if !got["truncated"].(bool) {
		t.Fatal("expected truncated=true")
	}
	todos := got["todos"].([]todoEntry)
	if len(todos) != 1 || todos[0].Service != "sqs" || todos[0].Priority != "P2" {
		t.Fatalf("unexpected filtered todos: %#v", todos)
	}
	byService := got["by_service"].(map[string]int)
	if len(byService) != 1 || byService["sqs"] != 2 {
		t.Fatalf("unexpected by_service: %#v", byService)
	}
	byPriority := got["by_priority"].(map[string]int)
	if len(byPriority) != 1 || byPriority["P2"] != 2 {
		t.Fatalf("unexpected by_priority: %#v", byPriority)
	}
}

func TestRepoFindTodosCanonicalOnlyAndTagGrouping(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "internal/services/sqs/handler.go", `package sqs

// TODO(priority:P1): implement queue policy validation
// TODO(perf): batch lookup cache
// FIXME(priority:P2): align wire error shape
`)

	params, _ := json.Marshal(map[string]any{"canonical_only": true})
	out, err := provider.toolFindTodos(context.Background(), params)
	if err != nil {
		t.Fatalf("toolFindTodos() error = %v", err)
	}
	got := out.(map[string]any)
	if got["count"].(int) != 2 || got["total_matches"].(int) != 2 {
		t.Fatalf("unexpected counts: %#v", got)
	}
	if got["canonical_count"].(int) != 2 {
		t.Fatalf("unexpected canonical_count: %v", got["canonical_count"])
	}
	todos := got["todos"].([]todoEntry)
	for _, todo := range todos {
		if !todo.Canonical {
			t.Fatalf("expected canonical-only results, got %#v", todo)
		}
		if todo.Tag != "" {
			t.Fatalf("unexpected legacy tag in canonical-only result: %#v", todo)
		}
	}
	byTag := got["by_tag"].(map[string]int)
	if len(byTag) != 0 {
		t.Fatalf("expected no by_tag entries for canonical-only results, got %#v", byTag)
	}

	allOut, err := provider.toolFindTodos(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolFindTodos() error = %v", err)
	}
	all := allOut.(map[string]any)
	if all["canonical_count"].(int) != 2 {
		t.Fatalf("unexpected canonical_count in full scan: %v", all["canonical_count"])
	}
	allByTag := all["by_tag"].(map[string]int)
	if allByTag["perf"] != 1 {
		t.Fatalf("unexpected by_tag: %#v", allByTag)
	}
}

func TestRepoServiceFilesIncludesExpectedPaths(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"internal/services/demo/service.go",
		"internal/services/demo/handler.go",
		"tests/integration/demo/demo_test.go",
		"docs/services/demo.md",
		"web/src/services/api/demo.ts",
		"web/src/lib/search-contributors/demo.ts",
		"web/src/routes/demo/index.tsx",
	} {
		writeTestFile(t, root, rel, "test")
	}
	params, _ := json.Marshal(map[string]any{"service": "demo"})
	out, err := provider.toolServiceFiles(context.Background(), params)
	if err != nil {
		t.Fatalf("toolServiceFiles() error = %v", err)
	}
	got := out.(map[string]any)
	internalFiles := got["internal_files"].([]string)
	if len(internalFiles) != 2 {
		t.Fatalf("expected 2 internal files, got %d", len(internalFiles))
	}
	if got["integration_test"] != "tests/integration/demo/demo_test.go" {
		t.Fatalf("unexpected integration test path: %v", got["integration_test"])
	}
	if got["service_doc"] != "docs/services/demo.md" {
		t.Fatalf("unexpected service doc path: %v", got["service_doc"])
	}
	if got["web_api"] != "web/src/services/api/demo.ts" {
		t.Fatalf("unexpected web api path: %v", got["web_api"])
	}
	if got["search_contributor"] != "web/src/lib/search-contributors/demo.ts" {
		t.Fatalf("unexpected search contributor path: %v", got["search_contributor"])
	}
	routes := got["web_routes"].([]string)
	if len(routes) != 1 || routes[0] != "web/src/routes/demo/index.tsx" {
		t.Fatalf("unexpected web routes: %#v", routes)
	}
}

func TestRepoChangeImpactIncludesServiceArtifacts(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"internal/services/demo/service.go",
		"tests/integration/demo/demo_test.go",
		"docs/services/demo.md",
		"web/src/services/api/demo.ts",
	} {
		writeTestFile(t, root, rel, "test")
	}
	params, _ := json.Marshal(map[string]any{"paths": []string{"internal/services/demo/service.go"}})
	out, err := provider.toolChangeImpact(context.Background(), params)
	if err != nil {
		t.Fatalf("toolChangeImpact() error = %v", err)
	}
	got := out.(map[string]any)
	services := got["services"].([]string)
	if len(services) != 1 || services[0] != "demo" {
		t.Fatalf("unexpected services: %#v", services)
	}
	recommended := got["recommended_tests"].([]string)
	if len(recommended) == 0 || recommended[0] != "go test ./tests/integration/demo/..." {
		t.Fatalf("unexpected recommended tests: %#v", recommended)
	}
	commands := got["recommended_commands"].([]string)
	haveMakeCheck := false
	haveUnit := false
	haveIntegration := false
	for _, cmd := range commands {
		if cmd == "make check" {
			haveMakeCheck = true
		}
		if cmd == "make test-unit" {
			haveUnit = true
		}
		if cmd == "make test-integration" {
			haveIntegration = true
		}
	}
	if !haveMakeCheck || !haveUnit || !haveIntegration {
		t.Fatalf("expected full validation command set, got commands=%#v", commands)
	}
	filesByRole := got["files_by_role"].(map[string][]string)
	if len(filesByRole["docs"]) != 1 || filesByRole["docs"][0] != "docs/services/demo.md" {
		t.Fatalf("unexpected docs mapping: %#v", filesByRole["docs"])
	}
	testFileMap := got["test_file_map"].(map[string][]string)
	mapped := testFileMap["internal/services/demo/service.go"]
	if len(mapped) != 1 || mapped[0] != "tests/integration/demo/demo_test.go" {
		t.Fatalf("unexpected test file mapping: %#v", testFileMap)
	}
}

func TestRepoChangeImpactMapsSharedPackageTests(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"internal/middleware/request_id.go",
		"internal/middleware/request_id_test.go",
	} {
		writeTestFile(t, root, rel, "test")
	}
	params, _ := json.Marshal(map[string]any{"paths": []string{"internal/middleware/request_id.go"}})
	out, err := provider.toolChangeImpact(context.Background(), params)
	if err != nil {
		t.Fatalf("toolChangeImpact() error = %v", err)
	}
	got := out.(map[string]any)
	testFileMap := got["test_file_map"].(map[string][]string)
	mapped := testFileMap["internal/middleware/request_id.go"]
	if len(mapped) != 1 || mapped[0] != "internal/middleware/request_id_test.go" {
		t.Fatalf("unexpected shared package test mapping: %#v", testFileMap)
	}
}

func TestRepoTestTargetsMapsPackages(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"internal/services/demo/service.go",
		"tests/integration/demo/demo_test.go",
		"web/src/routes/demo/index.tsx",
	} {
		writeTestFile(t, root, rel, "test")
	}
	params, _ := json.Marshal(map[string]any{"paths": []string{
		"internal/services/demo/service.go",
		"tests/integration/demo/demo_test.go",
		"web/src/routes/demo/index.tsx",
	}})
	out, err := provider.toolTestTargets(context.Background(), params)
	if err != nil {
		t.Fatalf("toolTestTargets() error = %v", err)
	}
	got := out.(map[string]any)
	unitPkgs := got["unit_packages"].([]string)
	if len(unitPkgs) != 1 || unitPkgs[0] != "./internal/services/demo/..." {
		t.Fatalf("unexpected unit packages: %#v", unitPkgs)
	}
	integrationPkgs := got["integration_packages"].([]string)
	if len(integrationPkgs) != 1 || integrationPkgs[0] != "./tests/integration/demo/..." {
		t.Fatalf("unexpected integration packages: %#v", integrationPkgs)
	}
	testFileMap := got["test_file_map"].(map[string][]string)
	mapped := testFileMap["internal/services/demo/service.go"]
	if len(mapped) != 1 || mapped[0] != "tests/integration/demo/demo_test.go" {
		t.Fatalf("unexpected test file mapping: %#v", testFileMap)
	}
	commands := got["recommended_commands"].([]string)
	haveWebBuild := false
	for _, cmd := range commands {
		if cmd == "cd web && npm run build" {
			haveWebBuild = true
			break
		}
	}
	if !haveWebBuild {
		t.Fatalf("expected web build command, got: %#v", commands)
	}
	haveMakeCheck := false
	haveUnit := false
	haveIntegration := false
	for _, cmd := range commands {
		if cmd == "make check" {
			haveMakeCheck = true
		}
		if cmd == "make test-unit" {
			haveUnit = true
		}
		if cmd == "make test-integration" {
			haveIntegration = true
		}
	}
	if !haveMakeCheck || !haveUnit || !haveIntegration {
		t.Fatalf("expected full validation command recommendations, got: %#v", commands)
	}
}

func TestRepoTestTargetsMapsSharedPackageTests(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"internal/router/router.go",
		"internal/router/router_test.go",
	} {
		writeTestFile(t, root, rel, "test")
	}
	params, _ := json.Marshal(map[string]any{"paths": []string{"internal/router/router.go"}})
	out, err := provider.toolTestTargets(context.Background(), params)
	if err != nil {
		t.Fatalf("toolTestTargets() error = %v", err)
	}
	got := out.(map[string]any)
	unitPkgs := got["unit_packages"].([]string)
	if len(unitPkgs) != 1 || unitPkgs[0] != "./internal/router/..." {
		t.Fatalf("unexpected unit packages: %#v", unitPkgs)
	}
	testFileMap := got["test_file_map"].(map[string][]string)
	mapped := testFileMap["internal/router/router.go"]
	if len(mapped) != 1 || mapped[0] != "internal/router/router_test.go" {
		t.Fatalf("unexpected shared package test mapping: %#v", testFileMap)
	}
}

func TestRepoRelatedFilesFromPath(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"internal/services/demo/service.go",
		"internal/services/demo/handler.go",
		"tests/integration/demo/demo_test.go",
		"docs/services/demo.md",
		"web/src/services/api/demo.ts",
		"web/src/lib/search-contributors/demo.ts",
		"web/src/routes/demo/index.tsx",
		"internal/router/topology.go",
	} {
		writeTestFile(t, root, rel, "test")
	}
	params, _ := json.Marshal(map[string]any{"path": "internal/services/demo/service.go"})
	out, err := provider.toolRelatedFiles(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRelatedFiles() error = %v", err)
	}
	got := out.(map[string]any)
	if got["service"] != "demo" {
		t.Fatalf("unexpected service: %v", got["service"])
	}
	companions := got["companions"].(map[string][]string)
	if len(companions["internal"]) != 2 {
		t.Fatalf("unexpected internal companions: %#v", companions["internal"])
	}
	if len(companions["web_routes"]) != 1 || companions["web_routes"][0] != "web/src/routes/demo/index.tsx" {
		t.Fatalf("unexpected web route companions: %#v", companions["web_routes"])
	}
}

func TestRepoEndpointMapGroupsInternalRoutes(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "internal/router/router.go", `package router

func sample(r interface{Get(string, any); Route(string, any)}) {
	r.Get("/_health", nil)
	r.Get("/_topology", nil)
	r.Route("/_debug", nil)
}
`)
	out, err := provider.toolEndpointMap(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolEndpointMap() error = %v", err)
	}
	got := out.(map[string]any)
	endpoints := got["endpoints"].(map[string][]string)
	if len(endpoints["health"]) != 1 || endpoints["health"][0] != "/_health" {
		t.Fatalf("unexpected health endpoints: %#v", endpoints["health"])
	}
	if len(endpoints["topology"]) != 1 || endpoints["topology"][0] != "/_topology" {
		t.Fatalf("unexpected topology endpoints: %#v", endpoints["topology"])
	}
	if len(endpoints["debug"]) != 1 || endpoints["debug"][0] != "/_debug" {
		t.Fatalf("unexpected debug endpoints: %#v", endpoints["debug"])
	}
}

func TestRepoFindSymbolReturnsDefinitionAndReferences(t *testing.T) {
	root := t.TempDir()
	// Use the regex finder explicitly so this test validates the regex path
	// independently of whatever LSP servers are installed in the environment.
	provider := newRepoProvider(root, newRegexSymbolFinder(root))
	writeTestFile(t, root, "internal/services/demo/service.go", `package demo

type DemoService struct{}

func NewDemoService() *DemoService {
	return &DemoService{}
}
`)
	writeTestFile(t, root, "internal/router/router.go", `package router

import "example/internal/services/demo"

func wire() {
	_ = demo.NewDemoService()
}
`)
	params, _ := json.Marshal(map[string]any{"symbol": "NewDemoService"})
	out, err := provider.toolFindSymbol(context.Background(), params)
	if err != nil {
		t.Fatalf("toolFindSymbol() error = %v", err)
	}
	got := out.(map[string]any)
	defs := got["definitions"].([]map[string]any)
	refs := got["references"].([]map[string]any)
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if len(refs) < 2 {
		t.Fatalf("expected at least 2 references, got %d", len(refs))
	}
	if got["backend"] != "regex" {
		t.Fatalf("expected regex backend, got %v", got["backend"])
	}
}

func TestRepoFindSymbolUsesConfiguredBackend(t *testing.T) {
	provider := newRepoProvider(t.TempDir(), stubSymbolFinder{
		result: SymbolFindResult{
			Backend:     "semantic-stub",
			Symbol:      "DemoSymbol",
			PathPrefix:  "internal/",
			Definitions: []SymbolHit{{Path: "internal/demo.go", Line: 7, Text: "func DemoSymbol() {}"}},
			References:  []SymbolHit{{Path: "internal/use.go", Line: 14, Text: "DemoSymbol()"}},
			MaxResults:  10,
		},
	})
	params, _ := json.Marshal(map[string]any{"symbol": "DemoSymbol", "path_prefix": "internal/", "max_results": 10})
	out, err := provider.toolFindSymbol(context.Background(), params)
	if err != nil {
		t.Fatalf("toolFindSymbol() error = %v", err)
	}
	got := out.(map[string]any)
	if got["backend"] != "semantic-stub" {
		t.Fatalf("unexpected backend: %v", got["backend"])
	}
	defs := got["definitions"].([]map[string]any)
	if len(defs) != 1 || defs[0]["path"] != "internal/demo.go" {
		t.Fatalf("unexpected definitions: %#v", defs)
	}
}

func TestRepoFindSymbolPropagatesBackendErrors(t *testing.T) {
	provider := newRepoProvider(t.TempDir(), stubSymbolFinder{err: errors.New("backend unavailable")})
	params, _ := json.Marshal(map[string]any{"symbol": "DemoSymbol"})
	_, err := provider.toolFindSymbol(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), "backend unavailable") {
		t.Fatalf("expected backend error, got %v", err)
	}
}

func TestRepoConventionsSnapshotIncludesRequestedScopes(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "CONTRIBUTING.md", "## Core principles\n- Test-first, always.\n")
	writeTestFile(t, root, "AGENTS.md", "## What agents must NOT do\n- Never leave the workspace broken.\n")
	writeTestFile(t, root, "tests/AGENTS.md", "## Testing conventions\n- Use Given/When/Then.\n")
	out, err := provider.toolConventionsSnapshot(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolConventionsSnapshot() error = %v", err)
	}
	got := out.(map[string]any)
	files := got["files"].([]map[string]any)
	if len(files) != 3 {
		t.Fatalf("expected 3 convention files, got %d", len(files))
	}
	haveContrib := false
	for _, f := range files {
		if f["path"] == "CONTRIBUTING.md" {
			haveContrib = true
			break
		}
	}
	if !haveContrib {
		t.Fatalf("expected CONTRIBUTING.md snapshot, got %#v", files)
	}
}

func TestRepoDocCoverageByService(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"docs/services/demo.md",
		"tests/integration/demo/demo_test.go",
		"web/src/services/api/demo.ts",
		"web/src/lib/search-contributors/demo.ts",
		"CHANGELOG.md",
	} {
		content := "test"
		if rel == "CHANGELOG.md" {
			content = "- Added demo service docs"
		}
		writeTestFile(t, root, rel, content)
	}
	params, _ := json.Marshal(map[string]any{"service": "demo"})
	out, err := provider.toolDocCoverage(context.Background(), params)
	if err != nil {
		t.Fatalf("toolDocCoverage() error = %v", err)
	}
	got := out.(map[string]any)
	if got["service"] != "demo" {
		t.Fatalf("unexpected service: %v", got["service"])
	}
	coverage := got["coverage"].(map[string]bool)
	if !coverage["service_doc"] || !coverage["integration_test"] || !coverage["web_api"] || !coverage["search_contributor"] {
		t.Fatalf("unexpected coverage map: %#v", coverage)
	}
	if !got["changelog_mentions"].(bool) {
		t.Fatal("expected changelog mention")
	}
}

func TestRepoCloudFormationLinksByService(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	for _, rel := range []string{
		"internal/services/demo/service.go",
		"docs/services/demo.md",
		"internal/services/cloudformation/provisioner_resources.go",
	} {
		content := "test"
		if strings.HasSuffix(rel, "provisioner_resources.go") {
			content = "// demo resource mapping\nfunc x(){ _ = \"demo\" }"
		}
		writeTestFile(t, root, rel, content)
	}
	params, _ := json.Marshal(map[string]any{"service": "demo"})
	out, err := provider.toolCloudFormationLinks(context.Background(), params)
	if err != nil {
		t.Fatalf("toolCloudFormationLinks() error = %v", err)
	}
	got := out.(map[string]any)
	if got["service"] != "demo" {
		t.Fatalf("unexpected service: %v", got["service"])
	}
	serviceFiles := got["service_files"].([]string)
	if len(serviceFiles) != 1 || serviceFiles[0] != "internal/services/demo/service.go" {
		t.Fatalf("unexpected service files: %#v", serviceFiles)
	}
	cfnFiles := got["cloudformation_files"].([]string)
	if len(cfnFiles) != 1 || cfnFiles[0] != "internal/services/cloudformation/provisioner_resources.go" {
		t.Fatalf("unexpected cloudformation files: %#v", cfnFiles)
	}
}

func TestRuntimeListInstancesIncludesEnvAndInputEndpoints(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "docker-compose.yml", `services:
  overcast:
    ports:
      - "4566:4566"
`)
	provider := NewRepoProvider(root)
	t.Setenv("OVERCAST_ENDPOINT", "overcast.local:4566")
	params, _ := json.Marshal(map[string]any{"endpoints": []string{"http://localhost:4566", "127.0.0.1:7777", "overcast:4566"}})
	out, err := provider.toolRuntimeListInstances(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeListInstances() error = %v", err)
	}
	got := out.(map[string]any)
	instances := got["instances"].([]map[string]any)
	if len(instances) < 4 {
		t.Fatalf("expected at least 4 unique instances, got %d", len(instances))
	}
	haveEnv := false
	haveMergedSources := false
	haveServiceDNSHint := false
	haveLoopbackKind := false
	haveComposePublished := false
	for _, item := range instances {
		if item["base_url"] == "http://overcast.local:4566" {
			haveEnv = true
		}
		if role, ok := item["role"].(string); !ok || role != "probe_target" {
			t.Fatalf("expected role=probe_target in instance, got %#v", item)
		}
		source, ok := item["source"].(string)
		if !ok || source == "" {
			t.Fatalf("expected non-empty source in instance, got %#v", item)
		}
		sources, ok := item["sources"].([]string)
		if !ok || len(sources) == 0 {
			t.Fatalf("expected non-empty sources in instance, got %#v", item)
		}
		if item["base_url"] == "http://localhost:4566" {
			for _, src := range sources {
				if src == "compose_published_4566" {
					haveComposePublished = true
					break
				}
			}
		}
		host, ok := item["host"].(string)
		if !ok || host == "" {
			t.Fatalf("expected host metadata in instance, got %#v", item)
		}
		if _, ok := item["port"].(string); !ok {
			t.Fatalf("expected port metadata in instance, got %#v", item)
		}
		kind, ok := item["endpoint_kind"].(string)
		if !ok || kind == "" {
			t.Fatalf("expected endpoint_kind metadata in instance, got %#v", item)
		}
		if kind == "loopback" {
			haveLoopbackKind = true
		}
		if host == "overcast" {
			if kind != "service_dns" {
				t.Fatalf("expected service_dns endpoint kind for compose-like host, got %#v", item)
			}
			if hint, ok := item["container_hint"].(bool); !ok || !hint {
				t.Fatalf("expected container_hint=true for compose-like host, got %#v", item)
			}
			haveServiceDNSHint = true
		}
		if item["base_url"] == "http://localhost:4566" && len(sources) > 1 {
			haveMergedSources = true
		}
	}
	if !haveEnv {
		t.Fatalf("expected env endpoint in instances: %#v", instances)
	}
	if !haveMergedSources {
		t.Fatalf("expected duplicate localhost endpoint to retain merged source metadata: %#v", instances)
	}
	if !haveComposePublished {
		t.Fatalf("expected compose source metadata on localhost endpoint: %#v", instances)
	}
	if !haveLoopbackKind {
		t.Fatalf("expected at least one loopback endpoint classification, got %#v", instances)
	}
	if !haveServiceDNSHint {
		t.Fatalf("expected service_dns/container_hint classification for compose-like endpoint, got %#v", instances)
	}
	discoveryContext, ok := got["discovery_context"].(map[string]any)
	if !ok {
		t.Fatalf("expected discovery_context metadata, got %#v", got)
	}
	if composePublished, _ := discoveryContext["compose_published_4566"].(bool); !composePublished {
		t.Fatalf("expected compose_published_4566=true in discovery_context, got %#v", discoveryContext)
	}
	if composeService, _ := discoveryContext["compose_overcast_service"].(bool); !composeService {
		t.Fatalf("expected compose_overcast_service=true in discovery_context, got %#v", discoveryContext)
	}
	composeFiles, ok := discoveryContext["compose_files"].([]string)
	if !ok || len(composeFiles) == 0 {
		t.Fatalf("expected compose_files metadata in discovery_context, got %#v", discoveryContext)
	}
	if signal, ok := discoveryContext["container_signal"].(string); !ok || signal == "" {
		t.Fatalf("expected container_signal metadata in discovery_context, got %#v", discoveryContext)
	}
	if note, ok := got["note"].(string); !ok || note == "" {
		t.Fatalf("expected clarifying note field in response, got %#v", got["note"])
	}
}

func TestRuntimeListInstancesIncludesLiveDockerComposeMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "docker-compose.yml", `services:
  overcast:
    image: overcast
`)

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	dockerScript := filepath.Join(binDir, "docker")
	writeTestFile(t, root, "bin/docker", "#!/bin/sh\nprintf '[{\"Service\":\"overcast\",\"State\":\"running\",\"Status\":\"Up 12s\",\"Publishers\":[{\"URL\":\"0.0.0.0:4566\",\"TargetPort\":4566,\"PublishedPort\":4566,\"Protocol\":\"tcp\"}]},{\"Service\":\"redis\",\"State\":\"running\",\"Status\":\"Up 12s\",\"Publishers\":[]}]'")
	if err := os.Chmod(dockerScript, 0o755); err != nil {
		t.Fatalf("chmod fake docker: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	provider := NewRepoProvider(root)
	out, err := provider.toolRuntimeListInstances(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolRuntimeListInstances() error = %v", err)
	}
	got := out.(map[string]any)
	discoveryContext, ok := got["discovery_context"].(map[string]any)
	if !ok {
		t.Fatalf("expected discovery_context metadata, got %#v", got)
	}
	if probe, _ := discoveryContext["docker_compose_probe"].(string); probe != "ok" {
		t.Fatalf("expected docker_compose_probe=ok, got %#v", discoveryContext)
	}
	if detected, _ := discoveryContext["docker_compose_detected"].(bool); !detected {
		t.Fatalf("expected docker_compose_detected=true, got %#v", discoveryContext)
	}
	if running, _ := discoveryContext["docker_compose_overcast_running"].(bool); !running {
		t.Fatalf("expected docker_compose_overcast_running=true, got %#v", discoveryContext)
	}
	ports, ok := discoveryContext["docker_compose_overcast_published_ports"].([]int)
	if !ok || len(ports) != 1 || ports[0] != 4566 {
		t.Fatalf("expected docker_compose_overcast_published_ports=[4566], got %#v", discoveryContext)
	}
	services, ok := discoveryContext["docker_compose_services"].([]string)
	if !ok || len(services) != 2 || services[0] != "overcast" || services[1] != "redis" {
		t.Fatalf("expected sorted docker_compose_services, got %#v", discoveryContext)
	}

	instances := got["instances"].([]map[string]any)
	haveDockerSource := false
	for _, item := range instances {
		if item["base_url"] != "http://localhost:4566" {
			continue
		}
		sources, _ := item["sources"].([]string)
		for _, source := range sources {
			if source == "docker_compose_ps_published" {
				haveDockerSource = true
				break
			}
		}
	}
	if !haveDockerSource {
		t.Fatalf("expected docker_compose_ps_published source on localhost endpoint, got %#v", instances)
	}
}

func TestRuntimeProbeInstanceReportsHealthAndMCP(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/_health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/_mcp":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			method, _ := req["method"].(string)
			w.Header().Set("Content-Type", "application/json")
			switch method {
			case "initialize":
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-init","result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}}}}`))
			case "tools/list":
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-tools","result":{"tools":[{"name":"repo_workspace_info"},{"name":"runtime_probe_instance"}]}}`))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	out, err := provider.toolRuntimeProbeInstance(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeProbeInstance() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) {
		t.Fatalf("expected reachable=true, got %#v", got)
	}
	if !got["health_ok"].(bool) {
		t.Fatalf("expected health_ok=true, got %#v", got)
	}
	if !got["mcp_available"].(bool) {
		t.Fatalf("expected mcp_available=true, got %#v", got)
	}
	if got["tool_count"].(int) != 2 {
		t.Fatalf("expected tool_count=2, got %#v", got["tool_count"])
	}
	if summary, ok := got["summary"].(string); !ok || summary == "" {
		t.Fatalf("expected summary field, got %#v", got["summary"])
	}
}

// TestRuntimeProbeInstanceCompletesLifecycleHandshake verifies that the probe
// sends notifications/initialized after initialize, preventing the server from
// getting stuck in initDone=true, ready=false state (which would block all
// subsequent operation requests with a lifecycle error).
func TestRuntimeProbeInstanceCompletesLifecycleHandshake(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	// Use a real MCP server to enforce lifecycle ordering.
	mcpSrv := NewServer(nil, nil)
	srv := httptest.NewServer(mcpSrv.RootHandler())
	defer srv.Close()

	// Probe the real server — it enforces lifecycle.
	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	out, err := provider.toolRuntimeProbeInstance(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeProbeInstance() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["mcp_available"].(bool) {
		t.Fatalf("expected mcp_available=true after probe, got %#v", got)
	}

	// The probe must have completed the full handshake (initialize +
	// notifications/initialized). Check the server's ready state directly so
	// we don't add extra HTTP connections that could perturb test scheduling.
	mcpSrv.mu.RLock()
	ready := mcpSrv.ready
	mcpSrv.mu.RUnlock()
	if !ready {
		t.Fatal("expected server to be in ready state after probe (notifications/initialized was not sent)")
	}
}

func TestRuntimeProbeInstanceUsesCacheAndForceRefresh(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/_health":
			callCount++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/_mcp":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			method, _ := req["method"].(string)
			w.Header().Set("Content-Type", "application/json")
			switch method {
			case "initialize":
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-init","result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}}}}`))
			case "tools/list":
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-tools","result":{"tools":[{"name":"runtime_probe_instance"}]}}`))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	firstOut, err := provider.toolRuntimeProbeInstance(context.Background(), params)
	if err != nil {
		t.Fatalf("first toolRuntimeProbeInstance() error = %v", err)
	}
	first := firstOut.(map[string]any)
	cacheFirst := first["cache"].(map[string]any)
	if cacheFirst["hit"] != false {
		t.Fatalf("expected first probe cache hit=false, got %#v", cacheFirst)
	}
	if callCount == 0 {
		t.Fatal("expected first probe to call health endpoint")
	}

	countAfterFirst := callCount
	secondOut, err := provider.toolRuntimeProbeInstance(context.Background(), params)
	if err != nil {
		t.Fatalf("second toolRuntimeProbeInstance() error = %v", err)
	}
	second := secondOut.(map[string]any)
	cacheSecond := second["cache"].(map[string]any)
	if cacheSecond["hit"] != true {
		t.Fatalf("expected second probe cache hit=true, got %#v", cacheSecond)
	}
	if callCount != countAfterFirst {
		t.Fatalf("expected cached probe to avoid extra health call, got count=%d want=%d", callCount, countAfterFirst)
	}

	forceParams, _ := json.Marshal(map[string]any{"endpoint": srv.URL, "force_refresh": true})
	thirdOut, err := provider.toolRuntimeProbeInstance(context.Background(), forceParams)
	if err != nil {
		t.Fatalf("force-refresh toolRuntimeProbeInstance() error = %v", err)
	}
	third := thirdOut.(map[string]any)
	cacheThird := third["cache"].(map[string]any)
	if cacheThird["hit"] != false {
		t.Fatalf("expected force-refresh probe cache hit=false, got %#v", cacheThird)
	}
	if callCount <= countAfterFirst {
		t.Fatalf("expected force-refresh probe to perform fresh health call, count=%d first=%d", callCount, countAfterFirst)
	}
}

func TestRuntimeRefreshProbeCacheClearsEndpointAndAll(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/_health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/_mcp":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			method, _ := req["method"].(string)
			w.Header().Set("Content-Type", "application/json")
			switch method {
			case "initialize":
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-init","result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}}}}`))
			case "tools/list":
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-tools","result":{"tools":[{"name":"runtime_probe_instance"}]}}`))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	probeParams, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	if _, err := provider.toolRuntimeProbeInstance(context.Background(), probeParams); err != nil {
		t.Fatalf("seed probe error = %v", err)
	}

	clearEndpointParams, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	clearedEndpointOut, err := provider.toolRuntimeRefreshProbeCache(context.Background(), clearEndpointParams)
	if err != nil {
		t.Fatalf("toolRuntimeRefreshProbeCache(endpoint) error = %v", err)
	}
	clearedEndpoint := clearedEndpointOut.(map[string]any)
	if clearedEndpoint["cleared"].(int) != 1 {
		t.Fatalf("expected cleared=1 for endpoint cache clear, got %#v", clearedEndpoint)
	}

	if _, err := provider.toolRuntimeProbeInstance(context.Background(), probeParams); err != nil {
		t.Fatalf("reseed probe error = %v", err)
	}
	clearAllParams, _ := json.Marshal(map[string]any{"clear_all": true})
	clearedAllOut, err := provider.toolRuntimeRefreshProbeCache(context.Background(), clearAllParams)
	if err != nil {
		t.Fatalf("toolRuntimeRefreshProbeCache(clear_all) error = %v", err)
	}
	clearedAll := clearedAllOut.(map[string]any)
	if clearedAll["scope"].(string) != "all" {
		t.Fatalf("expected scope=all for clear_all, got %#v", clearedAll)
	}
	if clearedAll["remaining"].(int) != 0 {
		t.Fatalf("expected remaining=0 after clear_all, got %#v", clearedAll)
	}
}

func TestRuntimeMCPCallProxiesRequest(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/list" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"proxy-call","result":{"tools":[{"name":"x"}]}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL, "method": "tools/list"})
	out, err := provider.toolRuntimeMCPCall(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeMCPCall() error = %v", err)
	}
	got := out.(map[string]any)
	resp := got["response"].(map[string]any)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("unexpected tools payload: %#v", tools)
	}
}

func TestRuntimeListServicesDelegatesViaMCP(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/call" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		params, _ := req["params"].(map[string]any)
		if params["name"] != "runtime_list_services" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-list-services","result":{"structuredContent":{"services":[{"name":"s3","state_backend":"memory","key_count":3},{"name":"sqs","state_backend":"memory","key_count":1}]}}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	out, err := provider.toolRuntimeListServices(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeListServices() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) || !got["ok"].(bool) {
		t.Fatalf("expected reachable/ok=true, got %#v", got)
	}
	servicesAny, _ := got["services"].([]any)
	if len(servicesAny) != 2 {
		t.Fatalf("expected two services, got %#v", got["services"])
	}
}

func TestRuntimeGetHealthReturnsHealthJSON(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/call" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-get-health","result":{"structuredContent":{"status":"ok","version":"dev","services":["s3","sqs"]}}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	out, err := provider.toolRuntimeGetHealth(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetHealth() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) {
		t.Fatalf("expected reachable=true, got %#v", got)
	}
	if !got["ok"].(bool) {
		t.Fatalf("expected ok=true, got %#v", got)
	}
	if got["status_code"].(int) != 200 {
		t.Fatalf("expected status_code=200, got %#v", got["status_code"])
	}
	resp := got["response"].(map[string]any)
	if resp["status"] != "ok" {
		t.Fatalf("expected status=ok in response, got %#v", resp)
	}
}

func TestRuntimeGetHealthUnreachable(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	// Use a known-unreachable address.
	params, _ := json.Marshal(map[string]any{"endpoint": "http://127.0.0.1:1", "timeout_ms": 200})
	out, err := provider.toolRuntimeGetHealth(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetHealth() should not return error on unreachable, got %v", err)
	}
	got := out.(map[string]any)
	if got["reachable"].(bool) {
		t.Fatalf("expected reachable=false for unreachable endpoint, got %#v", got)
	}
	if _, hasErr := got["error"]; !hasErr {
		t.Fatalf("expected error field in unreachable response, got %#v", got)
	}
}

func TestRuntimeGetConfigReturnsConfigJSON(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/call" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-get-config","result":{"structuredContent":{"debug_required":false,"host":"0.0.0.0","port":4566,"region":"us-east-1","debug":true}}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	out, err := provider.toolRuntimeGetConfig(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetConfig() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) {
		t.Fatalf("expected reachable=true, got %#v", got)
	}
	if !got["ok"].(bool) {
		t.Fatalf("expected ok=true, got %#v", got)
	}
	resp := got["response"].(map[string]any)
	if resp["region"] != "us-east-1" {
		t.Fatalf("expected region in config response, got %#v", resp)
	}
}

func TestRuntimeGetConfigIndicatesDebugRequired(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-get-config","result":{"structuredContent":{"debug_required":true}}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL})
	out, err := provider.toolRuntimeGetConfig(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetConfig() error = %v", err)
	}
	got := out.(map[string]any)
	if got["ok"].(bool) {
		t.Fatalf("expected ok=false for 404, got %#v", got)
	}
	if dr, _ := got["debug_required"].(bool); !dr {
		t.Fatalf("expected debug_required=true for 404, got %#v", got)
	}
}

func TestRuntimeGetServiceStateReturnsBoundedEntries(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/call" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-get-service-state","result":{"structuredContent":{"namespace":"","limit":10,"count":2,"truncated":false,"entries":{"s3:buckets":{"demo-bucket":"{}"},"sqs:queues":{"my-queue":"{}"}}}}}`))
	}))
	defer srv.Close()

	// Without namespace — uses /_debug/state.
	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL, "limit": 10})
	out, err := provider.toolRuntimeGetServiceState(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetServiceState() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) {
		t.Fatalf("expected reachable=true, got %#v", got)
	}
	if !got["ok"].(bool) {
		t.Fatalf("expected ok=true, got %#v", got)
	}
	if got["count"].(float64) != 2 {
		t.Fatalf("expected count=2 for two namespaces, got %#v", got["count"])
	}
	if got["truncated"].(bool) {
		t.Fatalf("expected truncated=false, got %#v", got["truncated"])
	}
}

func TestRuntimeGetServiceStateAppliesKeyPattern(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-get-service-state","result":{"structuredContent":{"namespace":"","limit":50,"count":1,"truncated":false,"entries":{"s3:buckets":"a"}}}}`))
	}))
	defer srv.Close()

	// key_pattern filters to keys containing "s3".
	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL, "key_pattern": "s3"})
	out, err := provider.toolRuntimeGetServiceState(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetServiceState() error = %v", err)
	}
	got := out.(map[string]any)
	if got["count"].(float64) != 1 {
		t.Fatalf("expected count=1 for key_pattern=s3, got %#v", got)
	}
	entries := got["entries"].(map[string]any)
	if _, ok := entries["s3:buckets"]; !ok {
		t.Fatalf("expected s3:buckets in filtered entries, got %#v", entries)
	}
}

func TestRuntimeGetRecentEventsDelegatesViaMCP(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/call" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		params, _ := req["params"].(map[string]any)
		if params["name"] != "runtime_get_recent_events" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-get-recent-events","result":{"structuredContent":{"limit":2,"count":2,"truncated":false,"events":[{"type":"s3:BucketCreated","source":"s3","time":"2026-04-22T12:00:00Z","payload":{"name":"alpha"}},{"type":"s3:BucketDeleted","source":"s3","time":"2026-04-22T12:00:01Z","payload":{"name":"alpha"}}]}}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL, "source": "s3", "limit": 2})
	out, err := provider.toolRuntimeGetRecentEvents(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetRecentEvents() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) || !got["ok"].(bool) {
		t.Fatalf("expected reachable/ok=true, got %#v", got)
	}
	if got["count"].(float64) != 2 {
		t.Fatalf("expected count=2, got %#v", got["count"])
	}
	eventsAny, _ := got["events"].([]any)
	if len(eventsAny) != 2 {
		t.Fatalf("expected 2 events, got %#v", got["events"])
	}
}

func TestRuntimeGetRecentEventsReturnsWrapperErrorOnMCPFailure(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"runtime-get-recent-events","error":{"code":-32603,"message":"runtime events backend unavailable"}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL, "source": "s3"})
	out, err := provider.toolRuntimeGetRecentEvents(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeGetRecentEvents() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) {
		t.Fatalf("expected reachable=true on MCP error path, got %#v", got)
	}
	if got["ok"].(bool) {
		t.Fatalf("expected ok=false on MCP error path, got %#v", got)
	}
	if got["status_code"].(int) != 500 {
		t.Fatalf("expected status_code=500 on MCP error path, got %#v", got["status_code"])
	}
	if msg, _ := got["error"].(string); !strings.Contains(msg, "runtime events backend unavailable") {
		t.Fatalf("expected delegated error message to propagate, got %#v", got)
	}
}

func TestRuntimeProbeKVStoreSupportsCursorAndLimit(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/call" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-kv-store","result":{"structuredContent":{"limit":2,"cursor":"","count":2,"total_matched":4,"truncated":true,"next_cursor":"s3:buckets|bravo","entries":[{"namespace":"s3:buckets","key":"alpha","value_preview":"111"},{"namespace":"s3:buckets","key":"bravo","value_preview":"222"}]}}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{
		"endpoint":       srv.URL,
		"limit":          2,
		"include_values": false,
	})
	out, err := provider.toolRuntimeProbeKVStore(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeProbeKVStore() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) || !got["ok"].(bool) {
		t.Fatalf("expected reachable/ok=true, got %#v", got)
	}
	if !got["truncated"].(bool) {
		t.Fatalf("expected truncated=true on first page, got %#v", got)
	}
	entriesAny, _ := got["entries"].([]any)
	entries := make([]map[string]any, 0, len(entriesAny))
	for _, entry := range entriesAny {
		entryMap, _ := entry.(map[string]any)
		entries = append(entries, entryMap)
	}
	if len(entries) != 2 {
		t.Fatalf("expected two entries on first page, got %#v", entries)
	}
	next, _ := got["next_cursor"].(string)
	if strings.TrimSpace(next) == "" {
		t.Fatalf("expected non-empty next_cursor, got %#v", got)
	}
}

func TestRuntimeProbeKVStoreIncludesValueWhenRequested(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/call" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		params, _ := req["params"].(map[string]any)
		if params["name"] != "runtime_probe_kv_store" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-kv-store","result":{"structuredContent":{"limit":50,"cursor":"","count":1,"total_matched":1,"truncated":false,"entries":[{"namespace":"ssm:parameters","key":"/app/db/password","value_preview":"very-lon","value":"very-long-secret-value-preview-me"}]}}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{
		"endpoint":       srv.URL,
		"include_values": true,
		"preview_bytes":  8,
	})
	out, err := provider.toolRuntimeProbeKVStore(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeProbeKVStore() error = %v", err)
	}
	got := out.(map[string]any)
	entriesAny, _ := got["entries"].([]any)
	entries := make([]map[string]any, 0, len(entriesAny))
	for _, entry := range entriesAny {
		entryMap, _ := entry.(map[string]any)
		entries = append(entries, entryMap)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %#v", entries)
	}
	if _, ok := entries[0]["value"]; !ok {
		t.Fatalf("expected full value when include_values=true, got %#v", entries[0])
	}
	if preview, _ := entries[0]["value_preview"].(string); len(preview) != 8 {
		t.Fatalf("expected preview length 8, got %q (%d)", preview, len(preview))
	}
}

func TestRuntimeProbeKVStoreReturnsWrapperErrorOnMCPFailure(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"probe-kv-store","error":{"code":-32603,"message":"state backend timeout"}}`))
	}))
	defer srv.Close()

	params, _ := json.Marshal(map[string]any{"endpoint": srv.URL, "namespace": "s3:buckets"})
	out, err := provider.toolRuntimeProbeKVStore(context.Background(), params)
	if err != nil {
		t.Fatalf("toolRuntimeProbeKVStore() error = %v", err)
	}
	got := out.(map[string]any)
	if !got["reachable"].(bool) {
		t.Fatalf("expected reachable=true on MCP error path, got %#v", got)
	}
	if got["ok"].(bool) {
		t.Fatalf("expected ok=false on MCP error path, got %#v", got)
	}
	if msg, _ := got["error"].(string); !strings.Contains(msg, "state backend timeout") {
		t.Fatalf("expected delegated error message to propagate, got %#v", got)
	}
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", rel, err)
	}
}

type stubSymbolFinder struct {
	result SymbolFindResult
	err    error
}

func (s stubSymbolFinder) FindSymbol(_ context.Context, _ SymbolQuery) (SymbolFindResult, error) {
	if s.err != nil {
		return SymbolFindResult{}, s.err
	}
	return s.result, nil
}
