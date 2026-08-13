package eventbridge

// Delivery-outcome visibility.
//
// The web UI's bus view needs to answer "did this event reach that function,
// and if not, why" — the other half of the bug issue #467 fixes, since the
// page could previously show a target that would never fire with no
// indication. Outcomes are kept in a fixed-size in-process ring rather than in
// state.Store on purpose: PutEvents fan-out is a hot path, this is diagnostic
// data with no durability requirement, and a store write per target per event
// would put I/O on that path for nothing.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// deliveryLogSize is how many recent target deliveries are retained, across
// all buses and rules. Sized to cover a debugging session's worth of events
// without unbounded growth in a long-running emulator.
const deliveryLogSize = 500

// Delivery outcomes, in the vocabulary the UI renders.
const (
	outcomeDelivered = "delivered"
	outcomeRetried   = "retried"
	outcomeDLQ       = "dlq"
	outcomeDropped   = "dropped"
)

// deliveryRecord is one target delivery attempt sequence.
type deliveryRecord struct {
	Region     string    `json:"Region"`
	Bus        string    `json:"Bus"`
	Rule       string    `json:"Rule"`
	TargetID   string    `json:"TargetId"`
	TargetARN  string    `json:"TargetArn"`
	TargetType string    `json:"TargetType"`
	Outcome    string    `json:"Outcome"`
	Attempts   int       `json:"Attempts"`
	EventID    string    `json:"EventId"`
	Error      string    `json:"Error,omitempty"`
	Time       time.Time `json:"Time"`
}

// deliveryLog is a fixed-capacity ring of delivery outcomes, newest last.
type deliveryLog struct {
	mu      sync.Mutex
	records []deliveryRecord
	next    int
	filled  bool
}

func newDeliveryLog() *deliveryLog {
	return &deliveryLog{}
}

// add records one outcome, overwriting the oldest entry when full.
func (l *deliveryLog) add(rec deliveryRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.records == nil {
		l.records = make([]deliveryRecord, deliveryLogSize)
	}
	l.records[l.next] = rec
	l.next = (l.next + 1) % len(l.records)
	if l.next == 0 {
		l.filled = true
	}
}

// snapshot returns the retained records newest-first, optionally filtered by
// region, bus and rule. An empty filter value matches everything.
func (l *deliveryLog) snapshot(region, bus, rule string) []deliveryRecord {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	count := l.next
	if l.filled {
		count = len(l.records)
	}
	out := make([]deliveryRecord, 0, count)
	for i := 0; i < count; i++ {
		// Walk backwards from the most recently written slot.
		idx := (l.next - 1 - i + len(l.records)) % len(l.records)
		rec := l.records[idx]
		if region != "" && rec.Region != region {
			continue
		}
		if bus != "" && rec.Bus != bus {
			continue
		}
		if rule != "" && rec.Rule != rule {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// recordDelivery appends an outcome to the service's delivery log.
func (s *Service) recordDelivery(rec deliveryRecord) {
	if s.deliveries == nil {
		return
	}
	rec.Time = s.clk.Now().UTC()
	s.deliveries.add(rec)
}

// adminListDeliveries serves GET /_overcast/eventbridge/deliveries, the web
// console's per-target delivery-outcome feed. Query parameters bus, rule and
// region narrow the result; all are optional.
//
// This is an emulator-only console endpoint, not an AWS API surface — it lives
// under /_overcast/ alongside the other console feeds for the same reason.
func (s *Service) adminListDeliveries(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	if region == "" {
		region = s.region(r.Context())
	}
	records := s.deliveries.snapshot(region, r.URL.Query().Get("bus"), r.URL.Query().Get("rule"))
	if records == nil {
		records = []deliveryRecord{}
	}
	if limit := serviceutil.ParseIntDefault(r.URL.Query().Get("limit"), 0); limit > 0 && limit < len(records) {
		records = records[:limit]
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Deliveries": records})
}

// adminListRuleTargets serves GET /_overcast/eventbridge/rule-targets, which
// pairs every rule on a bus with its targets and each target's resolved type,
// so the console can show what a rule would actually fan out to.
func (s *Service) adminListRuleTargets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bus := r.URL.Query().Get("bus")
	if bus == "" {
		bus = "default"
	}
	rules, err := s.loadRules(ctx, bus)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	type targetView struct {
		ID         string `json:"Id"`
		ARN        string `json:"Arn"`
		TargetType string `json:"TargetType"`
	}
	type ruleView struct {
		Name    string       `json:"Name"`
		State   string       `json:"State"`
		Targets []targetView `json:"Targets"`
	}
	views := make([]ruleView, 0, len(rules))
	for _, rule := range rules {
		targets, err := s.loadTargets(ctx, rule.EventBusName, rule.Name)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}
		view := ruleView{Name: rule.Name, State: rule.State, Targets: make([]targetView, 0, len(targets))}
		for _, target := range targets {
			view.Targets = append(view.Targets, targetView{
				ID:         target.ID,
				ARN:        target.ARN,
				TargetType: target.displayType(),
			})
		}
		views = append(views, view)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Rules": views})
}

// registerAdminRoutes mounts the console-only EventBridge endpoints.
func (s *Service) registerAdminRoutes(r chi.Router) {
	r.Get("/_overcast/eventbridge/deliveries", s.adminListDeliveries)
	r.Get("/_overcast/eventbridge/rule-targets", s.adminListRuleTargets)
}

// loadTargets reads a rule's stored targets. A malformed record yields no
// targets rather than an error: one bad payload must not break the rule list.
func (s *Service) loadTargets(ctx context.Context, bus, rule string) ([]ebTarget, error) {
	log := s.log.WithRecorder(ctx)

	key := serviceutil.RegionKey(s.region(ctx), bus+"/"+rule)
	raw, found, err := s.store.Get(ctx, nsTargets, key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	var targets []ebTarget
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		log.Warn("eventbridge: malformed targets record", zap.String("rule", rule), zap.Error(err))
		return nil, nil
	}
	return targets, nil
}

// loadRules reads every rule on a bus in the request's region.
func (s *Service) loadRules(ctx context.Context, bus string) ([]ebRule, error) {
	pairs, err := s.store.Scan(ctx, nsRules, serviceutil.RegionKey(s.region(ctx), bus+"/"))
	if err != nil {
		return nil, err
	}
	rules := make([]ebRule, 0, len(pairs))
	for _, kv := range pairs {
		var rule ebRule
		if json.Unmarshal([]byte(kv.Value), &rule) != nil {
			continue
		}
		rules = append(rules, rule)
	}
	return rules, nil
}
