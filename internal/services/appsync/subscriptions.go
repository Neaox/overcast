package appsync

// subscriptions.go — WebSocket subscription manager for AppSync real-time.
//
// Manages active WebSocket connections and subscription registrations.
// When a mutation is executed, matching subscriptions receive the result
// via their WebSocket connection.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// wsMessage represents a message in the AppSync real-time WebSocket protocol.
type wsMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// wsConnection tracks a single WebSocket connection.
type wsConnection struct {
	conn   *websocket.Conn
	apiID  string
	connID string
	cancel context.CancelFunc
}

// subscription tracks a single subscription registration.
type subscription struct {
	connID    string
	subID     string
	apiID     string
	query     string
	variables map[string]any
	fieldName string // the subscription field name (e.g. "onCreateTodo")
}

// subscriptionManager implements SubscriptionManager.
type subscriptionManager struct {
	mu    sync.RWMutex
	conns map[string]*wsConnection   // connectionId → connection
	subs  map[string][]*subscription // "apiId:fieldName" → subscriptions
	clk   clock.Clock
	log   *serviceutil.ServiceLogger
}

// newSubscriptionManager creates a new subscription manager.
func newSubscriptionManager(clk clock.Clock, log *serviceutil.ServiceLogger) *subscriptionManager {
	return &subscriptionManager{
		conns: make(map[string]*wsConnection),
		subs:  make(map[string][]*subscription),
		clk:   clk,
		log:   log,
	}
}

// addConnection registers a WebSocket connection.
func (sm *subscriptionManager) addConnection(connID, apiID string, conn *websocket.Conn, cancel context.CancelFunc) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.conns[connID] = &wsConnection{
		conn:   conn,
		apiID:  apiID,
		connID: connID,
		cancel: cancel,
	}
}

// removeConnection removes a connection and all its subscriptions.
func (sm *subscriptionManager) removeConnection(connID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if ws, ok := sm.conns[connID]; ok {
		ws.cancel() // stop keepalive goroutine; idempotent
		delete(sm.conns, connID)
	}
	// Remove all subscriptions for this connection.
	for key, subs := range sm.subs {
		filtered := subs[:0]
		for _, s := range subs {
			if s.connID != connID {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) == 0 {
			delete(sm.subs, key)
		} else {
			sm.subs[key] = filtered
		}
	}
}

// Register adds a subscription for the given API and connection.
func (sm *subscriptionManager) Register(_ context.Context, apiID, subscriptionID, connID, query string, variables map[string]any, fieldName string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	key := apiID + ":" + fieldName
	sm.subs[key] = append(sm.subs[key], &subscription{
		connID:    connID,
		subID:     subscriptionID,
		apiID:     apiID,
		query:     query,
		variables: variables,
		fieldName: fieldName,
	})
}

// Unregister removes a subscription by connection and subscription ID.
func (sm *subscriptionManager) Unregister(connID, subscriptionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for key, subs := range sm.subs {
		filtered := subs[:0]
		for _, s := range subs {
			if !(s.connID == connID && s.subID == subscriptionID) {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) == 0 {
			delete(sm.subs, key)
		} else {
			sm.subs[key] = filtered
		}
	}
}

// publishTarget holds the pre-serialised message and the connection to deliver it to.
// Built under lock so the actual writes can happen lock-free.
type publishTarget struct {
	conn *websocket.Conn
	msg  []byte
}

// Publish fans out a mutation result to all matching subscriptions.
// mutationField is the mutation field name (e.g. "createTodo").
// data is the resolved mutation result.
func (sm *subscriptionManager) Publish(_ context.Context, apiID, mutationField string, data map[string]any) {
	// Convention: mutation "createFoo" triggers subscription "onCreateFoo".
	subFieldName := "on" + strings.ToUpper(mutationField[:1]) + mutationField[1:]
	key := apiID + ":" + subFieldName

	// Snapshot matching subscriptions and their connections under the read lock,
	// then release it before performing any (potentially slow) writes.
	var targets []publishTarget
	sm.mu.RLock()
	subs := sm.subs[key]
	for _, sub := range subs {
		ws := sm.conns[sub.connID]
		if ws == nil {
			continue
		}

		payload := map[string]any{
			"data": map[string]any{
				sub.fieldName: data,
			},
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			continue
		}

		msg := wsMessage{
			ID:      sub.subID,
			Type:    "data",
			Payload: payloadJSON,
		}
		msgJSON, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		targets = append(targets, publishTarget{conn: ws.conn, msg: msgJSON})
	}
	sm.mu.RUnlock()

	// Write outside the lock — a slow/dead connection no longer blocks mutations
	// on other connections. Use context.Background so writes are not tied to the
	// HTTP request that triggered the mutation (publish is fire-and-forget).
	for _, t := range targets {
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = t.conn.Write(writeCtx, websocket.MessageText, t.msg)
		cancel()
	}
}

// sendKeepalives sends periodic keep-alive messages to a connection.
// Must be run in a goroutine. Respects context cancellation.
func (sm *subscriptionManager) sendKeepalives(ctx context.Context, conn *websocket.Conn) {
	ticker := sm.clk.Ticker(30 * time.Second)
	defer ticker.Stop()

	kaMsg, _ := json.Marshal(wsMessage{Type: "ka"})
	for {
		select {
		case <-ticker.C:
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = conn.Write(writeCtx, websocket.MessageText, kaMsg)
			cancel()
		case <-ctx.Done():
			return
		}
	}
}
