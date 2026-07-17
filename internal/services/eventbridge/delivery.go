package eventbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) deliverEvent(ctx context.Context, eventID string, entry map[string]any) {
	busName := stringValue(entry, "EventBusName")
	if busName == "" {
		busName = "default"
	}
	event := s.putEventsEnvelope(ctx, eventID, entry)
	prefix := serviceutil.RegionKey(s.region(ctx), busName+"/")
	pairs, err := s.store.Scan(ctx, nsRules, prefix)
	if err != nil {
		s.log.Error("eventbridge: deliver: scan rules", zap.Error(err))
		return
	}
	for _, kv := range pairs {
		var rule ebRule
		if json.Unmarshal([]byte(kv.Value), &rule) != nil || rule.State == "DISABLED" || rule.EventPattern == "" {
			continue
		}
		if eventPatternMatches(rule.EventPattern, event) {
			s.deliverTargets(ctx, rule, event)
		}
	}
}

func (s *Service) putEventsEnvelope(ctx context.Context, eventID string, entry map[string]any) map[string]any {
	detail := map[string]any{}
	if raw := stringValue(entry, "Detail"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &detail)
	}
	resources := []any{}
	if raw, ok := entry["Resources"].([]any); ok {
		resources = raw
	}
	return map[string]any{
		"version":     "0",
		"id":          eventID,
		"detail-type": stringValue(entry, "DetailType"),
		"source":      stringValue(entry, "Source"),
		"account":     s.cfg.AccountID,
		"time":        s.clk.Now().UTC().Format(time.RFC3339),
		"region":      s.region(ctx),
		"resources":   resources,
		"detail":      detail,
	}
}

func (s *Service) deliverTargets(ctx context.Context, rule ebRule, event map[string]any) {
	key := serviceutil.RegionKey(s.region(ctx), rule.EventBusName+"/"+rule.Name)
	targets := []ebTarget{}
	raw, found, err := s.store.Get(ctx, nsTargets, key)
	if err != nil {
		s.log.Error("eventbridge: deliver: get targets", zap.String("rule", rule.Name), zap.Error(err))
		return
	}
	if found {
		if err := json.Unmarshal([]byte(raw), &targets); err != nil {
			s.log.Warn("eventbridge: deliver: malformed targets", zap.String("rule", rule.Name), zap.Error(err))
			return
		}
	}
	for _, target := range targets {
		if err := s.deliverTarget(ctx, rule, target, event); err != nil {
			s.log.Warn("eventbridge: target delivery failed", zap.String("rule", rule.Name), zap.String("target", target.ID), zap.String("arn", target.ARN), zap.Error(err))
		}
	}
}

func (s *Service) deliverTarget(ctx context.Context, rule ebRule, target ebTarget, event map[string]any) error {
	payload, err := targetPayload(target, event)
	if err != nil {
		return err
	}
	arn := strings.ToLower(target.ARN)
	switch {
	case strings.Contains(arn, ":sqs:"):
		return s.invokeJSON(ctx, "AmazonSQS.SendMessage", map[string]any{
			"QueueUrl":    arnToQueueName(target.ARN),
			"MessageBody": string(payload),
		})
	case strings.Contains(arn, ":ecs:") && len(target.ECSParams) > 0:
		body := ecsRunTaskBody(rule, target)
		return s.invokeJSON(ctx, "AmazonEC2ContainerServiceV20141113.RunTask", body)
	default:
		return nil
	}
}

func targetPayload(target ebTarget, event map[string]any) ([]byte, error) {
	if target.Input != "" {
		return []byte(target.Input), nil
	}
	if target.InputPath != "" {
		return nil, fmt.Errorf("InputPath is not implemented")
	}
	return json.Marshal(event)
}

func ecsRunTaskBody(rule ebRule, target ebTarget) map[string]any {
	params := target.ECSParams
	body := map[string]any{
		"cluster":    target.ARN,
		"launchType": stringValue(params, "LaunchType"),
		"group":      "eventbridge:" + rule.Name,
	}
	if taskDef := stringValue(params, "TaskDefinitionArn"); taskDef != "" {
		body["taskDefinition"] = taskDef
	}
	if platform := stringValue(params, "PlatformVersion"); platform != "" {
		body["platformVersion"] = platform
	}
	if count := intValue(params, "TaskCount"); count > 0 {
		body["count"] = count
	}
	if netCfg, ok := params["NetworkConfiguration"].(map[string]any); ok {
		body["networkConfiguration"] = lowerEventBridgeKeys(netCfg)
	}
	return body
}

func lowerEventBridgeKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			if k == "" {
				out[k] = lowerEventBridgeKeys(value)
				continue
			}
			lk := strings.ToLower(k[:1]) + k[1:]
			if k == "AwsVpcConfiguration" {
				lk = "awsvpcConfiguration"
			}
			out[lk] = lowerEventBridgeKeys(value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = lowerEventBridgeKeys(value)
		}
		return out
	default:
		return v
	}
}

func (s *Service) invokeJSON(ctx context.Context, target string, body any) error {
	if s.router == nil {
		return fmt.Errorf("target router is not configured")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("X-Overcast-Region", s.region(ctx))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return fmt.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	return nil
}

func stringValue(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func intValue(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(v)
		return i
	default:
		return 0
	}
}

func arnToQueueName(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) >= 6 {
		return parts[5]
	}
	parts = strings.Split(arn, "/")
	return parts[len(parts)-1]
}
