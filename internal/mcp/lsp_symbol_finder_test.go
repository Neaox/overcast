package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// mockLSPServer simulates a minimal LSP server over in-process pipes.
type mockLSPServer struct {
	r *bufio.Reader
	w io.Writer
}

func (s *mockLSPServer) readMsg() (lspRawMessage, error) {
	contentLength := -1
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return lspRawMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			fmt.Sscanf(strings.TrimPrefix(line, "Content-Length: "), "%d", &contentLength)
		}
	}
	if contentLength < 0 {
		return lspRawMessage{}, fmt.Errorf("no Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(s.r, body); err != nil {
		return lspRawMessage{}, err
	}
	var msg lspRawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return lspRawMessage{}, err
	}
	return msg, nil
}

func (s *mockLSPServer) respond(id any, result any) {
	b, _ := json.Marshal(result)
	msg := lspRawMessage{JSONRPC: "2.0", ID: id, Result: b}
	body, _ := json.Marshal(msg)
	fmt.Fprintf(s.w, "Content-Length: %d\r\n\r\n", len(body))
	_, _ = s.w.Write(body)
}

// startMockLSP runs a goroutine that acts as a minimal LSP server.
// symbols is the []lspSymbolInfo to return for workspace/symbol queries.
// Returns client-side r, w for constructing an lspClient.
func startMockLSP(t *testing.T, symbols []lspSymbolInfo) (r io.Reader, w io.WriteCloser) {
	t.Helper()
	// serverR ← clientW  (client writes to server)
	serverR, clientW := io.Pipe()
	// clientR ← serverW  (server writes to client)
	clientR, serverW := io.Pipe()

	srv := &mockLSPServer{r: bufio.NewReader(serverR), w: serverW}

	go func() {
		defer serverR.Close()
		defer serverW.Close()

		// initialize
		msg, err := srv.readMsg()
		if err != nil || msg.Method != "initialize" {
			return
		}
		srv.respond(msg.ID, map[string]any{"capabilities": map[string]any{}})

		// initialized notification – no response
		msg, err = srv.readMsg()
		if err != nil || msg.Method != "initialized" {
			return
		}

		// workspace/symbol
		msg, err = srv.readMsg()
		if err != nil || msg.Method != "workspace/symbol" {
			return
		}
		srv.respond(msg.ID, symbols)

		// drain remaining messages until shutdown or EOF
		for {
			msg, err = srv.readMsg()
			if err != nil {
				return
			}
			if msg.Method == "shutdown" {
				srv.respond(msg.ID, nil)
			}
		}
	}()

	return clientR, clientW
}

func TestLSPClientWorkspaceSymbol(t *testing.T) {
	wantSymbols := []lspSymbolInfo{
		{
			Name: "FindSymbol",
			Kind: 12, // Function
			Location: lspLocation{
				URI:   "file:///workspace/internal/mcp/symbol_finder.go",
				Range: lspRange{Start: lspPosition{Line: 12, Character: 0}},
			},
		},
	}

	r, w := startMockLSP(t, wantSymbols)
	client, err := newLSPClientFromPipes(r, w, "/workspace")
	if err != nil {
		t.Fatalf("newLSPClientFromPipes: %v", err)
	}
	defer client.close()

	got, err := client.workspaceSymbol(context.Background(), "FindSymbol", 10)
	if err != nil {
		t.Fatalf("workspaceSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(got))
	}
	if got[0].Name != "FindSymbol" {
		t.Fatalf("unexpected symbol name: %q", got[0].Name)
	}
	if got[0].Location.Range.Start.Line != 12 {
		t.Fatalf("unexpected line: %d", got[0].Location.Range.Start.Line)
	}
}

func TestLSPSymbolFinderMapsResultsToSymbolHits(t *testing.T) {
	root := t.TempDir()
	wantSymbols := []lspSymbolInfo{
		{
			Name: "MyFunc",
			Kind: 12,
			Location: lspLocation{
				URI:   "file://" + root + "/internal/foo/foo.go",
				Range: lspRange{Start: lspPosition{Line: 4, Character: 0}},
			},
		},
		// Symbol outside workspaceRoot – should be excluded.
		{
			Name: "OtherFunc",
			Kind: 12,
			Location: lspLocation{
				URI:   "file:///some/other/project/bar.go",
				Range: lspRange{Start: lspPosition{Line: 0, Character: 0}},
			},
		},
	}

	r, w := startMockLSP(t, wantSymbols)
	client, err := newLSPClientFromPipes(r, w, root)
	if err != nil {
		t.Fatalf("newLSPClientFromPipes: %v", err)
	}

	finder := &lspSymbolFinder{
		backendName:   "lsp-test",
		workspaceRoot: root,
	}
	finder.client = client // inject pre-built client, skip lazy init

	result, err := finder.FindSymbol(context.Background(), SymbolQuery{Symbol: "MyFunc"})
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	if result.Backend != "lsp-test" {
		t.Fatalf("unexpected backend: %q", result.Backend)
	}
	if len(result.Definitions) != 1 {
		t.Fatalf("expected 1 definition (out-of-workspace symbol excluded), got %d", len(result.Definitions))
	}
	if result.Definitions[0].Path != "internal/foo/foo.go" {
		t.Fatalf("unexpected path: %q", result.Definitions[0].Path)
	}
	// LSP lines are 0-indexed; SymbolHit lines are 1-indexed.
	if result.Definitions[0].Line != 5 {
		t.Fatalf("expected line 5 (LSP line 4 + 1), got %d", result.Definitions[0].Line)
	}
}

func TestCompositeSymbolFinderFallsBackToRegexWhenLSPFails(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/mcp/server.go", "package mcp\n\nfunc NewServer() {}\n")

	failing := &lspSymbolFinder{
		command:       "nonexistent-lsp-binary-that-does-not-exist",
		args:          []string{},
		workspaceRoot: root,
		backendName:   "lsp-fail",
	}
	composite := &compositeSymbolFinder{
		lspFinders:  []*lspSymbolFinder{failing},
		regexFinder: newRegexSymbolFinder(root),
	}

	result, err := composite.FindSymbol(context.Background(), SymbolQuery{Symbol: "NewServer"})
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	if result.Backend != "regex" {
		t.Fatalf("expected regex fallback, got %q", result.Backend)
	}
	if len(result.Definitions) == 0 {
		t.Fatal("expected definitions from regex fallback")
	}
}

func TestCompositeSymbolFinderUsesLSPDefinitionsOverRegex(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/foo/foo.go", "package foo\n\nfunc MyFunc() {}\n")

	lspSymbols := []lspSymbolInfo{
		{
			Name: "MyFunc",
			Kind: 12,
			Location: lspLocation{
				URI:   "file://" + root + "/internal/foo/foo.go",
				Range: lspRange{Start: lspPosition{Line: 2, Character: 0}},
			},
		},
	}
	r, w := startMockLSP(t, lspSymbols)
	client, err := newLSPClientFromPipes(r, w, root)
	if err != nil {
		t.Fatalf("newLSPClientFromPipes: %v", err)
	}

	lf := &lspSymbolFinder{backendName: "lsp-go", workspaceRoot: root}
	lf.client = client

	composite := &compositeSymbolFinder{
		lspFinders:  []*lspSymbolFinder{lf},
		regexFinder: newRegexSymbolFinder(root),
	}

	result, err := composite.FindSymbol(context.Background(), SymbolQuery{Symbol: "MyFunc"})
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	if result.Backend != "lsp-go" {
		t.Fatalf("expected lsp-go backend, got %q", result.Backend)
	}
	// Definitions should come from LSP.
	if len(result.Definitions) == 0 {
		t.Fatal("expected definitions from LSP")
	}
	// References come from regex (all line hits), but the LSP definition line
	// should not be duplicated in references.
	for _, ref := range result.References {
		if ref.Path == "internal/foo/foo.go" && ref.Line == 3 {
			t.Errorf("LSP definition line should not appear in references: %+v", ref)
		}
	}
}
