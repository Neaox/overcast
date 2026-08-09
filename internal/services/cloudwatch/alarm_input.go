package cloudwatch

// PutMetricAlarm input parsing and validation.
//
// The two wire protocols CloudWatch speaks (Query form values and the JSON
// 1.0 target protocol) parse into one alarmInput, so validation — and in
// particular the §2.1 refusals — cannot drift between them.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

// alarmInput is a protocol-independent PutMetricAlarm request.
type alarmInput struct {
	AlarmName         string
	AlarmDescription  string
	MetricName        string
	Namespace         string
	Statistic         string
	ExtendedStatistic string
	Dimensions        []Dimension
	Period            int
	Unit              string
	EvaluationPeriods int
	DatapointsToAlarm int
	// Threshold is a pointer because 0 is a legitimate threshold ("greater
	// than zero errors" is the commonest alarm there is), so a plain float64
	// cannot tell an explicit 0 from an omitted parameter.
	Threshold               *float64
	ComparisonOperator      string
	TreatMissingData        string
	ActionsEnabled          *bool
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string

	// Tags are applied when the alarm is created. AWS ignores them on a
	// PutMetricAlarm that updates an existing alarm — TagResource is the
	// only way to change an existing alarm's tags.
	Tags []Tag

	// ThresholdMetricID is set for an anomaly-detection alarm, and
	// hasMetrics for a metric-math or multi-metric alarm. Both are refused —
	// they are recorded here only so the refusal can name what it saw.
	ThresholdMetricID string
	hasMetrics        bool

	// hasEvaluationCriteria marks a PromQL alarm (the EvaluationCriteria
	// union). Refused, for the same reason as the rest: there is no PromQL
	// engine behind the emulator.
	hasEvaluationCriteria bool
}

// Tag is a CloudWatch resource tag.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// errUnsupportedAlarm builds the 501 used for an alarm configuration Overcast
// will not pretend to evaluate. It carries the same
// `x-emulator-unsupported: true` header every other 501 does — set by the
// protocol writers, never here.
func errUnsupportedAlarm(what, detail string) *protocol.AWSError {
	return &protocol.AWSError{
		Code: "NotImplemented",
		Message: fmt.Sprintf("%s is not emulated by Overcast, and this alarm is refused rather than created in a state that looks armed but is never evaluated. %s "+
			"See docs/services/cloudwatch.md for what alarm configurations are evaluated.", what, detail),
		HTTPStatus: http.StatusNotImplemented,
	}
}

// errValidation builds the ValidationError real CloudWatch returns for a
// request that AWS itself would reject.
func errValidation(message string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ValidationError", Message: message, HTTPStatus: http.StatusBadRequest}
}

// validate checks the request the way real CloudWatch does, and refuses —
// loudly, with a 501 — every alarm shape this evaluator cannot actually
// decide. See docs/plans/full-emulation-priority.md §2.1: an alarm that looks
// armed but is never evaluated is worse than no alarm at all.
func (in *alarmInput) validate() *protocol.AWSError {
	if in.AlarmName == "" {
		return errValidation("1 validation error detected: Value null at 'alarmName' failed to satisfy constraint: Member must not be null")
	}

	// ---- §2.1 refusals: configurations that cannot be evaluated ----------

	if in.hasMetrics {
		return errUnsupportedAlarm("Metric-math and multi-metric alarms (the Metrics parameter)",
			"Only single-metric alarms — Namespace + MetricName + Statistic — are evaluated.")
	}
	if in.hasEvaluationCriteria {
		return errUnsupportedAlarm("PromQL alarms (the EvaluationCriteria parameter)",
			"There is no PromQL engine behind the emulator. Only single-metric alarms — Namespace + MetricName + Statistic — are evaluated.")
	}
	if in.ThresholdMetricID != "" {
		return errUnsupportedAlarm("Anomaly-detection alarms (ThresholdMetricId)",
			"There is no anomaly-detection model behind the emulator, so no band can be computed.")
	}
	if in.ExtendedStatistic != "" {
		return errUnsupportedAlarm("Extended statistics such as "+in.ExtendedStatistic,
			"PutMetricData stores pre-aggregated statistic values, so percentiles cannot be computed faithfully. Use Average, Sum, SampleCount, Minimum or Maximum.")
	}
	if anomalyComparisonOperators[in.ComparisonOperator] {
		return errUnsupportedAlarm("The comparison operator "+in.ComparisonOperator,
			"It only has meaning against an anomaly-detection band, which is not emulated.")
	}

	// ---- Validation real CloudWatch performs -----------------------------

	if in.Namespace == "" || in.MetricName == "" {
		return errValidation("The parameter MetricName and the parameter Namespace must both be specified for a metric alarm")
	}
	if in.Statistic != "" && !supportedStatistics[in.Statistic] {
		return errValidation(fmt.Sprintf("1 validation error detected: Value '%s' at 'statistic' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [Maximum, SampleCount, Sum, Minimum, Average]", in.Statistic))
	}
	if in.ComparisonOperator != "" && comparisonPhrases[in.ComparisonOperator] == "" {
		return errValidation(fmt.Sprintf("1 validation error detected: Value '%s' at 'comparisonOperator' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [GreaterThanOrEqualToThreshold, GreaterThanThreshold, LessThanThreshold, LessThanOrEqualToThreshold]", in.ComparisonOperator))
	}
	if in.TreatMissingData != "" && !treatMissingDataModes[in.TreatMissingData] {
		return errValidation(fmt.Sprintf("Invalid treat missing data %s. Valid values are: missing, ignore, breaching, notBreaching", in.TreatMissingData))
	}
	if in.Period != 0 && !validAlarmPeriod(in.Period) {
		return errValidation("Period must be 10, 20, 30, or a multiple of 60")
	}
	if in.EvaluationPeriods < 0 {
		return errValidation("EvaluationPeriods must be greater than zero")
	}
	if in.DatapointsToAlarm < 0 {
		return errValidation("DatapointsToAlarm must be greater than zero")
	}
	if in.DatapointsToAlarm > 0 && in.EvaluationPeriods > 0 && in.DatapointsToAlarm > in.EvaluationPeriods {
		return errValidation("DatapointsToAlarm cannot be greater than EvaluationPeriods")
	}

	// ---- Optional on the wire, required for *this* alarm shape -----------
	//
	// These five are marked "Required: No" on PutMetricAlarm only because an
	// EvaluationCriteria (PromQL) alarm defines them inside that structure
	// instead. For an alarm on a metric they are required, and AWS rejects the
	// request without them. Overcast used to substitute Average /
	// GreaterThanThreshold / 60s / 1 period / 0.0, which does not fail — it
	// arms an alarm nobody configured, and "greater than 0.0 on the Average"
	// is a different alarm from the one the caller half-described.
	if in.Statistic == "" {
		return errValidation("The parameter Statistic or the parameter ExtendedStatistic must be specified for a metric alarm")
	}
	if in.ComparisonOperator == "" {
		return errValidation("1 validation error detected: Value null at 'comparisonOperator' failed to satisfy constraint: Member must not be null")
	}
	if in.Period == 0 {
		return errValidation("The parameter Period must be specified for a metric alarm")
	}
	if in.EvaluationPeriods == 0 {
		return errValidation("1 validation error detected: Value null at 'evaluationPeriods' failed to satisfy constraint: Member must not be null")
	}
	if in.Threshold == nil {
		return errValidation("The parameter Threshold must be specified for an alarm based on a static threshold")
	}
	return nil
}

// validAlarmPeriod mirrors AWS's accepted periods: the three high-resolution
// values, or any multiple of a minute.
func validAlarmPeriod(period int) bool {
	switch period {
	case 10, 20, 30:
		return true
	}
	return period > 0 && period%60 == 0
}

// toAlarm builds the persisted alarm from a validated input, applying AWS's
// defaults. previous carries the alarm this call replaces, if any, so that
// PutMetricAlarm on an existing alarm keeps its current state instead of
// resetting it — matching AWS, which only resets state when the alarm is new.
func (in *alarmInput) toAlarm(arn string, now time.Time, previous *MetricAlarm) *MetricAlarm {
	alarm := &MetricAlarm{
		AlarmName:                          in.AlarmName,
		AlarmArn:                           arn,
		AlarmDescription:                   in.AlarmDescription,
		MetricName:                         in.MetricName,
		Namespace:                          in.Namespace,
		Statistic:                          in.Statistic,
		Dimensions:                         canonicalizeDimensions(in.Dimensions),
		Unit:                               in.Unit,
		Period:                             in.Period,
		EvaluationPeriods:                  in.EvaluationPeriods,
		DatapointsToAlarm:                  in.DatapointsToAlarm,
		Threshold:                          *in.Threshold,
		ComparisonOperator:                 in.ComparisonOperator,
		TreatMissingData:                   in.TreatMissingData,
		ActionsEnabled:                     true,
		AlarmActions:                       in.AlarmActions,
		OKActions:                          in.OKActions,
		InsufficientDataActions:            in.InsufficientDataActions,
		AlarmConfigurationUpdatedTimestamp: now.Format(time.RFC3339),
	}
	// ActionsEnabled is the one property AWS documents a default for that the
	// caller can also switch off, so it is the one that needs a pointer.
	// Statistic, ComparisonOperator, Period, EvaluationPeriods and Threshold
	// are not defaulted here — validate rejects an alarm that omits them
	// rather than inventing a configuration (see validate).
	if in.ActionsEnabled != nil {
		alarm.ActionsEnabled = *in.ActionsEnabled
	}

	if previous != nil {
		alarm.StateValue = previous.StateValue
		alarm.StateReason = previous.StateReason
		alarm.StateReasonData = previous.StateReasonData
		alarm.StateUpdatedTimestamp = previous.StateUpdatedTimestamp
		alarm.StateTransitionedTimestamp = previous.StateTransitionedTimestamp
		return alarm
	}
	alarm.StateValue = stateInsufficientData
	alarm.StateReason = "Unchecked: Initial alarm creation"
	alarm.StateUpdatedTimestamp = now.Format(time.RFC3339)
	return alarm
}

// ─── Query-protocol parsing ───────────────────────────────────────

// alarmInputFromForm reads a PutMetricAlarm request from Query form values.
func alarmInputFromForm(r *http.Request) alarmInput {
	in := alarmInput{
		AlarmName:               r.FormValue("AlarmName"),
		AlarmDescription:        r.FormValue("AlarmDescription"),
		MetricName:              r.FormValue("MetricName"),
		Namespace:               r.FormValue("Namespace"),
		Statistic:               r.FormValue("Statistic"),
		ExtendedStatistic:       r.FormValue("ExtendedStatistic"),
		Unit:                    r.FormValue("Unit"),
		Period:                  parseIntDefault(r.FormValue("Period"), 0),
		EvaluationPeriods:       parseIntDefault(r.FormValue("EvaluationPeriods"), 0),
		DatapointsToAlarm:       parseIntDefault(r.FormValue("DatapointsToAlarm"), 0),
		ComparisonOperator:      r.FormValue("ComparisonOperator"),
		TreatMissingData:        r.FormValue("TreatMissingData"),
		ThresholdMetricID:       r.FormValue("ThresholdMetricId"),
		Dimensions:              memberDimensions(r, "Dimensions"),
		AlarmActions:            memberList(r, "AlarmActions"),
		OKActions:               memberList(r, "OKActions"),
		InsufficientDataActions: memberList(r, "InsufficientDataActions"),
		Tags:                    memberTags(r, "Tags"),
		hasMetrics:              r.FormValue("Metrics.member.1.Id") != "",
		hasEvaluationCriteria:   hasFormPrefix(r, "EvaluationCriteria."),
	}
	if raw := r.FormValue("Threshold"); raw != "" {
		threshold := parseFloatDefault(raw, 0)
		in.Threshold = &threshold
	}
	if raw := r.FormValue("ActionsEnabled"); raw != "" {
		enabled := raw != "false"
		in.ActionsEnabled = &enabled
	}
	return in
}

// hasFormPrefix reports whether any form key starts with prefix, which is how
// a nested Query-protocol structure announces itself — the union member that
// was set is part of the key, so there is no single key to look for.
//
// It parses the form itself rather than assuming an earlier FormValue call
// already did: r.Form is nil until something populates it, and reading it
// directly would otherwise depend on evaluation order within the caller's
// struct literal. ParseForm is idempotent.
func hasFormPrefix(r *http.Request, prefix string) bool {
	_ = r.ParseForm()
	for key := range r.Form {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// memberList reads an AWS Query flattened list (`Name.member.N`).
func memberList(r *http.Request, name string) []string {
	var out []string
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s.member.%d", name, i))
		if v == "" {
			return out
		}
		out = append(out, v)
	}
}

// memberTags reads an AWS Query flattened tag list. A tag with an empty value
// is legal, so the key alone terminates the list.
func memberTags(r *http.Request, name string) []Tag {
	var out []Tag
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("%s.member.%d.Key", name, i))
		if key == "" {
			return out
		}
		out = append(out, Tag{Key: key, Value: r.FormValue(fmt.Sprintf("%s.member.%d.Value", name, i))})
	}
}

// memberDimensions reads an AWS Query flattened dimension list.
func memberDimensions(r *http.Request, name string) []Dimension {
	var out []Dimension
	for i := 1; ; i++ {
		dimName := r.FormValue(fmt.Sprintf("%s.member.%d.Name", name, i))
		if dimName == "" {
			return out
		}
		out = append(out, Dimension{Name: dimName, Value: r.FormValue(fmt.Sprintf("%s.member.%d.Value", name, i))})
	}
}

// ─── JSON-protocol parsing ────────────────────────────────────────

// putMetricAlarmJSONBody is the JSON 1.0 shape of PutMetricAlarm.
type putMetricAlarmJSONBody struct {
	AlarmName               string      `json:"AlarmName"`
	AlarmDescription        string      `json:"AlarmDescription"`
	MetricName              string      `json:"MetricName"`
	Namespace               string      `json:"Namespace"`
	Statistic               string      `json:"Statistic"`
	ExtendedStatistic       string      `json:"ExtendedStatistic"`
	Dimensions              []Dimension `json:"Dimensions"`
	Unit                    string      `json:"Unit"`
	Period                  int         `json:"Period"`
	EvaluationPeriods       int         `json:"EvaluationPeriods"`
	DatapointsToAlarm       int         `json:"DatapointsToAlarm"`
	Threshold               *float64    `json:"Threshold"`
	ComparisonOperator      string      `json:"ComparisonOperator"`
	TreatMissingData        string      `json:"TreatMissingData"`
	ActionsEnabled          *bool       `json:"ActionsEnabled"`
	AlarmActions            []string    `json:"AlarmActions"`
	OKActions               []string    `json:"OKActions"`
	InsufficientDataActions []string    `json:"InsufficientDataActions"`
	ThresholdMetricID       string      `json:"ThresholdMetricId"`
	Metrics                 []any       `json:"Metrics"`
	Tags                    []Tag       `json:"Tags"`
	EvaluationCriteria      any         `json:"EvaluationCriteria"`
}

// toInput converts the JSON body into the shared input type.
func (b *putMetricAlarmJSONBody) toInput() alarmInput {
	return alarmInput{
		AlarmName:               b.AlarmName,
		AlarmDescription:        b.AlarmDescription,
		MetricName:              b.MetricName,
		Namespace:               b.Namespace,
		Statistic:               b.Statistic,
		ExtendedStatistic:       b.ExtendedStatistic,
		Dimensions:              b.Dimensions,
		Unit:                    b.Unit,
		Period:                  b.Period,
		EvaluationPeriods:       b.EvaluationPeriods,
		DatapointsToAlarm:       b.DatapointsToAlarm,
		Threshold:               b.Threshold,
		ComparisonOperator:      b.ComparisonOperator,
		TreatMissingData:        b.TreatMissingData,
		ActionsEnabled:          b.ActionsEnabled,
		AlarmActions:            b.AlarmActions,
		OKActions:               b.OKActions,
		InsufficientDataActions: b.InsufficientDataActions,
		ThresholdMetricID:       b.ThresholdMetricID,
		Tags:                    b.Tags,
		hasMetrics:              len(b.Metrics) > 0,
		hasEvaluationCriteria:   b.EvaluationCriteria != nil,
	}
}

// ─── DescribeAlarmHistory input ───────────────────────────────────

// historyQuery is a protocol-independent DescribeAlarmHistory request.
type historyQuery struct {
	AlarmName       string
	HistoryItemType string
	StartDate       time.Time
	EndDate         time.Time
	MaxRecords      int
	ScanBy          string
}

// filter applies the query to a time-ordered (oldest first) history list and
// returns the page AWS would return for it.
func (q historyQuery) filter(items []AlarmHistoryItem) []AlarmHistoryItem {
	out := make([]AlarmHistoryItem, 0, len(items))
	for _, item := range items {
		if q.HistoryItemType != "" && item.HistoryItemType != q.HistoryItemType {
			continue
		}
		if !q.StartDate.IsZero() && item.Timestamp.Before(q.StartDate) {
			continue
		}
		if !q.EndDate.IsZero() && item.Timestamp.After(q.EndDate) {
			continue
		}
		out = append(out, item)
	}
	// AWS defaults to TimestampDescending.
	if !strings.EqualFold(q.ScanBy, "TimestampAscending") {
		for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
			out[l], out[r] = out[r], out[l]
		}
	}
	if q.MaxRecords > 0 && len(out) > q.MaxRecords {
		out = out[:q.MaxRecords]
	}
	return out
}

// historyQueryFromForm reads DescribeAlarmHistory from Query form values.
func historyQueryFromForm(r *http.Request) historyQuery {
	q := historyQuery{
		AlarmName:       r.FormValue("AlarmName"),
		HistoryItemType: r.FormValue("HistoryItemType"),
		MaxRecords:      parseIntDefault(r.FormValue("MaxRecords"), 0),
		ScanBy:          r.FormValue("ScanBy"),
	}
	if t, ok := parseCWTime(r.FormValue("StartDate")); ok {
		q.StartDate = t
	}
	if t, ok := parseCWTime(r.FormValue("EndDate")); ok {
		q.EndDate = t
	}
	return q
}
