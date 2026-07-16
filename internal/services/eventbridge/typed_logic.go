package eventbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

type createEventBusRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

type createEventBusResponse struct {
	EventBusArn string `json:"EventBusArn" cbor:"EventBusArn"`
}

type describeEventBusRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

type describeEventBusResponse struct {
	Name string `json:"Name" cbor:"Name"`
	Arn  string `json:"Arn" cbor:"Arn"`
}

type listEventBusesRequest struct{}

type eventBusResponse struct {
	Name string `json:"Name" cbor:"Name"`
	Arn  string `json:"Arn" cbor:"Arn"`
}

type listEventBusesResponse struct {
	EventBuses []eventBusResponse `json:"EventBuses" cbor:"EventBuses"`
}

type tagResourceRequest struct {
	ResourceARN string     `json:"ResourceARN" cbor:"ResourceARN"`
	Tags        []tagEntry `json:"Tags" cbor:"Tags"`
}

type tagEntry struct {
	Key   string `json:"Key" cbor:"Key"`
	Value string `json:"Value" cbor:"Value"`
}

type listTagsForResourceRequest struct {
	ResourceARN string `json:"ResourceARN" cbor:"ResourceARN"`
}

type listTagsForResourceResponse struct {
	Tags []tagEntry `json:"Tags" cbor:"Tags"`
}

type deleteEventBusRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

type putRuleRequest struct {
	Name         string `json:"Name" cbor:"Name"`
	EventBusName string `json:"EventBusName" cbor:"EventBusName"`
	State        string `json:"State" cbor:"State"`
	Description  string `json:"Description" cbor:"Description"`
	RoleARN      string `json:"RoleArn" cbor:"RoleArn"`
	EventPattern string `json:"EventPattern" cbor:"EventPattern"`
	ScheduleExpr string `json:"ScheduleExpression" cbor:"ScheduleExpression"`
}

type putRuleResponse struct {
	RuleArn string `json:"RuleArn" cbor:"RuleArn"`
}

type describeRuleRequest struct {
	Name         string `json:"Name" cbor:"Name"`
	EventBusName string `json:"EventBusName" cbor:"EventBusName"`
}

type listRulesRequest struct {
	EventBusName string `json:"EventBusName" cbor:"EventBusName"`
	NamePrefix   string `json:"NamePrefix" cbor:"NamePrefix"`
}

type listRulesResponse struct {
	Rules []ebRule `json:"Rules" cbor:"Rules"`
}

type putTargetsRequest struct {
	Rule         string     `json:"Rule" cbor:"Rule"`
	EventBusName string     `json:"EventBusName" cbor:"EventBusName"`
	Targets      []ebTarget `json:"Targets" cbor:"Targets"`
}

type targetsMutationResponse struct {
	FailedEntryCount int   `json:"FailedEntryCount" cbor:"FailedEntryCount"`
	FailedEntries    []any `json:"FailedEntries" cbor:"FailedEntries"`
}

type listTargetsByRuleRequest struct {
	Rule         string `json:"Rule" cbor:"Rule"`
	EventBusName string `json:"EventBusName" cbor:"EventBusName"`
}

type listTargetsByRuleResponse struct {
	Targets []ebTarget `json:"Targets" cbor:"Targets"`
}

type removeTargetsRequest struct {
	Rule         string   `json:"Rule" cbor:"Rule"`
	EventBusName string   `json:"EventBusName" cbor:"EventBusName"`
	Ids          []string `json:"Ids" cbor:"Ids"`
}

type setRuleStateRequest struct {
	Name         string `json:"Name" cbor:"Name"`
	EventBusName string `json:"EventBusName" cbor:"EventBusName"`
}

type deleteRuleRequest struct {
	Name         string `json:"Name" cbor:"Name"`
	EventBusName string `json:"EventBusName" cbor:"EventBusName"`
}

type putEventsRequest struct {
	Entries []map[string]any `json:"Entries" cbor:"Entries"`
}

type putEventsEntryResponse struct {
	EventId string `json:"EventId" cbor:"EventId"`
}

type putEventsResponse struct {
	FailedEntryCount int                      `json:"FailedEntryCount" cbor:"FailedEntryCount"`
	Entries          []putEventsEntryResponse `json:"Entries" cbor:"Entries"`
}

func (s *Service) createEventBusTyped(ctx context.Context, req *createEventBusRequest) (*createEventBusResponse, *protocol.AWSError) {
	arn := protocol.ARN(s.cfg.Region, s.cfg.AccountID, "events", "event-bus/"+req.Name)
	bus := eventBus{Name: req.Name, ARN: arn}
	b, _ := json.Marshal(bus)
	if err := s.store.Set(ctx, nsBuses, serviceutil.RegionKey(s.region(ctx), req.Name), string(b)); err != nil {
		return nil, protocol.ErrInternalError
	}
	s.publishCtx(ctx, events.EventBridgeBusCreated, events.ResourcePayload{Name: req.Name})
	return &createEventBusResponse{EventBusArn: arn}, nil
}

func (s *Service) describeEventBusTyped(ctx context.Context, req *describeEventBusRequest) (*describeEventBusResponse, *protocol.AWSError) {
	name := req.Name
	if name == "" {
		name = "default"
	}
	raw, found, err := s.store.Get(ctx, nsBuses, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil || !found {
		arn := protocol.ARN(s.cfg.Region, s.cfg.AccountID, "events", "event-bus/"+name)
		return &describeEventBusResponse{Name: name, Arn: arn}, nil
	}
	var bus eventBus
	json.Unmarshal([]byte(raw), &bus) //nolint:errcheck
	return &describeEventBusResponse{Name: bus.Name, Arn: bus.ARN}, nil
}

func (s *Service) listEventBusesTyped(ctx context.Context, _ *listEventBusesRequest) (*listEventBusesResponse, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsBuses, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	items := make([]eventBusResponse, 0, len(kvs)+1)
	defaultARN := protocol.ARN(s.cfg.Region, s.cfg.AccountID, "events", "event-bus/default")
	items = append(items, eventBusResponse{Name: "default", Arn: defaultARN})
	for _, kv := range kvs {
		var bus eventBus
		if json.Unmarshal([]byte(kv.Value), &bus) == nil && bus.Name != "default" {
			items = append(items, eventBusResponse{Name: bus.Name, Arn: bus.ARN})
		}
	}
	return &listEventBusesResponse{EventBuses: items}, nil
}

func (s *Service) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	existing := map[string]string{}
	if raw, found, _ := s.store.Get(ctx, nsTags, req.ResourceARN); found {
		json.Unmarshal([]byte(raw), &existing) //nolint:errcheck
	}
	for _, t := range req.Tags {
		existing[t.Key] = t.Value
	}
	b, _ := json.Marshal(existing)
	s.store.Set(ctx, nsTags, req.ResourceARN, string(b)) //nolint:errcheck
	return &struct{}{}, nil
}

func (s *Service) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	stored := map[string]string{}
	if raw, found, _ := s.store.Get(ctx, nsTags, req.ResourceARN); found {
		json.Unmarshal([]byte(raw), &stored) //nolint:errcheck
	}
	tags := make([]tagEntry, 0, len(stored))
	for k, v := range stored {
		tags = append(tags, tagEntry{Key: k, Value: v})
	}
	return &listTagsForResourceResponse{Tags: tags}, nil
}

func (s *Service) deleteEventBusTyped(ctx context.Context, req *deleteEventBusRequest) (*struct{}, *protocol.AWSError) {
	s.store.Delete(ctx, nsBuses, serviceutil.RegionKey(s.region(ctx), req.Name)) //nolint:errcheck
	s.publishCtx(ctx, events.EventBridgeBusDeleted, events.ResourcePayload{Name: req.Name})
	return &struct{}{}, nil
}

func (s *Service) putRuleTyped(ctx context.Context, req *putRuleRequest) (*putRuleResponse, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	if req.State == "" {
		req.State = "ENABLED"
	}
	if req.ScheduleExpr != "" {
		if _, err := nextRuleFire(req.ScheduleExpr, s.clk.Now(), s.clk.Now()); err != nil {
			return nil, scheduleValidationError(err)
		}
	}
	arn := protocol.ARN(s.cfg.Region, s.cfg.AccountID, "events", "rule/"+req.EventBusName+"/"+req.Name)
	rule := ebRule{
		Name:         req.Name,
		ARN:          arn,
		EventBusName: req.EventBusName,
		State:        req.State,
		Description:  req.Description,
		RoleARN:      req.RoleARN,
		EventPattern: req.EventPattern,
		ScheduleExpr: req.ScheduleExpr,
	}
	key := serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"+req.Name)
	b, _ := json.Marshal(rule)
	if err := s.store.Set(ctx, nsRules, key, string(b)); err != nil {
		return nil, protocol.ErrInternalError
	}
	if req.ScheduleExpr != "" {
		now := s.clk.Now()
		s.setLastFire(ctx, key, now)
		s.setNextFire(ctx, key, req.ScheduleExpr, now, now)
	}
	s.publishCtx(ctx, events.EventBridgeRuleCreated, events.ResourcePayload{Name: req.Name})
	return &putRuleResponse{RuleArn: arn}, nil
}

func (s *Service) describeRuleTyped(ctx context.Context, req *describeRuleRequest) (*ebRule, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"+req.Name)
	raw, found, err := s.store.Get(ctx, nsRules, key)
	if err != nil || !found {
		return nil, ruleNotFound("Rule not found.")
	}
	var rule ebRule
	json.Unmarshal([]byte(raw), &rule) //nolint:errcheck
	return &rule, nil
}

func (s *Service) listRulesTyped(ctx context.Context, req *listRulesRequest) (*listRulesResponse, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	kvs, err := s.store.Scan(ctx, nsRules, serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	items := make([]ebRule, 0, len(kvs))
	for _, kv := range kvs {
		var rule ebRule
		if json.Unmarshal([]byte(kv.Value), &rule) == nil {
			items = append(items, rule)
		}
	}
	return &listRulesResponse{Rules: items}, nil
}

func (s *Service) putTargetsTyped(ctx context.Context, req *putTargetsRequest) (*targetsMutationResponse, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"+req.Rule)
	existing := []ebTarget{}
	if raw, found, _ := s.store.Get(ctx, nsTargets, key); found {
		json.Unmarshal([]byte(raw), &existing) //nolint:errcheck
	}
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
	s.store.Set(ctx, nsTargets, key, string(b)) //nolint:errcheck
	return emptyTargetsMutation(), nil
}

func (s *Service) listTargetsByRuleTyped(ctx context.Context, req *listTargetsByRuleRequest) (*listTargetsByRuleResponse, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"+req.Rule)
	targets := []ebTarget{}
	if raw, found, _ := s.store.Get(ctx, nsTargets, key); found {
		json.Unmarshal([]byte(raw), &targets) //nolint:errcheck
	}
	return &listTargetsByRuleResponse{Targets: targets}, nil
}

func (s *Service) removeTargetsTyped(ctx context.Context, req *removeTargetsRequest) (*targetsMutationResponse, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"+req.Rule)
	existing := []ebTarget{}
	if raw, found, _ := s.store.Get(ctx, nsTargets, key); found {
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
	s.store.Set(ctx, nsTargets, key, string(b)) //nolint:errcheck
	return emptyTargetsMutation(), nil
}

func (s *Service) disableRuleTyped(ctx context.Context, req *setRuleStateRequest) (*struct{}, *protocol.AWSError) {
	return s.setRuleStateTyped(ctx, req, "DISABLED")
}

func (s *Service) enableRuleTyped(ctx context.Context, req *setRuleStateRequest) (*struct{}, *protocol.AWSError) {
	return s.setRuleStateTyped(ctx, req, "ENABLED")
}

func (s *Service) setRuleStateTyped(ctx context.Context, req *setRuleStateRequest, state string) (*struct{}, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"+req.Name)
	raw, found, err := s.store.Get(ctx, nsRules, key)
	if err != nil || !found {
		return nil, ruleNotFound(fmt.Sprintf("Rule %s does not exist.", req.Name))
	}
	var rule ebRule
	if json.Unmarshal([]byte(raw), &rule) == nil {
		rule.State = state
		b, _ := json.Marshal(rule)
		s.store.Set(ctx, nsRules, key, string(b)) //nolint:errcheck
	}
	return &struct{}{}, nil
}

func (s *Service) deleteRuleTyped(ctx context.Context, req *deleteRuleRequest) (*struct{}, *protocol.AWSError) {
	if req.EventBusName == "" {
		req.EventBusName = "default"
	}
	key := serviceutil.RegionKey(s.region(ctx), req.EventBusName+"/"+req.Name)
	s.store.Delete(ctx, nsRules, key)    //nolint:errcheck
	s.store.Delete(ctx, nsTargets, key)  //nolint:errcheck
	s.store.Delete(ctx, nsLastFire, key) //nolint:errcheck
	s.store.Delete(ctx, nsNextFire, key) //nolint:errcheck
	s.publishCtx(ctx, events.EventBridgeRuleDeleted, events.ResourcePayload{Name: req.Name})
	return &struct{}{}, nil
}

func (s *Service) putEventsTyped(ctx context.Context, req *putEventsRequest) (*putEventsResponse, *protocol.AWSError) {
	results := make([]putEventsEntryResponse, 0, len(req.Entries))
	for _, entry := range req.Entries {
		eventID := uuid.New().String()
		results = append(results, putEventsEntryResponse{EventId: eventID})
		s.deliverEvent(ctx, eventID, entry)
	}
	return &putEventsResponse{FailedEntryCount: 0, Entries: results}, nil
}

func (s *Service) publishCtx(ctx context.Context, t events.Type, payload any) {
	if s.bus != nil {
		s.bus.Publish(ctx, events.Event{Type: t, Payload: payload})
	}
}

func emptyTargetsMutation() *targetsMutationResponse {
	return &targetsMutationResponse{FailedEntryCount: 0, FailedEntries: []any{}}
}

func ruleNotFound(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}
