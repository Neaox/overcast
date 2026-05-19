// Package eventbridge provides emulation of Amazon EventBridge.
//
// Implemented: CreateEventBus, DescribeEventBus, ListEventBuses, TagResource,
// ListTagsForResource, DeleteEventBus, PutRule, DescribeRule, ListRules,
// PutTargets, ListTargetsByRule, RemoveTargets, DisableRule, EnableRule,
// DeleteRule, PutEvents.
package eventbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName  = "eventbridge"
	targetPrefix = "AWSEvents."
	nsBuses      = "eb:buses"
	nsRules      = "eb:rules"
	nsTags       = "eb:tags"
	nsTargets    = "eb:targets"
)

// Service implements router.Service and router.TargetDispatcher for EventBridge.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	bus     *events.Bus
	typedOp map[string]op.Operation
}

// New returns a configured EventBridge Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, _ clock.Clock) *Service {
	s := &Service{cfg: cfg, store: store, log: serviceutil.NewServiceLogger(logger, serviceName)}
	s.typedOp = s.typedOps()
	return s
}

// InitBus wires the event bus for event bus/rule lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.bus = bus
}

// publish emits an event if the bus is wired.
func (s *Service) publish(r *http.Request, t events.Type, payload any) {
	if s.bus != nil {
		s.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

// region returns the per-request region, falling back to the configured default.
func (s *Service) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.cfg.Region)
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return targetPrefix }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "EventBridge does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		// Preserve AWS JSON 1.1 on the existing switch path until JSON
		// wire-byte goldens cover EventBridge. CBOR uses typed operations.
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}

	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	s.dispatchLegacy(w, r, op)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "CreateEventBus":
		s.createEventBus(w, r)
	case "DescribeEventBus":
		s.describeEventBus(w, r)
	case "ListEventBuses":
		s.listEventBuses(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "DeleteEventBus":
		s.deleteEventBus(w, r)
	case "PutRule":
		s.putRule(w, r)
	case "DescribeRule":
		s.describeRule(w, r)
	case "ListRules":
		s.listRules(w, r)
	case "PutTargets":
		s.putTargets(w, r)
	case "ListTargetsByRule":
		s.listTargetsByRule(w, r)
	case "RemoveTargets":
		s.removeTargets(w, r)
	case "DisableRule":
		s.disableRule(w, r)
	case "EnableRule":
		s.enableRule(w, r)
	case "DeleteRule":
		s.deleteRule(w, r)
	case "PutEvents":
		s.putEvents(w, r)
	default:
		protocol.NotImplementedJSON(w, r)
	}
}

type eventBus struct {
	Name        string `json:"Name" cbor:"Name"`
	ARN         string `json:"Arn" cbor:"Arn"`
	Description string `json:"Description" cbor:"Description"`
}

type ebRule struct {
	Name         string `json:"Name" cbor:"Name"`
	ARN          string `json:"Arn" cbor:"Arn"`
	EventBusName string `json:"EventBusName" cbor:"EventBusName"`
	State        string `json:"State" cbor:"State"`
	Description  string `json:"Description" cbor:"Description"`
	EventPattern string `json:"EventPattern" cbor:"EventPattern"`
	ScheduleExpr string `json:"ScheduleExpression" cbor:"ScheduleExpression"`
}

type ebTarget struct {
	ID  string `json:"Id" cbor:"Id"`
	ARN string `json:"Arn" cbor:"Arn"`
}

func (s *Service) createEventBus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	arn := protocol.ARN(s.region(r.Context()), s.cfg.AccountID, "events", "event-bus/"+req.Name)
	bus := eventBus{Name: req.Name, ARN: arn}
	b, _ := json.Marshal(bus)
	if err := s.store.Set(r.Context(), nsBuses, serviceutil.RegionKey(s.region(r.Context()), req.Name), string(b)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	s.publish(r, events.EventBridgeBusCreated, events.ResourcePayload{Name: req.Name})
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"EventBusArn": arn})
}

func (s *Service) describeEventBus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "default"
	}
	raw, found, err := s.store.Get(r.Context(), nsBuses, serviceutil.RegionKey(s.region(r.Context()), req.Name))
	if err != nil || !found {
		// Return a default bus if not found
		arn := protocol.ARN(s.region(r.Context()), s.cfg.AccountID, "events", "event-bus/"+req.Name)
		protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Name": req.Name, "Arn": arn})
		return
	}
	var bus eventBus
	json.Unmarshal([]byte(raw), &bus) //nolint:errcheck
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Name": bus.Name, "Arn": bus.ARN})
}

func (s *Service) listEventBuses(w http.ResponseWriter, r *http.Request) {
	kvs, err := s.store.Scan(r.Context(), nsBuses, serviceutil.RegionKey(s.region(r.Context()), ""))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	items := make([]map[string]any, 0, len(kvs)+1)
	// Always include the default bus
	defaultARN := protocol.ARN(s.region(r.Context()), s.cfg.AccountID, "events", "event-bus/default")
	items = append(items, map[string]any{"Name": "default", "Arn": defaultARN})
	for _, kv := range kvs {
		var bus eventBus
		if json.Unmarshal([]byte(kv.Value), &bus) == nil && bus.Name != "default" {
			items = append(items, map[string]any{"Name": bus.Name, "Arn": bus.ARN})
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"EventBuses": items})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	// Tags are sent as an array of {Key, Value} objects by AWS SDKs.
	var req struct {
		ResourceARN string `json:"ResourceARN"`
		Tags        []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	// Merge with existing tags
	existing := map[string]string{}
	if raw, found, _ := s.store.Get(r.Context(), nsTags, req.ResourceARN); found {
		json.Unmarshal([]byte(raw), &existing) //nolint:errcheck
	}
	for _, t := range req.Tags {
		existing[t.Key] = t.Value
	}
	b, _ := json.Marshal(existing)
	s.store.Set(r.Context(), nsTags, req.ResourceARN, string(b)) //nolint:errcheck
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string `json:"ResourceARN"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	stored := map[string]string{}
	if raw, found, _ := s.store.Get(r.Context(), nsTags, req.ResourceARN); found {
		json.Unmarshal([]byte(raw), &stored) //nolint:errcheck
	}
	// Return Tags as array of {Key, Value} objects (AWS SDK wire format).
	tags := make([]map[string]string, 0, len(stored))
	for k, v := range stored {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Tags": tags})
}

func (s *Service) deleteEventBus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	s.store.Delete(r.Context(), nsBuses, serviceutil.RegionKey(s.region(r.Context()), req.Name)) //nolint:errcheck
	s.publish(r, events.EventBridgeBusDeleted, events.ResourcePayload{Name: req.Name})
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) putRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"Name"`
		EventBusName string `json:"EventBusName"`
		State        string `json:"State"`
		Description  string `json:"Description"`
		EventPattern string `json:"EventPattern"`
		ScheduleExpr string `json:"ScheduleExpression"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	if req.State == "" {
		req.State = "ENABLED"
	}
	arn := protocol.ARN(s.region(r.Context()), s.cfg.AccountID, "events", "rule/"+req.EventBusName+"/"+req.Name)
	rule := ebRule{
		Name:         req.Name,
		ARN:          arn,
		EventBusName: req.EventBusName,
		State:        req.State,
		Description:  req.Description,
		EventPattern: req.EventPattern,
		ScheduleExpr: req.ScheduleExpr,
	}
	key := serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"+req.Name)
	b, _ := json.Marshal(rule)
	if err := s.store.Set(r.Context(), nsRules, key, string(b)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	s.publish(r, events.EventBridgeRuleCreated, events.ResourcePayload{Name: req.Name})
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"RuleArn": arn})
}

func (s *Service) describeRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"Name"`
		EventBusName string `json:"EventBusName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"+req.Name)
	raw, found, err := s.store.Get(r.Context(), nsRules, key)
	if err != nil || !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Rule not found.", HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	var rule ebRule
	json.Unmarshal([]byte(raw), &rule) //nolint:errcheck
	protocol.WriteJSON(w, r, http.StatusOK, rule)
}

func (s *Service) listRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventBusName string `json:"EventBusName"`
		NamePrefix   string `json:"NamePrefix"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	kvs, err := s.store.Scan(r.Context(), nsRules, serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	items := make([]ebRule, 0, len(kvs))
	for _, kv := range kvs {
		var rule ebRule
		if json.Unmarshal([]byte(kv.Value), &rule) == nil {
			items = append(items, rule)
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Rules": items})
}

func (s *Service) putTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule         string     `json:"Rule"`
		EventBusName string     `json:"EventBusName"`
		Targets      []ebTarget `json:"Targets"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"+req.Rule)
	// Merge with existing targets
	existing := []ebTarget{}
	if raw, found, _ := s.store.Get(r.Context(), nsTargets, key); found {
		json.Unmarshal([]byte(raw), &existing) //nolint:errcheck
	}
	// Update by ID
	targetMap := map[string]ebTarget{}
	for _, t := range existing {
		targetMap[t.ID] = t
	}
	for _, t := range req.Targets {
		targetMap[t.ID] = t
	}
	merged := make([]ebTarget, 0, len(targetMap))
	for _, t := range targetMap {
		merged = append(merged, t)
	}
	b, _ := json.Marshal(merged)
	s.store.Set(r.Context(), nsTargets, key, string(b)) //nolint:errcheck
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"FailedEntryCount": 0, "FailedEntries": []any{}})
}

func (s *Service) listTargetsByRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule         string `json:"Rule"`
		EventBusName string `json:"EventBusName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"+req.Rule)
	targets := []ebTarget{}
	if raw, found, _ := s.store.Get(r.Context(), nsTargets, key); found {
		json.Unmarshal([]byte(raw), &targets) //nolint:errcheck
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Targets": targets})
}

func (s *Service) removeTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule         string   `json:"Rule"`
		EventBusName string   `json:"EventBusName"`
		Ids          []string `json:"Ids"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"+req.Rule)
	existing := []ebTarget{}
	if raw, found, _ := s.store.Get(r.Context(), nsTargets, key); found {
		json.Unmarshal([]byte(raw), &existing) //nolint:errcheck
	}
	removeSet := map[string]bool{}
	for _, id := range req.Ids {
		removeSet[id] = true
	}
	kept := make([]ebTarget, 0, len(existing))
	for _, t := range existing {
		if !removeSet[t.ID] {
			kept = append(kept, t)
		}
	}
	b, _ := json.Marshal(kept)
	s.store.Set(r.Context(), nsTargets, key, string(b)) //nolint:errcheck
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"FailedEntryCount": 0, "FailedEntries": []any{}})
}

func (s *Service) disableRule(w http.ResponseWriter, r *http.Request) {
	s.setRuleState(w, r, "DISABLED")
}

func (s *Service) enableRule(w http.ResponseWriter, r *http.Request) {
	s.setRuleState(w, r, "ENABLED")
}

func (s *Service) setRuleState(w http.ResponseWriter, r *http.Request, state string) {
	var req struct {
		Name         string `json:"Name"`
		EventBusName string `json:"EventBusName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"+req.Name)
	raw, found, err := s.store.Get(r.Context(), nsRules, key)
	if err != nil || !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Rule %s does not exist.", req.Name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	var rule ebRule
	if json.Unmarshal([]byte(raw), &rule) == nil {
		rule.State = state
		b, _ := json.Marshal(rule)
		s.store.Set(r.Context(), nsRules, key, string(b)) //nolint:errcheck
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) deleteRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"Name"`
		EventBusName string `json:"EventBusName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(r.Context()), req.EventBusName+"/"+req.Name)
	s.store.Delete(r.Context(), nsRules, key)   //nolint:errcheck
	s.store.Delete(r.Context(), nsTargets, key) //nolint:errcheck
	s.publish(r, events.EventBridgeRuleDeleted, events.ResourcePayload{Name: req.Name})
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) putEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Entries []map[string]any `json:"Entries"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	results := make([]map[string]any, 0, len(req.Entries))
	for range req.Entries {
		results = append(results, map[string]any{
			"EventId": uuid.New().String(),
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"FailedEntryCount": 0,
		"Entries":          results,
	})
}
