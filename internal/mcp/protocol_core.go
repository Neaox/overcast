package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type capabilityDescriptor struct {
	name  string
	apply func(*ServerCapabilities)
}

var capabilityDescriptors = []capabilityDescriptor{
	{
		name: "tools",
		apply: func(c *ServerCapabilities) {
			c.Tools = &ToolCapability{ListChanged: true}
		},
	},
	{
		name: "resources",
		apply: func(c *ServerCapabilities) {
			c.Resources = &ResourceCapability{Subscribe: true, ListChanged: true}
		},
	},
	{
		name: "prompts",
		apply: func(c *ServerCapabilities) {
			c.Prompts = &PromptsCapability{ListChanged: true}
		},
	},
	{
		name: "completions",
		apply: func(c *ServerCapabilities) {
			c.Completions = &CompletionsCapability{}
		},
	},
	{
		name: "logging",
		apply: func(c *ServerCapabilities) {
			c.Logging = &LoggingCapability{}
		},
	},
}

var (
	errUnknownSessionHeader = errors.New("unknown MCP session")
)

var loggingLevelRankings = map[string]int{
	"debug":     7,
	"info":      6,
	"notice":    5,
	"warning":   4,
	"error":     3,
	"critical":  2,
	"alert":     1,
	"emergency": 0,
}

func defaultServerCapabilities() ServerCapabilities {
	capabilities := ServerCapabilities{}
	for _, descriptor := range capabilityDescriptors {
		descriptor.apply(&capabilities)
	}
	return capabilities
}

func (s *Server) validateOptionalSessionHeader(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	if !s.hasSession(sessionID) {
		return errUnknownSessionHeader
	}
	return nil
}

func (s *Server) hasSession(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sessions[sessionID]
	return ok
}

func validateProtocolVersionHeader(requested string) *rpcError {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == ProtocolVersion {
		return nil
	}
	return &rpcError{
		Code:    RPCInvalidParams,
		Message: "unsupported MCP protocol version: requested " + strconv.Quote(requested) + ", server supports " + strconv.Quote(ProtocolVersion),
		Data: map[string]any{
			"supported": []string{ProtocolVersion},
			"requested": requested,
		},
	}
}

func validateLifecycle(initDone, ready bool) *rpcError {
	if !initDone {
		return &rpcError{Code: RPCInvalidRequest, Message: "initialize must be sent before operation requests"}
	}
	if !ready {
		return &rpcError{Code: RPCInvalidRequest, Message: "notifications/initialized must be sent before operation requests"}
	}
	return nil
}

func paginateRange(total int, cursor string, limit int) (start int, end int, nextCursor string, err *rpcError) {
	start = 0
	if cursor != "" {
		n, convErr := strconv.Atoi(cursor)
		if convErr != nil || n < 0 || n > total {
			return 0, 0, "", &rpcError{Code: RPCInvalidParams, Message: "invalid cursor"}
		}
		start = n
	}
	if limit <= 0 {
		limit = total
	}
	end = start + limit
	if end > total {
		end = total
	}
	if end < total {
		nextCursor = strconv.Itoa(end)
	}
	return start, end, nextCursor, nil
}

type listParams struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

func decodeOptionalParams[T any](params json.RawMessage) (T, *rpcError) {
	decoded := *new(T)
	if len(params) == 0 {
		return decoded, nil
	}
	if err := json.Unmarshal(params, &decoded); err != nil {
		return *new(T), &rpcError{Code: RPCInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return decoded, nil
}

func decodeRequiredParams[T any](params json.RawMessage, method string) (T, *rpcError) {
	decoded := *new(T)
	if len(params) == 0 {
		return decoded, &rpcError{Code: RPCInvalidParams, Message: method + " params required"}
	}
	if err := json.Unmarshal(params, &decoded); err != nil {
		return *new(T), &rpcError{Code: RPCInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return decoded, nil
}

func decodeRequiredURIParam(params json.RawMessage, method string) (string, *rpcError) {
	type uriParam struct {
		URI string `json:"uri"`
	}
	decoded, err := decodeRequiredParams[uriParam](params, method)
	if err != nil {
		return "", err
	}
	uri := strings.TrimSpace(decoded.URI)
	if uri == "" {
		return "", &rpcError{Code: RPCInvalidParams, Message: method + " uri required"}
	}
	return uri, nil
}

func uniquePrefixMatches(candidates []string, prefix string) []any {
	matches := collectUniqueSortedMatches(candidates, prefix, func(candidate string, normalizedPrefix string) bool {
		return normalizedPrefix == "" || strings.HasPrefix(strings.ToLower(candidate), normalizedPrefix)
	})
	if len(matches) == 0 && strings.TrimSpace(prefix) != "" {
		// Fallback to case-insensitive contains matching so completion still
		// returns useful suggestions when callers pass an interior fragment.
		matches = collectUniqueSortedMatches(candidates, prefix, func(candidate string, normalizedPrefix string) bool {
			return strings.Contains(strings.ToLower(candidate), normalizedPrefix)
		})
	}
	values := make([]any, 0, len(matches))
	for _, candidate := range matches {
		values = append(values, candidate)
	}
	return values
}

func collectUniqueSortedMatches(candidates []string, prefix string, matcher func(candidate string, normalizedPrefix string) bool) []string {
	matches := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	normalizedPrefix := strings.ToLower(strings.TrimSpace(prefix))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if !matcher(candidate, normalizedPrefix) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		matches = append(matches, candidate)
	}
	sort.Strings(matches)
	return matches
}

func decodeListParams(params json.RawMessage) (listParams, *rpcError) {
	return decodeOptionalParams[listParams](params)
}

func paginatedListResult[T any](params json.RawMessage, itemKey string, items []T) (map[string]any, *rpcError) {
	decoded, err := decodeListParams(params)
	if err != nil {
		return nil, err
	}
	start, end, nextCursor, pageErr := paginateRange(len(items), decoded.Cursor, decoded.Limit)
	if pageErr != nil {
		return nil, pageErr
	}
	result := map[string]any{itemKey: items[start:end]}
	if nextCursor != "" {
		result["nextCursor"] = nextCursor
	}
	return result, nil
}

func normalizeToolResult(result any, execErr error) ToolResult {
	if execErr != nil {
		return ToolResult{
			IsError:           true,
			Content:           TextContent(execErr.Error()),
			StructuredContent: map[string]any{"error": execErr.Error()},
		}
	}
	if toolResult, ok := result.(ToolResult); ok {
		return toolResult
	}
	return ToolResult{
		Content:           TextContent(toolResultText(result)),
		StructuredContent: result,
	}
}

func normalizeLoggingLevel(level string) (string, *rpcError) {
	normalized := strings.ToLower(strings.TrimSpace(level))
	if normalized == "warn" {
		normalized = "warning"
	}
	if _, ok := loggingLevelRankings[normalized]; !ok {
		return "", &rpcError{Code: RPCInvalidParams, Message: "invalid logging level"}
	}
	return normalized, nil
}

func loggingLevelRank(level string) int {
	normalized := strings.ToLower(strings.TrimSpace(level))
	rank, ok := loggingLevelRankings[normalized]
	if !ok {
		return loggingLevelRankings["info"]
	}
	return rank
}

func writeJSONRPCError(w http.ResponseWriter, id any, rpcErr *rpcError) {
	if rpcErr == nil {
		return
	}
	writeRPCResult(w, id, jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: rpcErr})
}
