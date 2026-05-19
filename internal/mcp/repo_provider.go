package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RepoProvider struct {
	workspaceRoot string
	symbolFinder  SymbolFinder
	probeCacheMu  sync.RWMutex
	probeCache    map[string]cachedProbeResult
	compatRunner  func(ctx context.Context, args []string) ([]byte, int, error)
}

type cachedProbeResult struct {
	result    map[string]any
	fetchedAt time.Time
	expiresAt time.Time
}

const runtimeProbeCacheTTL = 15 * time.Second
const compatSubsetMaxSuites = 3

var compatSuiteNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

type discoveredRuntimeEndpoint struct {
	raw    string
	source string
}

type dockerComposePSPublisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

type dockerComposePSService struct {
	Service    string                     `json:"Service"`
	Name       string                     `json:"Name"`
	State      string                     `json:"State"`
	Status     string                     `json:"Status"`
	Publishers []dockerComposePSPublisher `json:"Publishers"`
}

type commandEntry struct {
	Target      string `json:"target"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

type serviceCoverageEntry struct {
	Service        string `json:"service"`
	Tier           string `json:"tier"`
	Ops            *int   `json:"ops,omitempty"`
	KnownOps       *int   `json:"known_ops,omitempty"`
	ImplementedOps *int   `json:"implemented_ops,omitempty"`
	CoverageSource string `json:"coverage_source,omitempty"`
	Highlights     string `json:"highlights"`
}

type generatedSupportOperation struct {
	Operation string `json:"operation"`
	Category  string `json:"category"`
	Status    string `json:"status"`
	Notes     string `json:"notes,omitempty"`
}

type generatedSupportService struct {
	Service        string                      `json:"service"`
	DisplayName    string                      `json:"display_name"`
	TotalOps       int                         `json:"total_ops"`
	ImplementedOps int                         `json:"implemented_ops"`
	Operations     []generatedSupportOperation `json:"operations"`
}

type generatedSupportDoc struct {
	GeneratedBy string                    `json:"generated_by"`
	TotalOps    int                       `json:"total_ops"`
	Services    []generatedSupportService `json:"services"`
}

type todoEntry struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Marker    string `json:"marker"`
	Text      string `json:"text"`
	Priority  string `json:"priority,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Location  string `json:"location"`
	Service   string `json:"service,omitempty"`
	Language  string `json:"language,omitempty"`
	Canonical bool   `json:"canonical"`
}

func NewRepoProvider(workspaceRoot string) *RepoProvider {
	return newRepoProvider(workspaceRoot, nil)
}

func newRepoProvider(workspaceRoot string, symbolFinder SymbolFinder) *RepoProvider {
	if workspaceRoot == "" {
		wd, err := os.Getwd()
		if err == nil {
			workspaceRoot = wd
		}
	}
	if symbolFinder == nil {
		symbolFinder = newAutoSymbolFinder(workspaceRoot)
	}
	return &RepoProvider{
		workspaceRoot: workspaceRoot,
		symbolFinder:  symbolFinder,
		probeCache:    make(map[string]cachedProbeResult),
		compatRunner:  runCompatCommand,
	}
}

func (p *RepoProvider) Tools() []Tool {
	return []Tool{
		{Name: "workspace_server_info", Description: "Identify this as the Workspace MCP Server and explain its role in Overcast development and debugging.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"serverRole":{"type":"string"},"serverName":{"type":"string"},"serverVersion":{"type":"string"},"purpose":{"type":"string"},"description":{"type":"string"},"toolCategories":{"type":"object"},"gettingStarted":{"type":"object"},"commonWorkflows":{"type":"object"},"prerequisites":{"type":"object"},"capabilities":{"type":"object"},"overcastProject":{"type":"object"},"documentation":{"type":"object"},"mcp_version":{"type":"string"}},"required":["serverRole","serverName","serverVersion","description","mcp_version"]}`)},
		{Name: "repo_workspace_info", Description: "Return workspace-level metadata useful for generic repo navigation, including workspace root and service count.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_root":{"type":"string"},"mcp_version":{"type":"string"},"service_count":{"type":"integer"},"route_count":{"type":"integer"},"service_docs":{"type":"integer"},"key_paths":{"type":"object"}},"required":["workspace_root","mcp_version","service_count"]}`)},
		{Name: "repo_build_commands", Description: "Return the main Makefile build, test, lint, compat, and container commands with short descriptions.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"commands":{"type":"array","items":{"type":"object","properties":{"target":{"type":"string"},"description":{"type":"string"},"command":{"type":"string"}},"required":["target","description","command"]}},"windows_note":{"type":"string"}},"required":["count","commands"]}`)},
		{Name: "repo_find_todos", Description: "Find TODO and FIXME comments efficiently, extracting priority, tags, location buckets, and service grouping.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"priority":{"type":"string"},"path_prefix":{"type":"string"},"max_results":{"type":"integer"},"canonical_only":{"type":"boolean"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"total_matches":{"type":"integer"},"canonical_count":{"type":"integer"},"truncated":{"type":"boolean"},"scanned_files":{"type":"integer"},"todos":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer"},"marker":{"type":"string"},"text":{"type":"string"},"priority":{"type":"string"},"tag":{"type":"string"},"location":{"type":"string"},"service":{"type":"string"},"language":{"type":"string"},"canonical":{"type":"boolean"}},"required":["path","line","marker","text","location","canonical"]}},"by_priority":{"type":"object","additionalProperties":{"type":"integer"}},"by_location":{"type":"object","additionalProperties":{"type":"integer"}},"by_service":{"type":"object","additionalProperties":{"type":"integer"}},"by_tag":{"type":"object","additionalProperties":{"type":"integer"}}},"required":["count","total_matches","canonical_count","truncated","scanned_files","todos","by_priority","by_location","by_service","by_tag"]}`)},
		{Name: "repo_service_coverage", Description: "Return service coverage metadata, combining STATUS.md summary data with code-derived operation counts where available.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"services":{"type":"array","items":{"type":"object","properties":{"service":{"type":"string"},"tier":{"type":"string"},"ops":{"type":"integer"},"known_ops":{"type":"integer"},"implemented_ops":{"type":"integer"},"coverage_source":{"type":"string"},"highlights":{"type":"string"},"ops_status":{"type":"string"},"completion":{"type":"number"}},"required":["service","tier","highlights"]}},"service":{"type":"string"},"tier":{"type":"string"},"ops":{"type":"integer"},"known_ops":{"type":"integer"},"implemented_ops":{"type":"integer"},"coverage_source":{"type":"string"},"highlights":{"type":"string"},"ops_status":{"type":"string"},"completion":{"type":"number"}},"anyOf":[{"required":["count","services"]},{"required":["service","tier","highlights"]}]}`)},
		{Name: "repo_service_files", Description: "Return the main code, tests, docs, and web files associated with a service.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"}},"required":["service"]}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"internal_files":{"type":"array","items":{"type":"string"}},"integration_test":{"type":"string"},"service_doc":{"type":"string"},"web_api":{"type":"string"},"search_contributor":{"type":"string"},"web_routes":{"type":"array","items":{"type":"string"}}},"required":["service","internal_files"]}`)},
		{Name: "repo_doc_coverage", Description: "Report docs and companion artifact coverage for a service, including changelog mention signal.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"}},"required":["service"]}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"coverage":{"type":"object","additionalProperties":{"type":"boolean"}},"found_paths":{"type":"object","additionalProperties":{"type":"string"}},"changelog_mentions":{"type":"boolean"}},"required":["service","coverage","found_paths","changelog_mentions"]}`)},
		{Name: "repo_cloudformation_links", Description: "Map a service to relevant CloudFormation implementation files and local service artifacts.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"}},"required":["service"]}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"service_files":{"type":"array","items":{"type":"string"}},"service_doc":{"type":"string"},"cloudformation_files":{"type":"array","items":{"type":"string"}}},"required":["service","service_files","cloudformation_files"]}`)},
		{Name: "repo_service_manifest", Description: "Return generated service support metadata (manifest-like operation inventory) for one service or all services.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"generated_by":{"type":"string"},"total_ops":{"type":"integer"},"count":{"type":"integer"},"services":{"type":"array","items":{"type":"object"}}},"required":["count","services"]}`)},
		{Name: "repo_operation_support", Description: "Return support metadata for a specific operation within a service.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"operation":{"type":"string"}},"required":["service","operation"]}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"display_name":{"type":"string"},"operation":{"type":"string"},"category":{"type":"string"},"status":{"type":"string"},"notes":{"type":"string"}},"required":["service","operation","status"]}`)},
		{Name: "repo_find_symbol", Description: "Find symbol definitions and high-signal references across workspace files.", InputSchema: json.RawMessage(`{"type":"object","properties":{"symbol":{"type":"string"},"path_prefix":{"type":"string"},"max_results":{"type":"integer"}},"required":["symbol"]}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"backend":{"type":"string"},"symbol":{"type":"string"},"path_prefix":{"type":"string"},"definitions":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer"},"text":{"type":"string"}},"required":["path","line","text"]}},"references":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer"},"text":{"type":"string"}},"required":["path","line","text"]}},"truncated":{"type":"boolean"},"max_results":{"type":"integer"}},"required":["backend","symbol","definitions","references","truncated","max_results"]}`)},
		{Name: "repo_conventions_snapshot", Description: "Return a bounded snapshot of key project conventions from CONTRIBUTING and AGENTS guides.", InputSchema: json.RawMessage(`{"type":"object","properties":{"max_lines_per_file":{"type":"integer"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"lines":{"type":"array","items":{"type":"string"}}},"required":["path","lines"]}},"max_lines_per_file":{"type":"integer"}},"required":["files","max_lines_per_file"]}`)},
		{Name: "repo_topology_contributors", Description: "Return backend and web files that contribute to topology generation and rendering for a service or path.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"path":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"contributors":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}}},"required":["service","contributors"]}`)},
		{Name: "repo_related_files", Description: "Return companion files related to a given path or service, grouped by role.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"service":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"companions":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}}},"required":["service","companions"]}`)},
		{Name: "repo_endpoint_map", Description: "Return internal router endpoints grouped by concern, with optional path-prefix filtering.", InputSchema: json.RawMessage(`{"type":"object","properties":{"prefix":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"prefix":{"type":"string"},"endpoints":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}}},"required":["prefix","endpoints"]}`)},
		{Name: "repo_change_impact", Description: "Estimate impacted services, related artifacts, and recommended validation commands for one or more changed paths.", InputSchema: json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"}},"service":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"}},"services":{"type":"array","items":{"type":"string"}},"files_by_role":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}},"test_file_map":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}},"recommended_tests":{"type":"array","items":{"type":"string"}},"recommended_commands":{"type":"array","items":{"type":"string"}}},"required":["paths","services","files_by_role","test_file_map","recommended_tests","recommended_commands"]}`)},
		{Name: "repo_test_targets", Description: "Recommend focused test packages and commands based on changed paths or a service name.", InputSchema: json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"}},"service":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"unit_packages":{"type":"array","items":{"type":"string"}},"integration_packages":{"type":"array","items":{"type":"string"}},"test_file_map":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}},"recommended_commands":{"type":"array","items":{"type":"string"}}},"required":["unit_packages","integration_packages","test_file_map","recommended_commands"]}`)},
		{Name: "repo_compat_rerun_subset", Description: "Run a bounded compatibility test subset using an allowlisted suite set for safe debug iteration.", InputSchema: json.RawMessage(`{"type":"object","properties":{"suites":{"type":"array","items":{"type":"string"}},"endpoint":{"type":"string"},"timeout_seconds":{"type":"integer"},"max_output_lines":{"type":"integer"}},"required":["suites"]}`), Annotations: map[string]any{"readOnlyHint": false}, Execution: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"suites":{"type":"array","items":{"type":"string"}},"endpoint":{"type":"string"},"command":{"type":"array","items":{"type":"string"}},"success":{"type":"boolean"},"exit_code":{"type":"integer"},"duration_ms":{"type":"integer"},"output_preview":{"type":"array","items":{"type":"string"}},"truncated":{"type":"boolean"}},"required":["suites","endpoint","command","success","exit_code","duration_ms","output_preview","truncated"]}`)},
		{Name: "runtime_list_instances", Description: "List external Overcast runtime instances available for probing (e.g., localstack, compose services). Via Workspace MCP delegation to probe/debug Overcast instances.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoints":{"type":"array","items":{"type":"string"}}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"instances":{"type":"array","items":{"type":"object","properties":{"base_url":{"type":"string"},"health_url":{"type":"string"},"mcp_url":{"type":"string"},"role":{"type":"string"},"source":{"type":"string"},"sources":{"type":"array","items":{"type":"string"}},"host":{"type":"string"},"port":{"type":"string"},"endpoint_kind":{"type":"string"},"container_hint":{"type":"boolean"}},"required":["base_url","health_url","mcp_url","role","source","sources","host","port","endpoint_kind","container_hint"]}},"discovery_context":{"type":"object","properties":{"in_container":{"type":"boolean"},"container_signal":{"type":"string"},"compose_files":{"type":"array","items":{"type":"string"}},"compose_published_4566":{"type":"boolean"},"compose_overcast_service":{"type":"boolean"},"docker_compose_probe":{"type":"string"},"docker_compose_detected":{"type":"boolean"},"docker_compose_services":{"type":"array","items":{"type":"string"}},"docker_compose_overcast_running":{"type":"boolean"},"docker_compose_overcast_published_ports":{"type":"array","items":{"type":"integer"}},"docker_compose_error":{"type":"string"}}},"note":{"type":"string"}},"required":["count","instances"]}`)},
		{Name: "runtime_probe_instance", Description: "Probe an external Overcast instance for /_health and /_mcp availability. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"timeout_ms":{"type":"integer"},"force_refresh":{"type":"boolean"}},"required":["endpoint"]}`), Annotations: map[string]any{"readOnlyHint": true}, Execution: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"base_url":{"type":"string"},"health_url":{"type":"string"},"mcp_url":{"type":"string"},"reachable":{"type":"boolean"},"health_ok":{"type":"boolean"},"mcp_available":{"type":"boolean"},"summary":{"type":"string"},"health_status":{"type":"integer"},"mcp_protocol_version":{"type":"string"},"tool_count":{"type":"integer"},"tool_sample":{"type":"array","items":{"type":"string"}},"errors":{"type":"array","items":{"type":"string"}},"cache":{"type":"object","properties":{"hit":{"type":"boolean"},"age_ms":{"type":"integer"},"ttl_ms":{"type":"integer"}},"required":["hit"]}},"required":["base_url","health_url","mcp_url","reachable","health_ok","mcp_available","summary"]}`)},
		{Name: "runtime_refresh_probe_cache", Description: "Refresh or clear cached runtime probe results in Workspace MCP. Safe mutating action for live-instance debug loops.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"clear_all":{"type":"boolean"}}}`), Annotations: map[string]any{"readOnlyHint": false}, Execution: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"cleared":{"type":"integer"},"remaining":{"type":"integer"},"scope":{"type":"string"},"endpoint":{"type":"string"}},"required":["cleared","remaining","scope"]}`)},
		{Name: "runtime_mcp_call", Description: "Call an MCP method on an external Overcast instance. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"method":{"type":"string"},"params":{},"id":{}},"required":["endpoint","method"]}`), Annotations: map[string]any{"readOnlyHint": false}, Execution: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"response":{}},"required":["endpoint","response"]}`)},
		{Name: "runtime_list_services", Description: "List enabled services on an external Overcast instance with state metadata. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"timeout_ms":{"type":"integer"}},"required":["endpoint"]}`), Annotations: map[string]any{"readOnlyHint": true}, Execution: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"reachable":{"type":"boolean"},"status_code":{"type":"integer"},"ok":{"type":"boolean"},"services":{"type":"array","items":{"type":"object"}},"error":{"type":"string"}},"required":["endpoint","reachable"]}`)},
		{Name: "runtime_get_health", Description: "Fetch /_health from an external Overcast instance and return the full health JSON. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"timeout_ms":{"type":"integer"}},"required":["endpoint"]}`), Annotations: map[string]any{"readOnlyHint": true}, Execution: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"reachable":{"type":"boolean"},"status_code":{"type":"integer"},"ok":{"type":"boolean"},"response":{},"error":{"type":"string"}},"required":["endpoint","reachable"]}`)},
		{Name: "runtime_get_config", Description: "Fetch /_debug/config from an external Overcast instance (requires debug mode on target) and return the config JSON. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"timeout_ms":{"type":"integer"}},"required":["endpoint"]}`), Annotations: map[string]any{"readOnlyHint": true}, Execution: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"reachable":{"type":"boolean"},"status_code":{"type":"integer"},"ok":{"type":"boolean"},"debug_required":{"type":"boolean"},"response":{},"error":{"type":"string"}},"required":["endpoint","reachable"]}`)},
		{Name: "runtime_get_service_state", Description: "Fetch /_debug/state or /_debug/state/{namespace} from an external Overcast instance. Returns bounded key/value snapshot. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"namespace":{"type":"string"},"key_pattern":{"type":"string"},"limit":{"type":"integer"},"timeout_ms":{"type":"integer"}},"required":["endpoint"]}`), Annotations: map[string]any{"readOnlyHint": true}, Execution: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"namespace":{"type":"string"},"reachable":{"type":"boolean"},"status_code":{"type":"integer"},"ok":{"type":"boolean"},"debug_required":{"type":"boolean"},"count":{"type":"integer"},"truncated":{"type":"boolean"},"limit":{"type":"integer"},"entries":{},"error":{"type":"string"}},"required":["endpoint","reachable"]}`)},
		{Name: "runtime_get_recent_events", Description: "Return bounded recent runtime events from an external Overcast instance. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"source":{"type":"string"},"type":{"type":"string"},"limit":{"type":"integer"},"timeout_ms":{"type":"integer"}},"required":["endpoint"]}`), Annotations: map[string]any{"readOnlyHint": true}, Execution: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"reachable":{"type":"boolean"},"status_code":{"type":"integer"},"ok":{"type":"boolean"},"limit":{"type":"integer"},"count":{"type":"integer"},"truncated":{"type":"boolean"},"events":{"type":"array","items":{"type":"object"}},"error":{"type":"string"}},"required":["endpoint","reachable"]}`)},
		{Name: "runtime_probe_kv_store", Description: "Probe bounded key/value state from /_debug/state or /_debug/state/{namespace} with filtering and cursor paging. Via Workspace MCP delegation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"namespace":{"type":"string"},"key_pattern":{"type":"string"},"limit":{"type":"integer"},"cursor":{"type":"string"},"include_values":{"type":"boolean"},"preview_bytes":{"type":"integer"},"timeout_ms":{"type":"integer"}},"required":["endpoint"]}`), Annotations: map[string]any{"readOnlyHint": true}, Execution: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"},"namespace":{"type":"string"},"reachable":{"type":"boolean"},"status_code":{"type":"integer"},"ok":{"type":"boolean"},"debug_required":{"type":"boolean"},"limit":{"type":"integer"},"cursor":{"type":"string"},"next_cursor":{"type":"string"},"count":{"type":"integer"},"total_matched":{"type":"integer"},"truncated":{"type":"boolean"},"entries":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"value_preview":{"type":"string"},"value":{}},"required":["key"]}},"error":{"type":"string"}},"required":["endpoint","reachable"]}`)},
		{Name: "repo_list_files", Description: "List files under a workspace-relative path with bounded results. Useful for token-efficient context gathering.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"max_results":{"type":"integer"},"include_hidden":{"type":"boolean"}}}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"base":{"type":"string"},"count":{"type":"integer"},"files":{"type":"array","items":{"type":"string"}},"max_results":{"type":"integer"}},"required":["base","count","files","max_results"]}`)},
		{Name: "repo_read_file_snippet", Description: "Read a specific line range from a workspace file.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer"},"end_line":{"type":"integer"}},"required":["path"]}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer"},"end_line":{"type":"integer"},"line_count":{"type":"integer"},"content":{"type":"string"}},"required":["path","start_line","end_line","line_count","content"]}`)},
		{Name: "repo_service_capabilities", Description: "Return per-operation capability details (status, category, notes) for a service. Requires dev build.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"}},"required":["service"]}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"count":{"type":"integer"},"operations":{"type":"array","items":{"type":"object","properties":{"operation":{"type":"string"},"category":{"type":"string"},"status":{"type":"string"},"notes":{"type":"string"}},"required":["operation","category","status"]}}},"required":["service","count","operations"]}`)},
	}
}

func (p *RepoProvider) Handler(name string) (HandlerFunc, bool) {
	handlers := map[string]HandlerFunc{
		"workspace_server_info":       p.toolWorkspaceServerInfo,
		"repo_workspace_info":         p.toolWorkspaceInfo,
		"repo_build_commands":         p.toolBuildCommands,
		"repo_find_todos":             p.toolFindTodos,
		"repo_service_coverage":       p.toolServiceCoverage,
		"repo_service_files":          p.toolServiceFiles,
		"repo_doc_coverage":           p.toolDocCoverage,
		"repo_cloudformation_links":   p.toolCloudFormationLinks,
		"repo_service_manifest":       p.toolServiceManifest,
		"repo_operation_support":      p.toolOperationSupport,
		"repo_find_symbol":            p.toolFindSymbol,
		"repo_conventions_snapshot":   p.toolConventionsSnapshot,
		"repo_topology_contributors":  p.toolTopologyContributors,
		"repo_related_files":          p.toolRelatedFiles,
		"repo_endpoint_map":           p.toolEndpointMap,
		"repo_change_impact":          p.toolChangeImpact,
		"repo_test_targets":           p.toolTestTargets,
		"repo_compat_rerun_subset":    p.toolCompatRerunSubset,
		"runtime_list_instances":      p.toolRuntimeListInstances,
		"runtime_probe_instance":      p.toolRuntimeProbeInstance,
		"runtime_refresh_probe_cache": p.toolRuntimeRefreshProbeCache,
		"runtime_mcp_call":            p.toolRuntimeMCPCall,
		"runtime_list_services":       p.toolRuntimeListServices,
		"runtime_get_health":          p.toolRuntimeGetHealth,
		"runtime_get_config":          p.toolRuntimeGetConfig,
		"runtime_get_service_state":   p.toolRuntimeGetServiceState,
		"runtime_get_recent_events":   p.toolRuntimeGetRecentEvents,
		"runtime_probe_kv_store":      p.toolRuntimeProbeKVStore,
		"repo_list_files":             p.toolListFiles,
		"repo_read_file_snippet":      p.toolReadFileSnippet,
		"repo_service_capabilities":   p.toolServiceCapabilities,
	}
	fn, ok := handlers[name]
	return fn, ok
}

func (p *RepoProvider) toolDocCoverage(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service string `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	service := strings.ToLower(strings.TrimSpace(args.Service))
	if service == "" {
		return nil, fmt.Errorf("service is required")
	}
	paths := map[string]string{
		"service_doc":        filepath.Join("docs", "services", service+".md"),
		"integration_test":   filepath.Join("tests", "integration", service, service+"_test.go"),
		"web_api":            filepath.Join("web", "src", "services", "api", service+".ts"),
		"search_contributor": filepath.Join("web", "src", "lib", "search-contributors", service+".ts"),
	}
	coverage := make(map[string]bool, len(paths))
	foundPaths := make(map[string]string, len(paths))
	for key, rel := range paths {
		abs := filepath.Join(p.workspaceRoot, rel)
		coverage[key] = fileExists(abs)
		if coverage[key] {
			foundPaths[key] = filepath.ToSlash(rel)
		}
	}
	changelogMentions := false
	if b, err := os.ReadFile(filepath.Join(p.workspaceRoot, "CHANGELOG.md")); err == nil {
		changelogMentions = strings.Contains(strings.ToLower(string(b)), service)
	}
	return map[string]any{
		"service":            service,
		"coverage":           coverage,
		"found_paths":        foundPaths,
		"changelog_mentions": changelogMentions,
	}, nil
}

func (p *RepoProvider) toolCloudFormationLinks(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service string `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	service := strings.ToLower(strings.TrimSpace(args.Service))
	if service == "" {
		return nil, fmt.Errorf("service is required")
	}
	serviceFiles := []string{}
	if files, err := p.listRelativeFiles(filepath.Join(p.workspaceRoot, "internal", "services", service)); err == nil {
		serviceFiles = files
	}
	docPath := filepath.Join("docs", "services", service+".md")
	if !fileExists(filepath.Join(p.workspaceRoot, docPath)) {
		docPath = ""
	}
	cfnFiles := []string{}
	cfnRoot := filepath.Join(p.workspaceRoot, "internal", "services", "cloudformation")
	if files, err := p.listRelativeFiles(cfnRoot); err == nil {
		for _, rel := range files {
			abs := filepath.Join(p.workspaceRoot, filepath.FromSlash(rel))
			b, readErr := os.ReadFile(abs)
			if readErr != nil {
				continue
			}
			if strings.Contains(strings.ToLower(string(b)), service) {
				cfnFiles = append(cfnFiles, rel)
			}
		}
	}
	return map[string]any{
		"service":              service,
		"service_files":        uniqueSorted(serviceFiles),
		"service_doc":          filepath.ToSlash(docPath),
		"cloudformation_files": uniqueSorted(cfnFiles),
	}, nil
}

func (p *RepoProvider) toolServiceManifest(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service string `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	doc, err := p.readGeneratedServiceSupport()
	if err != nil {
		return nil, err
	}
	service := strings.ToLower(strings.TrimSpace(args.Service))
	if service == "" {
		return map[string]any{
			"generated_by": doc.GeneratedBy,
			"total_ops":    doc.TotalOps,
			"count":        len(doc.Services),
			"services":     doc.Services,
		}, nil
	}
	for _, entry := range doc.Services {
		if strings.EqualFold(entry.Service, service) {
			return map[string]any{
				"generated_by": doc.GeneratedBy,
				"total_ops":    doc.TotalOps,
				"count":        1,
				"services":     []generatedSupportService{entry},
			}, nil
		}
	}
	return nil, fmt.Errorf("service %q not found in generated support metadata", service)
}

func (p *RepoProvider) toolOperationSupport(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service   string `json:"service"`
		Operation string `json:"operation"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	service := strings.ToLower(strings.TrimSpace(args.Service))
	operation := strings.TrimSpace(args.Operation)
	if service == "" || operation == "" {
		return nil, fmt.Errorf("service and operation are required")
	}
	doc, err := p.readGeneratedServiceSupport()
	if err != nil {
		return nil, err
	}
	for _, entry := range doc.Services {
		if !strings.EqualFold(entry.Service, service) {
			continue
		}
		for _, op := range entry.Operations {
			if strings.EqualFold(op.Operation, operation) {
				return map[string]any{
					"service":      entry.Service,
					"display_name": entry.DisplayName,
					"operation":    op.Operation,
					"category":     op.Category,
					"status":       op.Status,
					"notes":        op.Notes,
				}, nil
			}
		}
		return nil, fmt.Errorf("operation %q not found for service %q", operation, service)
	}
	return nil, fmt.Errorf("service %q not found in generated support metadata", service)
}

func (p *RepoProvider) toolTopologyContributors(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service string `json:"service"`
		Path    string `json:"path"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	service := strings.ToLower(strings.TrimSpace(args.Service))
	if service == "" && strings.TrimSpace(args.Path) != "" {
		service = serviceFromPath(args.Path)
	}
	if service == "" {
		return nil, fmt.Errorf("service or path is required")
	}
	contributors := map[string][]string{
		"router_topology":  {},
		"service_internal": {},
		"service_tests":    {},
		"web_routes":       {},
		"web_api":          {},
	}
	if files, err := p.findRelativePathsContaining(filepath.Join(p.workspaceRoot, "internal", "router"), "topology"); err == nil {
		contributors["router_topology"] = append(contributors["router_topology"], files...)
	}
	if files, err := p.listRelativeFiles(filepath.Join(p.workspaceRoot, "internal", "services", service)); err == nil {
		contributors["service_internal"] = append(contributors["service_internal"], files...)
	}
	if files, err := p.listRelativeFiles(filepath.Join(p.workspaceRoot, "tests", "integration", service)); err == nil {
		contributors["service_tests"] = append(contributors["service_tests"], files...)
	}
	if routes, err := p.findRelativePathsContaining(filepath.Join(p.workspaceRoot, "web", "src", "routes"), service); err == nil {
		contributors["web_routes"] = append(contributors["web_routes"], routes...)
	}
	webAPI := filepath.Join("web", "src", "services", "api", service+".ts")
	if fileExists(filepath.Join(p.workspaceRoot, webAPI)) {
		contributors["web_api"] = append(contributors["web_api"], filepath.ToSlash(webAPI))
	}
	for key := range contributors {
		contributors[key] = uniqueSorted(contributors[key])
	}
	return map[string]any{"service": service, "contributors": contributors}, nil
}

func (p *RepoProvider) readGeneratedServiceSupport() (generatedSupportDoc, error) {
	var doc generatedSupportDoc
	path := filepath.Join(p.workspaceRoot, "docs", "generated", "service-support.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doc, fmt.Errorf("generated support metadata missing at %q", filepath.ToSlash(filepath.Join("docs", "generated", "service-support.json")))
		}
		return doc, err
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return doc, fmt.Errorf("invalid generated support metadata: %w", err)
	}
	return doc, nil
}

func (p *RepoProvider) toolFindSymbol(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Symbol     string `json:"symbol"`
		PathPrefix string `json:"path_prefix"`
		MaxResults int    `json:"max_results"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	result, err := p.symbolFinder.FindSymbol(ctx, SymbolQuery{
		Symbol:     args.Symbol,
		PathPrefix: args.PathPrefix,
		MaxResults: args.MaxResults,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"backend":     result.Backend,
		"symbol":      result.Symbol,
		"path_prefix": result.PathPrefix,
		"definitions": symbolHitsToMaps(result.Definitions),
		"references":  symbolHitsToMaps(result.References),
		"truncated":   result.Truncated,
		"max_results": result.MaxResults,
	}, nil
}

func (p *RepoProvider) toolConventionsSnapshot(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		MaxLinesPerFile int `json:"max_lines_per_file"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	maxLines := args.MaxLinesPerFile
	if maxLines <= 0 {
		maxLines = 40
	}
	paths := []string{"CONTRIBUTING.md", "AGENTS.md", filepath.ToSlash(filepath.Join("tests", "AGENTS.md"))}
	files := make([]map[string]any, 0, len(paths))
	for _, rel := range paths {
		abs := filepath.Join(p.workspaceRoot, filepath.FromSlash(rel))
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		lines := boundedImportantLines(string(b), maxLines)
		files = append(files, map[string]any{"path": rel, "lines": lines})
	}
	return map[string]any{"files": files, "max_lines_per_file": maxLines}, nil
}

func (p *RepoProvider) toolWorkspaceServerInfo(_ context.Context, _ json.RawMessage) (any, error) {
	result := map[string]any{
		"serverRole":    "workspace",
		"serverName":    "Overcast Workspace MCP",
		"serverVersion": "1.0.0",
		"purpose":       "Develop and debug Overcast",
		"description":   "This is the Workspace MCP Server. It helps agents work with the Overcast codebase, run tests, and probe/debug Overcast runtime instances. Use repo_* tools for workspace operations and runtime_* tools to interact with external Overcast instances (via delegation).",
		"toolCategories": map[string]string{
			"workspace":  "repo_* tools - for developing Overcast (find symbols, check coverage, run tests, etc.)",
			"delegation": "runtime_* tools - for probing and debugging external Overcast instances",
		},
		"gettingStarted": map[string]any{
			"step1": "Call workspace_server_info (this tool) to understand the server",
			"step2": "Use repo_workspace_info to understand the workspace structure",
			"step3": "Use repo_build_commands to see available build/test commands",
			"step4": "Use runtime_list_instances to discover available external instances (if any)",
			"step5": "Use runtime_probe_instance to check if instances are healthy",
		},
		"commonWorkflows": map[string]any{
			"developFeature": []string{
				"1. repo_find_symbol - locate relevant code",
				"2. repo_related_files - find tests and docs",
				"3. Make changes locally",
				"4. repo_build_commands - build and test",
				"5. repo_change_impact - verify no breakage",
			},
			"debugInstance": []string{
				"1. runtime_list_instances - find instances",
				"2. runtime_probe_instance - check health",
				"3. runtime_mcp_call - invoke MCP methods on instance",
				"4. Analyze response or errors",
			},
			"verifyChanges": []string{
				"1. repo_test_targets - get focused test commands",
				"2. repo_change_impact - estimate affected services",
				"3. repo_doc_coverage - verify docs updated",
			},
		},
		"prerequisites": map[string]string{
			"workspace": "Valid Overcast source tree at workspace root",
			"go":        "Go 1.21+ (for building/testing)",
			"docker":    "Docker (for running Overcast instances locally)",
			"instances": "Optional: docker-compose.yml defines local instance endpoints",
		},
		"capabilities": map[string]bool{
			"readSourceCode":     true,
			"findSymbols":        true,
			"analyzeImpact":      true,
			"runTests":           true,
			"probeExternalSvc":   true,
			"delegateToInstance": true,
		},
		"overcastProject": map[string]any{
			"name":        "Overcast",
			"description": "A high-fidelity local AWS emulator for development and testing. Emulates ~27 AWS services with accurate request/response semantics.",
			"purpose":     "Enable local development, testing, and debugging without AWS credentials or costs. Primary use: unit/integration testing and rapid iteration.",
			"approach":    "Emulates the most-used 20% of each service's API with high fidelity rather than 100% API parity. Focus on correctness over completeness.",
			"notFor":      "Performance testing, security boundary, or production dependency. Local dev and CI only.",
			"keyFeatures": []string{
				"Supports 27 AWS services (S3, SQS, DynamoDB, Lambda, ECS, EC2, VPC, IAM, CloudFormation, EventBridge, KMS, SNS, etc.)",
				"Docker-based for isolation and reproducibility",
				"HTTP API compatible with AWS CLI and SDKs",
				"MCP server interface for IDE integration and automation",
				"SQLite or in-memory state storage",
			},
			"architecture": "Unified HTTP server with per-service handlers. Stdio and HTTP transports via MCP spec 2025-11-25.",
		},
		"documentation": map[string]string{
			"readme":       "README.md - Overview and quickstart",
			"contributing": "CONTRIBUTING.md - Coding standards and architecture",
			"status":       "STATUS.md - Implementation status by service",
			"agents":       "AGENTS.md - Guidelines for AI agents working on Overcast",
		},
		"mcp_version": ProtocolVersion,
	}
	return ToolResult{
		Content:           TextContent("Workspace MCP server for Overcast. Use repo_* tools for local workspace analysis and runtime_* tools to discover or delegate to running Overcast instances."),
		StructuredContent: result,
	}, nil
}

func (p *RepoProvider) toolWorkspaceInfo(_ context.Context, _ json.RawMessage) (any, error) {
	entries, err := os.ReadDir(filepath.Join(p.workspaceRoot, "internal", "services"))
	routesCount := countFiles(filepath.Join(p.workspaceRoot, "web", "src", "routes"), ".tsx")
	serviceDocsCount := countFiles(filepath.Join(p.workspaceRoot, "docs", "services"), ".md")
	if err != nil {
		return map[string]any{"workspace_root": p.workspaceRoot, "mcp_version": ProtocolVersion, "service_count": 0, "route_count": routesCount, "service_docs": serviceDocsCount}, nil
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			count++
		}
	}
	return map[string]any{
		"workspace_root": p.workspaceRoot,
		"mcp_version":    ProtocolVersion,
		"service_count":  count,
		"route_count":    routesCount,
		"service_docs":   serviceDocsCount,
		"key_paths":      map[string]string{"services": "internal/services", "tests": "tests/integration", "service_docs": "docs/services", "web_routes": "web/src/routes", "web_api": "web/src/services/api"},
	}, nil
}

func (p *RepoProvider) toolBuildCommands(_ context.Context, _ json.RawMessage) (any, error) {
	b, err := os.ReadFile(filepath.Join(p.workspaceRoot, "Makefile"))
	if err != nil {
		return nil, fmt.Errorf("read Makefile: %w", err)
	}
	commands := make([]commandEntry, 0, 32)
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, "## "), ":", 2)
		if len(parts) != 2 {
			continue
		}
		target := strings.TrimSpace(parts[0])
		commands = append(commands, commandEntry{Target: target, Description: strings.TrimSpace(parts[1]), Command: "make " + target})
	}
	result := map[string]any{"count": len(commands), "commands": commands, "windows_note": "Use task <target> on Windows; Taskfile targets mirror Makefile targets."}
	return ToolResult{
		Content:           TextContent(fmt.Sprintf("Found %d documented Makefile commands for building, testing, linting, and container workflows.", len(commands))),
		StructuredContent: result,
	}, nil
}

func (p *RepoProvider) toolFindTodos(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service       string `json:"service"`
		Priority      string `json:"priority"`
		PathPrefix    string `json:"path_prefix"`
		MaxResults    int    `json:"max_results"`
		CanonicalOnly bool   `json:"canonical_only"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	serviceFilter := strings.ToLower(strings.TrimSpace(args.Service))
	priorityFilter := strings.ToUpper(strings.TrimSpace(args.Priority))
	pathPrefix := filepath.ToSlash(strings.TrimSpace(args.PathPrefix))
	if pathPrefix == "." {
		pathPrefix = ""
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 200
	}

	todos := make([]todoEntry, 0, minInt(maxResults, 64))
	byPriority := map[string]int{}
	byLocation := map[string]int{}
	byService := map[string]int{}
	byTag := map[string]int{}
	totalMatches := 0
	canonicalCount := 0
	scannedFiles := 0
	truncated := false

	err := filepath.WalkDir(p.workspaceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			rel, err := filepath.Rel(p.workspaceRoot, path)
			if err != nil {
				return filepath.SkipDir
			}
			rel = filepath.ToSlash(rel)
			switch name {
			case ".git", "node_modules", "bin", "dist", "coverage", "tmp":
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") && rel != "." && rel != ".github" {
				return filepath.SkipDir
			}
			if pathPrefix != "" && rel != "." && !strings.HasPrefix(rel+"/", strings.TrimSuffix(pathPrefix, "/")+"/") && !strings.HasPrefix(strings.TrimSuffix(pathPrefix, "/")+"/", rel+"/") && rel != pathPrefix {
				return nil
			}
			return nil
		}

		rel, err := filepath.Rel(p.workspaceRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if pathPrefix != "" && !strings.HasPrefix(rel, strings.TrimSuffix(pathPrefix, "/")+"/") && rel != pathPrefix {
			return nil
		}
		if !isLikelyTODOFile(rel) {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		scannedFiles++

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			entry, ok := parseTODOEntry(rel, lineNo, scanner.Text())
			if !ok {
				continue
			}
			if args.CanonicalOnly && !entry.Canonical {
				continue
			}
			if serviceFilter != "" && entry.Service != serviceFilter {
				continue
			}
			if priorityFilter != "" && entry.Priority != priorityFilter {
				continue
			}
			totalMatches++
			if entry.Canonical {
				canonicalCount++
			}
			if entry.Priority != "" {
				byPriority[entry.Priority]++
			}
			if entry.Tag != "" {
				byTag[entry.Tag]++
			}
			byLocation[entry.Location]++
			if entry.Service != "" {
				byService[entry.Service]++
			}
			if len(todos) < maxResults {
				todos = append(todos, entry)
			} else {
				truncated = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"count":           len(todos),
		"total_matches":   totalMatches,
		"canonical_count": canonicalCount,
		"truncated":       truncated,
		"scanned_files":   scannedFiles,
		"todos":           todos,
		"by_priority":     byPriority,
		"by_location":     byLocation,
		"by_service":      byService,
		"by_tag":          byTag,
	}, nil
}

func (p *RepoProvider) toolServiceCoverage(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service string `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	coverage, err := p.readServiceCoverage()
	if err != nil {
		coverage = nil
	}
	coverage = p.mergeCodeDerivedCoverage(coverage)
	if args.Service == "" {
		return map[string]any{"count": len(coverage), "services": coverage}, nil
	}
	for _, entry := range coverage {
		if strings.EqualFold(entry.Service, args.Service) {
			return entry, nil
		}
	}
	if entry, ok := p.codeDerivedCoverageForService(args.Service); ok {
		return entry, nil
	}
	return nil, fmt.Errorf("service %q not found in coverage metadata", args.Service)
}

func (p *RepoProvider) toolServiceFiles(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Service string `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if args.Service == "" {
		return nil, fmt.Errorf("service is required")
	}
	service := strings.ToLower(args.Service)
	internalFiles, err := p.listRelativeFiles(filepath.Join(p.workspaceRoot, "internal", "services", service))
	if err != nil {
		return nil, fmt.Errorf("service %q not found under internal/services", service)
	}
	result := map[string]any{"service": service, "internal_files": internalFiles}
	for key, rel := range map[string]string{
		"integration_test":   filepath.Join("tests", "integration", service, service+"_test.go"),
		"service_doc":        filepath.Join("docs", "services", service+".md"),
		"web_api":            filepath.Join("web", "src", "services", "api", service+".ts"),
		"search_contributor": filepath.Join("web", "src", "lib", "search-contributors", service+".ts"),
	} {
		if fileExists(filepath.Join(p.workspaceRoot, rel)) {
			result[key] = filepath.ToSlash(rel)
		}
	}
	if routes, err := p.findRelativePathsContaining(filepath.Join(p.workspaceRoot, "web", "src", "routes"), service); err == nil && len(routes) > 0 {
		result["web_routes"] = routes
	}
	return result, nil
}

func (p *RepoProvider) toolListFiles(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Path          string `json:"path"`
		MaxResults    int    `json:"max_results"`
		IncludeHidden bool   `json:"include_hidden"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	base := args.Path
	if base == "" {
		base = "."
	}
	basePath, err := p.resolveWorkspacePath(base)
	if err != nil {
		return nil, err
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 200
	}
	files := make([]string, 0, maxResults)
	err = filepath.WalkDir(basePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(p.workspaceRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			if !args.IncludeHidden && strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !args.IncludeHidden && strings.HasPrefix(name, ".") {
			return nil
		}
		files = append(files, rel)
		if len(files) >= maxResults {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return map[string]any{"base": filepath.ToSlash(base), "count": len(files), "files": files, "max_results": maxResults}, nil
}

func (p *RepoProvider) toolRelatedFiles(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Path    string `json:"path"`
		Service string `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	service := strings.ToLower(strings.TrimSpace(args.Service))
	if service == "" && args.Path != "" {
		service = serviceFromPath(args.Path)
	}
	if service == "" {
		return nil, fmt.Errorf("path or service is required")
	}
	companions := map[string][]string{
		"internal":            {},
		"integration_tests":   {},
		"docs":                {},
		"web_api":             {},
		"search_contributors": {},
		"web_routes":          {},
		"topology":            {},
	}
	if files, err := p.listRelativeFiles(filepath.Join(p.workspaceRoot, "internal", "services", service)); err == nil {
		companions["internal"] = append(companions["internal"], files...)
	}
	for key, rel := range map[string]string{
		"integration_tests":   filepath.Join("tests", "integration", service, service+"_test.go"),
		"docs":                filepath.Join("docs", "services", service+".md"),
		"web_api":             filepath.Join("web", "src", "services", "api", service+".ts"),
		"search_contributors": filepath.Join("web", "src", "lib", "search-contributors", service+".ts"),
	} {
		if fileExists(filepath.Join(p.workspaceRoot, rel)) {
			companions[key] = append(companions[key], filepath.ToSlash(rel))
		}
	}
	if routes, err := p.findRelativePathsContaining(filepath.Join(p.workspaceRoot, "web", "src", "routes"), service); err == nil {
		companions["web_routes"] = append(companions["web_routes"], routes...)
	}
	if topologyFiles, err := p.findRelativePathsContaining(filepath.Join(p.workspaceRoot, "internal", "router"), "topology"); err == nil {
		companions["topology"] = append(companions["topology"], topologyFiles...)
	}
	for key := range companions {
		companions[key] = uniqueSorted(companions[key])
	}
	return map[string]any{"service": service, "companions": companions}, nil
}

func (p *RepoProvider) toolEndpointMap(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Prefix string `json:"prefix"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	prefix := strings.TrimSpace(args.Prefix)
	routerRoot := filepath.Join(p.workspaceRoot, "internal", "router")
	files, err := p.listRelativeFiles(routerRoot)
	if err != nil {
		return nil, fmt.Errorf("router files unavailable: %w", err)
	}
	endpointsByConcern := map[string][]string{
		"health":   {},
		"debug":    {},
		"topology": {},
		"mcp":      {},
		"internal": {},
	}
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		abs := filepath.Join(p.workspaceRoot, filepath.FromSlash(rel))
		b, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		for _, endpoint := range extractEndpointsFromSource(string(b)) {
			if prefix != "" && !strings.HasPrefix(endpoint, prefix) {
				continue
			}
			concern := classifyEndpoint(endpoint)
			endpointsByConcern[concern] = append(endpointsByConcern[concern], endpoint)
		}
	}
	for key := range endpointsByConcern {
		endpointsByConcern[key] = uniqueSorted(endpointsByConcern[key])
	}
	return map[string]any{"prefix": prefix, "endpoints": endpointsByConcern}, nil
}

func (p *RepoProvider) toolChangeImpact(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Paths   []string `json:"paths"`
		Service string   `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	paths := normalizeRelPaths(args.Paths)
	services := make(map[string]struct{})
	if args.Service != "" {
		services[strings.ToLower(args.Service)] = struct{}{}
	}
	for _, path := range paths {
		if svc := serviceFromPath(path); svc != "" {
			services[svc] = struct{}{}
		}
	}
	serviceList := sortedKeys(services)
	filesByRole := map[string][]string{
		"internal": {},
		"tests":    {},
		"docs":     {},
		"web":      {},
		"topology": {},
	}
	for _, path := range paths {
		switch {
		case strings.Contains(path, "topology"):
			filesByRole["topology"] = append(filesByRole["topology"], path)
		case strings.HasPrefix(path, "internal/"):
			filesByRole["internal"] = append(filesByRole["internal"], path)
		case strings.HasPrefix(path, "tests/"):
			filesByRole["tests"] = append(filesByRole["tests"], path)
		case strings.HasPrefix(path, "docs/"):
			filesByRole["docs"] = append(filesByRole["docs"], path)
		case strings.HasPrefix(path, "web/"):
			filesByRole["web"] = append(filesByRole["web"], path)
		}
	}
	recommendedTests := make(map[string]struct{})
	recommendedCommands := make(map[string]struct{})
	testFileMap := make(map[string][]string)
	hasGoValidation := false
	for _, path := range paths {
		if files, err := p.collectCandidateTestsForPath(path); err == nil && len(files) > 0 {
			testFileMap[path] = files
		}
	}
	for _, svc := range serviceList {
		servicePath := filepath.ToSlash(filepath.Join("internal", "services", svc))
		if files, err := p.collectCandidateTestsForPath(servicePath); err == nil && len(files) > 0 {
			testFileMap[servicePath] = files
		}
		for key, rel := range map[string]string{
			"docs":  filepath.Join("docs", "services", svc+".md"),
			"tests": filepath.Join("tests", "integration", svc, svc+"_test.go"),
			"web":   filepath.Join("web", "src", "services", "api", svc+".ts"),
		} {
			if fileExists(filepath.Join(p.workspaceRoot, rel)) {
				filesByRole[key] = append(filesByRole[key], filepath.ToSlash(rel))
			}
		}
		if _, err := os.Stat(filepath.Join(p.workspaceRoot, "tests", "integration", svc)); err == nil {
			recommendedTests[fmt.Sprintf("go test ./tests/integration/%s/...", svc)] = struct{}{}
		}
		if _, err := os.Stat(filepath.Join(p.workspaceRoot, "internal", "services", svc)); err == nil {
			recommendedCommands[fmt.Sprintf("go test ./internal/services/%s/...", svc)] = struct{}{}
			hasGoValidation = true
		}
	}
	for _, path := range paths {
		if strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "tests/") {
			hasGoValidation = true
		}
		if strings.HasPrefix(path, "web/") {
			recommendedCommands["cd web && npm run build"] = struct{}{}
		}
	}
	if len(recommendedTests) > 0 {
		recommendedCommands["make test-integration"] = struct{}{}
	}
	if hasGoValidation {
		recommendedCommands["make test-unit"] = struct{}{}
		recommendedCommands["make check"] = struct{}{}
	}
	for key := range filesByRole {
		filesByRole[key] = uniqueSorted(filesByRole[key])
	}
	return map[string]any{
		"paths":                paths,
		"services":             serviceList,
		"files_by_role":        filesByRole,
		"test_file_map":        mapValuesSorted(testFileMap),
		"recommended_tests":    sortedKeys(recommendedTests),
		"recommended_commands": sortedKeys(recommendedCommands),
	}, nil
}

func (p *RepoProvider) toolTestTargets(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Paths   []string `json:"paths"`
		Service string   `json:"service"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	paths := normalizeRelPaths(args.Paths)
	unitPackages := make(map[string]struct{})
	integrationPackages := make(map[string]struct{})
	testFileMap := make(map[string][]string)
	recommended := make(map[string]struct{})
	if args.Service != "" {
		svc := strings.ToLower(args.Service)
		if _, err := os.Stat(filepath.Join(p.workspaceRoot, "internal", "services", svc)); err == nil {
			unitPackages[fmt.Sprintf("./internal/services/%s/...", svc)] = struct{}{}
		}
		if _, err := os.Stat(filepath.Join(p.workspaceRoot, "tests", "integration", svc)); err == nil {
			integrationPackages[fmt.Sprintf("./tests/integration/%s/...", svc)] = struct{}{}
		}
		if files, err := p.collectCandidateTestsForPath(filepath.Join("internal", "services", svc)); err == nil {
			testFileMap[filepath.ToSlash(filepath.Join("internal", "services", svc))] = files
		}
	}
	for _, path := range paths {
		if files, err := p.collectCandidateTestsForPath(path); err == nil && len(files) > 0 {
			testFileMap[path] = files
		}
		switch {
		case strings.HasPrefix(path, "internal/services/"):
			if svc := serviceFromPath(path); svc != "" {
				unitPackages[fmt.Sprintf("./internal/services/%s/...", svc)] = struct{}{}
			}
		case strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "cmd/"):
			if pkg := packagePatternForPath(path); pkg != "" {
				unitPackages[pkg] = struct{}{}
			}
		case strings.HasPrefix(path, "tests/integration/"):
			if svc := serviceFromPath(path); svc != "" {
				integrationPackages[fmt.Sprintf("./tests/integration/%s/...", svc)] = struct{}{}
			}
		case strings.HasPrefix(path, "web/"):
			recommended["cd web && npm run build"] = struct{}{}
			recommended["cd web && npm run lint"] = struct{}{}
		}
	}
	for pkg := range unitPackages {
		recommended[fmt.Sprintf("go test %s", pkg)] = struct{}{}
	}
	for pkg := range integrationPackages {
		recommended[fmt.Sprintf("go test %s", pkg)] = struct{}{}
	}
	if len(unitPackages) > 0 {
		recommended["make test-unit"] = struct{}{}
	}
	if len(integrationPackages) > 0 {
		recommended["make test-integration"] = struct{}{}
	}
	if len(unitPackages) > 0 || len(integrationPackages) > 0 {
		recommended["make check"] = struct{}{}
	}
	return map[string]any{
		"unit_packages":        sortedKeys(unitPackages),
		"integration_packages": sortedKeys(integrationPackages),
		"test_file_map":        mapValuesSorted(testFileMap),
		"recommended_commands": sortedKeys(recommended),
	}, nil
}

func (p *RepoProvider) toolReadFileSnippet(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if args.StartLine <= 0 {
		args.StartLine = 1
	}
	if args.EndLine < args.StartLine {
		args.EndLine = args.StartLine
	}
	absPath, err := p.resolveWorkspacePath(args.Path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := make([]string, 0, args.EndLine-args.StartLine+1)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < args.StartLine {
			continue
		}
		if lineNo > args.EndLine {
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(p.workspaceRoot, absPath)
	return map[string]any{"path": filepath.ToSlash(rel), "start_line": args.StartLine, "end_line": args.EndLine, "line_count": len(lines), "content": strings.Join(lines, "\n")}, nil
}

func (p *RepoProvider) toolRuntimeListInstances(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoints []string `json:"endpoints"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	endpoints := []discoveredRuntimeEndpoint{
		{raw: "http://localhost:4566", source: "default_localhost"},
		{raw: "http://127.0.0.1:4566", source: "default_loopback"},
	}
	composeEndpoints, discoveryContext := p.discoverComposeRuntimeEndpoints()
	endpoints = append(endpoints, composeEndpoints...)
	if env := strings.TrimSpace(os.Getenv("OVERCAST_ENDPOINT")); env != "" {
		endpoints = append(endpoints, discoveredRuntimeEndpoint{raw: env, source: "env_overcast_endpoint"})
	}
	for _, endpoint := range args.Endpoints {
		endpoints = append(endpoints, discoveredRuntimeEndpoint{raw: endpoint, source: "input_endpoint"})
	}

	out := make([]map[string]any, 0, len(endpoints))
	seen := make(map[string]int, len(endpoints))
	for _, candidate := range endpoints {
		base, err := normalizeEndpoint(candidate.raw)
		if err != nil {
			continue
		}
		host, port, endpointKind, containerHint := classifyDiscoveredEndpoint(base)
		if idx, ok := seen[base]; ok {
			existing := out[idx]
			sources, _ := existing["sources"].([]string)
			existing["sources"] = uniqueSorted(append(sources, candidate.source))
			if existingHint, ok := existing["container_hint"].(bool); ok {
				existing["container_hint"] = existingHint || containerHint
			}
			continue
		}
		seen[base] = len(out)
		out = append(out, map[string]any{
			"base_url":       base,
			"health_url":     buildEndpointPath(base, "/_health"),
			"mcp_url":        buildEndpointPath(base, "/_mcp"),
			"role":           "probe_target",
			"source":         candidate.source,
			"sources":        []string{candidate.source},
			"host":           host,
			"port":           port,
			"endpoint_kind":  endpointKind,
			"container_hint": containerHint,
		})
	}
	return map[string]any{
		"count":             len(out),
		"instances":         out,
		"discovery_context": discoveryContext,
		"note":              "These are external runtime instances to probe. The MCP server itself (providing these tools) is accessed via the tool interface.",
	}, nil
}

func (p *RepoProvider) toolCompatRerunSubset(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Suites         []string `json:"suites"`
		Endpoint       string   `json:"endpoint"`
		TimeoutSeconds int      `json:"timeout_seconds"`
		MaxOutputLines int      `json:"max_output_lines"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if len(args.Suites) == 0 {
		return nil, fmt.Errorf("suites is required")
	}
	if len(args.Suites) > compatSubsetMaxSuites {
		return nil, fmt.Errorf("at most %d suites allowed per re-run", compatSubsetMaxSuites)
	}

	allowed, err := p.compatSuiteAllowlist()
	if err != nil {
		return nil, err
	}
	suites := make([]string, 0, len(args.Suites))
	seenSuites := make(map[string]struct{}, len(args.Suites))
	for _, suite := range args.Suites {
		suite = strings.ToLower(strings.TrimSpace(suite))
		if suite == "" {
			continue
		}
		if !compatSuiteNamePattern.MatchString(suite) {
			return nil, fmt.Errorf("invalid suite name %q", suite)
		}
		if _, ok := allowed[suite]; !ok {
			return nil, fmt.Errorf("suite %q is not allowlisted", suite)
		}
		if _, dup := seenSuites[suite]; dup {
			continue
		}
		seenSuites[suite] = struct{}{}
		suites = append(suites, suite)
	}
	if len(suites) == 0 {
		return nil, fmt.Errorf("no valid suites provided")
	}

	endpoint := strings.TrimSpace(args.Endpoint)
	if endpoint == "" {
		endpoint = "http://localhost:4566"
	}
	if _, err := normalizeEndpoint(endpoint); err != nil {
		return nil, err
	}

	maxLines := args.MaxOutputLines
	if maxLines <= 0 {
		maxLines = 80
	}
	if maxLines > 200 {
		maxLines = 200
	}

	timeout := 120 * time.Second
	if args.TimeoutSeconds > 0 {
		if args.TimeoutSeconds > 600 {
			args.TimeoutSeconds = 600
		}
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	suiteCSV := strings.Join(suites, ",")
	cmdArgs := []string{"run", "./cmd/compat", "--format", "agent", "--endpoint", endpoint, "--suite", suiteCSV}

	start := time.Now()
	out, exitCode, runErr := p.compatRunner(runCtx, cmdArgs)
	duration := time.Since(start)
	lines := splitLinesBounded(string(out), maxLines)
	truncated := len(lines) == maxLines && countLines(string(out)) > maxLines

	result := map[string]any{
		"suites":         suites,
		"endpoint":       endpoint,
		"command":        append([]string{"go"}, cmdArgs...),
		"success":        runErr == nil && exitCode == 0,
		"exit_code":      exitCode,
		"duration_ms":    int(duration / time.Millisecond),
		"output_preview": lines,
		"truncated":      truncated,
	}
	if runErr != nil {
		result["error"] = runErr.Error()
	}
	return result, nil
}

func (p *RepoProvider) compatSuiteAllowlist() (map[string]struct{}, error) {
	allowed := make(map[string]struct{}, 16)
	suitesDir := filepath.Join(p.workspaceRoot, "compat", "suites")
	entries, err := os.ReadDir(suitesDir)
	if err != nil {
		return nil, fmt.Errorf("read compat suites: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(strings.ToLower(entry.Name()))
		if !compatSuiteNamePattern.MatchString(name) {
			continue
		}
		allowed[name] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("no compat suites discovered under compat/suites")
	}
	return allowed, nil
}

func runCompatCommand(ctx context.Context, args []string) ([]byte, int, error) {
	if len(args) == 0 {
		return nil, -1, fmt.Errorf("empty command args")
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		cmd.Dir = wd
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out, exitErr.ExitCode(), err
	}
	return out, -1, err
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	count := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			count++
		}
	}
	return count
}

func splitLinesBounded(s string, maxLines int) []string {
	if maxLines <= 0 {
		return []string{}
	}
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, minInt(len(raw), maxLines))
	for _, line := range raw {
		if len(out) >= maxLines {
			break
		}
		out = append(out, line)
	}
	return out
}

func (p *RepoProvider) discoverComposeRuntimeEndpoints() ([]discoveredRuntimeEndpoint, map[string]any) {
	inContainer, containerSignal := detectContainerRuntimeSignal()
	files := []string{"docker-compose.dev.yml", "docker-compose.yml"}
	candidates := make([]discoveredRuntimeEndpoint, 0, 4)
	composeFiles := make([]string, 0, len(files))
	composePublished4566 := false
	composeOvercastService := false
	for _, file := range files {
		path := filepath.Join(p.workspaceRoot, file)
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		composeFiles = append(composeFiles, file)
		text := strings.ToLower(string(content))
		if strings.Contains(text, "4566:4566") {
			composePublished4566 = true
			candidates = append(candidates, discoveredRuntimeEndpoint{raw: "http://localhost:4566", source: "compose_published_4566"})
		}
		if strings.Contains(text, "overcast:") {
			composeOvercastService = true
		}
		if inContainer && composeOvercastService {
			candidates = append(candidates, discoveredRuntimeEndpoint{raw: "http://overcast:4566", source: "compose_service_overcast"})
		}
	}
	dockerEndpoints, dockerContext := p.discoverLiveDockerComposeEndpoints(composeFiles, inContainer)
	candidates = append(candidates, dockerEndpoints...)
	context := map[string]any{
		"in_container":             inContainer,
		"container_signal":         containerSignal,
		"compose_files":            uniqueSorted(composeFiles),
		"compose_published_4566":   composePublished4566,
		"compose_overcast_service": composeOvercastService,
	}
	for key, value := range dockerContext {
		context[key] = value
	}
	return candidates, context
}

func (p *RepoProvider) discoverLiveDockerComposeEndpoints(composeFiles []string, inContainer bool) ([]discoveredRuntimeEndpoint, map[string]any) {
	context := map[string]any{
		"docker_compose_probe":                    "skipped",
		"docker_compose_detected":                 false,
		"docker_compose_services":                 []string{},
		"docker_compose_overcast_running":         false,
		"docker_compose_overcast_published_ports": []int{},
	}
	if len(composeFiles) == 0 {
		return nil, context
	}
	if _, err := exec.LookPath("docker"); err != nil {
		context["docker_compose_probe"] = "unavailable"
		return nil, context
	}

	args := make([]string, 0, 2+len(composeFiles)*2)
	args = append(args, "compose")
	for _, file := range composeFiles {
		args = append(args, "-f", file)
	}
	args = append(args, "ps", "--format", "json")

	cmd := exec.Command("docker", args...)
	cmd.Dir = p.workspaceRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		context["docker_compose_probe"] = "error"
		errText := strings.TrimSpace(string(out))
		if errText == "" {
			errText = strings.TrimSpace(err.Error())
		}
		context["docker_compose_error"] = trimToLength(errText, 240)
		return nil, context
	}

	services, parseErr := parseDockerComposePSOutput(out)
	if parseErr != nil {
		context["docker_compose_probe"] = "error"
		context["docker_compose_error"] = trimToLength(parseErr.Error(), 240)
		return nil, context
	}

	context["docker_compose_probe"] = "ok"
	context["docker_compose_detected"] = len(services) > 0

	candidates := make([]discoveredRuntimeEndpoint, 0, 4)
	serviceNames := make([]string, 0, len(services))
	overcastPorts := make([]int, 0, 2)
	overcastRunning := false

	for _, svc := range services {
		if strings.TrimSpace(svc.Service) != "" {
			serviceNames = append(serviceNames, svc.Service)
		}
		if !strings.EqualFold(strings.TrimSpace(svc.Service), "overcast") {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(svc.State))
		status := strings.ToLower(strings.TrimSpace(svc.Status))
		if state == "running" || strings.Contains(status, "running") || strings.Contains(status, "up") {
			overcastRunning = true
		}
		for _, pub := range svc.Publishers {
			targetPort := pub.TargetPort
			if targetPort == 0 {
				targetPort = parsePortFromURL(pub.URL)
			}
			if targetPort != 4566 {
				continue
			}
			publishedPort := pub.PublishedPort
			if publishedPort == 0 {
				publishedPort = targetPort
			}
			if publishedPort <= 0 {
				continue
			}
			overcastPorts = append(overcastPorts, publishedPort)
			candidates = append(candidates, discoveredRuntimeEndpoint{raw: fmt.Sprintf("http://localhost:%d", publishedPort), source: "docker_compose_ps_published"})
		}
		if inContainer {
			candidates = append(candidates, discoveredRuntimeEndpoint{raw: "http://overcast:4566", source: "docker_compose_ps_service"})
		}
	}

	context["docker_compose_services"] = uniqueSorted(serviceNames)
	context["docker_compose_overcast_running"] = overcastRunning
	context["docker_compose_overcast_published_ports"] = uniqueSortedInts(overcastPorts)

	return candidates, context
}

func parseDockerComposePSOutput(out []byte) ([]dockerComposePSService, error) {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}

	services := make([]dockerComposePSService, 0, 8)
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &services); err != nil {
			return nil, fmt.Errorf("parse docker compose ps array: %w", err)
		}
		return services, nil
	}

	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var svc dockerComposePSService
		if err := json.Unmarshal([]byte(line), &svc); err != nil {
			return nil, fmt.Errorf("parse docker compose ps line: %w", err)
		}
		services = append(services, svc)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan docker compose ps output: %w", err)
	}
	return services, nil
}

func parsePortFromURL(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if !strings.Contains(raw, "://") {
		raw = "tcp://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	portText := strings.TrimSpace(parsed.Port())
	if portText == "" {
		return 0
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return 0
	}
	return port
}

func uniqueSortedInts(values []int) []int {
	if len(values) == 0 {
		return []int{}
	}
	set := make(map[int]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	out := make([]int, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

func trimToLength(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return strings.TrimSpace(value[:maxLen])
}

func detectContainerRuntimeSignal() (bool, string) {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true, "dockerenv"
	}
	if strings.TrimSpace(os.Getenv("container")) != "" {
		return true, "env_container"
	}
	return false, "none"
}

func classifyDiscoveredEndpoint(base string) (host string, port string, endpointKind string, containerHint bool) {
	host = ""
	port = ""
	endpointKind = "unknown"
	containerHint = false

	parsed, err := url.Parse(base)
	if err != nil {
		return host, port, endpointKind, containerHint
	}
	host = strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	port = strings.TrimSpace(parsed.Port())

	if host == "localhost" {
		endpointKind = "loopback"
		return host, port, endpointKind, false
	}

	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsLoopback():
			endpointKind = "loopback"
		case ip.IsPrivate():
			endpointKind = "private_ip"
		default:
			endpointKind = "public_ip"
		}
		return host, port, endpointKind, false
	}

	if strings.Contains(host, "docker") || strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") {
		containerHint = true
	}
	if !strings.Contains(host, ".") {
		endpointKind = "service_dns"
	} else {
		endpointKind = "hostname"
	}

	return host, port, endpointKind, containerHint
}

func (p *RepoProvider) toolRuntimeProbeInstance(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint     string `json:"endpoint"`
		TimeoutMS    int    `json:"timeout_ms"`
		ForceRefresh bool   `json:"force_refresh"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	if !args.ForceRefresh {
		if cached, ok := p.getProbeCache(base); ok {
			cachedOut := cloneMapAny(cached.result)
			age := time.Since(cached.fetchedAt)
			if age < 0 {
				age = 0
			}
			cachedOut["cache"] = map[string]any{
				"hit":    true,
				"age_ms": int(age / time.Millisecond),
				"ttl_ms": int(runtimeProbeCacheTTL / time.Millisecond),
			}
			return cachedOut, nil
		}
	}
	timeout := 2500 * time.Millisecond
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	client := &http.Client{Timeout: timeout}

	out := map[string]any{
		"base_url":      base,
		"health_url":    buildEndpointPath(base, "/_health"),
		"mcp_url":       buildEndpointPath(base, "/_mcp"),
		"reachable":     false,
		"health_ok":     false,
		"mcp_available": false,
	}
	errors := make([]string, 0, 4)

	healthReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, buildEndpointPath(base, "/_health"), nil)
	healthResp, healthErr := client.Do(healthReq)
	if healthErr != nil {
		errors = append(errors, "health probe failed: "+healthErr.Error())
	} else {
		out["reachable"] = true
		out["health_status"] = healthResp.StatusCode
		out["health_ok"] = healthResp.StatusCode >= 200 && healthResp.StatusCode < 300
		_ = healthResp.Body.Close()
	}

	initBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "probe-init",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "overcast-workspace-mcp", "version": "1.0.0"},
		},
	}
	initResp, initErr := doJSONRPC(ctx, client, buildEndpointPath(base, "/_mcp"), initBody)
	if initErr != nil {
		errors = append(errors, "mcp initialize failed: "+initErr.Error())
	} else {
		out["mcp_available"] = true
		if res, ok := initResp["result"].(map[string]any); ok {
			if pv, ok := res["protocolVersion"]; ok {
				out["mcp_protocol_version"] = pv
			}
		}
		// Complete the MCP lifecycle handshake before sending operation requests.
		// Per spec, the client must send notifications/initialized after initialize.
		// Without this the server stays in initDone=true, ready=false state, which
		// blocks all subsequent callers with a lifecycle error.
		_, _ = doJSONRPC(ctx, client, buildEndpointPath(base, "/_mcp"), map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
		toolsBody := map[string]any{"jsonrpc": "2.0", "id": "probe-tools", "method": "tools/list"}
		toolsResp, toolsErr := doJSONRPC(ctx, client, buildEndpointPath(base, "/_mcp"), toolsBody)
		if toolsErr != nil {
			errors = append(errors, "mcp tools/list failed: "+toolsErr.Error())
		} else {
			if res, ok := toolsResp["result"].(map[string]any); ok {
				if rawTools, ok := res["tools"].([]any); ok {
					out["tool_count"] = len(rawTools)
					names := make([]string, 0, minInt(len(rawTools), 20))
					for i := 0; i < len(rawTools) && i < 20; i++ {
						toolMap, ok := rawTools[i].(map[string]any)
						if !ok {
							continue
						}
						name, _ := toolMap["name"].(string)
						if strings.TrimSpace(name) != "" {
							names = append(names, name)
						}
					}
					out["tool_sample"] = names
				}
			}
		}
	}

	if len(errors) > 0 {
		out["errors"] = errors
	}

	// Generate human-readable summary for agent consumption
	out["summary"] = generateProbeSummary(out)
	out["cache"] = map[string]any{"hit": false, "ttl_ms": int(runtimeProbeCacheTTL / time.Millisecond)}
	p.setProbeCache(base, out)

	return out, nil
}

func (p *RepoProvider) toolRuntimeRefreshProbeCache(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint string `json:"endpoint"`
		ClearAll bool   `json:"clear_all"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	p.probeCacheMu.Lock()
	defer p.probeCacheMu.Unlock()

	if args.ClearAll {
		cleared := len(p.probeCache)
		p.probeCache = make(map[string]cachedProbeResult)
		return map[string]any{
			"cleared":   cleared,
			"remaining": 0,
			"scope":     "all",
		}, nil
	}

	endpoint := strings.TrimSpace(args.Endpoint)
	if endpoint == "" {
		cleared := len(p.probeCache)
		p.probeCache = make(map[string]cachedProbeResult)
		return map[string]any{
			"cleared":   cleared,
			"remaining": 0,
			"scope":     "all_default",
		}, nil
	}
	base, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	cleared := 0
	if _, ok := p.probeCache[base]; ok {
		delete(p.probeCache, base)
		cleared = 1
	}
	return map[string]any{
		"endpoint":  base,
		"cleared":   cleared,
		"remaining": len(p.probeCache),
		"scope":     "endpoint",
	}, nil
}

func (p *RepoProvider) getProbeCache(base string) (cachedProbeResult, bool) {
	now := time.Now().UTC()
	p.probeCacheMu.RLock()
	entry, ok := p.probeCache[base]
	p.probeCacheMu.RUnlock()
	if !ok {
		return cachedProbeResult{}, false
	}
	if now.After(entry.expiresAt) {
		p.probeCacheMu.Lock()
		delete(p.probeCache, base)
		p.probeCacheMu.Unlock()
		return cachedProbeResult{}, false
	}
	return entry, true
}

func (p *RepoProvider) setProbeCache(base string, result map[string]any) {
	now := time.Now().UTC()
	p.probeCacheMu.Lock()
	p.probeCache[base] = cachedProbeResult{
		result:    cloneMapAny(result),
		fetchedAt: now,
		expiresAt: now.Add(runtimeProbeCacheTTL),
	}
	p.probeCacheMu.Unlock()
}

func cloneMapAny(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(in)
	if err != nil {
		out := make(map[string]any, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		fallback := make(map[string]any, len(in))
		for k, v := range in {
			fallback[k] = v
		}
		return fallback
	}
	return out
}

func generateProbeSummary(result map[string]any) string {
	reachable, _ := result["reachable"].(bool)
	healthOk, _ := result["health_ok"].(bool)
	mcpAvailable, _ := result["mcp_available"].(bool)
	toolCount, _ := result["tool_count"].(float64)

	if !reachable {
		return "Instance unreachable: connection failed"
	}
	if !healthOk {
		status, _ := result["health_status"].(float64)
		return fmt.Sprintf("Instance unhealthy: /_health returned %d", int(status))
	}
	if !mcpAvailable {
		return "MCP not available: initialize request failed"
	}
	if toolCount > 0 {
		return fmt.Sprintf("Instance healthy: MCP available with %d tools", int(toolCount))
	}
	return "Instance healthy: MCP available"
}

func (p *RepoProvider) toolRuntimeMCPCall(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint string          `json:"endpoint"`
		Method   string          `json:"method"`
		Params   json.RawMessage `json:"params"`
		ID       any             `json:"id"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if strings.TrimSpace(args.Method) == "" {
		return nil, fmt.Errorf("method is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	id := args.ID
	if id == nil {
		id = "proxy-call"
	}
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  args.Method,
	}
	if len(args.Params) > 0 {
		var parsed any
		if err := json.Unmarshal(args.Params, &parsed); err != nil {
			return nil, fmt.Errorf("invalid params payload: %w", err)
		}
		body["params"] = parsed
	}
	mcpClient := &http.Client{Timeout: 5 * time.Second}
	mcpEndpointURL := buildEndpointPath(base, "/_mcp")
	// Do the MCP lifecycle handshake before operation requests. Skip for lifecycle
	// and notification methods — callers may be managing those explicitly.
	method := strings.TrimSpace(args.Method)
	if method != "initialize" && method != "ping" && !strings.HasPrefix(method, "notifications/") {
		doMCPEnsureReady(ctx, mcpClient, mcpEndpointURL)
	}
	resp, err := doJSONRPC(ctx, mcpClient, mcpEndpointURL, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"endpoint": mcpEndpointURL,
		"response": resp,
	}, nil
}

func (p *RepoProvider) toolRuntimeGetHealth(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint  string `json:"endpoint"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := 3 * time.Second
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	client := &http.Client{Timeout: timeout}
	endpointURL := buildEndpointPath(base, "/_mcp")
	doMCPEnsureReady(ctx, client, endpointURL)
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "runtime-get-health",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_get_health",
			"arguments": map[string]any{},
		},
	}
	resp, err := doJSONRPC(ctx, client, endpointURL, body)
	if err != nil {
		return map[string]any{
			"endpoint":  endpointURL,
			"reachable": false,
			"error":     err.Error(),
		}, nil
	}
	if rpcErr, ok := resp["error"].(map[string]any); ok {
		message, _ := rpcErr["message"].(string)
		if strings.TrimSpace(message) == "" {
			message = "runtime instance returned MCP error"
		}
		return map[string]any{
			"endpoint":    endpointURL,
			"reachable":   true,
			"status_code": 500,
			"ok":          false,
			"error":       message,
		}, nil
	}
	result, _ := resp["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	return map[string]any{
		"endpoint":    endpointURL,
		"reachable":   true,
		"status_code": 200,
		"ok":          true,
		"response":    structured,
	}, nil
}

func (p *RepoProvider) toolRuntimeListServices(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint  string `json:"endpoint"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := 3 * time.Second
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	client := &http.Client{Timeout: timeout}
	endpointURL := buildEndpointPath(base, "/_mcp")
	doMCPEnsureReady(ctx, client, endpointURL)
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "runtime-list-services",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_list_services",
			"arguments": map[string]any{},
		},
	}
	resp, err := doJSONRPC(ctx, client, endpointURL, body)
	if err != nil {
		return map[string]any{
			"endpoint":  endpointURL,
			"reachable": false,
			"error":     err.Error(),
		}, nil
	}
	if rpcErr, ok := resp["error"].(map[string]any); ok {
		message, _ := rpcErr["message"].(string)
		if strings.TrimSpace(message) == "" {
			message = "runtime instance returned MCP error"
		}
		return map[string]any{
			"endpoint":    endpointURL,
			"reachable":   true,
			"status_code": 500,
			"ok":          false,
			"error":       message,
		}, nil
	}
	result, _ := resp["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	out := map[string]any{
		"endpoint":    endpointURL,
		"reachable":   true,
		"status_code": 200,
		"ok":          true,
	}
	if services, ok := structured["services"]; ok {
		out["services"] = services
	}
	return out, nil
}

func (p *RepoProvider) toolRuntimeGetConfig(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint  string `json:"endpoint"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := 3 * time.Second
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	client := &http.Client{Timeout: timeout}
	endpointURL := buildEndpointPath(base, "/_mcp")
	doMCPEnsureReady(ctx, client, endpointURL)
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "runtime-get-config",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_get_config",
			"arguments": map[string]any{},
		},
	}
	resp, err := doJSONRPC(ctx, client, endpointURL, body)
	if err != nil {
		return map[string]any{
			"endpoint":  endpointURL,
			"reachable": false,
			"error":     err.Error(),
		}, nil
	}
	if rpcErr, ok := resp["error"].(map[string]any); ok {
		message, _ := rpcErr["message"].(string)
		if strings.TrimSpace(message) == "" {
			message = "runtime instance returned MCP error"
		}
		return map[string]any{
			"endpoint":    endpointURL,
			"reachable":   true,
			"status_code": 500,
			"ok":          false,
			"error":       message,
		}, nil
	}
	result, _ := resp["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	out := map[string]any{
		"endpoint":    endpointURL,
		"reachable":   true,
		"status_code": 200,
		"ok":          true,
		"response":    structured,
	}
	if debugRequired, _ := structured["debug_required"].(bool); debugRequired {
		out["debug_required"] = true
		out["status_code"] = 404
		out["ok"] = false
	}
	return out, nil
}

func (p *RepoProvider) toolRuntimeGetServiceState(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint   string `json:"endpoint"`
		Namespace  string `json:"namespace"`
		KeyPattern string `json:"key_pattern"`
		Limit      int    `json:"limit"`
		TimeoutMS  int    `json:"timeout_ms"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := 5 * time.Second
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	client := &http.Client{Timeout: timeout}
	endpointURL := buildEndpointPath(base, "/_mcp")
	doMCPEnsureReady(ctx, client, endpointURL)
	callArgs := map[string]any{}
	if ns := strings.TrimSpace(args.Namespace); ns != "" {
		callArgs["namespace"] = ns
	}
	if keyPattern := strings.TrimSpace(args.KeyPattern); keyPattern != "" {
		callArgs["key_pattern"] = keyPattern
	}
	if args.Limit > 0 {
		callArgs["limit"] = args.Limit
	}
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "runtime-get-service-state",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_get_service_state",
			"arguments": callArgs,
		},
	}
	resp, err := doJSONRPC(ctx, client, endpointURL, body)
	if err != nil {
		return map[string]any{
			"endpoint":  endpointURL,
			"namespace": strings.TrimSpace(args.Namespace),
			"reachable": false,
			"error":     err.Error(),
		}, nil
	}
	if rpcErr, ok := resp["error"].(map[string]any); ok {
		message, _ := rpcErr["message"].(string)
		if strings.TrimSpace(message) == "" {
			message = "runtime instance returned MCP error"
		}
		return map[string]any{
			"endpoint":    endpointURL,
			"namespace":   strings.TrimSpace(args.Namespace),
			"reachable":   true,
			"status_code": 500,
			"ok":          false,
			"error":       message,
		}, nil
	}
	result, _ := resp["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	out := map[string]any{
		"endpoint":    endpointURL,
		"namespace":   strings.TrimSpace(args.Namespace),
		"reachable":   true,
		"status_code": 200,
		"ok":          true,
	}
	for _, key := range []string{"namespace", "limit", "count", "truncated", "entries"} {
		if value, ok := structured[key]; ok {
			out[key] = value
		}
	}
	if debugRequired, _ := structured["debug_required"].(bool); debugRequired {
		out["debug_required"] = true
		out["status_code"] = 404
		out["ok"] = false
	}
	return out, nil
}

func (p *RepoProvider) toolRuntimeGetRecentEvents(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint  string `json:"endpoint"`
		Source    string `json:"source"`
		Type      string `json:"type"`
		Limit     int    `json:"limit"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := 5 * time.Second
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}

	callArgs := map[string]any{}
	if source := strings.TrimSpace(args.Source); source != "" {
		callArgs["source"] = source
	}
	if eventType := strings.TrimSpace(args.Type); eventType != "" {
		callArgs["type"] = eventType
	}
	if args.Limit > 0 {
		callArgs["limit"] = args.Limit
	}

	client := &http.Client{Timeout: timeout}
	endpointURL := buildEndpointPath(base, "/_mcp")
	doMCPEnsureReady(ctx, client, endpointURL)
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "runtime-get-recent-events",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_get_recent_events",
			"arguments": callArgs,
		},
	}
	resp, err := doJSONRPC(ctx, client, endpointURL, body)
	if err != nil {
		return map[string]any{
			"endpoint":  endpointURL,
			"reachable": false,
			"error":     err.Error(),
		}, nil
	}
	if rpcErr, ok := resp["error"].(map[string]any); ok {
		message, _ := rpcErr["message"].(string)
		if strings.TrimSpace(message) == "" {
			message = "runtime instance returned MCP error"
		}
		return map[string]any{
			"endpoint":    endpointURL,
			"reachable":   true,
			"status_code": 500,
			"ok":          false,
			"error":       message,
		}, nil
	}

	result, _ := resp["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	out := map[string]any{
		"endpoint":    endpointURL,
		"reachable":   true,
		"status_code": 200,
		"ok":          true,
	}
	for _, key := range []string{"limit", "count", "truncated", "events"} {
		if value, ok := structured[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (p *RepoProvider) toolRuntimeProbeKVStore(ctx context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Endpoint      string `json:"endpoint"`
		Namespace     string `json:"namespace"`
		KeyPattern    string `json:"key_pattern"`
		Limit         int    `json:"limit"`
		Cursor        string `json:"cursor"`
		IncludeValues bool   `json:"include_values"`
		PreviewBytes  int    `json:"preview_bytes"`
		TimeoutMS     int    `json:"timeout_ms"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Endpoint) == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	base, err := normalizeEndpoint(args.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := 5 * time.Second
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	callArgs := map[string]any{}
	if ns := strings.TrimSpace(args.Namespace); ns != "" {
		callArgs["namespace"] = ns
	}
	if keyPattern := strings.TrimSpace(args.KeyPattern); keyPattern != "" {
		callArgs["key_pattern"] = keyPattern
	}
	if args.Limit > 0 {
		callArgs["limit"] = args.Limit
	}
	if cursor := strings.TrimSpace(args.Cursor); cursor != "" {
		callArgs["cursor"] = cursor
	}
	if args.IncludeValues {
		callArgs["include_values"] = true
	}
	if args.PreviewBytes > 0 {
		callArgs["preview_bytes"] = args.PreviewBytes
	}

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "probe-kv-store",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_probe_kv_store",
			"arguments": callArgs,
		},
	}
	client := &http.Client{Timeout: timeout}
	kvEndpointURL := buildEndpointPath(base, "/_mcp")
	doMCPEnsureReady(ctx, client, kvEndpointURL)
	resp, err := doJSONRPC(ctx, client, kvEndpointURL, body)
	if err != nil {
		return map[string]any{
			"endpoint":  kvEndpointURL,
			"reachable": false,
			"error":     err.Error(),
		}, nil
	}

	out := map[string]any{
		"endpoint":  kvEndpointURL,
		"reachable": true,
	}
	if rpcErr, ok := resp["error"].(map[string]any); ok {
		out["ok"] = false
		if msg, ok := rpcErr["message"].(string); ok && strings.TrimSpace(msg) != "" {
			out["error"] = msg
		} else {
			out["error"] = "runtime instance returned MCP error"
		}
		return out, nil
	}
	result, _ := resp["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	if structured == nil {
		structured = map[string]any{}
	}
	for k, v := range structured {
		out[k] = v
	}
	if _, ok := out["ok"]; !ok {
		out["ok"] = true
	}
	return out, nil
}

func (p *RepoProvider) resolveWorkspacePath(path string) (string, error) {
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(p.workspaceRoot, path))
	}
	root := filepath.Clean(p.workspaceRoot)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes workspace root", path)
	}
	return abs, nil
}

func (p *RepoProvider) listRelativeFiles(root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(p.workspaceRoot, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (p *RepoProvider) findRelativePathsContaining(root, needle string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	var out []string
	needle = strings.ToLower(needle)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(p.workspaceRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(strings.ToLower(rel), needle) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func splitMarkdownRow(line string) []string {
	parts := strings.Split(line, "|")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return clean
}

func normalizeRelPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		path = strings.TrimPrefix(path, "./")
		out = append(out, path)
	}
	return uniqueSorted(out)
}

func serviceFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "services" {
			return strings.ToLower(parts[i+1])
		}
	}
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "integration" {
			return strings.ToLower(parts[i+1])
		}
	}
	if len(parts) >= 3 && parts[0] == "docs" && parts[1] == "services" {
		name := strings.TrimSuffix(parts[2], filepath.Ext(parts[2]))
		if name != "" {
			return strings.ToLower(name)
		}
	}
	if len(parts) >= 6 && parts[0] == "web" && parts[1] == "src" && parts[2] == "services" && parts[3] == "api" {
		name := strings.TrimSuffix(parts[4], filepath.Ext(parts[4]))
		if name != "" {
			return strings.ToLower(name)
		}
	}
	return ""
}

func packagePatternForPath(path string) string {
	path = filepath.ToSlash(path)
	dir := path
	if strings.Contains(path, ".") {
		dir = filepath.ToSlash(filepath.Dir(path))
	}
	if dir == "." || dir == "" {
		return ""
	}
	return "./" + strings.TrimPrefix(dir, "./") + "/..."
}

func (p *RepoProvider) collectCandidateTestsForPath(path string) ([]string, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	if path == "" {
		return nil, nil
	}

	tests := make([]string, 0, 8)
	abs := filepath.Join(p.workspaceRoot, filepath.FromSlash(path))
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	if strings.HasSuffix(path, "_test.go") {
		return []string{path}, nil
	}

	searchDir := path
	if !info.IsDir() {
		searchDir = filepath.ToSlash(filepath.Dir(path))
	}
	if searchDir == "." {
		searchDir = ""
	}
	if searchDir != "" {
		dirMatches, err := p.listMatchingTestFiles(searchDir)
		if err != nil {
			return nil, err
		}
		tests = append(tests, dirMatches...)
	}

	if svc := serviceFromPath(path); svc != "" {
		integrationDir := filepath.Join("tests", "integration", svc)
		integrationMatches, err := p.listMatchingTestFiles(filepath.ToSlash(integrationDir))
		if err != nil {
			return nil, err
		}
		tests = append(tests, integrationMatches...)
	}

	return uniqueSorted(tests), nil
}

func (p *RepoProvider) listMatchingTestFiles(relDir string) ([]string, error) {
	relDir = filepath.ToSlash(strings.TrimSpace(relDir))
	relDir = strings.TrimPrefix(relDir, "./")
	if relDir == "" {
		return nil, nil
	}
	absDir := filepath.Join(p.workspaceRoot, filepath.FromSlash(relDir))
	entries, err := os.ReadDir(absDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.ToSlash(filepath.Join(relDir, name)))
	}
	return uniqueSorted(out), nil
}

func mapValuesSorted(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = uniqueSorted(values)
	}
	return out
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func extractEndpointsFromSource(source string) []string {
	out := make([]string, 0, 32)
	for _, token := range []string{"\"/_", "\"/mcp", "\"/api"} {
		idx := 0
		for {
			i := strings.Index(source[idx:], token)
			if i < 0 {
				break
			}
			start := idx + i + 1
			end := start
			for end < len(source) && source[end] != '"' {
				end++
			}
			if end <= len(source) {
				path := source[start:end]
				if strings.HasPrefix(path, "/") {
					out = append(out, path)
				}
			}
			idx = end
			if idx >= len(source) {
				break
			}
		}
	}
	return uniqueSorted(out)
}

func classifyEndpoint(path string) string {
	switch {
	case strings.HasPrefix(path, "/_health"):
		return "health"
	case strings.HasPrefix(path, "/_debug"):
		return "debug"
	case strings.HasPrefix(path, "/_topology"):
		return "topology"
	case strings.HasPrefix(path, "/_mcp") || strings.HasPrefix(path, "/mcp"):
		return "mcp"
	default:
		return "internal"
	}
}

func isLikelySourceFile(path string) bool {
	for _, ext := range []string{".go", ".ts", ".tsx", ".js", ".jsx", ".md", ".yaml", ".yml"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func isLikelyTODOFile(path string) bool {
	for _, ext := range []string{".go", ".ts", ".tsx", ".js", ".jsx", ".md", ".yaml", ".yml", ".json", ".sh", ".py", ".mjs", ".cjs", ".toml"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	base := filepath.Base(path)
	if base == "Makefile" || base == "Dockerfile" {
		return true
	}
	return false
}

func parseTODOEntry(path string, line int, raw string) (todoEntry, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return todoEntry{}, false
	}
	idx := strings.Index(trimmed, "TODO")
	marker := "TODO"
	if idx < 0 {
		idx = strings.Index(trimmed, "FIXME")
		marker = "FIXME"
	}
	if idx < 0 {
		return todoEntry{}, false
	}
	rest := strings.TrimSpace(trimmed[idx+len(marker):])
	if strings.HasPrefix(rest, "()") {
		return todoEntry{}, false
	}

	entry := todoEntry{
		Path:      filepath.ToSlash(path),
		Line:      line,
		Marker:    marker,
		Location:  classifyTodoLocation(path),
		Service:   serviceFromPath(path),
		Language:  languageFromPath(path),
		Canonical: false,
	}

	if strings.HasPrefix(rest, "(") {
		if end := strings.Index(rest, ")"); end > 1 {
			meta := strings.TrimSpace(rest[1:end])
			rest = strings.TrimSpace(rest[end+1:])
			if strings.HasPrefix(rest, ":") {
				rest = strings.TrimSpace(rest[1:])
			}
			if strings.HasPrefix(strings.ToLower(meta), "priority:") {
				entry.Priority = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(meta), "priority:")))
				entry.Canonical = entry.Priority != ""
			} else if meta != "" {
				entry.Tag = meta
			}
		}
	} else {
		rest = strings.TrimLeft(rest, ":- ")
	}

	if entry.Priority == "" {
		if priority := extractPriority(raw); priority != "" {
			entry.Priority = priority
		}
	}
	entry.Text = strings.TrimSpace(strings.TrimRight(rest, "."))
	if entry.Text == "" {
		entry.Text = strings.TrimSpace(trimmed[idx:])
	}
	return entry, true
}

func extractPriority(line string) string {
	upper := strings.ToUpper(line)
	for _, p := range []string{"P1", "P2", "P3", "P4", "P5"} {
		if strings.Contains(upper, p) {
			return p
		}
	}
	return ""
}

func classifyTodoLocation(path string) string {
	path = filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(path, "internal/services/"):
		return "internal_service"
	case strings.HasPrefix(path, "internal/"):
		return "internal"
	case strings.HasPrefix(path, "tests/integration/"):
		return "integration_test"
	case strings.HasPrefix(path, "tests/"):
		return "tests"
	case strings.HasPrefix(path, "docs/services/"):
		return "service_doc"
	case strings.HasPrefix(path, "docs/"):
		return "docs"
	case strings.HasPrefix(path, "web/src/services/api/"):
		return "web_api"
	case strings.HasPrefix(path, "web/"):
		return "web"
	case strings.HasPrefix(path, ".github/"):
		return "ci"
	case strings.HasPrefix(path, "compat/"):
		return "compat"
	case strings.HasPrefix(path, "cmd/"):
		return "cmd"
	case strings.HasPrefix(path, "scripts/"):
		return "scripts"
	case !strings.Contains(path, "/"):
		return "root"
	default:
		return "other"
	}
}

func languageFromPath(path string) string {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".go":
		return "go"
	case ".ts":
		return "ts"
	case ".tsx":
		return "tsx"
	case ".js":
		return "js"
	case ".jsx":
		return "jsx"
	case ".md":
		return "md"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".sh":
		return "sh"
	case ".py":
		return "py"
	case ".toml":
		return "toml"
	default:
		if filepath.Base(path) == "Makefile" {
			return "make"
		}
		if filepath.Base(path) == "Dockerfile" {
			return "dockerfile"
		}
		return ""
	}
}

func boundedImportantLines(content string, maxLines int) []string {
	out := make([]string, 0, maxLines)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "##") || strings.HasPrefix(trimmed, "-") || strings.Contains(trimmed, "MUST") || strings.Contains(trimmed, "Never") {
			out = append(out, trimmed)
		}
		if len(out) >= maxLines {
			break
		}
	}
	if len(out) == 0 {
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			out = append(out, trimmed)
			if len(out) >= maxLines {
				break
			}
		}
	}
	return out
}

func countFiles(root, ext string) int {
	count := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ext == "" || strings.HasSuffix(d.Name(), ext) {
			count++
		}
		return nil
	})
	return count
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("endpoint is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid endpoint %q", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func buildEndpointPath(base, suffix string) string {
	return strings.TrimRight(base, "/") + suffix
}

// doMCPEnsureReady performs the MCP initialization handshake (initialize +
// notifications/initialized) on a best-effort basis before operation requests.
// Errors are intentionally ignored: some callers (e.g. test mocks) may not
// enforce the lifecycle, and a failed handshake must not prevent the caller
// from proceeding. Against a real Overcast server this ensures the server is in
// ready state before the first tools/call request is sent.
func doMCPEnsureReady(ctx context.Context, client *http.Client, endpoint string) {
	initBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      "lifecycle-init",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "overcast-workspace-mcp", "version": "1.0.0"},
		},
	}
	_, _ = doJSONRPC(ctx, client, endpoint, initBody)
	// notifications/initialized has no id — server returns 204 No Content with empty
	// body, which causes doJSONRPC to return an EOF decode error that we ignore.
	_, _ = doJSONRPC(ctx, client, endpoint, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func doJSONRPC(ctx context.Context, client *http.Client, endpoint string, payload map[string]any) (map[string]any, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	const maxResponseSize = 10 * 1024 * 1024 // 10 MiB
	var out map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func symbolHitsToMaps(hits []SymbolHit) []map[string]any {
	out := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		out = append(out, map[string]any{"path": hit.Path, "line": hit.Line, "text": hit.Text})
	}
	return out
}

// jsonErr is a MCP-compatible JSON-RPC error used by tool handlers.
type jsonErr struct {
	code int
	msg  string
}

func (e *jsonErr) Error() string { return e.msg }
