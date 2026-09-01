package cloudwatch

import (
	"context"
	"net/http"
	"time"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/protocol/op"
)

// The typed operations below are what lets CloudWatch answer over Smithy RPC
// v2 CBOR (issue #1280). The pinned model declares rpcv2Cbor for every
// CloudWatch operation, and a newer SDK major negotiates it for a service that
// declares it — but Overcast's smithyRPCService registration had no
// ProtocolService for CloudWatch, so supports() answered false for every
// operation and the router turned each call away with writeNotImplemented's
// 501. (Not the 415 an unregistered service gets: CloudWatch was in the
// dispatcher map all along, keyed by its target prefix. It answered as a
// service with no such operation rather than as one that does not speak the
// protocol, which is the more misleading of the two.)
//
// Each operation here is a thin binding onto a *core*: a protocol-neutral
// func(ctx, *In) (*Out, *protocol.AWSError) holding the whole of the
// operation's behaviour. The awsJson handlers in service.go call the same
// cores — they keep only their own decode step, because their malformed-body
// error is spelled differently from the codec's, and their wire bytes must not
// move. This is the shape #1169 established with computeMetricDataResults:
// one implementation, one entry point per protocol, never a copy per protocol.
//
// The Query/XML handlers are the third door, and they are not typed: the Query
// protocol encodes lists as MetricDataQueries.member.N form fields, which no
// struct decode reproduces. They call the same helpers underneath
// (computeMetricDataResults, storeAlarmFromInput, classifyTagResource, ...).

// typedOps returns the operations CloudWatch answers over the typed
// dispatcher, keyed by AWS operation name. The keys are exactly s.ops' keys —
// TestTypedOps_CoverEveryDispatchedOperation holds the two tables together, so
// an operation added to one and not the other fails the build rather than
// becoming a protocol asymmetry.
func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"DeleteAlarms": op.NewTyped[deleteAlarmsRequest, emptyResponse](
			"DeleteAlarms", s.deleteAlarmsCore,
		),
		"DescribeAlarmHistory": op.NewTyped[describeAlarmHistoryRequest, describeAlarmHistoryResponse](
			"DescribeAlarmHistory", s.describeAlarmHistoryCore,
		),
		"DescribeAlarms": op.NewTyped[describeAlarmsRequest, describeAlarmsResponse](
			"DescribeAlarms", s.describeAlarmsCore,
		),
		"DescribeAlarmsForMetric": op.NewTyped[describeAlarmsForMetricRequest, describeAlarmsForMetricResponse](
			"DescribeAlarmsForMetric", s.describeAlarmsForMetricCore,
		),
		"DisableAlarmActions": op.NewTyped[alarmActionsRequest, emptyResponse](
			"DisableAlarmActions", s.disableAlarmActionsCore,
		),
		"EnableAlarmActions": op.NewTyped[alarmActionsRequest, emptyResponse](
			"EnableAlarmActions", s.enableAlarmActionsCore,
		),
		"GetMetricData": op.NewTyped[getMetricDataRequest, getMetricDataResponse](
			"GetMetricData", s.getMetricDataCore,
		),
		"GetMetricStatistics": op.NewTyped[getMetricStatisticsRequest, getMetricStatisticsResponse](
			"GetMetricStatistics", s.getMetricStatisticsCore,
		),
		"ListMetrics": op.NewTyped[listMetricsRequest, listMetricsResponse](
			"ListMetrics", s.listMetricsCore,
		),
		"ListTagsForResource": op.NewTyped[listTagsForResourceRequest, listTagsForResourceResponse](
			"ListTagsForResource", s.listTagsForResourceCore,
		),
		"PutMetricAlarm": op.NewTyped[putMetricAlarmJSONBody, putMetricAlarmResponse](
			"PutMetricAlarm", s.putMetricAlarmCore,
		),
		"PutMetricData": op.NewTyped[putMetricDataRequest, emptyResponse](
			"PutMetricData", s.putMetricDataCore,
		),
		"SetAlarmState": op.NewTyped[setAlarmStateRequest, emptyResponse](
			"SetAlarmState", s.setAlarmStateCore,
		),
		"TagResource": op.NewTyped[tagResourceRequest, emptyResponse](
			"TagResource", s.tagResourceCore,
		),
		"UntagResource": op.NewTyped[untagResourceRequest, emptyResponse](
			"UntagResource", s.untagResourceCore,
		),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	out := make([]op.Operation, 0, len(s.typedOp))
	for _, operation := range s.typedOp {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
//
// awsQuery is deliberately absent even though CloudWatch answers it: this list
// is consulted by the typed dispatcher, and the Query protocol's member.N
// encoding is served by the legacy handlers rather than by the typed
// operations above. Adding it here would claim the typed path decodes Query
// bodies, which it does not.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}

// emptyResponse is the success body of an operation AWS models with no output
// members. It renders as JSON "{}" and as an empty CBOR map, which is what the
// handlers wrote before they were typed.
type emptyResponse struct{}

// ─── ListMetrics ──────────────────────────────────────────────

type listMetricsRequest struct {
	Namespace string `json:"Namespace"`
}

type listMetricsResponse struct {
	Metrics []*Metric `json:"Metrics"`
}

func (s *Service) listMetricsCore(ctx context.Context, in *listMetricsRequest) (*listMetricsResponse, *protocol.AWSError) {
	metrics, err := s.store.mergedListMetrics(ctx, in.Namespace)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listMetricsResponse{Metrics: metrics}, nil
}

// ─── DescribeAlarms ───────────────────────────────────────────

type describeAlarmsRequest struct {
	AlarmNames []string `json:"AlarmNames"`
}

// metricAlarmJSON is the alarm as the non-Query protocols render it. It is not
// MetricAlarm: the three timestamps are epoch-second numbers here and RFC 3339
// strings in the persisted shape, and Overcast's own bookkeeping fields
// (UnevaluatedReason, ManualStateHoldUntil) are not wire members at all.
type metricAlarmJSON struct {
	AlarmName                          string      `json:"AlarmName"`
	AlarmArn                           string      `json:"AlarmArn"`
	MetricName                         string      `json:"MetricName,omitempty"`
	Namespace                          string      `json:"Namespace,omitempty"`
	Statistic                          string      `json:"Statistic,omitempty"`
	Dimensions                         []Dimension `json:"Dimensions,omitempty"`
	Unit                               string      `json:"Unit,omitempty"`
	Period                             int         `json:"Period,omitempty"`
	EvaluationPeriods                  int         `json:"EvaluationPeriods,omitempty"`
	DatapointsToAlarm                  int         `json:"DatapointsToAlarm,omitempty"`
	Threshold                          float64     `json:"Threshold,omitempty"`
	ComparisonOperator                 string      `json:"ComparisonOperator,omitempty"`
	ActionsEnabled                     bool        `json:"ActionsEnabled"`
	AlarmActions                       []string    `json:"AlarmActions,omitempty"`
	OKActions                          []string    `json:"OKActions,omitempty"`
	InsufficientDataActions            []string    `json:"InsufficientDataActions,omitempty"`
	StateValue                         string      `json:"StateValue"`
	StateReason                        string      `json:"StateReason"`
	StateReasonData                    string      `json:"StateReasonData,omitempty"`
	AlarmDescription                   string      `json:"AlarmDescription,omitempty"`
	TreatMissingData                   string      `json:"TreatMissingData,omitempty"`
	StateUpdatedTimestamp              float64     `json:"StateUpdatedTimestamp,omitempty"`
	StateTransitionedTimestamp         float64     `json:"StateTransitionedTimestamp,omitempty"`
	AlarmConfigurationUpdatedTimestamp float64     `json:"AlarmConfigurationUpdatedTimestamp,omitempty"`
}

type describeAlarmsResponse struct {
	MetricAlarms []metricAlarmJSON `json:"MetricAlarms"`
}

func (s *Service) describeAlarmsCore(ctx context.Context, in *describeAlarmsRequest) (*describeAlarmsResponse, *protocol.AWSError) {
	alarms, err := s.store.listAlarms(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}

	filterNames := make(map[string]bool, len(in.AlarmNames))
	for _, name := range in.AlarmNames {
		if name != "" {
			filterNames[name] = true
		}
	}

	out := make([]metricAlarmJSON, 0, len(alarms))
	for _, a := range alarms {
		if len(filterNames) > 0 && !filterNames[a.AlarmName] {
			continue
		}
		alarm := metricAlarmJSON{
			AlarmName:               a.AlarmName,
			AlarmArn:                a.AlarmArn,
			MetricName:              a.MetricName,
			Namespace:               a.Namespace,
			Statistic:               a.Statistic,
			Dimensions:              a.Dimensions,
			Unit:                    a.Unit,
			Period:                  a.Period,
			EvaluationPeriods:       a.EvaluationPeriods,
			DatapointsToAlarm:       a.DatapointsToAlarm,
			Threshold:               a.Threshold,
			ComparisonOperator:      a.ComparisonOperator,
			ActionsEnabled:          a.ActionsEnabled,
			AlarmActions:            a.AlarmActions,
			OKActions:               a.OKActions,
			InsufficientDataActions: a.InsufficientDataActions,
			StateValue:              a.StateValue,
			StateReason:             a.StateReason,
			StateReasonData:         a.StateReasonData,
			AlarmDescription:        a.AlarmDescription,
			TreatMissingData:        a.TreatMissingData,
		}
		if t, err := time.Parse(time.RFC3339, a.StateUpdatedTimestamp); err == nil {
			alarm.StateUpdatedTimestamp = epochSeconds(t)
		}
		if t, err := time.Parse(time.RFC3339, a.StateTransitionedTimestamp); err == nil {
			alarm.StateTransitionedTimestamp = epochSeconds(t)
		}
		if t, err := time.Parse(time.RFC3339, a.AlarmConfigurationUpdatedTimestamp); err == nil {
			alarm.AlarmConfigurationUpdatedTimestamp = epochSeconds(t)
		}
		out = append(out, alarm)
	}

	return &describeAlarmsResponse{MetricAlarms: out}, nil
}

// ─── DescribeAlarmsForMetric ──────────────────────────────────

type describeAlarmsForMetricRequest struct {
	MetricName string `json:"MetricName"`
	Namespace  string `json:"Namespace"`
}

type alarmSummary struct {
	AlarmName string `json:"AlarmName"`
	AlarmArn  string `json:"AlarmArn"`
}

type describeAlarmsForMetricResponse struct {
	MetricAlarms []alarmSummary `json:"MetricAlarms"`
}

func (s *Service) describeAlarmsForMetricCore(ctx context.Context, in *describeAlarmsForMetricRequest) (*describeAlarmsForMetricResponse, *protocol.AWSError) {
	alarms, err := s.store.listAlarms(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	out := make([]alarmSummary, 0, len(alarms))
	for _, a := range alarms {
		if (in.MetricName != "" && a.MetricName != in.MetricName) || (in.Namespace != "" && a.Namespace != in.Namespace) {
			continue
		}
		out = append(out, alarmSummary{AlarmName: a.AlarmName, AlarmArn: a.AlarmArn})
	}
	return &describeAlarmsForMetricResponse{MetricAlarms: out}, nil
}

// ─── DeleteAlarms ─────────────────────────────────────────────

type deleteAlarmsRequest struct {
	AlarmNames []string `json:"AlarmNames"`
}

func (s *Service) deleteAlarmsCore(ctx context.Context, in *deleteAlarmsRequest) (*emptyResponse, *protocol.AWSError) {
	for _, name := range in.AlarmNames {
		if name == "" {
			continue
		}
		s.removeAlarm(ctx, name)
	}
	return &emptyResponse{}, nil
}

// ─── EnableAlarmActions / DisableAlarmActions ─────────────────

type alarmActionsRequest struct {
	AlarmNames []string `json:"AlarmNames"`
}

func (s *Service) enableAlarmActionsCore(ctx context.Context, in *alarmActionsRequest) (*emptyResponse, *protocol.AWSError) {
	return s.alarmActionsCore(ctx, in, true)
}

func (s *Service) disableAlarmActionsCore(ctx context.Context, in *alarmActionsRequest) (*emptyResponse, *protocol.AWSError) {
	return s.alarmActionsCore(ctx, in, false)
}

func (s *Service) alarmActionsCore(ctx context.Context, in *alarmActionsRequest, enabled bool) (*emptyResponse, *protocol.AWSError) {
	for _, name := range in.AlarmNames {
		if name != "" {
			s.applyActionsEnabled(ctx, name, enabled)
		}
	}
	return &emptyResponse{}, nil
}

// ─── SetAlarmState ────────────────────────────────────────────

type setAlarmStateRequest struct {
	AlarmName       string `json:"AlarmName"`
	StateValue      string `json:"StateValue"`
	StateReason     string `json:"StateReason"`
	StateReasonData string `json:"StateReasonData"`
}

func (s *Service) setAlarmStateCore(ctx context.Context, in *setAlarmStateRequest) (*emptyResponse, *protocol.AWSError) {
	if aerr := validateSetAlarmState(in.AlarmName, in.StateValue); aerr != nil {
		return nil, aerr
	}
	alarm, found := s.store.getAlarm(ctx, in.AlarmName)
	if !found {
		return nil, errAlarmNotFound(in.AlarmName)
	}
	s.applyAlarmState(ctx, alarm, in.StateValue, in.StateReason, in.StateReasonData, true)
	return &emptyResponse{}, nil
}

// ─── PutMetricAlarm ───────────────────────────────────────────

// putMetricAlarmResponse carries no wire members — PutMetricAlarm's modeled
// output is empty — but an alarm Overcast will not evaluate has to say so, and
// a typed Fn is never handed the ResponseWriter. Limitationer is that channel:
// op.Invoke marks each reason as x-overcast-emulation-limitation, exactly as
// the Query and JSON handlers' own protocol.MarkLimitation call does.
type putMetricAlarmResponse struct {
	limitations []string
}

func (r putMetricAlarmResponse) EmulationLimitations() []string { return r.limitations }

func (s *Service) putMetricAlarmCore(ctx context.Context, body *putMetricAlarmJSONBody) (*putMetricAlarmResponse, *protocol.AWSError) {
	in := body.toInput()
	if aerr := in.validate(); aerr != nil {
		return nil, aerr
	}
	alarm, aerr := s.storeAlarmFromInput(ctx, in, jsonTagCfg)
	if aerr != nil {
		return nil, aerr
	}
	out := &putMetricAlarmResponse{}
	if alarm.UnevaluatedReason != "" {
		out.limitations = []string{alarm.UnevaluatedReason}
	}
	return out, nil
}

// ─── PutMetricData ────────────────────────────────────────────

type statisticValuesInput struct {
	SampleCount float64 `json:"SampleCount"`
	Sum         float64 `json:"Sum"`
	Minimum     float64 `json:"Minimum"`
	Maximum     float64 `json:"Maximum"`
}

type metricDatumInput struct {
	MetricName      string                `json:"MetricName"`
	Timestamp       *float64              `json:"Timestamp,omitempty"`
	Value           *float64              `json:"Value,omitempty"`
	Unit            string                `json:"Unit,omitempty"`
	Dimensions      []Dimension           `json:"Dimensions,omitempty"`
	StatisticValues *statisticValuesInput `json:"StatisticValues,omitempty"`
}

type putMetricDataRequest struct {
	Namespace  string             `json:"Namespace"`
	MetricData []metricDatumInput `json:"MetricData"`
}

func (s *Service) putMetricDataCore(ctx context.Context, in *putMetricDataRequest) (*emptyResponse, *protocol.AWSError) {
	if in.Namespace == "" {
		return nil, &protocol.AWSError{Code: "MissingParameter", Message: "Namespace is required", HTTPStatus: http.StatusBadRequest}
	}

	for _, datum := range in.MetricData {
		if datum.MetricName == "" {
			continue
		}
		dimensions := canonicalizeDimensions(datum.Dimensions)
		if err := s.store.putMetric(ctx, in.Namespace, datum.MetricName, dimensions); err != nil {
			return nil, protocol.ErrInternalError
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

		if err := s.store.putMetricDataPoint(ctx, dp); err != nil {
			return nil, protocol.ErrInternalError
		}
	}

	return &emptyResponse{}, nil
}

// ─── GetMetricStatistics ──────────────────────────────────────

type getMetricStatisticsRequest struct {
	Namespace  string      `json:"Namespace"`
	MetricName string      `json:"MetricName"`
	StartTime  float64     `json:"StartTime"`
	EndTime    float64     `json:"EndTime"`
	Period     int         `json:"Period"`
	Statistics []string    `json:"Statistics"`
	Dimensions []Dimension `json:"Dimensions"`
}

type datapointJSON struct {
	Timestamp   float64 `json:"Timestamp"`
	Average     float64 `json:"Average,omitempty"`
	Sum         float64 `json:"Sum,omitempty"`
	SampleCount float64 `json:"SampleCount,omitempty"`
	Minimum     float64 `json:"Minimum,omitempty"`
	Maximum     float64 `json:"Maximum,omitempty"`
	Unit        string  `json:"Unit,omitempty"`
}

type getMetricStatisticsResponse struct {
	Label      string          `json:"Label"`
	Datapoints []datapointJSON `json:"Datapoints"`
}

func (s *Service) getMetricStatisticsCore(ctx context.Context, in *getMetricStatisticsRequest) (*getMetricStatisticsResponse, *protocol.AWSError) {
	if in.Namespace == "" || in.MetricName == "" || in.Period <= 0 || in.StartTime == 0 || in.EndTime == 0 {
		return nil, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "Namespace, MetricName, StartTime, EndTime, and Period are required",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	startTime := parseEpochSeconds(in.StartTime)
	endTime := parseEpochSeconds(in.EndTime)
	if endTime.Before(startTime) {
		return nil, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "EndTime must be after StartTime",
			HTTPStatus: http.StatusBadRequest,
		}
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
	points, err := s.store.mergedMetricDataPoints(ctx, in.Namespace, in.MetricName, dimensions, startTime, endTime)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	buckets := aggregateMetricBuckets(points, startTime.UTC(), endTime.UTC(), in.Period)

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

	return &getMetricStatisticsResponse{Label: in.MetricName, Datapoints: datapoints}, nil
}

// ─── GetMetricData ────────────────────────────────────────────
//
// Real CloudWatch diverges from its own Query encoding here in three ways the
// structured protocols share and #1169 first bridged: MetricDataQueries is an
// array of structures rather than MetricDataQueries.member.N; StartTime,
// EndTime and Timestamps are epoch-second numbers rather than ISO-8601
// strings; and MetricDataResults is an array of structures rather than Query's
// parallel Timestamps/Values member lists. The evaluation itself is
// computeMetricDataResults, shared with the Query handler.

type metricJSON struct {
	Namespace  string      `json:"Namespace"`
	MetricName string      `json:"MetricName"`
	Dimensions []Dimension `json:"Dimensions,omitempty"`
}

type metricStatJSON struct {
	Metric metricJSON `json:"Metric"`
	Period int        `json:"Period"`
	Stat   string     `json:"Stat"`
	Unit   string     `json:"Unit,omitempty"`
}

type metricDataQueryJSON struct {
	Id         string          `json:"Id"`
	MetricStat *metricStatJSON `json:"MetricStat,omitempty"`
	Expression string          `json:"Expression,omitempty"`
	Label      string          `json:"Label,omitempty"`
}

type getMetricDataRequest struct {
	MetricDataQueries []metricDataQueryJSON `json:"MetricDataQueries"`
	StartTime         float64               `json:"StartTime"`
	EndTime           float64               `json:"EndTime"`
	ScanBy            string                `json:"ScanBy"`
}

type metricDataResultJSON struct {
	Id         string    `json:"Id"`
	Label      string    `json:"Label"`
	Timestamps []float64 `json:"Timestamps"`
	Values     []float64 `json:"Values"`
	StatusCode string    `json:"StatusCode"`
}

type getMetricDataResponse struct {
	MetricDataResults []metricDataResultJSON `json:"MetricDataResults"`
}

func (s *Service) getMetricDataCore(ctx context.Context, in *getMetricDataRequest) (*getMetricDataResponse, *protocol.AWSError) {
	if in.StartTime == 0 || in.EndTime == 0 {
		return nil, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "StartTime and EndTime are required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	startTime := parseEpochSeconds(in.StartTime)
	endTime := parseEpochSeconds(in.EndTime)

	scanBy := in.ScanBy
	if scanBy == "" {
		scanBy = "TimestampDescending"
	}

	errIncompleteQuery := &protocol.AWSError{
		Code:       "InvalidParameterValue",
		Message:    "Each query must include MetricStat with Metric, Period, and Stat",
		HTTPStatus: http.StatusBadRequest,
	}

	queries := make([]metricDataQueryInput, 0, len(in.MetricDataQueries))
	for _, q := range in.MetricDataQueries {
		mdq := metricDataQueryInput{id: q.Id, expression: q.Expression}
		if mdq.expression == "" {
			if q.MetricStat == nil {
				return nil, errIncompleteQuery
			}
			mdq.namespace = q.MetricStat.Metric.Namespace
			mdq.metricName = q.MetricStat.Metric.MetricName
			mdq.period = q.MetricStat.Period
			mdq.stat = q.MetricStat.Stat
			if mdq.stat == "" {
				mdq.stat = "Average"
			}
			mdq.dimensions = canonicalizeDimensions(q.MetricStat.Metric.Dimensions)
			if mdq.namespace == "" || mdq.metricName == "" || mdq.period <= 0 {
				return nil, errIncompleteQuery
			}
		}
		queries = append(queries, mdq)
	}

	dataResults, aerr := s.computeMetricDataResults(ctx, queries, startTime, endTime, scanBy)
	if aerr != nil {
		return nil, aerr
	}

	out := make([]metricDataResultJSON, 0, len(dataResults))
	for _, res := range dataResults {
		timestamps := make([]float64, len(res.timestamps))
		for i, t := range res.timestamps {
			timestamps[i] = epochSeconds(t)
		}
		out = append(out, metricDataResultJSON{
			Id:         res.id,
			Label:      res.label,
			Timestamps: timestamps,
			Values:     res.values,
			StatusCode: "Complete",
		})
	}

	return &getMetricDataResponse{MetricDataResults: out}, nil
}

// ─── DescribeAlarmHistory ─────────────────────────────────────

type describeAlarmHistoryRequest struct {
	AlarmName       string   `json:"AlarmName"`
	HistoryItemType string   `json:"HistoryItemType"`
	StartDate       *float64 `json:"StartDate"`
	EndDate         *float64 `json:"EndDate"`
	MaxRecords      int      `json:"MaxRecords"`
	ScanBy          string   `json:"ScanBy"`
}

type historyItemJSON struct {
	AlarmName       string  `json:"AlarmName"`
	AlarmType       string  `json:"AlarmType"`
	Timestamp       float64 `json:"Timestamp"`
	HistoryItemType string  `json:"HistoryItemType"`
	HistorySummary  string  `json:"HistorySummary"`
	HistoryData     string  `json:"HistoryData,omitempty"`
}

type describeAlarmHistoryResponse struct {
	AlarmHistoryItems []historyItemJSON `json:"AlarmHistoryItems"`
}

func (s *Service) describeAlarmHistoryCore(ctx context.Context, in *describeAlarmHistoryRequest) (*describeAlarmHistoryResponse, *protocol.AWSError) {
	q := historyQuery{
		AlarmName:       in.AlarmName,
		HistoryItemType: in.HistoryItemType,
		MaxRecords:      in.MaxRecords,
		ScanBy:          in.ScanBy,
	}
	if in.StartDate != nil {
		q.StartDate = parseEpochSeconds(*in.StartDate)
	}
	if in.EndDate != nil {
		q.EndDate = parseEpochSeconds(*in.EndDate)
	}

	items, err := s.store.listAlarmHistory(ctx, in.AlarmName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	filtered := q.filter(items)
	out := make([]historyItemJSON, 0, len(filtered))
	for _, item := range filtered {
		out = append(out, historyItemJSON{
			AlarmName:       item.AlarmName,
			AlarmType:       item.AlarmType,
			Timestamp:       epochSeconds(item.Timestamp),
			HistoryItemType: item.HistoryItemType,
			HistorySummary:  item.HistorySummary,
			HistoryData:     item.HistoryData,
		})
	}
	return &describeAlarmHistoryResponse{AlarmHistoryItems: out}, nil
}

// ─── Tagging ──────────────────────────────────────────────────

type listTagsForResourceRequest struct {
	ResourceARN string `json:"ResourceARN"`
}

type listTagsForResourceResponse struct {
	Tags []Tag `json:"Tags"`
}

func (s *Service) listTagsForResourceCore(ctx context.Context, in *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	if aerr := s.classifyTagResource(ctx, in.ResourceARN).jsonError(in.ResourceARN); aerr != nil {
		return nil, aerr
	}
	tags, aerr := s.resourceTags(ctx, in.ResourceARN)
	if aerr != nil {
		return nil, aerr
	}
	return &listTagsForResourceResponse{Tags: sortedTags(tags)}, nil
}

type tagResourceRequest struct {
	ResourceARN string `json:"ResourceARN"`
	Tags        []Tag  `json:"Tags"`
}

func (s *Service) tagResourceCore(ctx context.Context, in *tagResourceRequest) (*emptyResponse, *protocol.AWSError) {
	if aerr := s.classifyTagResource(ctx, in.ResourceARN).jsonError(in.ResourceARN); aerr != nil {
		return nil, aerr
	}
	if aerr := s.addResourceTags(ctx, in.ResourceARN, in.Tags, jsonTagCfg); aerr != nil {
		return nil, aerr
	}
	return &emptyResponse{}, nil
}

type untagResourceRequest struct {
	ResourceARN string   `json:"ResourceARN"`
	TagKeys     []string `json:"TagKeys"`
}

func (s *Service) untagResourceCore(ctx context.Context, in *untagResourceRequest) (*emptyResponse, *protocol.AWSError) {
	if aerr := s.classifyTagResource(ctx, in.ResourceARN).jsonError(in.ResourceARN); aerr != nil {
		return nil, aerr
	}
	if aerr := s.removeResourceTags(ctx, in.ResourceARN, in.TagKeys); aerr != nil {
		return nil, aerr
	}
	return &emptyResponse{}, nil
}
