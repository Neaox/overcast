package mcp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SymbolFinder interface {
	FindSymbol(ctx context.Context, query SymbolQuery) (SymbolFindResult, error)
}

type SymbolQuery struct {
	Symbol     string
	PathPrefix string
	MaxResults int
}

type SymbolHit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type SymbolFindResult struct {
	Backend     string      `json:"backend"`
	Symbol      string      `json:"symbol"`
	PathPrefix  string      `json:"path_prefix"`
	Definitions []SymbolHit `json:"definitions"`
	References  []SymbolHit `json:"references"`
	Truncated   bool        `json:"truncated"`
	MaxResults  int         `json:"max_results"`
}

type regexSymbolFinder struct {
	workspaceRoot string
}

func newRegexSymbolFinder(workspaceRoot string) SymbolFinder {
	return &regexSymbolFinder{workspaceRoot: workspaceRoot}
}

func (f *regexSymbolFinder) FindSymbol(_ context.Context, query SymbolQuery) (SymbolFindResult, error) {
	symbol := strings.TrimSpace(query.Symbol)
	if symbol == "" {
		return SymbolFindResult{}, fmt.Errorf("symbol is required")
	}
	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}
	prefix := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(query.PathPrefix)), "./")
	wordRE := regexp.MustCompile(`\b` + regexp.QuoteMeta(symbol) + `\b`)
	defRE := regexp.MustCompile(`\b(func|type|var|const|class|interface)\b[^\n]*\b` + regexp.QuoteMeta(symbol) + `\b`)

	defs := make([]SymbolHit, 0, 8)
	refs := make([]SymbolHit, 0, maxResults)
	err := filepath.WalkDir(f.workspaceRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(f.workspaceRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			return nil
		}
		if !isLikelySourceFile(rel) {
			return nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if !wordRE.MatchString(line) {
				continue
			}
			hit := SymbolHit{Path: rel, Line: lineNo, Text: strings.TrimSpace(line)}
			if defRE.MatchString(line) {
				defs = append(defs, hit)
			}
			if len(refs) < maxResults {
				refs = append(refs, hit)
			}
		}
		return nil
	})
	if err != nil {
		return SymbolFindResult{}, err
	}

	return SymbolFindResult{
		Backend:     "regex",
		Symbol:      symbol,
		PathPrefix:  prefix,
		Definitions: defs,
		References:  refs,
		Truncated:   len(refs) >= maxResults,
		MaxResults:  maxResults,
	}, nil
}
