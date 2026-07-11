package appsync

// handler_subscriptions.go — WebSocket endpoint for AppSync real-time subscriptions.
//
// Implemented:
//   - HandleWebSocket  GET /_appsync/{apiId}/realtime
//
// Protocol messages:
//   connection_init → connection_ack
//   start          → start_ack (subscribe)
//   stop           → complete  (unsubscribe)
//   ka             ← periodic keep-alive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/Neaox/overcast/internal/protocol"
)

// HandleWebSocket handles GET /_appsync/{apiId}/realtime — upgrades to WebSocket
// and manages the AppSync real-time subscription protocol.
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	// Verify the API exists.
	api, err := h.store.GetAPI(r.Context(), apiID)
	if err != nil || api == nil {
		http.Error(w, "API not found", http.StatusNotFound)
		return
	}
	authReq := realtimeAuthRequest(r)
	if _, aerr := h.authenticateRequest(authReq, api); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Accept the WebSocket upgrade.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // emulator: accept any origin
	})
	if err != nil {
		return
	}

	connID := uuid.NewString()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	h.subscriptions.addConnection(connID, apiID, conn, cancel)
	defer func() {
		h.subscriptions.removeConnection(connID)
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	// Start keep-alive goroutine.
	go h.subscriptions.sendKeepalives(ctx, conn)

	// Message loop.
	for {
		_, msgBytes, readErr := conn.Read(ctx)
		if readErr != nil {
			return // connection closed or error
		}

		var msg wsMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "connection_init":
			h.handleConnectionInit(ctx, conn)

		case "start":
			h.handleSubscriptionStart(ctx, conn, connID, apiID, msg)

		case "stop":
			h.handleSubscriptionStop(ctx, conn, connID, msg)

		case "connection_terminate":
			return
		}
	}
}

// handleConnectionInit responds to a connection_init message with connection_ack.
func (h *Handler) handleConnectionInit(ctx context.Context, conn *websocket.Conn) {
	ack := wsMessage{
		Type:    "connection_ack",
		Payload: json.RawMessage(`{"connectionTimeoutMs":300000}`),
	}
	data, _ := json.Marshal(ack)
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, websocket.MessageText, data)
}

// handleSubscriptionStart processes a "start" message to register a subscription.
func (h *Handler) handleSubscriptionStart(ctx context.Context, conn *websocket.Conn, connID, apiID string, msg wsMessage) {
	// Parse the payload to get query and variables.
	var payload struct {
		Query      string         `json:"query"`
		Variables  map[string]any `json:"variables"`
		Data       string         `json:"data"`
		Extensions struct {
			Authorization map[string]any `json:"authorization"`
		} `json:"extensions"`
	}
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	if payload.Data != "" {
		var data struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal([]byte(payload.Data), &data); err == nil {
			payload.Query = data.Query
			payload.Variables = data.Variables
		}
	}

	// Extract the subscription field name from the query.
	fieldName := extractSubscriptionFieldName(payload.Query)

	// Register the subscription.
	h.subscriptions.Register(ctx, apiID, msg.ID, connID, payload.Query, payload.Variables, fieldName)

	// Send start_ack.
	ack := wsMessage{
		ID:   msg.ID,
		Type: "start_ack",
	}
	data, _ := json.Marshal(ack)
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, websocket.MessageText, data)
}

func realtimeAuthRequest(r *http.Request) *http.Request {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	if encoded := r.URL.Query().Get("header"); encoded != "" {
		if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			applyRealtimeAuthHeader(clone.Header, decoded)
		}
	}
	for _, protocolValue := range clone.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(protocolValue, ",") {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "header-") {
				continue
			}
			encoded := strings.TrimPrefix(part, "header-")
			if decoded, err := base64.RawURLEncoding.DecodeString(encoded); err == nil {
				applyRealtimeAuthHeader(clone.Header, decoded)
			}
		}
	}
	return clone
}

func applyRealtimeAuthHeader(h http.Header, raw []byte) {
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		return
	}
	for k, v := range values {
		if strings.EqualFold(k, "host") {
			continue
		}
		h.Set(k, v)
	}
}

// handleSubscriptionStop processes a "stop" message to unregister a subscription.
func (h *Handler) handleSubscriptionStop(ctx context.Context, conn *websocket.Conn, connID string, msg wsMessage) {
	h.subscriptions.Unregister(connID, msg.ID)

	// Send complete.
	complete := wsMessage{
		ID:   msg.ID,
		Type: "complete",
	}
	data, _ := json.Marshal(complete)
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, websocket.MessageText, data)
}

// extractSubscriptionFieldName parses a subscription query and returns the
// top-level field name (e.g. "onCreateTodo" from "subscription { onCreateTodo { id title } }").
func extractSubscriptionFieldName(query string) string {
	doc, err := parser.ParseQuery(&ast.Source{Name: "sub", Input: query})
	if err != nil || len(doc.Operations) == 0 {
		// Fallback: try simple string parsing.
		return extractSubscriptionFieldNameSimple(query)
	}
	op := doc.Operations[0]
	if len(op.SelectionSet) == 0 {
		return ""
	}
	if field, ok := op.SelectionSet[0].(*ast.Field); ok {
		return field.Name
	}
	return ""
}

// extractSubscriptionFieldNameSimple does a best-effort extraction of the
// subscription field name from a query string without full parsing.
func extractSubscriptionFieldNameSimple(query string) string {
	// Look for "subscription" keyword then first word after "{"
	q := strings.TrimSpace(query)
	idx := strings.Index(q, "{")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(q[idx+1:])
	// Find the field name — it's the first word.
	end := strings.IndexAny(rest, " {(\n\t}")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}
