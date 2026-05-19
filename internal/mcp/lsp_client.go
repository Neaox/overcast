package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// lspRawMessage is a JSON-RPC 2.0 message as used by the Language Server Protocol.
type lspRawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lspRPCError    `json:"error,omitempty"`
}

type lspRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// lspSymbolInfo is an LSP SymbolInformation / WorkspaceSymbol result.
type lspSymbolInfo struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      lspLocation `json:"location"`
	ContainerName string      `json:"containerName,omitempty"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// lspClient is a minimal LSP client that communicates with a language server
// using JSON-RPC 2.0 messages framed with Content-Length headers.
type lspClient struct {
	cmd    *exec.Cmd // nil when created from pre-existing pipes
	w      io.WriteCloser
	r      *bufio.Reader
	mu     sync.Mutex
	nextID int
}

// newLSPClient starts a language server subprocess and performs the LSP
// initialize handshake. The context deadline applies to the full lifetime of
// the child process.
func newLSPClient(ctx context.Context, command string, args []string, workspaceRoot string) (*lspClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", command, err)
	}
	c := &lspClient{cmd: cmd, w: stdin, r: bufio.NewReader(stdout)}
	if err := c.initialize(workspaceRoot); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("lsp initialize: %w", err)
	}
	return c, nil
}

// newLSPClientFromPipes creates an LSP client over caller-supplied pipes.
// Used in tests to inject a mock LSP server.
func newLSPClientFromPipes(r io.Reader, w io.WriteCloser, workspaceRoot string) (*lspClient, error) {
	c := &lspClient{w: w, r: bufio.NewReader(r)}
	if err := c.initialize(workspaceRoot); err != nil {
		return nil, fmt.Errorf("lsp initialize: %w", err)
	}
	return c, nil
}

// initialize sends the LSP initialize request and the initialized notification.
func (c *lspClient) initialize(workspaceRoot string) error {
	uri := "file://" + workspaceRoot
	params := map[string]any{
		"processId": nil,
		"rootUri":   uri,
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"symbol": map[string]any{
					"symbolKind": map[string]any{
						"valueSet": []int{
							1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12,
							13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26,
						},
					},
				},
			},
		},
		"workspaceFolders": []map[string]any{
			{"uri": uri, "name": "workspace"},
		},
	}
	if _, err := c.call("initialize", params); err != nil {
		return err
	}
	return c.sendNotify("initialized", map[string]any{})
}

// workspaceSymbol queries the server for symbols matching query.
func (c *lspClient) workspaceSymbol(ctx context.Context, query string, limit int) ([]lspSymbolInfo, error) {
	_ = ctx // deadline is enforced at the exec.CommandContext level
	result, err := c.call("workspace/symbol", map[string]any{"query": query})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	var symbols []lspSymbolInfo
	if err := json.Unmarshal(result, &symbols); err != nil {
		return nil, fmt.Errorf("parse workspace/symbol response: %w", err)
	}
	if limit > 0 && len(symbols) > limit {
		symbols = symbols[:limit]
	}
	return symbols, nil
}

// close shuts down the LSP client and waits for the server process to exit.
func (c *lspClient) close() error {
	c.mu.Lock()
	// Graceful shutdown: send shutdown request, drain its response, then exit.
	c.nextID++
	id := c.nextID
	shutdownMsg := lspRawMessage{JSONRPC: "2.0", ID: id, Method: "shutdown"}
	if err := c.writeFramed(shutdownMsg); err == nil {
		_, _ = c.readResponse(id) // drain response so server is not blocked writing
	}
	exitMsg := lspRawMessage{JSONRPC: "2.0", Method: "exit"}
	_ = c.writeFramed(exitMsg)
	_ = c.w.Close()
	c.mu.Unlock()

	if c.cmd != nil {
		return c.cmd.Wait()
	}
	return nil
}

// call sends a JSON-RPC request and returns the result.
func (c *lspClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	msg := lspRawMessage{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		msg.Params = b
	}
	if err := c.writeFramed(msg); err != nil {
		return nil, err
	}
	return c.readResponse(id)
}

// sendNotify sends a JSON-RPC notification (no response expected).
func (c *lspClient) sendNotify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	msg := lspRawMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = b
	}
	return c.writeFramed(msg)
}

// writeFramed serialises msg and writes it with a Content-Length header.
// Caller must hold c.mu.
func (c *lspClient) writeFramed(msg lspRawMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(c.w, header); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// readResponse reads messages from the server, skipping notifications and
// server-initiated requests, until it finds the response with the given id.
// Caller must hold c.mu.
func (c *lspClient) readResponse(id int) (json.RawMessage, error) {
	for {
		msg, err := c.readFramed()
		if err != nil {
			return nil, err
		}
		// Messages with a Method are notifications or server-to-client requests;
		// skip them — we are not a full LSP client.
		if msg.Method != "" {
			continue
		}
		// Match response by numeric ID.
		if msgID, ok := msg.ID.(float64); ok && int(msgID) == id {
			if msg.Error != nil {
				return nil, fmt.Errorf("lsp error %d: %s", msg.Error.Code, msg.Error.Message)
			}
			return msg.Result, nil
		}
	}
}

// readFramed reads one Content-Length-framed JSON-RPC message.
// Caller must hold c.mu.
func (c *lspClient) readFramed() (lspRawMessage, error) {
	contentLength := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return lspRawMessage{}, fmt.Errorf("read lsp header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			n, err := strconv.Atoi(strings.TrimPrefix(line, "Content-Length: "))
			if err != nil {
				return lspRawMessage{}, fmt.Errorf("parse Content-Length: %w", err)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return lspRawMessage{}, fmt.Errorf("missing Content-Length header")
	}
	const maxLSPMessageSize = 64 * 1024 * 1024 // 64 MiB
	if contentLength > maxLSPMessageSize {
		return lspRawMessage{}, fmt.Errorf("lsp message too large: %d bytes (max %d)", contentLength, maxLSPMessageSize)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return lspRawMessage{}, fmt.Errorf("read lsp body: %w", err)
	}
	var msg lspRawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return lspRawMessage{}, fmt.Errorf("parse lsp message: %w", err)
	}
	return msg, nil
}
