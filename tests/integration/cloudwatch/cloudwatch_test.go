// Package cloudwatch_test contains integration tests for the CloudWatch emulator.
//
// Run: go test ./tests/integration/cloudwatch/...
package cloudwatch_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/tests/helpers"
)

// cwCall performs a CloudWatch Query-protocol request.
func cwCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2010-08-01")
	body := strings.NewReader(params.Encode())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	if err != nil {
		t.Fatalf("cwCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cwCall: do: %v", err)
	}
	return resp
}

// cwTargetCall performs a CloudWatch JSON protocol request (AWS SDK shape).
func cwTargetCall(t *testing.T, srv *helpers.TestServer, action string, payload any) *http.Response {
	t.Helper()

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("cwTargetCall: marshal: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("cwTargetCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801."+action)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cwTargetCall: do: %v", err)
	}
	return resp
}

func putAlarm(t *testing.T, srv *helpers.TestServer, name string) *http.Response {
	t.Helper()
	return cwCall(t, srv, "PutMetricAlarm", url.Values{
		"AlarmName":          {name},
		"Namespace":          {"TestNS"},
		"MetricName":         {"TestMetric"},
		"ComparisonOperator": {"GreaterThanThreshold"},
		"EvaluationPeriods":  {"1"},
		"Threshold":          {"100"},
		"Period":             {"60"},
		"Statistic":          {"Average"},
	})
}

// ─── PutMetricAlarm ───────────────────────────────────────────────────────────

func TestPutMetricAlarm_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: PutMetricAlarm is called
	resp := putAlarm(t, srv, "test-alarm")
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── DescribeAlarms ───────────────────────────────────────────────────────────

func TestDescribeAlarms_afterPut(t *testing.T) {
	// Given: an alarm has been created
	srv := helpers.NewTestServer(t)
	cr := putAlarm(t, srv, "test-alarm")
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: DescribeAlarms is called
	resp := cwCall(t, srv, "DescribeAlarms", nil)
	defer resp.Body.Close()

	// Then: 200 and the alarm is listed with UI-relevant metadata
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<AlarmName>test-alarm</AlarmName>") {
		t.Fatalf("expected alarm name in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Threshold>100</Threshold>") {
		t.Fatalf("expected threshold in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<EvaluationPeriods>1</EvaluationPeriods>") {
		t.Fatalf("expected evaluation periods in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<StateUpdatedTimestamp>") {
		t.Fatalf("expected state updated timestamp in response, got: %s", xml)
	}
}

// ─── DeleteAlarms ─────────────────────────────────────────────────────────────

func TestDeleteAlarms_success(t *testing.T) {
	// Given: an alarm exists
	srv := helpers.NewTestServer(t)
	cr := putAlarm(t, srv, "to-delete")
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: DeleteAlarms is called
	del := cwCall(t, srv, "DeleteAlarms", url.Values{
		"AlarmNames.member.1": {"to-delete"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: DescribeAlarms returns no alarms
	resp := cwCall(t, srv, "DescribeAlarms", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── GetMetricStatistics ─────────────────────────────────────────────────────

func TestGetMetricStatistics_afterPutMetricData(t *testing.T) {
	// Given: datapoints are published for a metric
	base := time.Now().UTC().Truncate(time.Second)
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Timestamp":  {base.Add(-50 * time.Second).Format(time.RFC3339)},
		"MetricData.member.1.Value":      {"40"},
		"MetricData.member.1.Unit":       {"Percent"},
		"MetricData.member.2.MetricName": {"CPUUtilization"},
		"MetricData.member.2.Timestamp":  {base.Add(-40 * time.Second).Format(time.RFC3339)},
		"MetricData.member.2.Value":      {"60"},
		"MetricData.member.2.Unit":       {"Percent"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricStatistics is requested over that window
	resp := cwCall(t, srv, "GetMetricStatistics", url.Values{
		"Namespace":           {"TestNS"},
		"MetricName":          {"CPUUtilization"},
		"StartTime":           {base.Add(-1 * time.Minute).Format(time.RFC3339)},
		"EndTime":             {base.Add(1 * time.Minute).Format(time.RFC3339)},
		"Period":              {"60"},
		"Statistics.member.1": {"Average"},
		"Statistics.member.2": {"Sum"},
		"Statistics.member.3": {"SampleCount"},
	})
	defer resp.Body.Close()

	// Then: one aggregate datapoint is returned with expected values
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<Average>50</Average>") {
		t.Fatalf("expected average=50 in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Sum>100</Sum>") {
		t.Fatalf("expected sum=100 in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<SampleCount>2</SampleCount>") {
		t.Fatalf("expected samplecount=2 in response, got: %s", xml)
	}
}

func TestGetMetricStatistics_dimensionOrderInsensitive(t *testing.T) {
	// Given: datapoints are published with dimensions in one order
	base := time.Now().UTC().Truncate(time.Second)
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                                     {"TestNS"},
		"MetricData.member.1.MetricName":                {"RequestCount"},
		"MetricData.member.1.Timestamp":                 {base.Add(-30 * time.Second).Format(time.RFC3339)},
		"MetricData.member.1.Value":                     {"10"},
		"MetricData.member.1.Dimensions.member.1.Name":  {"Service"},
		"MetricData.member.1.Dimensions.member.1.Value": {"api"},
		"MetricData.member.1.Dimensions.member.2.Name":  {"Env"},
		"MetricData.member.1.Dimensions.member.2.Value": {"dev"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricStatistics is requested with reversed dimension order
	resp := cwCall(t, srv, "GetMetricStatistics", url.Values{
		"Namespace":                 {"TestNS"},
		"MetricName":                {"RequestCount"},
		"StartTime":                 {base.Add(-1 * time.Minute).Format(time.RFC3339)},
		"EndTime":                   {base.Add(1 * time.Minute).Format(time.RFC3339)},
		"Period":                    {"60"},
		"Statistics.member.1":       {"Sum"},
		"Dimensions.member.1.Name":  {"Env"},
		"Dimensions.member.1.Value": {"dev"},
		"Dimensions.member.2.Name":  {"Service"},
		"Dimensions.member.2.Value": {"api"},
	})
	defer resp.Body.Close()

	// Then: datapoints still match and are returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "<Sum>10</Sum>") {
		t.Fatalf("expected sum datapoint in response, got: %s", string(body))
	}
}

func TestListMetrics_includesDimensions(t *testing.T) {
	// Given: a metric has been published with dimensions
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                                     {"TestNS"},
		"MetricData.member.1.MetricName":                {"RequestCount"},
		"MetricData.member.1.Value":                     {"10"},
		"MetricData.member.1.Dimensions.member.1.Name":  {"Service"},
		"MetricData.member.1.Dimensions.member.1.Value": {"api"},
		"MetricData.member.1.Dimensions.member.2.Name":  {"Env"},
		"MetricData.member.1.Dimensions.member.2.Value": {"dev"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: ListMetrics is requested for that namespace
	resp := cwCall(t, srv, "ListMetrics", url.Values{"Namespace": {"TestNS"}})
	defer resp.Body.Close()

	// Then: the metric entry includes its dimensions
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<MetricName>RequestCount</MetricName>") {
		t.Fatalf("expected metric name in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Name>Env</Name>") || !strings.Contains(xml, "<Value>dev</Value>") {
		t.Fatalf("expected Env dimension in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Name>Service</Name>") || !strings.Contains(xml, "<Value>api</Value>") {
		t.Fatalf("expected Service dimension in response, got: %s", xml)
	}
}

func TestListMetrics_jsonTargetCompatibility(t *testing.T) {
	// Given: a metric has been published using Query protocol.
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                                     {"CompatNS"},
		"MetricData.member.1.MetricName":                {"RequestCount"},
		"MetricData.member.1.Value":                     {"5"},
		"MetricData.member.1.Dimensions.member.1.Name":  {"Service"},
		"MetricData.member.1.Dimensions.member.1.Value": {"web"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: ListMetrics is called via JSON target protocol (AWS SDK wire format).
	resp := cwTargetCall(t, srv, "ListMetrics", map[string]any{"Namespace": "CompatNS"})
	defer resp.Body.Close()

	// Then: request succeeds and returns JSON metrics payload.
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	jsonBody := string(body)
	if !strings.Contains(jsonBody, "\"Metrics\"") {
		t.Fatalf("expected Metrics array in response, got: %s", jsonBody)
	}
	if !strings.Contains(jsonBody, "\"MetricName\":\"RequestCount\"") {
		t.Fatalf("expected metric name in response, got: %s", jsonBody)
	}
}

func TestGetMetricStatistics_memoryRetentionWindow(t *testing.T) {
	// Given: memory-mode CloudWatch datapoints include one outside the retention window
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	base := srv.Clock.Now().UTC()

	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Timestamp":  {base.Add(-2 * time.Hour).Format(time.RFC3339)},
		"MetricData.member.1.Value":      {"100"},
		"MetricData.member.2.MetricName": {"CPUUtilization"},
		"MetricData.member.2.Timestamp":  {base.Add(-30 * time.Minute).Format(time.RFC3339)},
		"MetricData.member.2.Value":      {"40"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricStatistics reads over a range that covers both timestamps
	resp := cwCall(t, srv, "GetMetricStatistics", url.Values{
		"Namespace":           {"TestNS"},
		"MetricName":          {"CPUUtilization"},
		"StartTime":           {base.Add(-3 * time.Hour).Format(time.RFC3339)},
		"EndTime":             {base.Format(time.RFC3339)},
		"Period":              {"3600"},
		"Statistics.member.1": {"Sum"},
	})
	defer resp.Body.Close()

	// Then: only the retained datapoint contributes to aggregates
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<Sum>40</Sum>") {
		t.Fatalf("expected memory retention to keep only recent datapoint, got: %s", xml)
	}
	if strings.Contains(xml, "<Sum>100</Sum>") || strings.Contains(xml, "<Sum>140</Sum>") {
		t.Fatalf("expected expired datapoint to be excluded in memory mode, got: %s", xml)
	}
}

func TestGetMetricStatistics_persistentDoesNotEnforceMemoryRetention(t *testing.T) {
	// Given: CloudWatch is configured with a persistent service backend
	srv := helpers.NewTestServer(
		t,
		helpers.WithMockClock(),
		helpers.WithServiceStates(map[string]config.StateBackend{"cloudwatch": config.StateBackendPersistent}),
	)
	base := srv.Clock.Now().UTC()

	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Timestamp":  {base.Add(-2 * time.Hour).Format(time.RFC3339)},
		"MetricData.member.1.Value":      {"100"},
		"MetricData.member.2.MetricName": {"CPUUtilization"},
		"MetricData.member.2.Timestamp":  {base.Add(-30 * time.Minute).Format(time.RFC3339)},
		"MetricData.member.2.Value":      {"40"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricStatistics reads over the full range
	resp := cwCall(t, srv, "GetMetricStatistics", url.Values{
		"Namespace":           {"TestNS"},
		"MetricName":          {"CPUUtilization"},
		"StartTime":           {base.Add(-3 * time.Hour).Format(time.RFC3339)},
		"EndTime":             {base.Format(time.RFC3339)},
		"Period":              {"3600"},
		"Statistics.member.1": {"Sum"},
	})
	defer resp.Body.Close()

	// Then: both datapoints remain visible for durable backends
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<Sum>100</Sum>") || !strings.Contains(xml, "<Sum>40</Sum>") {
		t.Fatalf("expected both datapoints to be returned in persistent mode, got: %s", xml)
	}
}

func TestGetMetricData_afterPutMetricData(t *testing.T) {
	// Given: datapoints are published for a metric
	base := time.Now().UTC().Truncate(time.Second)
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Timestamp":  {base.Add(-50 * time.Second).Format(time.RFC3339)},
		"MetricData.member.1.Value":      {"40"},
		"MetricData.member.2.MetricName": {"CPUUtilization"},
		"MetricData.member.2.Timestamp":  {base.Add(-40 * time.Second).Format(time.RFC3339)},
		"MetricData.member.2.Value":      {"60"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricData requests an aggregated query
	resp := cwCall(t, srv, "GetMetricData", url.Values{
		"StartTime":                     {base.Add(-1 * time.Minute).Format(time.RFC3339)},
		"EndTime":                       {base.Add(1 * time.Minute).Format(time.RFC3339)},
		"ScanBy":                        {"TimestampAscending"},
		"MetricDataQueries.member.1.Id": {"q1"},
		"MetricDataQueries.member.1.MetricStat.Metric.Namespace":  {"TestNS"},
		"MetricDataQueries.member.1.MetricStat.Metric.MetricName": {"CPUUtilization"},
		"MetricDataQueries.member.1.MetricStat.Period":            {"60"},
		"MetricDataQueries.member.1.MetricStat.Stat":              {"Average"},
	})
	defer resp.Body.Close()

	// Then: one completed result with expected id and value is returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<Id>q1</Id>") {
		t.Fatalf("expected query id q1 in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Values><member>50</member></Values>") {
		t.Fatalf("expected average value 50 in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<StatusCode>Complete</StatusCode>") {
		t.Fatalf("expected complete status code in response, got: %s", xml)
	}
}

func TestGetMetricData_multipleQueries(t *testing.T) {
	// Given: datapoints are published for two metrics
	base := time.Now().UTC().Truncate(time.Second)
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Timestamp":  {base.Add(-50 * time.Second).Format(time.RFC3339)},
		"MetricData.member.1.Value":      {"40"},
		"MetricData.member.2.MetricName": {"CPUUtilization"},
		"MetricData.member.2.Timestamp":  {base.Add(-40 * time.Second).Format(time.RFC3339)},
		"MetricData.member.2.Value":      {"60"},
		"MetricData.member.3.MetricName": {"RequestCount"},
		"MetricData.member.3.Timestamp":  {base.Add(-50 * time.Second).Format(time.RFC3339)},
		"MetricData.member.3.Value":      {"10"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricData requests both metrics
	resp := cwCall(t, srv, "GetMetricData", url.Values{
		"StartTime":                     {base.Add(-1 * time.Minute).Format(time.RFC3339)},
		"EndTime":                       {base.Add(1 * time.Minute).Format(time.RFC3339)},
		"MetricDataQueries.member.1.Id": {"cpu"},
		"MetricDataQueries.member.1.MetricStat.Metric.Namespace":  {"TestNS"},
		"MetricDataQueries.member.1.MetricStat.Metric.MetricName": {"CPUUtilization"},
		"MetricDataQueries.member.1.MetricStat.Period":            {"60"},
		"MetricDataQueries.member.1.MetricStat.Stat":              {"Sum"},
		"MetricDataQueries.member.2.Id":                           {"req"},
		"MetricDataQueries.member.2.MetricStat.Metric.Namespace":  {"TestNS"},
		"MetricDataQueries.member.2.MetricStat.Metric.MetricName": {"RequestCount"},
		"MetricDataQueries.member.2.MetricStat.Period":            {"60"},
		"MetricDataQueries.member.2.MetricStat.Stat":              {"Sum"},
	})
	defer resp.Body.Close()

	// Then: both query ids and expected sums are present
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<Id>cpu</Id>") || !strings.Contains(xml, "<Id>req</Id>") {
		t.Fatalf("expected both query ids in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Values><member>100</member></Values>") {
		t.Fatalf("expected cpu sum 100 in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Values><member>10</member></Values>") {
		t.Fatalf("expected request sum 10 in response, got: %s", xml)
	}
}

func TestGetMetricData_expressionBinary(t *testing.T) {
	// Given: datapoints are published for two metrics in the same period
	base := time.Now().UTC().Truncate(time.Second)
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Timestamp":  {base.Add(-50 * time.Second).Format(time.RFC3339)},
		"MetricData.member.1.Value":      {"40"},
		"MetricData.member.2.MetricName": {"CPUUtilization"},
		"MetricData.member.2.Timestamp":  {base.Add(-40 * time.Second).Format(time.RFC3339)},
		"MetricData.member.2.Value":      {"60"},
		"MetricData.member.3.MetricName": {"RequestCount"},
		"MetricData.member.3.Timestamp":  {base.Add(-50 * time.Second).Format(time.RFC3339)},
		"MetricData.member.3.Value":      {"10"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricData evaluates m1+m2
	resp := cwCall(t, srv, "GetMetricData", url.Values{
		"StartTime":                     {base.Add(-1 * time.Minute).Format(time.RFC3339)},
		"EndTime":                       {base.Add(1 * time.Minute).Format(time.RFC3339)},
		"ScanBy":                        {"TimestampAscending"},
		"MetricDataQueries.member.1.Id": {"m1"},
		"MetricDataQueries.member.1.MetricStat.Metric.Namespace":  {"TestNS"},
		"MetricDataQueries.member.1.MetricStat.Metric.MetricName": {"CPUUtilization"},
		"MetricDataQueries.member.1.MetricStat.Period":            {"60"},
		"MetricDataQueries.member.1.MetricStat.Stat":              {"Sum"},
		"MetricDataQueries.member.2.Id":                           {"m2"},
		"MetricDataQueries.member.2.MetricStat.Metric.Namespace":  {"TestNS"},
		"MetricDataQueries.member.2.MetricStat.Metric.MetricName": {"RequestCount"},
		"MetricDataQueries.member.2.MetricStat.Period":            {"60"},
		"MetricDataQueries.member.2.MetricStat.Stat":              {"Sum"},
		"MetricDataQueries.member.3.Id":                           {"expr1"},
		"MetricDataQueries.member.3.Expression":                   {"m1+m2"},
	})
	defer resp.Body.Close()

	// Then: expression query contains expected computed value
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<Id>expr1</Id>") {
		t.Fatalf("expected expression query id in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Values><member>110</member></Values>") {
		t.Fatalf("expected expression value 110 in response, got: %s", xml)
	}
}

func TestGetMetricData_expressionFunctions(t *testing.T) {
	// Given: datapoints produce two buckets for the same metric query
	base := time.Now().UTC().Truncate(time.Second)
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Timestamp":  {base.Add(-90 * time.Second).Format(time.RFC3339)},
		"MetricData.member.1.Value":      {"40"},
		"MetricData.member.2.MetricName": {"CPUUtilization"},
		"MetricData.member.2.Timestamp":  {base.Add(-30 * time.Second).Format(time.RFC3339)},
		"MetricData.member.2.Value":      {"60"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: GetMetricData evaluates AVG(m1) and SUM(m1)
	resp := cwCall(t, srv, "GetMetricData", url.Values{
		"StartTime":                     {base.Add(-2 * time.Minute).Format(time.RFC3339)},
		"EndTime":                       {base.Add(1 * time.Minute).Format(time.RFC3339)},
		"ScanBy":                        {"TimestampAscending"},
		"MetricDataQueries.member.1.Id": {"m1"},
		"MetricDataQueries.member.1.MetricStat.Metric.Namespace":  {"TestNS"},
		"MetricDataQueries.member.1.MetricStat.Metric.MetricName": {"CPUUtilization"},
		"MetricDataQueries.member.1.MetricStat.Period":            {"60"},
		"MetricDataQueries.member.1.MetricStat.Stat":              {"Average"},
		"MetricDataQueries.member.2.Id":                           {"avg1"},
		"MetricDataQueries.member.2.Expression":                   {"AVG(m1)"},
		"MetricDataQueries.member.3.Id":                           {"sum1"},
		"MetricDataQueries.member.3.Expression":                   {"SUM(m1)"},
	})
	defer resp.Body.Close()

	// Then: expression function queries return aggregated values
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, "<Id>avg1</Id>") || !strings.Contains(xml, "<Id>sum1</Id>") {
		t.Fatalf("expected function query ids in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Values><member>50</member></Values>") {
		t.Fatalf("expected AVG(m1)=50 in response, got: %s", xml)
	}
	if !strings.Contains(xml, "<Values><member>100</member></Values>") {
		t.Fatalf("expected SUM(m1)=100 in response, got: %s", xml)
	}
}

func TestAlarmAutoTransitionsToAlarm(t *testing.T) {
	// Given: a mock-clock server with an alarm and breaching datapoint
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	alarm := cwCall(t, srv, "PutMetricAlarm", url.Values{
		"AlarmName":          {"cpu-alarm"},
		"Namespace":          {"TestNS"},
		"MetricName":         {"CPUUtilization"},
		"ComparisonOperator": {"GreaterThanThreshold"},
		"EvaluationPeriods":  {"1"},
		"Threshold":          {"50"},
		"Period":             {"60"},
		"Statistic":          {"Average"},
	})
	defer alarm.Body.Close()
	helpers.AssertStatus(t, alarm, http.StatusOK)

	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                      {"TestNS"},
		"MetricData.member.1.MetricName": {"CPUUtilization"},
		"MetricData.member.1.Value":      {"90"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	// When: the evaluator tick advances
	srv.Clock.Add(2 * time.Second)

	// Then: alarm state is ALARM
	resp := cwCall(t, srv, "DescribeAlarms", url.Values{"AlarmNames.member.1": {"cpu-alarm"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "<StateValue>ALARM</StateValue>") {
		t.Fatalf("expected ALARM state in response, got: %s", string(body))
	}
}

func TestAlarmAutoTransitionsToInsufficientData(t *testing.T) {
	// Given: a mock-clock server with an alarm and no datapoints
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	alarm := cwCall(t, srv, "PutMetricAlarm", url.Values{
		"AlarmName":          {"cpu-no-data"},
		"Namespace":          {"TestNS"},
		"MetricName":         {"CPUUtilization"},
		"ComparisonOperator": {"GreaterThanThreshold"},
		"EvaluationPeriods":  {"1"},
		"Threshold":          {"50"},
		"Period":             {"60"},
		"Statistic":          {"Average"},
	})
	defer alarm.Body.Close()
	helpers.AssertStatus(t, alarm, http.StatusOK)

	// When: the evaluator tick advances without any datapoints
	srv.Clock.Add(2 * time.Second)

	// Then: alarm state is INSUFFICIENT_DATA
	resp := cwCall(t, srv, "DescribeAlarms", url.Values{"AlarmNames.member.1": {"cpu-no-data"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "<StateValue>INSUFFICIENT_DATA</StateValue>") {
		t.Fatalf("expected INSUFFICIENT_DATA state in response, got: %s", string(body))
	}
}
