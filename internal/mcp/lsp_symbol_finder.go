package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// lspSymbolFinder implements SymbolFinder by querying a language server over
// LSP using workspace/symbol. The server process is started lazily on first use.
type lspSymbolFinder struct {
	command       string
	args          []string
	workspaceRoot string
	backendName   string

	once    sync.Once
	client  *lspClient // set either by lazy init or by tests injecting a pre-built client
	initErr error
}

func (f *lspSymbolFinder) getClient() (*lspClient, error) {
	f.once.Do(func() {
		if f.client != nil {
			return // pre-injected by tests
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		f.client, f.initErr = newLSPClient(ctx, f.command, f.args, f.workspaceRoot)
	})
	return f.client, f.initErr
}

// FindSymbol queries the LSP server for workspace-wide symbol definitions.
// References are intentionally left nil; the compositeSymbolFinder supplements
// them from the regex finder.
func (f *lspSymbolFinder) FindSymbol(ctx context.Context, query SymbolQuery) (SymbolFindResult, error) {
	client, err := f.getClient()
	if err != nil {
		return SymbolFindResult{}, err
	}
	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}
	symbols, err := client.workspaceSymbol(ctx, query.Symbol, maxResults)
	if err != nil {
		return SymbolFindResult{}, err
	}
	prefix := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(query.PathPrefix)), "./")
	defs := make([]SymbolHit, 0, len(symbols))
	for _, s := range symbols {
		rel := lspURIToRel(s.Location.URI, f.workspaceRoot)
		if rel == "" {
			continue
		}
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			continue
		}
		defs = append(defs, SymbolHit{
			Path: rel,
			Line: s.Location.Range.Start.Line + 1, // LSP is 0-indexed
			Text: s.Name,
		})
	}
	return SymbolFindResult{
		Backend:     f.backendName,
		Symbol:      query.Symbol,
		PathPrefix:  prefix,
		Definitions: defs,
		References:  nil,
		MaxResults:  maxResults,
		Truncated:   len(defs) >= maxResults,
	}, nil
}

// compositeSymbolFinder combines one or more LSP finders (for definitions) with
// a regex finder (for references and definition fallback).
type compositeSymbolFinder struct {
	lspFinders  []*lspSymbolFinder
	regexFinder SymbolFinder
}

func (c *compositeSymbolFinder) FindSymbol(ctx context.Context, query SymbolQuery) (SymbolFindResult, error) {
	var allDefs []SymbolHit
	backends := make([]string, 0, len(c.lspFinders))

	for _, lf := range c.lspFinders {
		r, err := lf.FindSymbol(ctx, query)
		if err == nil && len(r.Definitions) > 0 {
			allDefs = append(allDefs, r.Definitions...)
			backends = append(backends, r.Backend)
		}
	}

	regexResult, _ := c.regexFinder.FindSymbol(ctx, query)

	defs := allDefs
	if len(defs) == 0 {
		defs = regexResult.Definitions
	}

	// Build a set of path:line pairs already covered by definitions to avoid
	// duplicating them in the references list.
	defSet := make(map[string]bool, len(defs))
	for _, d := range defs {
		defSet[fmt.Sprintf("%s:%d", d.Path, d.Line)] = true
	}
	refs := make([]SymbolHit, 0, len(regexResult.References))
	for _, ref := range regexResult.References {
		if !defSet[fmt.Sprintf("%s:%d", ref.Path, ref.Line)] {
			refs = append(refs, ref)
		}
	}

	backend := "regex"
	if len(backends) > 0 {
		backend = strings.Join(backends, "+")
	}

	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	return SymbolFindResult{
		Backend:     backend,
		Symbol:      query.Symbol,
		PathPrefix:  query.PathPrefix,
		Definitions: defs,
		References:  refs,
		MaxResults:  maxResults,
		Truncated:   regexResult.Truncated,
	}, nil
}

// newAutoSymbolFinder returns the best available SymbolFinder for the given
// workspace. It probes for gopls (Go) and typescript-language-server (TypeScript)
// and falls back to the regex-based finder when neither is available.
func newAutoSymbolFinder(workspaceRoot string) SymbolFinder {
	regex := newRegexSymbolFinder(workspaceRoot)

	lspFinders := make([]*lspSymbolFinder, 0, 2)

	if _, err := exec.LookPath("gopls"); err == nil {
		lspFinders = append(lspFinders, &lspSymbolFinder{
			command:       "gopls",
			args:          []string{"-mode=stdio"},
			workspaceRoot: workspaceRoot,
			backendName:   "lsp-go",
		})
	}

	// Probe for typescript-language-server in PATH or as a local binary inside
	// the web sub-directory (common in monorepo setups).
	if tsBin := findTSLanguageServer(workspaceRoot); tsBin != "" {
		tsRoot := filepath.Join(workspaceRoot, "web")
		lspFinders = append(lspFinders, &lspSymbolFinder{
			command:       tsBin,
			args:          []string{"--stdio"},
			workspaceRoot: tsRoot,
			backendName:   "lsp-ts",
		})
	}

	if len(lspFinders) == 0 {
		return regex
	}
	return &compositeSymbolFinder{lspFinders: lspFinders, regexFinder: regex}
}

// findTSLanguageServer returns the path to typescript-language-server if found,
// checking PATH and then the web/node_modules/.bin directory.
func findTSLanguageServer(workspaceRoot string) string {
	if p, err := exec.LookPath("typescript-language-server"); err == nil {
		return p
	}
	local := filepath.Join(workspaceRoot, "web", "node_modules", ".bin", "typescript-language-server")
	if _, err := exec.LookPath(local); err == nil {
		return local
	}
	return ""
}

// lspURIToRel converts a file:// URI to a workspace-relative slash path.
// Returns "" if the URI is outside workspaceRoot or not a file URI.
func lspURIToRel(uri, workspaceRoot string) string {
	path := strings.TrimPrefix(uri, "file://")
	rel, err := filepath.Rel(workspaceRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}
