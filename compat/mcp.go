package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	intmcp "github.com/Neaox/overcast/internal/mcp"
)

type registryData struct {
	Groups []registryGroup `json:"groups"`
}

type registryGroup struct {
	Name    string         `json:"name"`
	Service string         `json:"service"`
	Tests   []registryTest `json:"tests"`
}

type registryTest struct {
	Name    string   `json:"name"`
	Op      string   `json:"op,omitempty"`
	Depends []string `json:"depends,omitempty"`
}

// NewMCPServer creates an MCP server that combines generic repo tools with
// compat-specific orchestration tools.
func NewMCPServer(orch *Orchestrator, registryPath, workspaceRoot string, logger *slog.Logger) *intmcp.Server {
	providers := []intmcp.ToolProvider{
		intmcp.NewRepoProvider(workspaceRoot),
		newCompatMCPProvider(orch, registryPath),
	}
	return intmcp.NewServer(orch, logger, providers...)
}

type compatMCPProvider struct {
	orchestrator *Orchestrator
	registryPath string
}

func newCompatMCPProvider(orch *Orchestrator, registryPath string) *compatMCPProvider {
	return &compatMCPProvider{orchestrator: orch, registryPath: registryPath}
}

func (p *compatMCPProvider) Tools() []intmcp.Tool {
	return []intmcp.Tool{
		{Name: "compat_list_suites", Description: "List all suite runners and their current state (building, ready, busy, error, stopped).", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "compat_list_services", Description: "List all AWS services from the test registry with group and test counts.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "compat_list_tests", Description: "List tests from the registry with optional filtering. Returns test names, groups, services, and last result status if available.", InputSchema: json.RawMessage(`{"type":"object","properties":{"service":{"type":"string"},"group":{"type":"string"},"suite":{"type":"string"}}}`)},
		{Name: "compat_get_results", Description: "Get test results summary, optionally filtered by suite, service, group, test name, or status.", InputSchema: json.RawMessage(`{"type":"object","properties":{"suite":{"type":"string"},"service":{"type":"string"},"group":{"type":"string"},"test":{"type":"string"},"status":{"type":"string"}}}`)},
		{Name: "compat_get_queue", Description: "Show what tests are currently queued or running across all suites.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "compat_run_tests", Description: "Queue tests for execution by service, group, test, suite, or all.", InputSchema: json.RawMessage(`{"type":"object","properties":{"test":{"type":"string"},"group":{"type":"string"},"service":{"type":"string"},"suite":{"type":"string"},"all":{"type":"boolean"}}}`)},
		{Name: "compat_run_failing", Description: "Re-run failing tests, optionally filtered by suite or service.", InputSchema: json.RawMessage(`{"type":"object","properties":{"suite":{"type":"string"},"service":{"type":"string"}}}`)},
		{Name: "compat_cancel", Description: "Cancel queued or running tests.", InputSchema: json.RawMessage(`{"type":"object","properties":{"batch_id":{"type":"string"},"suite":{"type":"string"},"group":{"type":"string"},"test":{"type":"string"},"all":{"type":"boolean"}}}`)},
		{Name: "compat_reload_suite", Description: "Hot-reload a suite runner process.", InputSchema: json.RawMessage(`{"type":"object","properties":{"suite":{"type":"string"}},"required":["suite"]}`)},
	}
}

func (p *compatMCPProvider) Handler(name string) (intmcp.HandlerFunc, bool) {
	handlers := map[string]intmcp.HandlerFunc{
		"compat_list_suites":   p.toolListSuites,
		"compat_list_services": p.toolListServices,
		"compat_list_tests":    p.toolListTests,
		"compat_get_results":   p.toolGetResults,
		"compat_get_queue":     p.toolGetQueue,
		"compat_run_tests":     p.toolRunTests,
		"compat_run_failing":   p.toolRunFailing,
		"compat_cancel":        p.toolCancel,
		"compat_reload_suite":  p.toolReloadSuite,
	}
	fn, ok := handlers[name]
	return fn, ok
}

func (p *compatMCPProvider) toolListSuites(_ context.Context, _ json.RawMessage) (any, error) {
	return p.orchestrator.SuiteStates(), nil
}

func (p *compatMCPProvider) toolListServices(_ context.Context, _ json.RawMessage) (any, error) {
	reg, err := p.loadRegistry()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	type serviceInfo struct {
		Service    string `json:"service"`
		GroupCount int    `json:"group_count"`
		TestCount  int    `json:"test_count"`
	}
	byService := make(map[string]*serviceInfo)
	for _, g := range reg.Groups {
		si, ok := byService[g.Service]
		if !ok {
			si = &serviceInfo{Service: g.Service}
			byService[g.Service] = si
		}
		si.GroupCount++
		si.TestCount += len(g.Tests)
	}
	services := make([]serviceInfo, 0, len(byService))
	for _, si := range byService {
		services = append(services, *si)
	}
	return services, nil
}

func (p *compatMCPProvider) toolListTests(_ context.Context, params json.RawMessage) (any, error) {
	var filter struct {
		Service string `json:"service"`
		Group   string `json:"group"`
		Suite   string `json:"suite"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &filter)
	}
	reg, err := p.loadRegistry()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	type testEntry struct {
		Group   string `json:"group"`
		Service string `json:"service"`
		Test    string `json:"test"`
		Op      string `json:"op,omitempty"`
		Status  string `json:"status,omitempty"`
		Suite   string `json:"suite,omitempty"`
	}
	var entries []testEntry
	for _, g := range reg.Groups {
		if filter.Service != "" && g.Service != filter.Service {
			continue
		}
		if filter.Group != "" && g.Name != filter.Group {
			continue
		}
		for _, t := range g.Tests {
			e := testEntry{Group: g.Name, Service: g.Service, Test: t.Name, Op: t.Op}
			results := p.orchestrator.Results(filter.Suite, "", g.Name, t.Name, "")
			if len(results) > 0 {
				e.Status = string(results[0].Status)
				e.Suite = results[0].Suite
			}
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func (p *compatMCPProvider) toolGetResults(_ context.Context, params json.RawMessage) (any, error) {
	var filter struct {
		Suite   string `json:"suite"`
		Service string `json:"service"`
		Group   string `json:"group"`
		Test    string `json:"test"`
		Status  string `json:"status"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &filter)
	}
	return p.orchestrator.Results(filter.Suite, filter.Service, filter.Group, filter.Test, filter.Status), nil
}

func (p *compatMCPProvider) toolGetQueue(_ context.Context, _ json.RawMessage) (any, error) {
	return p.orchestrator.QueueState(), nil
}

func (p *compatMCPProvider) toolRunTests(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Test    string `json:"test"`
		Group   string `json:"group"`
		Service string `json:"service"`
		Suite   string `json:"suite"`
		All     bool   `json:"all"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	var tests []TestRef
	var suites []string
	if args.Suite != "" {
		suites = []string{args.Suite}
	}
	if args.All || (args.Test == "" && args.Group == "" && args.Service == "") {
	} else if args.Service != "" {
		tests = p.groupsForService(args.Service)
		if len(tests) == 0 {
			return nil, fmt.Errorf("no groups found for service %q", args.Service)
		}
	} else if args.Group != "" {
		ref := TestRef{Group: args.Group}
		if args.Test != "" {
			ref.Tests = []string{args.Test}
		}
		tests = []TestRef{ref}
	} else if args.Test != "" {
		tests = p.findTestInRegistry(args.Test)
		if len(tests) == 0 {
			return nil, fmt.Errorf("test %q not found in registry", args.Test)
		}
	}
	batchID, queued, skipped := p.orchestrator.SubmitTests(suites, tests)
	return map[string]any{"batch_id": batchID, "queued": queued, "skipped_duplicates": skipped}, nil
}

func (p *compatMCPProvider) toolRunFailing(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Suite   string `json:"suite"`
		Service string `json:"service"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &args)
	}
	batchID, queued := p.orchestrator.SubmitFailingTests(args.Suite, args.Service)
	return map[string]any{"batch_id": batchID, "queued_count": len(queued)}, nil
}

func (p *compatMCPProvider) toolCancel(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		BatchID string `json:"batch_id"`
		Suite   string `json:"suite"`
		Group   string `json:"group"`
		Test    string `json:"test"`
		All     bool   `json:"all"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &args)
	}
	cancelled := p.orchestrator.CancelTests(args.BatchID, args.Suite, args.Group, args.Test, args.All)
	return map[string]any{"cancelled": cancelled}, nil
}

func (p *compatMCPProvider) toolReloadSuite(_ context.Context, params json.RawMessage) (any, error) {
	var args struct {
		Suite string `json:"suite"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &args)
	}
	if args.Suite == "" {
		return nil, fmt.Errorf("suite name required")
	}
	if err := p.orchestrator.ReloadSuite(args.Suite); err != nil {
		return nil, err
	}
	return map[string]any{"state": "building"}, nil
}

func (p *compatMCPProvider) loadRegistry() (*registryData, error) {
	data, err := os.ReadFile(p.registryPath)
	if err != nil {
		return nil, err
	}
	var reg registryData
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

func (p *compatMCPProvider) groupsForService(service string) []TestRef {
	reg, err := p.loadRegistry()
	if err != nil {
		return nil
	}
	var refs []TestRef
	for _, g := range reg.Groups {
		if g.Service == service {
			refs = append(refs, TestRef{Group: g.Name})
		}
	}
	return refs
}

func (p *compatMCPProvider) findTestInRegistry(test string) []TestRef {
	reg, err := p.loadRegistry()
	if err != nil {
		return nil
	}
	var refs []TestRef
	for _, g := range reg.Groups {
		for _, t := range g.Tests {
			if t.Name == test {
				refs = append(refs, TestRef{Group: g.Name, Tests: []string{test}})
				break
			}
		}
	}
	return refs
}
