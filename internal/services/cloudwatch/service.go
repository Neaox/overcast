// Package cloudwatch provides a basic emulation of Amazon CloudWatch (metrics + alarms).
//
// Implemented operations: PutMetricAlarm, DescribeAlarms, DeleteAlarms,
// PutMetricData, GetMetricStatistics, GetMetricData, ListMetrics,
// ListTagsForResource, TagResource, UntagResource.
//
// Enough for CDK stacks that reference AWS::CloudWatch::Alarm resources.
package cloudwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "cloudwatch"

// ─── Types ────────────────────────────────────────────────────

// MetricAlarm represents a CloudWatch alarm.
type MetricAlarm struct {
	AlarmName                          string   `json:"AlarmName"`
	AlarmArn                           string   `json:"AlarmArn"`
	MetricName                         string   `json:"MetricName,omitempty"`
	Namespace                          string   `json:"Namespace,omitempty"`
	Statistic                          string   `json:"Statistic,omitempty"`
	Period                             int      `json:"Period,omitempty"`
	EvaluationPeriods                  int      `json:"EvaluationPeriods,omitempty"`
	Threshold                          float64  `json:"Threshold,omitempty"`
	ComparisonOperator                 string   `json:"ComparisonOperator,omitempty"`
	ActionsEnabled                     bool     `json:"ActionsEnabled"`
	AlarmActions                       []string `json:"AlarmActions,omitempty"`
	OKActions                          []string `json:"OKActions,omitempty"`
	StateValue                         string   `json:"StateValue"`
	StateReason                        string   `json:"StateReason"`
	AlarmDescription                   string   `json:"AlarmDescription,omitempty"`
	TreatMissingData                   string   `json:"TreatMissingData,omitempty"`
	StateUpdatedTimestamp              string   `json:"StateUpdatedTimestamp,omitempty"`
	AlarmConfigurationUpdatedTimestamp string   `json:"AlarmConfigurationUpdatedTimestamp,omitempty"`
}

// Metric represents a CloudWatch metric entry.
type Metric struct {
	Namespace  string      `json:"Namespace"`
	MetricName string      `json:"MetricName"`
	Dimensions []Dimension `json:"Dimensions,omitempty"`
}

// MetricDataPoint stores a single published datapoint.
type MetricDataPoint struct {
	Namespace   string      `json:"Namespace"`
	MetricName  string      `json:"MetricName"`
	Dimensions  []Dimension `json:"Dimensions,omitempty"`
	Timestamp   time.Time   `json:"Timestamp"`
	Unit        string      `json:"Unit,omitempty"`
	SampleCount float64     `json:"SampleCount"`
	Sum         float64     `json:"Sum"`
	Minimum     float64     `json:"Minimum"`
	Maximum     float64     `json:"Maximum"`
}

// Dimension is a name/value pair for metric filtering.
type Dimension struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// ─── Store ────────────────────────────────────────────────────

type cloudwatchStore struct {
	store state.Store
	clk   clock.Clock
}

func newCloudwatchStore(s state.Store, clk clock.Clock) *cloudwatchStore {
	return &cloudwatchStore{store: s, clk: clk}
}

const (
	nsAlarms     = "cloudwatch:alarms"
	nsMetrics    = "cloudwatch:metrics"
	nsMetricData = "cloudwatch:metricdata"
	nsTags       = "cloudwatch:tags"

	// memoryMetricDataRetention is the retention window applied to
	// PutMetricData datapoints in every backend mode (storage-plan.md 3.4).
	// It used to be enforced only in memory mode (backendMode() gate,
	// removed); real CloudWatch retains metric data for up to 15 months at
	// declining resolution, but Overcast only needs to bound growth for
	// local dev/test workflows, not serve historical analysis, so a single
	// flat window is enough.
	memoryMetricDataRetention = 1 * time.Hour

	// metricDataSweepInterval is how often Service.runMetricDataSweeper
	// scans the entire cloudwatch:metricdata namespace and deletes points
	// older than memoryMetricDataRetention, regardless of backend mode.
	// Deliberately well under the retention window so hybrid/persistent
	// backends — which no longer benefit from memory mode's small working
	// set — don't accumulate more than about one interval's worth of
	// overdue rows on disk between sweeps.
	metricDataSweepInterval = 5 * time.Minute

	// metricDataScanPageSize bounds each internal ScanPage fetch inside
	// listMetricDataPoints (storage-plan.md A5). Keys within one metric's
	// prefix are already time-ordered (see the key-format comment on
	// putMetricDataPoint), so a range read only needs to walk pages until it
	// passes the requested window's end — it does not need to load the rest
	// of a large, mostly-out-of-window history in one shot. This value is
	// just the per-round-trip chunk size, not a cap on how many points a
	// window can contain.
	metricDataScanPageSize = 256
)

func canonicalizeDimensions(in []Dimension) []Dimension {
	out := make([]Dimension, 0, len(in))
	for _, d := range in {
		if d.Name == "" {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Value < out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func dimensionsKey(dimensions []Dimension) string {
	if len(dimensions) == 0 {
		return "-"
	}
	var b strings.Builder
	for i, d := range canonicalizeDimensions(dimensions) {
		if i > 0 {
			b.WriteString("|")
		}
		b.WriteString(d.Name)
		b.WriteString("=")
		b.WriteString(d.Value)
	}
	return b.String()
}

func (s *cloudwatchStore) putAlarm(ctx context.Context, a *MetricAlarm) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsAlarms, a.AlarmName, string(raw))
}

func (s *cloudwatchStore) getAlarm(ctx context.Context, name string) (*MetricAlarm, bool) {
	raw, found, err := s.store.Get(ctx, nsAlarms, name)
	if err != nil || !found {
		return nil, false
	}
	var a MetricAlarm
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, false
	}
	return &a, true
}

func (s *cloudwatchStore) listAlarms(ctx context.Context) ([]*MetricAlarm, error) {
	pairs, err := s.store.Scan(ctx, nsAlarms, "")
	if err != nil {
		return nil, err
	}
	out := make([]*MetricAlarm, 0, len(pairs))
	for _, kv := range pairs {
		var a MetricAlarm
		if err := json.Unmarshal([]byte(kv.Value), &a); err != nil {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}

func (s *cloudwatchStore) deleteAlarm(ctx context.Context, name string) error {
	return s.store.Delete(ctx, nsAlarms, name)
}

func (s *cloudwatchStore) putMetric(ctx context.Context, namespace, metricName string, dimensions []Dimension) error {
	metricDims := canonicalizeDimensions(dimensions)
	key := namespace + "/" + metricName + "/" + dimensionsKey(metricDims)
	m := Metric{Namespace: namespace, MetricName: metricName, Dimensions: metricDims}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsMetrics, key, string(raw))
}

// metricDataPrefix builds the cloudwatch:metricdata key prefix shared by
// every datapoint for one namespace/metric/dimension-set combination. Keys
// are this prefix plus the point's UnixNano timestamp
// (metricDataKeyForNanos), which for any date in roughly 2001-2286 is a
// fixed-width 19-digit decimal string — so within one prefix, key order is
// time order. listMetricDataPoints (A5) relies on that to turn a full
// prefix scan into a bounded key-range read.
func metricDataPrefix(namespace, metricName, dimsKey string) string {
	return namespace + "/" + metricName + "/" + dimsKey + "/"
}

// metricDataKeyForNanos appends a UnixNano timestamp to a metricDataPrefix
// result to build a full cloudwatch:metricdata key.
func metricDataKeyForNanos(prefix string, nanos int64) string {
	return prefix + strconv.FormatInt(nanos, 10)
}

// metricDataKeySuffixNanos extracts the trailing UnixNano timestamp from a
// cloudwatch:metricdata key without needing to know the namespace/metric/
// dimension prefix in advance — used by sweepMetricDataOnce, which scans
// across every metric's keys at once, and as the range-read boundary check
// in listMetricDataPoints. Returns false for a key that doesn't end in a
// parseable integer suffix (defensively: a malformed/foreign key should be
// skipped, not crash the scan — see AGENTS.md on isolating malformed
// persisted records).
func metricDataKeySuffixNanos(key string) (int64, bool) {
	idx := strings.LastIndex(key, "/")
	if idx < 0 || idx == len(key)-1 {
		return 0, false
	}
	n, err := strconv.ParseInt(key[idx+1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (s *cloudwatchStore) putMetricDataPoint(ctx context.Context, dp *MetricDataPoint) error {
	metricDims := canonicalizeDimensions(dp.Dimensions)
	dp.Dimensions = metricDims
	key := metricDataKeyForNanos(metricDataPrefix(dp.Namespace, dp.MetricName, dimensionsKey(metricDims)), dp.Timestamp.UnixNano())
	raw, err := json.Marshal(dp)
	if err != nil {
		return err
	}
	if err := s.store.Set(ctx, nsMetricData, key, string(raw)); err != nil {
		return err
	}
	// Deliberately NO inline prune here. An earlier version pruned this
	// metric's expired points on every put, which on the hybrid/persistent
	// backends meant a TierCached SQLite Scan + JSON decode of every retained
	// point per PutMetricData — O(points-in-window) per write, quadratic over
	// a burst to one metric (measured: see
	// BenchmarkCloudWatch_PutMetricDataHybrid_*Retained in
	// metric_burst_bench_test.go). Retention doesn't need it: reads filter
	// and drop expired points themselves (listMetricDataPoints), and the
	// periodic sweep (Service.runMetricDataSweeper / sweepMetricDataOnce)
	// guarantees physical cleanup within one sweep interval, so the only
	// cost of not pruning inline is at most one interval's worth of expired
	// rows per hot metric — invisible to readers.
	return nil
}

// listMetricDataPoints returns the datapoints for one namespace/metric/
// dimension-set combination whose timestamp falls in [startTime, endTime]
// (inclusive both ends — matching aggregateMetricBuckets' own
// ts.Before(startTime) || ts.After(endTime) filter, which every caller
// applies to this method's result immediately afterward).
//
// storage-plan.md A5: rather than Scan-ing the metric's entire retained
// history and JSON-decoding every point just to throw away everything
// outside the window, this walks ScanPage in metricDataScanPageSize chunks
// starting just before the window and stops the moment a key's timestamp
// suffix exceeds endTime — since keys within a prefix are time-ordered, no
// point past that can be in range, so nothing after it needs to be fetched
// or decoded. Namespace/metric/dimension filtering is still exact (it's the
// ScanPage prefix); only the time bound is new.
//
// The read-path retention behavior is unchanged: any in-window point older
// than the retention cutoff is deleted here rather than returned (see
// metricDataRetentionCutoff's doc comment) — it just now only ever inspects
// points inside the caller's requested window instead of the whole metric,
// which is fine because the periodic sweep (sweepMetricDataOnce) is the
// backstop that guarantees full-history cleanup regardless of what any
// particular read happens to touch.
func (s *cloudwatchStore) listMetricDataPoints(ctx context.Context, namespace, metricName string, dimensions []Dimension, startTime, endTime time.Time) ([]*MetricDataPoint, error) {
	metricDims := canonicalizeDimensions(dimensions)
	prefix := metricDataPrefix(namespace, metricName, dimensionsKey(metricDims))
	startNanos := startTime.UnixNano()
	endNanos := endTime.UnixNano()

	cutoff := s.metricDataRetentionCutoff()
	out := make([]*MetricDataPoint, 0)

	// Exclusive predecessor of the inclusive lower bound: ScanPage's
	// startAfter is strictly-after semantics, so to include the point at
	// exactly startNanos we must pass the key just below it.
	startAfter := metricDataKeyForNanos(prefix, startNanos-1)

	for {
		page, next, err := s.store.ScanPage(ctx, nsMetricData, prefix, startAfter, metricDataScanPageSize)
		if err != nil {
			return nil, err
		}
		for _, kv := range page {
			nanos, ok := metricDataKeySuffixNanos(kv.Key)
			if !ok {
				continue
			}
			if nanos > endNanos {
				// Keys are time-ordered within this prefix — everything from
				// here on is past the window, so stop without decoding it.
				return out, nil
			}
			var dp MetricDataPoint
			if err := json.Unmarshal([]byte(kv.Value), &dp); err != nil {
				continue
			}
			if dp.Timestamp.UTC().Before(cutoff) {
				_ = s.store.Delete(ctx, nsMetricData, kv.Key)
				continue
			}
			out = append(out, &dp)
		}
		if next == "" {
			return out, nil
		}
		startAfter = next
	}
}

// metricDataRetentionCutoff returns the time before which cloudwatch:metricdata
// points are eligible for deletion. Applied universally across every backend
// mode (storage-plan.md 3.4) — there used to be a backendMode() gate here that
// disabled retention outside memory mode; hybrid/persistent backends grew
// unboundedly on disk as a result. Both the read-path filter
// (listMetricDataPoints, which drops and deletes expired points as it reads)
// and the periodic background sweep (sweepMetricDataOnce, driven by
// Service.runMetricDataSweeper) use this cutoff. The write path deliberately
// does not — see putMetricDataPoint.
func (s *cloudwatchStore) metricDataRetentionCutoff() time.Time {
	return s.clk.Now().UTC().Add(-memoryMetricDataRetention)
}

// sweepMetricDataOnce scans every point in the cloudwatch:metricdata
// namespace — across every namespace/metric/dimension combination — and
// deletes any older than the retention cutoff. This full scan is intended
// for periodic background use only (Service.runMetricDataSweeper): together
// with listMetricDataPoints' read-path filtering it is the entire retention
// mechanism, guaranteeing metrics that stop being written or read still get
// physically pruned, in every backend mode including hybrid/persistent
// (storage-plan.md 3.4). Extracted as a directly-callable method, separate
// from the ticker-loop wrapper, so tests can trigger one sweep
// deterministically without waiting on a real or mocked ticker tick.
func (s *cloudwatchStore) sweepMetricDataOnce(ctx context.Context) {
	cutoffNanos := s.metricDataRetentionCutoff().UnixNano()
	pairs, err := s.store.Scan(ctx, nsMetricData, "")
	if err != nil {
		return
	}
	for _, kv := range pairs {
		// storage-plan.md A5: the timestamp lives in the key suffix, so the
		// sweep never needs to JSON-decode a point's value just to check its
		// age — same isolation-of-malformed-records posture as before: a key
		// whose suffix doesn't parse is skipped rather than deleted or
		// treated as a scan failure.
		nanos, ok := metricDataKeySuffixNanos(kv.Key)
		if !ok {
			continue
		}
		if nanos < cutoffNanos {
			_ = s.store.Delete(ctx, nsMetricData, kv.Key)
		}
	}
}

func (s *cloudwatchStore) listMetrics(ctx context.Context, namespace string) ([]*Metric, error) {
	pairs, err := s.store.Scan(ctx, nsMetrics, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Metric, 0, len(pairs))
	for _, kv := range pairs {
		var m Metric
		if err := json.Unmarshal([]byte(kv.Value), &m); err != nil {
			continue
		}
		if namespace != "" && m.Namespace != namespace {
			continue
		}
		out = append(out, &m)
	}
	return out, nil
}

func (s *cloudwatchStore) setTags(ctx context.Context, arn string, tags map[string]string) error {
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsTags, arn, string(raw))
}

func (s *cloudwatchStore) getTags(ctx context.Context, arn string) (map[string]string, error) {
	raw, found, err := s.store.Get(ctx, nsTags, arn)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]string{}, nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service and router.QueryDispatcher for CloudWatch.
type Service struct {
	log   *serviceutil.ServiceLogger
	store *cloudwatchStore
	cfg   *config.Config
	clk   clock.Clock
	ops   map[string]http.HandlerFunc

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// New returns a configured CloudWatch Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		log:    serviceutil.NewServiceLogger(logger, serviceName),
		store:  newCloudwatchStore(st, clk),
		cfg:    cfg,
		clk:    clk,
		stopCh: make(chan struct{}),
	}
	s.ops = map[string]http.HandlerFunc{
		"PutMetricAlarm":          s.putMetricAlarm,
		"DescribeAlarms":          s.describeAlarms,
		"DeleteAlarms":            s.deleteAlarms,
		"PutMetricData":           s.putMetricData,
		"GetMetricStatistics":     s.getMetricStatistics,
		"GetMetricData":           s.getMetricData,
		"ListMetrics":             s.listMetrics,
		"ListTagsForResource":     s.listTagsForResource,
		"TagResource":             s.tagResource,
		"UntagResource":           s.untagResource,
		"DescribeAlarmsForMetric": s.describeAlarmsForMetric,
		"SetAlarmState":           s.setAlarmState,
	}
	s.wg.Add(1)
	go s.runAlarmEvaluator()
	s.startMetricDataSweeper()
	return s
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}

// cloudwatchJSONTargetPrefix is the X-Amz-Target prefix the JS/TS AWS SDK
// uses for CloudWatch metrics calls sent over the JSON protocol
// (Content-Type: application/x-amz-json-1.0). CloudWatch is the
// historical precedent for AWS switching a service's wire protocol
// without notice (see docs/plans/level2-codegen.md); this constant only
// remains as Dispatch's defensive fallback below, not its primary path.
const cloudwatchJSONTargetPrefix = "GraniteServiceVersion20100801."

// TargetPrefix satisfies router.TargetDispatcher for AWS SDK JSON protocol.
func (s *Service) TargetPrefix() string { return cloudwatchJSONTargetPrefix }

// Dispatch satisfies router.TargetDispatcher.
//
// Like every other JSON-tier service, the operation name comes from
// codec.FromContext — populated by middleware.Protocol identifying the
// X-Amz-Target header before Dispatch ever runs. Dispatch no longer parses
// X-Amz-Target itself; the header-trimming fallback below only covers
// callers that invoke Dispatch without going through the router's
// middleware chain (e.g. a unit test constructing a bare request).
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	_, action := codec.FromContext(r.Context())
	if action == "" {
		target := r.Header.Get("X-Amz-Target")
		trimmed := strings.TrimPrefix(target, cloudwatchJSONTargetPrefix)
		if trimmed == target {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "UnknownOperationException",
				Message:    "Unknown target: " + target,
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		action = trimmed
	}

	s.dispatchJSON(w, r, action)
}

func (s *Service) Stop(ctx context.Context) {
	s.stopOnce.Do(func() { close(s.stopCh) })
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return
	case <-done:
		return
	}
}

// OwnsVersion satisfies router.QueryVersionOwner.
func (s *Service) OwnsVersion(version string) bool {
	return version == awsapi.VersionCloudWatch
}

// DispatchQuery satisfies router.QueryDispatcher.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	if fn, ok := s.ops[action]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedQueryXML(w, r)
}

func (s *Service) dispatchJSON(w http.ResponseWriter, r *http.Request, action string) {
	switch action {
	case "ListMetrics":
		s.listMetricsJSON(w, r)
	case "DescribeAlarms":
		s.describeAlarmsJSON(w, r)
	case "GetMetricStatistics":
		s.getMetricStatisticsJSON(w, r)
	case "PutMetricData":
		s.putMetricDataJSON(w, r)
	case "PutMetricAlarm":
		s.putMetricAlarmJSON(w, r)
	case "DeleteAlarms":
		s.deleteAlarmsJSON(w, r)
	case "DescribeAlarmsForMetric":
		s.describeAlarmsForMetricJSON(w, r)
	default:
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "UnknownOperationException",
			Message:    "Unknown target: GraniteServiceVersion20100801." + action,
			HTTPStatus: http.StatusBadRequest,
		})
	}
}

func writeJSONResult(w http.ResponseWriter, r *http.Request, body any) {
	protocol.WriteJSON(w, r, http.StatusOK, body)
}

func parseEpochSeconds(ts float64) time.Time {
	sec := int64(ts)
	nsec := int64((ts - float64(sec)) * float64(time.Second))
	return time.Unix(sec, nsec).UTC()
}

func epochSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}

// ─── Handlers ─────────────────────────────────────────────────

func (s *Service) listMetricsJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace string `json:"Namespace"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)

	metrics, err := s.store.listMetrics(r.Context(), in.Namespace)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	writeJSONResult(w, r, struct {
		Metrics []*Metric `json:"Metrics"`
	}{Metrics: metrics})
}

func (s *Service) describeAlarmsJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AlarmNames []string `json:"AlarmNames"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)

	alarms, err := s.store.listAlarms(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	filterNames := make(map[string]bool, len(in.AlarmNames))
	for _, name := range in.AlarmNames {
		if name != "" {
			filterNames[name] = true
		}
	}

	type metricAlarmJSON struct {
		AlarmName                          string   `json:"AlarmName"`
		AlarmArn                           string   `json:"AlarmArn"`
		MetricName                         string   `json:"MetricName,omitempty"`
		Namespace                          string   `json:"Namespace,omitempty"`
		Statistic                          string   `json:"Statistic,omitempty"`
		Period                             int      `json:"Period,omitempty"`
		EvaluationPeriods                  int      `json:"EvaluationPeriods,omitempty"`
		Threshold                          float64  `json:"Threshold,omitempty"`
		ComparisonOperator                 string   `json:"ComparisonOperator,omitempty"`
		ActionsEnabled                     bool     `json:"ActionsEnabled"`
		AlarmActions                       []string `json:"AlarmActions,omitempty"`
		OKActions                          []string `json:"OKActions,omitempty"`
		StateValue                         string   `json:"StateValue"`
		StateReason                        string   `json:"StateReason"`
		AlarmDescription                   string   `json:"AlarmDescription,omitempty"`
		TreatMissingData                   string   `json:"TreatMissingData,omitempty"`
		StateUpdatedTimestamp              float64  `json:"StateUpdatedTimestamp,omitempty"`
		AlarmConfigurationUpdatedTimestamp float64  `json:"AlarmConfigurationUpdatedTimestamp,omitempty"`
	}

	out := make([]metricAlarmJSON, 0, len(alarms))
	for _, a := range alarms {
		if len(filterNames) > 0 && !filterNames[a.AlarmName] {
			continue
		}
		alarm := metricAlarmJSON{
			AlarmName:          a.AlarmName,
			AlarmArn:           a.AlarmArn,
			MetricName:         a.MetricName,
			Namespace:          a.Namespace,
			Statistic:          a.Statistic,
			Period:             a.Period,
			EvaluationPeriods:  a.EvaluationPeriods,
			Threshold:          a.Threshold,
			ComparisonOperator: a.ComparisonOperator,
			ActionsEnabled:     a.ActionsEnabled,
			AlarmActions:       a.AlarmActions,
			OKActions:          a.OKActions,
			StateValue:         a.StateValue,
			StateReason:        a.StateReason,
			AlarmDescription:   a.AlarmDescription,
			TreatMissingData:   a.TreatMissingData,
		}
		if t, err := time.Parse(time.RFC3339, a.StateUpdatedTimestamp); err == nil {
			alarm.StateUpdatedTimestamp = epochSeconds(t)
		}
		if t, err := time.Parse(time.RFC3339, a.AlarmConfigurationUpdatedTimestamp); err == nil {
			alarm.AlarmConfigurationUpdatedTimestamp = epochSeconds(t)
		}
		out = append(out, alarm)
	}

	writeJSONResult(w, r, struct {
		MetricAlarms []metricAlarmJSON `json:"MetricAlarms"`
	}{MetricAlarms: out})
}

func (s *Service) getMetricStatisticsJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace  string      `json:"Namespace"`
		MetricName string      `json:"MetricName"`
		StartTime  float64     `json:"StartTime"`
		EndTime    float64     `json:"EndTime"`
		Period     int         `json:"Period"`
		Statistics []string    `json:"Statistics"`
		Dimensions []Dimension `json:"Dimensions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterValue", Message: "Invalid JSON body", HTTPStatus: http.StatusBadRequest})
		return
	}
	if in.Namespace == "" || in.MetricName == "" || in.Period <= 0 || in.StartTime == 0 || in.EndTime == 0 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "MissingParameter", Message: "Namespace, MetricName, StartTime, EndTime, and Period are required", HTTPStatus: http.StatusBadRequest})
		return
	}

	startTime := parseEpochSeconds(in.StartTime)
	endTime := parseEpochSeconds(in.EndTime)
	if endTime.Before(startTime) {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterValue", Message: "EndTime must be after StartTime", HTTPStatus: http.StatusBadRequest})
		return
	}

	requestedStats := map[string]bool{}
	for _, st := range in.Statistics {
		if st != "" {
			requestedStats[st] = true
		}
	}
	if len(requestedStats) == 0 {
		requestedStats["Average"] = true
	}

	dimensions := canonicalizeDimensions(in.Dimensions)
	points, err := s.store.listMetricDataPoints(r.Context(), in.Namespace, in.MetricName, dimensions, startTime, endTime)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	buckets := aggregateMetricBuckets(points, startTime.UTC(), endTime.UTC(), in.Period)

	type datapointJSON struct {
		Timestamp   float64 `json:"Timestamp"`
		Average     float64 `json:"Average,omitempty"`
		Sum         float64 `json:"Sum,omitempty"`
		SampleCount float64 `json:"SampleCount,omitempty"`
		Minimum     float64 `json:"Minimum,omitempty"`
		Maximum     float64 `json:"Maximum,omitempty"`
		Unit        string  `json:"Unit,omitempty"`
	}
	datapoints := make([]datapointJSON, 0, len(buckets))
	for _, b := range buckets {
		dp := datapointJSON{Timestamp: epochSeconds(b.timestamp)}
		if requestedStats["Average"] && b.sample > 0 {
			dp.Average = b.sum / b.sample
		}
		if requestedStats["Sum"] {
			dp.Sum = b.sum
		}
		if requestedStats["SampleCount"] {
			dp.SampleCount = b.sample
		}
		if requestedStats["Minimum"] {
			dp.Minimum = b.min
		}
		if requestedStats["Maximum"] {
			dp.Maximum = b.max
		}
		if b.unit != "" {
			dp.Unit = b.unit
		}
		datapoints = append(datapoints, dp)
	}

	writeJSONResult(w, r, struct {
		Label      string          `json:"Label"`
		Datapoints []datapointJSON `json:"Datapoints"`
	}{
		Label:      in.MetricName,
		Datapoints: datapoints,
	})
}

func (s *Service) putMetricDataJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace  string `json:"Namespace"`
		MetricData []struct {
			MetricName      string      `json:"MetricName"`
			Timestamp       *float64    `json:"Timestamp,omitempty"`
			Value           *float64    `json:"Value,omitempty"`
			Unit            string      `json:"Unit,omitempty"`
			Dimensions      []Dimension `json:"Dimensions,omitempty"`
			StatisticValues *struct {
				SampleCount float64 `json:"SampleCount"`
				Sum         float64 `json:"Sum"`
				Minimum     float64 `json:"Minimum"`
				Maximum     float64 `json:"Maximum"`
			} `json:"StatisticValues,omitempty"`
		} `json:"MetricData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterValue", Message: "Invalid JSON body", HTTPStatus: http.StatusBadRequest})
		return
	}
	if in.Namespace == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "MissingParameter", Message: "Namespace is required", HTTPStatus: http.StatusBadRequest})
		return
	}

	for _, datum := range in.MetricData {
		if datum.MetricName == "" {
			continue
		}
		dimensions := canonicalizeDimensions(datum.Dimensions)
		if err := s.store.putMetric(r.Context(), in.Namespace, datum.MetricName, dimensions); err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}

		ts := s.clk.Now().UTC()
		if datum.Timestamp != nil {
			ts = parseEpochSeconds(*datum.Timestamp)
		}

		dp := &MetricDataPoint{
			Namespace:  in.Namespace,
			MetricName: datum.MetricName,
			Dimensions: dimensions,
			Timestamp:  ts,
			Unit:       datum.Unit,
		}

		if datum.StatisticValues != nil {
			dp.SampleCount = datum.StatisticValues.SampleCount
			dp.Sum = datum.StatisticValues.Sum
			dp.Minimum = datum.StatisticValues.Minimum
			dp.Maximum = datum.StatisticValues.Maximum
		} else if datum.Value != nil {
			dp.SampleCount = 1
			dp.Sum = *datum.Value
			dp.Minimum = *datum.Value
			dp.Maximum = *datum.Value
		}

		if err := s.store.putMetricDataPoint(r.Context(), dp); err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}
	}

	writeJSONResult(w, r, struct{}{})
}

func (s *Service) putMetricAlarmJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AlarmName          string  `json:"AlarmName"`
		MetricName         string  `json:"MetricName"`
		Namespace          string  `json:"Namespace"`
		Statistic          string  `json:"Statistic"`
		Period             int     `json:"Period"`
		EvaluationPeriods  int     `json:"EvaluationPeriods"`
		Threshold          float64 `json:"Threshold"`
		ComparisonOperator string  `json:"ComparisonOperator"`
		ActionsEnabled     *bool   `json:"ActionsEnabled"`
		AlarmDescription   string  `json:"AlarmDescription"`
		TreatMissingData   string  `json:"TreatMissingData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterValue", Message: "Invalid JSON body", HTTPStatus: http.StatusBadRequest})
		return
	}
	if in.AlarmName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "MissingParameter", Message: "AlarmName is required", HTTPStatus: http.StatusBadRequest})
		return
	}

	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)
	arn := protocol.ARN(region, s.cfg.AccountID, "cloudwatch", "alarm:"+in.AlarmName)
	now := s.clk.Now().UTC().Format(time.RFC3339)
	actionsEnabled := true
	if in.ActionsEnabled != nil {
		actionsEnabled = *in.ActionsEnabled
	}
	alarm := &MetricAlarm{
		AlarmName:                          in.AlarmName,
		AlarmArn:                           arn,
		MetricName:                         in.MetricName,
		Namespace:                          in.Namespace,
		Statistic:                          in.Statistic,
		Period:                             in.Period,
		EvaluationPeriods:                  in.EvaluationPeriods,
		Threshold:                          in.Threshold,
		ComparisonOperator:                 in.ComparisonOperator,
		ActionsEnabled:                     actionsEnabled,
		AlarmDescription:                   in.AlarmDescription,
		TreatMissingData:                   in.TreatMissingData,
		StateValue:                         "INSUFFICIENT_DATA",
		StateReason:                        "Insufficient Data: awaiting datapoints",
		StateUpdatedTimestamp:              now,
		AlarmConfigurationUpdatedTimestamp: now,
	}
	if alarm.Statistic == "" {
		alarm.Statistic = "Average"
	}
	if alarm.ComparisonOperator == "" {
		alarm.ComparisonOperator = "GreaterThanThreshold"
	}
	if alarm.Period <= 0 {
		alarm.Period = 60
	}
	if alarm.EvaluationPeriods <= 0 {
		alarm.EvaluationPeriods = 1
	}

	if err := s.store.putAlarm(r.Context(), alarm); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	writeJSONResult(w, r, struct{}{})
}

func (s *Service) deleteAlarmsJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AlarmNames []string `json:"AlarmNames"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	for _, name := range in.AlarmNames {
		if name == "" {
			continue
		}
		_ = s.store.deleteAlarm(r.Context(), name)
	}
	writeJSONResult(w, r, struct{}{})
}

func (s *Service) describeAlarmsForMetricJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MetricName string `json:"MetricName"`
		Namespace  string `json:"Namespace"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)

	alarms, err := s.store.listAlarms(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	type alarmSummary struct {
		AlarmName string `json:"AlarmName"`
		AlarmArn  string `json:"AlarmArn"`
	}
	out := make([]alarmSummary, 0, len(alarms))
	for _, a := range alarms {
		if (in.MetricName != "" && a.MetricName != in.MetricName) || (in.Namespace != "" && a.Namespace != in.Namespace) {
			continue
		}
		out = append(out, alarmSummary{AlarmName: a.AlarmName, AlarmArn: a.AlarmArn})
	}
	writeJSONResult(w, r, struct {
		MetricAlarms []alarmSummary `json:"MetricAlarms"`
	}{MetricAlarms: out})
}

func (s *Service) putMetricAlarm(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("AlarmName")
	if name == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "MissingParameter", Message: "AlarmName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)
	arn := protocol.ARN(region, s.cfg.AccountID, "cloudwatch", "alarm:"+name)
	now := s.clk.Now().UTC().Format(time.RFC3339)

	alarm := &MetricAlarm{
		AlarmName:                          name,
		AlarmArn:                           arn,
		MetricName:                         r.FormValue("MetricName"),
		Namespace:                          r.FormValue("Namespace"),
		Statistic:                          r.FormValue("Statistic"),
		Period:                             parseIntDefault(r.FormValue("Period"), 60),
		EvaluationPeriods:                  parseIntDefault(r.FormValue("EvaluationPeriods"), 1),
		Threshold:                          parseFloatDefault(r.FormValue("Threshold"), 0),
		ComparisonOperator:                 r.FormValue("ComparisonOperator"),
		ActionsEnabled:                     r.FormValue("ActionsEnabled") != "false",
		AlarmDescription:                   r.FormValue("AlarmDescription"),
		TreatMissingData:                   r.FormValue("TreatMissingData"),
		StateValue:                         "INSUFFICIENT_DATA",
		StateReason:                        "Insufficient Data: awaiting datapoints",
		StateUpdatedTimestamp:              now,
		AlarmConfigurationUpdatedTimestamp: now,
	}
	if alarm.Statistic == "" {
		alarm.Statistic = "Average"
	}
	if alarm.ComparisonOperator == "" {
		alarm.ComparisonOperator = "GreaterThanThreshold"
	}
	if alarm.Period <= 0 {
		alarm.Period = 60
	}
	if alarm.EvaluationPeriods <= 0 {
		alarm.EvaluationPeriods = 1
	}

	if err := s.store.putAlarm(r.Context(), alarm); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	writeXMLResult(w, r, "PutMetricAlarm", "")
}

func (s *Service) describeAlarms(w http.ResponseWriter, r *http.Request) {
	alarms, err := s.store.listAlarms(r.Context())
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	// Filter by AlarmNames if provided.
	filterNames := make(map[string]bool)
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("AlarmNames.member.%d", i))
		if name == "" {
			break
		}
		filterNames[name] = true
	}

	var filtered []*MetricAlarm
	for _, a := range alarms {
		if len(filterNames) > 0 && !filterNames[a.AlarmName] {
			continue
		}
		filtered = append(filtered, a)
	}

	var members strings.Builder
	for _, a := range filtered {
		members.WriteString("<member>")
		members.WriteString("<AlarmName>" + xmlEscape(a.AlarmName) + "</AlarmName>")
		members.WriteString("<AlarmArn>" + xmlEscape(a.AlarmArn) + "</AlarmArn>")
		if a.MetricName != "" {
			members.WriteString("<MetricName>" + xmlEscape(a.MetricName) + "</MetricName>")
		}
		if a.Namespace != "" {
			members.WriteString("<Namespace>" + xmlEscape(a.Namespace) + "</Namespace>")
		}
		if a.ComparisonOperator != "" {
			members.WriteString("<ComparisonOperator>" + a.ComparisonOperator + "</ComparisonOperator>")
		}
		if a.Statistic != "" {
			members.WriteString("<Statistic>" + xmlEscape(a.Statistic) + "</Statistic>")
		}
		if a.Period > 0 {
			members.WriteString("<Period>" + strconv.Itoa(a.Period) + "</Period>")
		}
		if a.EvaluationPeriods > 0 {
			members.WriteString("<EvaluationPeriods>" + strconv.Itoa(a.EvaluationPeriods) + "</EvaluationPeriods>")
		}
		members.WriteString("<Threshold>" + strconv.FormatFloat(a.Threshold, 'f', -1, 64) + "</Threshold>")
		members.WriteString(fmt.Sprintf("<ActionsEnabled>%t</ActionsEnabled>", a.ActionsEnabled))
		members.WriteString("<StateValue>" + a.StateValue + "</StateValue>")
		members.WriteString("<StateReason>" + xmlEscape(a.StateReason) + "</StateReason>")
		if a.TreatMissingData != "" {
			members.WriteString("<TreatMissingData>" + xmlEscape(a.TreatMissingData) + "</TreatMissingData>")
		}
		if a.StateUpdatedTimestamp != "" {
			members.WriteString("<StateUpdatedTimestamp>" + xmlEscape(a.StateUpdatedTimestamp) + "</StateUpdatedTimestamp>")
		}
		if a.AlarmConfigurationUpdatedTimestamp != "" {
			members.WriteString("<AlarmConfigurationUpdatedTimestamp>" + xmlEscape(a.AlarmConfigurationUpdatedTimestamp) + "</AlarmConfigurationUpdatedTimestamp>")
		}
		if a.AlarmDescription != "" {
			members.WriteString("<AlarmDescription>" + xmlEscape(a.AlarmDescription) + "</AlarmDescription>")
		}
		members.WriteString("</member>")
	}

	body := fmt.Sprintf("<MetricAlarms>%s</MetricAlarms>", members.String())
	writeXMLResult(w, r, "DescribeAlarms", body)
}

func (s *Service) deleteAlarms(w http.ResponseWriter, r *http.Request) {
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("AlarmNames.member.%d", i))
		if name == "" {
			break
		}
		_ = s.store.deleteAlarm(r.Context(), name)
	}
	writeXMLResult(w, r, "DeleteAlarms", "")
}

func (s *Service) putMetricData(w http.ResponseWriter, r *http.Request) {
	ns := r.FormValue("Namespace")
	if ns == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "MissingParameter", Message: "Namespace is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// Persist metric metadata and datapoints so GetMetricStatistics can round-trip values.
	for i := 1; ; i++ {
		metricName := r.FormValue(fmt.Sprintf("MetricData.member.%d.MetricName", i))
		if metricName == "" {
			break
		}

		dimensions := make([]Dimension, 0)
		for j := 1; ; j++ {
			dimName := r.FormValue(fmt.Sprintf("MetricData.member.%d.Dimensions.member.%d.Name", i, j))
			if dimName == "" {
				break
			}
			dimVal := r.FormValue(fmt.Sprintf("MetricData.member.%d.Dimensions.member.%d.Value", i, j))
			dimensions = append(dimensions, Dimension{Name: dimName, Value: dimVal})
		}
		dimensions = canonicalizeDimensions(dimensions)

		ts := s.clk.Now().UTC()
		if rawTS := r.FormValue(fmt.Sprintf("MetricData.member.%d.Timestamp", i)); rawTS != "" {
			if parsed, ok := parseCWTime(rawTS); ok {
				ts = parsed.UTC()
			}
		}

		unit := r.FormValue(fmt.Sprintf("MetricData.member.%d.Unit", i))

		sampleCountRaw := r.FormValue(fmt.Sprintf("MetricData.member.%d.StatisticValues.SampleCount", i))
		sumRaw := r.FormValue(fmt.Sprintf("MetricData.member.%d.StatisticValues.Sum", i))
		minRaw := r.FormValue(fmt.Sprintf("MetricData.member.%d.StatisticValues.Minimum", i))
		maxRaw := r.FormValue(fmt.Sprintf("MetricData.member.%d.StatisticValues.Maximum", i))

		dp := &MetricDataPoint{
			Namespace:  ns,
			MetricName: metricName,
			Dimensions: dimensions,
			Timestamp:  ts,
			Unit:       unit,
		}
		if sampleCountRaw != "" && sumRaw != "" && minRaw != "" && maxRaw != "" {
			sampleCount, err1 := strconv.ParseFloat(sampleCountRaw, 64)
			sum, err2 := strconv.ParseFloat(sumRaw, 64)
			min, err3 := strconv.ParseFloat(minRaw, 64)
			max, err4 := strconv.ParseFloat(maxRaw, 64)
			if err1 == nil && err2 == nil && err3 == nil && err4 == nil {
				dp.SampleCount = sampleCount
				dp.Sum = sum
				dp.Minimum = min
				dp.Maximum = max
			}
		}
		if dp.SampleCount == 0 {
			valueRaw := r.FormValue(fmt.Sprintf("MetricData.member.%d.Value", i))
			value, err := strconv.ParseFloat(valueRaw, 64)
			if err != nil {
				continue
			}
			dp.SampleCount = 1
			dp.Sum = value
			dp.Minimum = value
			dp.Maximum = value
		}

		_ = s.store.putMetric(r.Context(), ns, metricName, dimensions)
		_ = s.store.putMetricDataPoint(r.Context(), dp)
	}
	writeXMLResult(w, r, "PutMetricData", "")
}

func (s *Service) getMetricStatistics(w http.ResponseWriter, r *http.Request) {
	namespace := r.FormValue("Namespace")
	metricName := r.FormValue("MetricName")
	startRaw := r.FormValue("StartTime")
	endRaw := r.FormValue("EndTime")
	periodRaw := r.FormValue("Period")

	if namespace == "" || metricName == "" || startRaw == "" || endRaw == "" || periodRaw == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "MissingParameter", Message: "Namespace, MetricName, StartTime, EndTime, and Period are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	startTime, ok1 := parseCWTime(startRaw)
	endTime, ok2 := parseCWTime(endRaw)
	periodSec, err := strconv.Atoi(periodRaw)
	if !ok1 || !ok2 || err != nil || periodSec <= 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "InvalidParameterValue", Message: "Invalid StartTime, EndTime, or Period",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	dimensions := make([]Dimension, 0)
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("Dimensions.member.%d.Name", i))
		if name == "" {
			break
		}
		value := r.FormValue(fmt.Sprintf("Dimensions.member.%d.Value", i))
		dimensions = append(dimensions, Dimension{Name: name, Value: value})
	}
	dimensions = canonicalizeDimensions(dimensions)

	requestedStats := map[string]bool{}
	for i := 1; ; i++ {
		stat := r.FormValue(fmt.Sprintf("Statistics.member.%d", i))
		if stat == "" {
			break
		}
		requestedStats[stat] = true
	}
	if len(requestedStats) == 0 {
		requestedStats["Average"] = true
	}

	points, err := s.store.listMetricDataPoints(r.Context(), namespace, metricName, dimensions, startTime, endTime)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	buckets := aggregateMetricBuckets(points, startTime.UTC(), endTime.UTC(), periodSec)

	var members strings.Builder
	for _, b := range buckets {
		members.WriteString("<member>")
		members.WriteString("<Timestamp>" + b.timestamp.UTC().Format(time.RFC3339) + "</Timestamp>")
		if requestedStats["Average"] && b.sample > 0 {
			members.WriteString("<Average>" + strconv.FormatFloat(b.sum/b.sample, 'f', -1, 64) + "</Average>")
		}
		if requestedStats["Sum"] {
			members.WriteString("<Sum>" + strconv.FormatFloat(b.sum, 'f', -1, 64) + "</Sum>")
		}
		if requestedStats["SampleCount"] {
			members.WriteString("<SampleCount>" + strconv.FormatFloat(b.sample, 'f', -1, 64) + "</SampleCount>")
		}
		if requestedStats["Minimum"] {
			members.WriteString("<Minimum>" + strconv.FormatFloat(b.min, 'f', -1, 64) + "</Minimum>")
		}
		if requestedStats["Maximum"] {
			members.WriteString("<Maximum>" + strconv.FormatFloat(b.max, 'f', -1, 64) + "</Maximum>")
		}
		if b.unit != "" {
			members.WriteString("<Unit>" + xmlEscape(b.unit) + "</Unit>")
		}
		members.WriteString("</member>")
	}
	body := "<Label>" + xmlEscape(metricName) + "</Label><Datapoints>" + members.String() + "</Datapoints>"
	writeXMLResult(w, r, "GetMetricStatistics", body)
}

func (s *Service) getMetricData(w http.ResponseWriter, r *http.Request) {
	startRaw := r.FormValue("StartTime")
	endRaw := r.FormValue("EndTime")
	if startRaw == "" || endRaw == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "MissingParameter", Message: "StartTime and EndTime are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	startTime, ok1 := parseCWTime(startRaw)
	endTime, ok2 := parseCWTime(endRaw)
	if !ok1 || !ok2 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "InvalidParameterValue", Message: "Invalid StartTime or EndTime",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	scanBy := r.FormValue("ScanBy")
	if scanBy == "" {
		scanBy = "TimestampDescending"
	}

	resultsByID := map[string]metricDataResult{}
	var results strings.Builder
	for i := 1; ; i++ {
		id := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.Id", i))
		if id == "" {
			break
		}
		expr := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.Expression", i))
		if expr != "" {
			res, err := evaluateMetricExpression(strings.TrimSpace(expr), resultsByID, scanBy)
			if err != nil {
				protocol.WriteXMLError(w, r, &protocol.AWSError{
					Code:       "InvalidParameterValue",
					Message:    "Invalid expression for query id " + id + ": " + err.Error(),
					HTTPStatus: http.StatusBadRequest,
				})
				return
			}
			res.id = id
			res.label = expr
			resultsByID[id] = res
			results.WriteString(renderMetricDataResultMember(res))
			continue
		}

		namespace := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.MetricStat.Metric.Namespace", i))
		metricName := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.MetricStat.Metric.MetricName", i))
		periodRaw := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.MetricStat.Period", i))
		stat := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.MetricStat.Stat", i))
		if stat == "" {
			stat = "Average"
		}
		periodSec, err := strconv.Atoi(periodRaw)
		if namespace == "" || metricName == "" || err != nil || periodSec <= 0 {
			protocol.WriteXMLError(w, r, &protocol.AWSError{
				Code: "InvalidParameterValue", Message: "Each query must include MetricStat with Metric, Period, and Stat",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}

		dimensions := make([]Dimension, 0)
		for j := 1; ; j++ {
			name := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.MetricStat.Metric.Dimensions.member.%d.Name", i, j))
			if name == "" {
				break
			}
			value := r.FormValue(fmt.Sprintf("MetricDataQueries.member.%d.MetricStat.Metric.Dimensions.member.%d.Value", i, j))
			dimensions = append(dimensions, Dimension{Name: name, Value: value})
		}
		dimensions = canonicalizeDimensions(dimensions)

		points, err := s.store.listMetricDataPoints(r.Context(), namespace, metricName, dimensions, startTime, endTime)
		if err != nil {
			protocol.WriteXMLError(w, r, protocol.ErrInternalError)
			return
		}
		buckets := aggregateMetricBuckets(points, startTime.UTC(), endTime.UTC(), periodSec)
		if scanBy == "TimestampDescending" {
			for l, r := 0, len(buckets)-1; l < r; l, r = l+1, r-1 {
				buckets[l], buckets[r] = buckets[r], buckets[l]
			}
		}

		res := buildMetricDataResult(id, metricName, stat, buckets)
		resultsByID[id] = res
		results.WriteString(renderMetricDataResultMember(res))
	}

	writeXMLResult(w, r, "GetMetricData", "<MetricDataResults>"+results.String()+"</MetricDataResults>")
}

type metricBucket struct {
	timestamp time.Time
	sample    float64
	sum       float64
	min       float64
	max       float64
	unit      string
	set       bool
}

type metricDataResult struct {
	id         string
	label      string
	timestamps []time.Time
	values     []float64
}

func buildMetricDataResult(id, label, stat string, buckets []*metricBucket) metricDataResult {
	out := metricDataResult{id: id, label: label, timestamps: make([]time.Time, 0, len(buckets)), values: make([]float64, 0, len(buckets))}
	for _, b := range buckets {
		v, ok := metricStatValue(stat, b)
		if !ok {
			continue
		}
		out.timestamps = append(out.timestamps, b.timestamp.UTC())
		out.values = append(out.values, v)
	}
	return out
}

func renderMetricDataResultMember(result metricDataResult) string {
	var timestamps strings.Builder
	var values strings.Builder
	for i := range result.timestamps {
		timestamps.WriteString("<member>" + result.timestamps[i].UTC().Format(time.RFC3339) + "</member>")
		values.WriteString("<member>" + strconv.FormatFloat(result.values[i], 'f', -1, 64) + "</member>")
	}
	var b strings.Builder
	b.WriteString("<member>")
	b.WriteString("<Id>" + xmlEscape(result.id) + "</Id>")
	b.WriteString("<Label>" + xmlEscape(result.label) + "</Label>")
	b.WriteString("<Timestamps>" + timestamps.String() + "</Timestamps>")
	b.WriteString("<Values>" + values.String() + "</Values>")
	b.WriteString("<StatusCode>Complete</StatusCode>")
	b.WriteString("</member>")
	return b.String()
}

func evaluateMetricExpression(expr string, byID map[string]metricDataResult, scanBy string) (metricDataResult, error) {
	exprNoSpace := strings.ReplaceAll(expr, " ", "")
	funcRE := regexp.MustCompile(`(?i)^(SUM|AVG|MIN|MAX)\(([A-Za-z][A-Za-z0-9_]*)\)$`)
	if matches := funcRE.FindStringSubmatch(exprNoSpace); len(matches) == 3 {
		fn := strings.ToUpper(matches[1])
		id := matches[2]
		src, ok := byID[id]
		if !ok {
			return metricDataResult{}, fmt.Errorf("unknown query id %q", id)
		}
		if len(src.values) == 0 || len(src.timestamps) == 0 {
			return metricDataResult{timestamps: []time.Time{}, values: []float64{}}, nil
		}
		agg := src.values[0]
		sum := src.values[0]
		for i := 1; i < len(src.values); i++ {
			sum += src.values[i]
			switch fn {
			case "MIN":
				if src.values[i] < agg {
					agg = src.values[i]
				}
			case "MAX":
				if src.values[i] > agg {
					agg = src.values[i]
				}
			}
		}
		switch fn {
		case "SUM":
			agg = sum
		case "AVG":
			agg = sum / float64(len(src.values))
		}
		ts := src.timestamps[0]
		if scanBy == "TimestampDescending" {
			ts = src.timestamps[len(src.timestamps)-1]
		}
		return metricDataResult{timestamps: []time.Time{ts.UTC()}, values: []float64{agg}}, nil
	}

	binaryRE := regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*)([+\-*/])([A-Za-z][A-Za-z0-9_]*)$`)
	if matches := binaryRE.FindStringSubmatch(exprNoSpace); len(matches) == 4 {
		leftID := matches[1]
		op := matches[2]
		rightID := matches[3]
		left, ok := byID[leftID]
		if !ok {
			return metricDataResult{}, fmt.Errorf("unknown query id %q", leftID)
		}
		right, ok := byID[rightID]
		if !ok {
			return metricDataResult{}, fmt.Errorf("unknown query id %q", rightID)
		}

		rightByTS := map[int64]float64{}
		for i := range right.timestamps {
			rightByTS[right.timestamps[i].UTC().Unix()] = right.values[i]
		}
		timestamps := make([]time.Time, 0, len(left.timestamps))
		values := make([]float64, 0, len(left.timestamps))
		for i := range left.timestamps {
			ts := left.timestamps[i].UTC()
			rv, ok := rightByTS[ts.Unix()]
			if !ok {
				continue
			}
			lv := left.values[i]
			var out float64
			switch op {
			case "+":
				out = lv + rv
			case "-":
				out = lv - rv
			case "*":
				out = lv * rv
			case "/":
				if rv == 0 {
					continue
				}
				out = lv / rv
			}
			timestamps = append(timestamps, ts)
			values = append(values, out)
		}
		return metricDataResult{timestamps: timestamps, values: values}, nil
	}

	return metricDataResult{}, fmt.Errorf("unsupported expression syntax")
}

func aggregateMetricBuckets(points []*MetricDataPoint, startTime, endTime time.Time, periodSec int) []*metricBucket {
	bucketsByKey := map[int64]*metricBucket{}
	for _, p := range points {
		ts := p.Timestamp.UTC()
		if ts.Before(startTime) || ts.After(endTime) {
			continue
		}
		bucketStart := startTime.Add(time.Duration(((ts.Unix()-startTime.Unix())/int64(periodSec))*int64(periodSec)) * time.Second)
		key := bucketStart.Unix()
		b, found := bucketsByKey[key]
		if !found {
			b = &metricBucket{timestamp: bucketStart, min: p.Minimum, max: p.Maximum, unit: p.Unit, set: true}
			bucketsByKey[key] = b
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
		if b.unit == "" {
			b.unit = p.Unit
		}
	}

	keys := make([]int64, 0, len(bucketsByKey))
	for k := range bucketsByKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	out := make([]*metricBucket, 0, len(keys))
	for _, k := range keys {
		out = append(out, bucketsByKey[k])
	}
	return out
}

func metricStatValue(stat string, b *metricBucket) (float64, bool) {
	switch stat {
	case "Average":
		if b.sample <= 0 {
			return 0, false
		}
		return b.sum / b.sample, true
	case "Sum":
		return b.sum, true
	case "SampleCount":
		return b.sample, true
	case "Minimum":
		return b.min, true
	case "Maximum":
		return b.max, true
	default:
		return 0, false
	}
}

func (s *Service) listMetrics(w http.ResponseWriter, r *http.Request) {
	ns := r.FormValue("Namespace")
	metrics, err := s.store.listMetrics(r.Context(), ns)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	var members strings.Builder
	for _, m := range metrics {
		members.WriteString("<member>")
		members.WriteString("<Namespace>" + xmlEscape(m.Namespace) + "</Namespace>")
		members.WriteString("<MetricName>" + xmlEscape(m.MetricName) + "</MetricName>")
		if len(m.Dimensions) > 0 {
			members.WriteString("<Dimensions>")
			for _, dimension := range m.Dimensions {
				members.WriteString("<member>")
				members.WriteString("<Name>" + xmlEscape(dimension.Name) + "</Name>")
				members.WriteString("<Value>" + xmlEscape(dimension.Value) + "</Value>")
				members.WriteString("</member>")
			}
			members.WriteString("</Dimensions>")
		}
		members.WriteString("</member>")
	}
	body := fmt.Sprintf("<Metrics>%s</Metrics>", members.String())
	writeXMLResult(w, r, "ListMetrics", body)
}

func (s *Service) describeAlarmsForMetric(w http.ResponseWriter, r *http.Request) {
	metricName := r.FormValue("MetricName")
	namespace := r.FormValue("Namespace")
	alarms, err := s.store.listAlarms(r.Context())
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	var members strings.Builder
	for _, a := range alarms {
		if (metricName != "" && a.MetricName != metricName) ||
			(namespace != "" && a.Namespace != namespace) {
			continue
		}
		members.WriteString("<member>")
		members.WriteString("<AlarmName>" + xmlEscape(a.AlarmName) + "</AlarmName>")
		members.WriteString("<AlarmArn>" + xmlEscape(a.AlarmArn) + "</AlarmArn>")
		members.WriteString("</member>")
	}
	body := fmt.Sprintf("<MetricAlarms>%s</MetricAlarms>", members.String())
	writeXMLResult(w, r, "DescribeAlarmsForMetric", body)
}

func (s *Service) setAlarmState(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("AlarmName")
	alarm, found := s.store.getAlarm(r.Context(), name)
	if !found {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "ResourceNotFound", Message: "Alarm " + name + " not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	alarm.StateValue = r.FormValue("StateValue")
	alarm.StateReason = r.FormValue("StateReason")
	alarm.StateUpdatedTimestamp = s.clk.Now().UTC().Format(time.RFC3339)
	if err := s.store.putAlarm(r.Context(), alarm); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	writeXMLResult(w, r, "SetAlarmState", "")
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceARN")
	tags, err := s.store.getTags(r.Context(), arn)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	var members strings.Builder
	for k, v := range tags {
		members.WriteString("<member>")
		members.WriteString("<Key>" + xmlEscape(k) + "</Key>")
		members.WriteString("<Value>" + xmlEscape(v) + "</Value>")
		members.WriteString("</member>")
	}
	body := fmt.Sprintf("<Tags>%s</Tags>", members.String())
	writeXMLResult(w, r, "ListTagsForResource", body)
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceARN")
	tags, err := s.store.getTags(r.Context(), arn)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	for i := 1; ; i++ {
		k := r.FormValue(fmt.Sprintf("Tags.member.%d.Key", i))
		if k == "" {
			break
		}
		v := r.FormValue(fmt.Sprintf("Tags.member.%d.Value", i))
		tags[k] = v
	}
	if err := s.store.setTags(r.Context(), arn, tags); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	writeXMLResult(w, r, "TagResource", "")
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceARN")
	tags, err := s.store.getTags(r.Context(), arn)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	for i := 1; ; i++ {
		k := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if k == "" {
			break
		}
		delete(tags, k)
	}
	if err := s.store.setTags(r.Context(), arn, tags); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	writeXMLResult(w, r, "UntagResource", "")
}

func (s *Service) runAlarmEvaluator() {
	defer s.wg.Done()
	ticker := s.clk.Ticker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.evaluateAlarms()
		}
	}
}

// startMetricDataSweeper starts the background metric-data retention sweep.
// It periodically deletes cloudwatch:metricdata points older than the
// retention window, in every backend mode (storage-plan.md 3.4). Tracked by
// the same s.wg/s.stopCh as runAlarmEvaluator, so Service.Stop waits for
// both goroutines together.
//
// The ticker is created here — synchronously, on New's calling goroutine —
// rather than inside the spawned goroutine. This matters for tests: it
// guarantees clk.Ticker has already registered with a mock clock by the time
// New() returns, so a test that calls mock.Add() immediately after
// constructing the Service can't race against the goroutine not having
// started yet (advancing a mock clock before a ticker exists is simply lost
// — the ticker only anchors its first firing to whatever time it observes at
// creation).
func (s *Service) startMetricDataSweeper() {
	ticker := s.clk.Ticker(metricDataSweepInterval)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.store.sweepMetricDataOnce(context.Background())
			}
		}
	}()
}

func (s *Service) evaluateAlarms() {
	ctx := context.Background()
	alarms, err := s.store.listAlarms(ctx)
	if err != nil {
		return
	}
	now := s.clk.Now().UTC()
	for _, alarm := range alarms {
		if alarm.MetricName == "" || alarm.Namespace == "" {
			continue
		}
		period := alarm.Period
		if period <= 0 {
			period = 60
		}
		evaluationPeriods := alarm.EvaluationPeriods
		if evaluationPeriods <= 0 {
			evaluationPeriods = 1
		}

		windowStart := now.Add(-time.Duration(period*evaluationPeriods) * time.Second)
		points, err := s.store.listMetricDataPoints(ctx, alarm.Namespace, alarm.MetricName, nil, windowStart, now)
		if err != nil {
			continue
		}
		buckets := aggregateMetricBuckets(points, windowStart, now, period)
		if len(buckets) == 0 || len(buckets) < evaluationPeriods {
			s.setAlarmEvalState(ctx, alarm, "INSUFFICIENT_DATA", "Insufficient Data: fewer datapoints than EvaluationPeriods")
			continue
		}

		recent := buckets[len(buckets)-evaluationPeriods:]
		breaching := true
		for _, bucket := range recent {
			v, ok := metricStatValue(alarm.Statistic, bucket)
			if !ok || !compareThreshold(v, alarm.Threshold, alarm.ComparisonOperator) {
				breaching = false
				break
			}
		}
		if breaching {
			s.setAlarmEvalState(ctx, alarm, "ALARM", "Threshold Crossed: datapoints breaching threshold")
		} else {
			s.setAlarmEvalState(ctx, alarm, "OK", "Threshold Not Crossed")
		}
	}
}

func (s *Service) setAlarmEvalState(ctx context.Context, alarm *MetricAlarm, stateValue, stateReason string) {
	if alarm.StateValue == stateValue && alarm.StateReason == stateReason {
		return
	}
	alarm.StateValue = stateValue
	alarm.StateReason = stateReason
	alarm.StateUpdatedTimestamp = s.clk.Now().UTC().Format(time.RFC3339)
	_ = s.store.putAlarm(ctx, alarm)
}

// ─── Helpers ──────────────────────────────────────────────────

func writeXMLResult(w http.ResponseWriter, r *http.Request, action, body string) {
	xml := fmt.Sprintf(`<%sResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/"><%sResult>%s</%sResult><ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata></%sResponse>`,
		action, action, body, action, protocol.RequestIDFromContext(r.Context()), action)
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("x-amzn-requestid", protocol.RequestIDFromContext(r.Context()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml))
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

func parseCWTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts, true
	}
	if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(sec, 0).UTC(), true
	}
	return time.Time{}, false
}

func parseIntDefault(v string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return n
}

func parseFloatDefault(v string, fallback float64) float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return fallback
	}
	return n
}

func compareThreshold(v, threshold float64, op string) bool {
	switch op {
	case "GreaterThanThreshold":
		return v > threshold
	case "GreaterThanOrEqualToThreshold":
		return v >= threshold
	case "LessThanThreshold":
		return v < threshold
	case "LessThanOrEqualToThreshold":
		return v <= threshold
	default:
		return false
	}
}
