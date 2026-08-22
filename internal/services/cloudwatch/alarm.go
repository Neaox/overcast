package cloudwatch

// Alarm evaluation.
//
// Real CloudWatch evaluates each alarm once per period, over the last
// EvaluationPeriods periods aligned to the period boundary, and moves the
// alarm to ALARM when DatapointsToAlarm of those periods breach the
// threshold ("M out of N"). Periods with no data are filled according to
// TreatMissingData. Alarm actions fire on a *transition*, never on a
// re-evaluation that lands on the same state.
//
// What this file deliberately does NOT do is quietly approximate the alarm
// shapes it cannot evaluate. Metric-math expressions, anomaly-detection
// bands, extended statistics and composite alarms are refused at
// configuration time with a 501 (see validateAlarmInput) rather than
// accepted and left sitting in INSUFFICIENT_DATA looking armed — the failure
// mode docs/plans/full-emulation-priority.md §2.1 exists to prevent.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/alarmaction"
)

// Alarm state values, as AWS spells them.
const (
	stateOK               = "OK"
	stateAlarm            = "ALARM"
	stateInsufficientData = "INSUFFICIENT_DATA"
)

// alarmEvaluationInterval is how often the single background evaluator wakes.
// A tick with no alarms configured costs one atomic load and nothing else
// (see Service.evaluateAlarmsOnce), and an alarm is only re-evaluated when
// its aligned evaluation window has actually advanced — so this interval
// controls how promptly a closed period is noticed, not how much work is
// done per second.
const alarmEvaluationInterval = 1 * time.Second

// alarmHistoryRetained caps how many history items are kept per alarm.
// Real CloudWatch retains alarm history for 14 days; Overcast bounds it by
// count instead, because the value here is "show the recent transitions",
// not historical analysis, and an unbounded namespace is a growth leak.
const alarmHistoryRetained = 100

// awsTimestampLayout is the millisecond-precision layout AWS uses inside
// StateReasonData and alarm notification bodies.
const awsTimestampLayout = alarmaction.TimestampLayout

// reasonDatapointLayout is the layout AWS uses for the datapoint timestamps
// embedded in a StateReason sentence, e.g. "[5.0 (03/08/26 12:00:00)]".
const reasonDatapointLayout = "02/01/06 15:04:05"

// supportedStatistics is the AWS Statistic enum. Anything outside it is
// either an extended statistic (refused separately, with a clearer message)
// or not a statistic at all.
var supportedStatistics = map[string]bool{
	"Average":     true,
	"Sum":         true,
	"SampleCount": true,
	"Minimum":     true,
	"Maximum":     true,
}

// comparisonPhrases maps each threshold comparison operator this evaluator
// implements to the phrase AWS uses for it in StateReason.
var comparisonPhrases = map[string]string{
	"GreaterThanThreshold":          "greater than",
	"GreaterThanOrEqualToThreshold": "greater than or equal to",
	"LessThanThreshold":             "less than",
	"LessThanOrEqualToThreshold":    "less than or equal to",
}

// anomalyComparisonOperators are the operators that only mean anything
// against an anomaly-detection band. They are refused rather than evaluated
// against a plain Threshold, which would be a different alarm than the one
// the caller asked for.
var anomalyComparisonOperators = map[string]bool{
	"LessThanLowerOrGreaterThanUpperThreshold": true,
	"LessThanLowerThreshold":                   true,
	"GreaterThanUpperThreshold":                true,
}

// treatMissingDataModes is the AWS enum, spelled exactly as AWS spells it
// (lowerCamelCase, unlike almost every other CloudWatch enum).
var treatMissingDataModes = map[string]bool{
	"missing":      true,
	"ignore":       true,
	"breaching":    true,
	"notBreaching": true,
}

// AlarmHistoryItem is one row of DescribeAlarmHistory.
type AlarmHistoryItem struct {
	AlarmName       string    `json:"AlarmName"`
	AlarmType       string    `json:"AlarmType"`
	Timestamp       time.Time `json:"Timestamp"`
	HistoryItemType string    `json:"HistoryItemType"`
	HistorySummary  string    `json:"HistorySummary"`
	HistoryData     string    `json:"HistoryData"`
}

// ─── History storage ──────────────────────────────────────────────

// alarmHistoryKey builds a key that sorts by alarm then by time, so one
// alarm's history is a contiguous, time-ordered prefix range.
func alarmHistoryKey(alarmName string, ts time.Time) string {
	return alarmName + "/" + fmt.Sprintf("%019d", ts.UTC().UnixNano())
}

// putAlarmHistory appends one history item and trims the alarm's history to
// alarmHistoryRetained entries.
func (s *cloudwatchStore) putAlarmHistory(ctx context.Context, item AlarmHistoryItem) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if err := s.store.Set(ctx, nsAlarmHistory, alarmHistoryKey(item.AlarmName, item.Timestamp), string(raw)); err != nil {
		return err
	}
	pairs, err := s.store.Scan(ctx, nsAlarmHistory, item.AlarmName+"/")
	if err != nil {
		return nil // trimming is best-effort; the item is already stored
	}
	for i := 0; i < len(pairs)-alarmHistoryRetained; i++ {
		_ = s.store.Delete(ctx, nsAlarmHistory, pairs[i].Key)
	}
	return nil
}

// listAlarmHistory returns one alarm's history items, oldest first. A
// malformed record is skipped rather than failing the whole read (AGENTS.md
// § malformed persisted state).
func (s *cloudwatchStore) listAlarmHistory(ctx context.Context, alarmName string) ([]AlarmHistoryItem, error) {
	prefix := ""
	if alarmName != "" {
		prefix = alarmName + "/"
	}
	pairs, err := s.store.Scan(ctx, nsAlarmHistory, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]AlarmHistoryItem, 0, len(pairs))
	for _, kv := range pairs {
		var item AlarmHistoryItem
		if err := json.Unmarshal([]byte(kv.Value), &item); err != nil {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

// deleteAlarmHistory removes every history item for one alarm, so a deleted
// alarm leaves nothing behind.
func (s *cloudwatchStore) deleteAlarmHistory(ctx context.Context, alarmName string) {
	pairs, err := s.store.Scan(ctx, nsAlarmHistory, alarmName+"/")
	if err != nil {
		return
	}
	for _, kv := range pairs {
		_ = s.store.Delete(ctx, nsAlarmHistory, kv.Key)
	}
}

// ─── Evaluation ───────────────────────────────────────────────────

// alignDown returns the start of the period containing t, aligned to the
// epoch exactly as CloudWatch aligns its own period boundaries.
func alignDown(t time.Time, periodSec int) time.Time {
	if periodSec <= 0 {
		periodSec = 60
	}
	secs := t.UTC().Unix()
	return time.Unix(secs-(((secs%int64(periodSec))+int64(periodSec))%int64(periodSec)), 0).UTC()
}

// evalMemo is what the evaluator remembers about one alarm between ticks, so
// a tick can decide "nothing to do" without touching the store.
type evalMemo struct {
	// windowEndUnix is the aligned end of the last evaluated range.
	windowEndUnix int64
	// configTS is the alarm's AlarmConfigurationUpdatedTimestamp at that
	// evaluation — a reconfigured alarm is always re-evaluated.
	configTS string
}

// metricWindowKey identifies one metric read within a single tick, so several
// alarms sharing a metric *and* a window share one store read.
//
// The window bounds are part of the key deliberately: two alarms on the same
// metric with different Period or EvaluationPeriods need different windows,
// and reusing one alarm's points for the other's window would evaluate it
// against the wrong data.
type metricWindowKey struct {
	namespace  string
	metricName string
	dimsKey    string
	startUnix  int64
	endUnix    int64
}

// runAlarmEvaluator is the single background evaluation loop. There is one
// per Service, never one per alarm, and Service.Stop drains it.
func (s *Service) runAlarmEvaluator() {
	defer s.wg.Done()
	defer s.alarmTicker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.alarmTicker.C:
			s.evaluateAlarmsOnce(context.Background())
		}
	}
}

// invalidateAlarmCount marks the cached alarm count unknown, so the next tick
// re-reads it. Called whenever an alarm is created or deleted.
func (s *Service) invalidateAlarmCount() { s.alarmCount.Store(-1) }

// evaluateAlarmsOnce runs one evaluation pass. Extracted from the ticker loop
// so tests drive it directly instead of racing a clock.
//
// Cost when idle is one atomic load: with a known-zero alarm count the pass
// returns before issuing any store call at all. When alarms do exist, each
// metric's datapoints are read once per tick and shared by every alarm
// watching that metric, and an alarm whose aligned evaluation window has not
// advanced since its last evaluation is skipped entirely.
func (s *Service) evaluateAlarmsOnce(ctx context.Context) {
	observed := s.alarmCount.Load()
	if observed == 0 {
		return
	}

	alarms, err := s.store.listAlarms(ctx)
	if err != nil {
		return
	}
	// Compare-and-swap rather than Store: a handler that created or deleted
	// an alarm while this scan was in flight has already set the count back
	// to -1, and overwriting that with a stale length would leave a new alarm
	// unnoticed forever once the count reached zero.
	s.alarmCount.CompareAndSwap(observed, int64(len(alarms)))
	if len(alarms) == 0 {
		return
	}

	now := s.clk.Now().UTC()
	pointCache := make(map[metricWindowKey][]*MetricDataPoint)
	live := make(map[string]struct{}, len(alarms))

	for _, alarm := range alarms {
		live[alarm.AlarmName] = struct{}{}
		if !alarm.evaluable() {
			continue
		}
		s.evaluateAlarm(ctx, alarm, now, pointCache)
	}
	s.pruneEvalMemo(live)
}

// pruneEvalMemo drops memos for alarms that no longer exist, bounding the map
// to live alarms even when one is removed from the store behind the API's
// back (DeleteAlarms clears its own memo directly).
func (s *Service) pruneEvalMemo(live map[string]struct{}) {
	s.evalMu.Lock()
	defer s.evalMu.Unlock()
	if len(s.evalMemo) <= len(live) {
		return
	}
	for name := range s.evalMemo {
		if _, ok := live[name]; !ok {
			delete(s.evalMemo, name)
		}
	}
}

// evaluable reports whether this alarm is one the evaluator can actually
// decide. Alarms that are not (metric math, anomaly detection, extended
// statistics) are refused at PutMetricAlarm time and so should never reach
// here; this is the belt-and-braces check for a record written by an older
// build, which must be left alone rather than silently mis-evaluated.
func (a *MetricAlarm) evaluable() bool {
	return a.Namespace != "" && a.MetricName != "" && supportedStatistics[a.Statistic] &&
		comparisonPhrases[a.ComparisonOperator] != "" && a.ExtendedStatistic == ""
}

// evaluateAlarm decides one alarm's state for the window ending at the last
// closed period boundary before now.
func (s *Service) evaluateAlarm(ctx context.Context, alarm *MetricAlarm, now time.Time, cache map[metricWindowKey][]*MetricDataPoint) {
	period := alarm.effectivePeriod()
	evaluationPeriods := alarm.effectiveEvaluationPeriods()

	// Evaluate only closed periods, as CloudWatch does: a period still
	// accumulating datapoints is not yet a datapoint.
	windowEnd := alignDown(now, period)
	windowStart := windowEnd.Add(-time.Duration(period*evaluationPeriods) * time.Second)
	if !windowEnd.After(windowStart) {
		return
	}

	// A manually-set state is held for one full evaluation range so that
	// SetAlarmState remains usable for local testing (see setAlarmStateHold).
	if hold, ok := parseStoredTime(alarm.ManualStateHoldUntil); ok && now.Before(hold) {
		return
	}

	s.evalMu.Lock()
	memo, seen := s.evalMemo[alarm.AlarmName]
	s.evalMu.Unlock()
	if seen && memo.windowEndUnix >= windowEnd.Unix() && memo.configTS == alarm.AlarmConfigurationUpdatedTimestamp {
		return
	}

	key := metricWindowKey{
		namespace:  alarm.Namespace,
		metricName: alarm.MetricName,
		dimsKey:    dimensionsKey(alarm.Dimensions),
		startUnix:  windowStart.Unix(),
		endUnix:    windowEnd.Unix(),
	}
	points, cached := cache[key]
	if !cached {
		loaded, err := s.store.mergedMetricDataPoints(ctx, alarm.Namespace, alarm.MetricName, alarm.Dimensions, windowStart, windowEnd)
		if err != nil {
			return
		}
		points = loaded
		cache[key] = points
	}

	verdict := evaluateWindow(alarm, points, windowStart, windowEnd, period, evaluationPeriods)

	s.evalMu.Lock()
	s.evalMemo[alarm.AlarmName] = evalMemo{windowEndUnix: windowEnd.Unix(), configTS: alarm.AlarmConfigurationUpdatedTimestamp}
	s.evalMu.Unlock()

	if verdict.retain {
		return
	}
	s.applyAlarmState(ctx, alarm, verdict.state, verdict.reason, verdict.reasonData, false)
}

// windowVerdict is one evaluation's outcome.
type windowVerdict struct {
	state      string
	reason     string
	reasonData string
	// retain is true when TreatMissingData=ignore and the range held no
	// data at all — AWS keeps the current state rather than moving.
	retain bool
}

// periodSample is one aligned period's aggregate, or its absence.
type periodSample struct {
	start       time.Time
	value       float64
	sampleCount float64
	present     bool
}

// evaluateWindow applies CloudWatch's documented evaluation rules to one
// aligned window: bucket the points by period, fill the gaps per
// TreatMissingData, then apply the "M out of N" rule.
func evaluateWindow(alarm *MetricAlarm, points []*MetricDataPoint, windowStart, windowEnd time.Time, period, evaluationPeriods int) windowVerdict {
	samples := bucketByPeriod(alarm.Statistic, alarm.Unit, points, windowStart, period, evaluationPeriods)

	presentCount := 0
	for _, sample := range samples {
		if sample.present {
			presentCount++
		}
	}

	mode := alarm.TreatMissingData
	if mode == "" {
		mode = "missing"
	}

	if presentCount == 0 {
		switch mode {
		case "breaching":
			return windowVerdict{
				state:      stateAlarm,
				reason:     missingDataReason(evaluationPeriods, "breaching"),
				reasonData: buildStateReasonData(alarm, samples, windowStart, windowEnd, period),
			}
		case "notBreaching":
			return windowVerdict{
				state:      stateOK,
				reason:     missingDataReason(evaluationPeriods, "notBreaching"),
				reasonData: buildStateReasonData(alarm, samples, windowStart, windowEnd, period),
			}
		case "ignore":
			return windowVerdict{retain: true}
		default: // "missing"
			return windowVerdict{
				state:      stateInsufficientData,
				reason:     insufficientDataReason(evaluationPeriods),
				reasonData: buildStateReasonData(alarm, samples, windowStart, windowEnd, period),
			}
		}
	}

	breaching := 0
	evaluated := 0
	for _, sample := range samples {
		if sample.present {
			evaluated++
			if compareThreshold(sample.value, alarm.Threshold, alarm.ComparisonOperator) {
				breaching++
			}
			continue
		}
		switch mode {
		case "breaching":
			evaluated++
			breaching++
		case "notBreaching":
			evaluated++
		default:
			// "missing" and "ignore" both drop the period from the count.
		}
	}

	datapointsToAlarm := alarm.effectiveDatapointsToAlarm()
	reasonData := buildStateReasonData(alarm, samples, windowStart, windowEnd, period)
	if breaching >= datapointsToAlarm {
		return windowVerdict{
			state:      stateAlarm,
			reason:     thresholdReason(alarm, samples, breaching, evaluated, datapointsToAlarm, true),
			reasonData: reasonData,
		}
	}
	return windowVerdict{
		state:      stateOK,
		reason:     thresholdReason(alarm, samples, evaluated-breaching, evaluated, datapointsToAlarm, false),
		reasonData: reasonData,
	}
}

// bucketByPeriod aggregates points into the evaluationPeriods aligned periods
// starting at windowStart, returning one entry per period in time order.
//
// unit is the alarm's Unit, and selects which datapoints count. AWS aggregates
// datapoints of different units separately, so an alarm that names a unit sees
// only that unit's data; an alarm that names none sees everything published
// for the metric.
func bucketByPeriod(statistic, unit string, points []*MetricDataPoint, windowStart time.Time, period, evaluationPeriods int) []periodSample {
	buckets := make([]*metricBucket, evaluationPeriods)
	startUnix := windowStart.Unix()
	for _, p := range points {
		if !unitMatches(unit, p.Unit) {
			continue
		}
		offset := (p.Timestamp.UTC().Unix() - startUnix) / int64(period)
		if offset < 0 || offset >= int64(evaluationPeriods) {
			continue
		}
		b := buckets[offset]
		if b == nil {
			b = &metricBucket{
				timestamp: windowStart.Add(time.Duration(offset) * time.Duration(period) * time.Second),
				min:       p.Minimum,
				max:       p.Maximum,
				unit:      p.Unit,
			}
			buckets[offset] = b
		}
		b.sample += p.SampleCount
		b.sum += p.Sum
		if !b.set || p.Minimum < b.min {
			b.min = p.Minimum
		}
		if !b.set || p.Maximum > b.max {
			b.max = p.Maximum
		}
		b.set = true
	}

	out := make([]periodSample, evaluationPeriods)
	for i, b := range buckets {
		start := windowStart.Add(time.Duration(i) * time.Duration(period) * time.Second)
		out[i] = periodSample{start: start}
		if b == nil || !b.set {
			continue
		}
		v, ok := metricStatValue(statistic, b)
		if !ok {
			continue
		}
		out[i].value = v
		out[i].sampleCount = b.sample
		out[i].present = true
	}
	return out
}

// unitMatches reports whether a datapoint published as pointUnit belongs to an
// alarm configured for alarmUnit.
//
// Divergence, deliberate: AWS records a datapoint published without a unit as
// "None", so on AWS an alarm that names Count never sees it and sits in
// INSUFFICIENT_DATA forever — the trap the PutMetricAlarm docs warn about when
// recommending you omit Unit. Overcast lets an unqualified datapoint satisfy
// any alarm unit instead, because locally-published metrics routinely omit the
// unit while the CDK construct that built the alarm supplied one. A datapoint
// that *does* name a unit is still held to it, which is what makes a
// mixed-unit metric evaluate separately per unit the way AWS does.
func unitMatches(alarmUnit, pointUnit string) bool {
	if alarmUnit == "" || pointUnit == "" {
		return true
	}
	return alarmUnit == pointUnit
}

// ─── Reason text ──────────────────────────────────────────────────

// thresholdReason renders the sentence AWS puts in StateReason for a
// threshold-driven transition.
func thresholdReason(alarm *MetricAlarm, samples []periodSample, matched, evaluated, datapointsToAlarm int, breach bool) string {
	phrase := comparisonPhrases[alarm.ComparisonOperator]
	if !breach {
		phrase = "not " + phrase
	}
	verb := "was"
	if matched != 1 {
		verb = "were"
	}
	transition := "OK -> ALARM"
	if !breach {
		transition = "ALARM -> OK"
	}
	return fmt.Sprintf("Threshold Crossed: %d out of the last %d datapoints %s %s %s the threshold (%s) (minimum %d datapoint%s for %s transition).",
		matched, evaluated, formatDatapointList(samples), verb, phrase,
		strconv.FormatFloat(alarm.Threshold, 'f', -1, 64),
		datapointsToAlarm, plural(datapointsToAlarm), transition)
}

// insufficientDataReason renders AWS's INSUFFICIENT_DATA sentence.
func insufficientDataReason(evaluationPeriods int) string {
	return fmt.Sprintf("Insufficient Data: %d datapoint%s %s unknown.",
		evaluationPeriods, plural(evaluationPeriods), wasWere(evaluationPeriods))
}

// missingDataReason renders the sentence for a range with no data at all that
// TreatMissingData nevertheless resolved to a definite state.
func missingDataReason(evaluationPeriods int, mode string) string {
	return fmt.Sprintf("Insufficient Data: %d datapoint%s %s unknown and are treated as %s (TreatMissingData: %s).",
		evaluationPeriods, plural(evaluationPeriods), wasWere(evaluationPeriods), mode, mode)
}

// formatDatapointList renders the "[5.0 (03/08/26 12:00:00)]" bracket AWS
// embeds in StateReason, listing only the periods that had data.
func formatDatapointList(samples []periodSample) string {
	parts := make([]string, 0, len(samples))
	for _, sample := range samples {
		if !sample.present {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%s)",
			strconv.FormatFloat(sample.value, 'f', -1, 64),
			sample.start.UTC().Format(reasonDatapointLayout)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// buildStateReasonData renders the StateReasonData document in the shape AWS
// publishes it, so an SDK consumer that parses it keeps working.
func buildStateReasonData(alarm *MetricAlarm, samples []periodSample, windowStart, windowEnd time.Time, period int) string {
	recent := make([]float64, 0, len(samples))
	evaluated := make([]map[string]any, 0, len(samples))
	for _, sample := range samples {
		if !sample.present {
			continue
		}
		recent = append(recent, sample.value)
		evaluated = append(evaluated, map[string]any{
			"timestamp":   sample.start.UTC().Format(awsTimestampLayout),
			"sampleCount": sample.sampleCount,
			"value":       sample.value,
		})
	}
	doc := map[string]any{
		"version":             "1.0",
		"queryDate":           windowEnd.UTC().Format(awsTimestampLayout),
		"startDate":           windowStart.UTC().Format(awsTimestampLayout),
		"statistic":           alarm.Statistic,
		"period":              period,
		"recentDatapoints":    recent,
		"threshold":           alarm.Threshold,
		"evaluatedDatapoints": evaluated,
	}
	if alarm.TreatMissingData != "" {
		doc["treatMissingData"] = alarm.TreatMissingData
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(raw)
}

// ─── Transitions ──────────────────────────────────────────────────

// applyAlarmState moves an alarm to state, persisting it, recording history
// and firing its actions — but only when the state actually changes, which is
// when real CloudWatch fires actions.
//
// manual marks a SetAlarmState call: the new state is additionally held
// against the evaluator for one evaluation range so it can be observed.
func (s *Service) applyAlarmState(ctx context.Context, alarm *MetricAlarm, state, reason, reasonData string, manual bool) {
	now := s.clk.Now().UTC()
	oldState := alarm.StateValue
	changed := oldState != state

	alarm.StateValue = state
	alarm.StateReason = reason
	alarm.StateReasonData = reasonData
	alarm.StateUpdatedTimestamp = now.Format(time.RFC3339)
	if changed {
		alarm.StateTransitionedTimestamp = alarm.StateUpdatedTimestamp
	}
	if manual {
		alarm.ManualStateHoldUntil = s.setAlarmStateHold(alarm, now).Format(time.RFC3339)
	} else {
		alarm.ManualStateHoldUntil = ""
	}

	if err := s.store.putAlarm(ctx, alarm); err != nil {
		log := s.log.WithRecorder(ctx)
		log.Error("persist alarm state", zap.String("alarm", alarm.AlarmName), zap.Error(err))
		return
	}
	if !changed {
		return
	}

	_ = s.store.putAlarmHistory(ctx, AlarmHistoryItem{
		AlarmName:       alarm.AlarmName,
		AlarmType:       "MetricAlarm",
		Timestamp:       now,
		HistoryItemType: "StateUpdate",
		HistorySummary:  fmt.Sprintf("Alarm updated from %s to %s", oldState, state),
		HistoryData:     historyData(oldState, state, reason, reasonData, now),
	})

	s.dispatchTransition(alarm, oldState, state, reason, reasonData, now)
}

// setAlarmStateHold returns the instant until which a manually-set state is
// protected from the evaluator.
//
// Divergence, deliberate: real AWS reverts a manually-set state at the next
// evaluation, which can be almost immediately. Overcast holds it for one full
// evaluation range (Period × EvaluationPeriods) so that a manual
// SetAlarmState is actually observable in DescribeAlarms and actually reaches
// its actions — issue #473's definition of done requires manual override to
// stay usable for local testing.
func (s *Service) setAlarmStateHold(alarm *MetricAlarm, now time.Time) time.Time {
	span := time.Duration(alarm.effectivePeriod()*alarm.effectiveEvaluationPeriods()) * time.Second
	return now.Add(span)
}

// historyData renders the HistoryData JSON document AWS attaches to a
// StateUpdate history item.
func historyData(oldState, newState, reason, reasonData string, now time.Time) string {
	doc := map[string]any{
		"version":      "1.0",
		"oldState":     map[string]any{"stateValue": oldState},
		"newState":     map[string]any{"stateValue": newState, "stateReason": reason, "stateReasonData": reasonData},
		"stateUpdated": now.UTC().Format(awsTimestampLayout),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(raw)
}

// dispatchTransition hands one transition to the alarm-action dispatcher.
//
// It runs on its own goroutine, tracked by the service's WaitGroup so Stop
// drains it, because delivery goes through the root router (an SNS fan-out,
// possibly onward to Lambda) and must not stall the evaluation loop. The
// goroutine count is bounded by transitions, not by alarms or by ticks: an
// alarm that keeps evaluating to the same state produces none.
func (s *Service) dispatchTransition(alarm *MetricAlarm, oldState, newState, reason, reasonData string, now time.Time) {
	if s.actions == nil {
		return
	}
	transition := alarmaction.Transition{
		AlarmName:          alarm.AlarmName,
		AlarmARN:           alarm.AlarmArn,
		Region:             regionFromAlarmARN(alarm.AlarmArn, s.cfg.Region),
		AccountID:          s.cfg.AccountID,
		OldState:           oldState,
		NewState:           newState,
		Reason:             reason,
		ReasonData:         reasonData,
		Timestamp:          now,
		Actions:            alarm.actionsFor(newState),
		ActionsEnabled:     alarm.ActionsEnabled,
		AlarmDescription:   alarm.AlarmDescription,
		Namespace:          alarm.Namespace,
		MetricName:         alarm.MetricName,
		Statistic:          alarm.Statistic,
		Period:             alarm.effectivePeriod(),
		EvaluationPeriods:  alarm.effectiveEvaluationPeriods(),
		Threshold:          alarm.Threshold,
		ComparisonOperator: alarm.ComparisonOperator,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		outcomes := s.actions.Dispatch(context.Background(), transition)
		for _, outcome := range outcomes {
			s.recordActionHistory(transition, outcome)
		}
	}()
}

// recordActionHistory writes the Action history item AWS records for each
// action an alarm invoked — including, unlike AWS, the ones Overcast could
// not deliver, so an undelivered action is visible rather than invisible.
func (s *Service) recordActionHistory(t alarmaction.Transition, outcome alarmaction.Outcome) {
	summary := fmt.Sprintf("Successfully executed action %s", outcome.ARN)
	switch {
	case outcome.Unsupported:
		summary = fmt.Sprintf("Action %s was NOT executed: this target type is not emulated by Overcast", outcome.ARN)
	case outcome.Err != nil:
		summary = fmt.Sprintf("Failed to execute action %s: %v", outcome.ARN, outcome.Err)
	}
	_ = s.store.putAlarmHistory(context.Background(), AlarmHistoryItem{
		AlarmName:       t.AlarmName,
		AlarmType:       "MetricAlarm",
		Timestamp:       s.clk.Now().UTC(),
		HistoryItemType: "Action",
		HistorySummary:  summary,
	})
}

// actionsFor returns the action ARNs configured for one state.
func (a *MetricAlarm) actionsFor(state string) []string {
	switch state {
	case stateAlarm:
		return a.AlarmActions
	case stateOK:
		return a.OKActions
	case stateInsufficientData:
		return a.InsufficientDataActions
	default:
		return nil
	}
}

func (a *MetricAlarm) effectivePeriod() int {
	if a.Period <= 0 {
		return 60
	}
	return a.Period
}

func (a *MetricAlarm) effectiveEvaluationPeriods() int {
	if a.EvaluationPeriods <= 0 {
		return 1
	}
	return a.EvaluationPeriods
}

// effectiveDatapointsToAlarm defaults to EvaluationPeriods, exactly as AWS
// does when DatapointsToAlarm is omitted.
func (a *MetricAlarm) effectiveDatapointsToAlarm() int {
	if a.DatapointsToAlarm <= 0 {
		return a.effectiveEvaluationPeriods()
	}
	return a.DatapointsToAlarm
}

// parseStoredTime parses one of the RFC3339 timestamps this package stores.
func parseStoredTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// regionFromAlarmARN reads the region out of an alarm ARN, so an alarm
// created against a non-default region reports and dispatches in that region.
func regionFromAlarmARN(arn, fallback string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) == 6 && parts[3] != "" {
		return parts[3]
	}
	return fallback
}
