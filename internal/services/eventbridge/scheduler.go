package eventbridge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (s *Service) startEngine() {
	s.startOnce.Do(func() {
		// Register the ticker before the goroutine starts. Mock clocks only fire
		// timers that already exist when test time is advanced.
		ticker := s.clk.Ticker(engineTick)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer ticker.Stop()
			for {
				select {
				case <-s.stopCh:
					return
				case <-ticker.C:
					s.tickSchedules()
				}
			}
		}()
	})
}

func (s *Service) tickSchedules() {
	ctx := context.Background()
	now := s.clk.Now()
	pairs, err := s.store.Scan(ctx, nsRules, "")
	if err != nil {
		s.log.Error("eventbridge: tick: scan rules", zap.Error(err))
		return
	}
	for _, kv := range pairs {
		var rule ebRule
		if json.Unmarshal([]byte(kv.Value), &rule) != nil || rule.ScheduleExpr == "" || rule.State == "DISABLED" {
			continue
		}
		next := s.getNextFire(ctx, kv.Key)
		if next.IsZero() {
			last := s.getLastFire(ctx, kv.Key)
			var err error
			next, err = nextRuleFire(rule.ScheduleExpr, last, now)
			if err != nil {
				s.log.Warn("eventbridge: tick: invalid schedule expression", zap.String("rule", rule.Name), zap.String("expr", rule.ScheduleExpr), zap.Error(err))
				continue
			}
			s.storeTime(ctx, nsNextFire, kv.Key, next)
		}
		if next.IsZero() || now.Before(next) {
			continue
		}
		eventID := uuid.NewString()
		event := scheduledEvent(rule, eventID, now, s.cfg.AccountID, regionFromRuleKey(kv.Key, s.cfg.Region))
		s.deliverTargets(ctx, rule, event)
		s.setLastFire(ctx, kv.Key, now)
		s.setNextFire(ctx, kv.Key, rule.ScheduleExpr, now, now)
	}
}

func scheduledEvent(rule ebRule, eventID string, now time.Time, accountID, region string) map[string]any {
	return map[string]any{
		"version":     "0",
		"id":          eventID,
		"detail-type": "Scheduled Event",
		"source":      "aws.events",
		"account":     accountID,
		"time":        now.UTC().Format(time.RFC3339),
		"region":      region,
		"resources":   []any{rule.ARN},
		"detail":      map[string]any{},
	}
}

func (s *Service) getLastFire(ctx context.Context, key string) time.Time {
	return s.getStoredTime(ctx, nsLastFire, key, "last fire")
}

func (s *Service) getNextFire(ctx context.Context, key string) time.Time {
	return s.getStoredTime(ctx, nsNextFire, key, "next fire")
}

func (s *Service) getStoredTime(ctx context.Context, namespace, key, label string) time.Time {
	raw, found, err := s.store.Get(ctx, namespace, key)
	if err != nil {
		s.log.Error("eventbridge: get "+label, zap.String("ruleKey", key), zap.Error(err))
		return time.Time{}
	}
	if !found {
		return time.Time{}
	}
	var t time.Time
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		s.log.Warn("eventbridge: malformed "+label, zap.String("ruleKey", key), zap.Error(err))
		return time.Time{}
	}
	return t
}

func (s *Service) setNextFire(ctx context.Context, key, expr string, lastFire, now time.Time) {
	next, err := nextRuleFire(expr, lastFire, now)
	if err != nil {
		s.log.Warn("eventbridge: calculate next fire", zap.String("ruleKey", key), zap.String("expr", expr), zap.Error(err))
		return
	}
	s.storeTime(ctx, nsNextFire, key, next)
}

func (s *Service) storeTime(ctx context.Context, namespace, key string, t time.Time) {
	if t.IsZero() {
		return
	}
	raw, err := json.Marshal(t)
	if err != nil {
		s.log.Error("eventbridge: marshal schedule time", zap.String("ruleKey", key), zap.Error(err))
		return
	}
	if err := s.store.Set(ctx, namespace, key, string(raw)); err != nil {
		s.log.Error("eventbridge: store schedule time", zap.String("ruleKey", key), zap.Error(err))
	}
}

func (s *Service) setLastFire(ctx context.Context, key string, t time.Time) {
	s.storeTime(ctx, nsLastFire, key, t)
}
