package cloudwatch_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"testing"
	"time"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// cwServiceShape is the Smithy service shape CloudWatch's RPC v2 URI names.
// It is the same string as the awsJson target prefix, minus the dot.
const cwServiceShape = "GraniteServiceVersion20100801"

// cwCBORCall performs a CloudWatch Smithy RPC v2 CBOR request: the operation
// and service are named in the URI, and Smithy-Protocol is what separates it
// from an S3 object read of a legal-looking path.
func cwCBORCall(t *testing.T, srv *helpers.TestServer, operation string, payload any) *http.Response {
	t.Helper()

	body, err := cborlib.Marshal(payload)
	if err != nil {
		t.Fatalf("cwCBORCall: marshal: %v", err)
	}
	uri := srv.URL + "/service/" + cwServiceShape + "/operation/" + operation
	req, err := http.NewRequest(http.MethodPost, uri, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("cwCBORCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cwCBORCall: do: %v", err)
	}
	return resp
}

// cwDispatchedOperations is every CloudWatch operation Overcast dispatches.
// The in-package TestTypedOps_CoverEveryDispatchedOperation is what keeps the
// emulator's two tables in step; this list is the wire-side restatement, so a
// new operation that is served over Query and JSON but not over CBOR fails
// here rather than only in the router's dev-tagged protocol-symmetry gate.
var cwDispatchedOperations = []string{
	"DeleteAlarms",
	"DescribeAlarmHistory",
	"DescribeAlarms",
	"DescribeAlarmsForMetric",
	"DisableAlarmActions",
	"EnableAlarmActions",
	"GetMetricData",
	"GetMetricStatistics",
	"ListMetrics",
	"ListTagsForResource",
	"PutMetricAlarm",
	"PutMetricData",
	"SetAlarmState",
	"TagResource",
	"UntagResource",
}

// TestCBOR_EveryDispatchedOperationIsReachable pins issue #1280: CloudWatch is
// the only dispatched service whose pinned model declares rpcv2Cbor, and it
// answered none of its fifteen declared operations over it — its
// smithyRPCService registration carried no ProtocolService, so the RPC v2 door
// was never claimed and every call got 415 UnsupportedProtocol. A newer SDK
// major negotiates rpcv2Cbor for a service that declares it, so a caller who
// cannot force another protocol saw a wall of refusals where AWS answers.
//
// Like the router's own protocol probe, this asserts reachability only: an
// empty CBOR map is a valid body for every request shape (all members are
// optional to the decoder), so a validation error is the expected answer from
// a working handler and counts as reached. Only 415 (protocol refused) and 501
// (nobody serves this) are failures.
func TestCBOR_EveryDispatchedOperationIsReachable(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for _, operation := range cwDispatchedOperations {
		t.Run(operation, func(t *testing.T) {
			resp := cwCBORCall(t, srv, operation, map[string]any{})
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			switch resp.StatusCode {
			case http.StatusUnsupportedMediaType:
				t.Fatalf("%s over rpcv2Cbor: 415 UnsupportedProtocol — CloudWatch does not claim the protocol its model declares; body: %x", operation, body)
			case http.StatusNotImplemented:
				t.Fatalf("%s over rpcv2Cbor: 501 — declared by the model and dispatched over awsJson/awsQuery, but nothing serves it here; body: %x", operation, body)
			}
			if got := resp.Header.Get("Smithy-Protocol"); got != "rpc-v2-cbor" {
				t.Errorf("%s: Smithy-Protocol = %q, want rpc-v2-cbor", operation, got)
			}
			if got := resp.Header.Get("Content-Type"); got != "application/cbor" {
				t.Errorf("%s: Content-Type = %q, want application/cbor", operation, got)
			}
		})
	}
}

// TestCBOR_MatchesJSON is the parity check #1280 asks for: the same operation
// answered over rpcv2Cbor and over awsJson1_0 must produce the same result,
// shape-normalised — CBOR integers and JSON's float64 numbers are the same
// value written two ways, and that is the only difference allowed.
//
// GetMetricStatistics is the operation used because it exercises the parts a
// per-protocol reimplementation would drift on: a nested Dimensions list in
// the request, epoch-second timestamps in both directions, and an aggregated
// float that has to come out of the same bucketing.
func TestCBOR_MatchesJSON(t *testing.T) {
	// Given: two datapoints on one dimensioned metric.
	base := time.Now().UTC().Truncate(time.Second)
	srv := helpers.NewTestServer(t)
	put := cwCall(t, srv, "PutMetricData", url.Values{
		"Namespace":                                     {"CborNS"},
		"MetricData.member.1.MetricName":                {"CPUUtilization"},
		"MetricData.member.1.Dimensions.member.1.Name":  {"InstanceId"},
		"MetricData.member.1.Dimensions.member.1.Value": {"i-1234"},
		"MetricData.member.1.Timestamp":                 {base.Add(-50 * time.Second).Format(time.RFC3339)},
		"MetricData.member.1.Value":                     {"40"},
		"MetricData.member.2.MetricName":                {"CPUUtilization"},
		"MetricData.member.2.Dimensions.member.1.Name":  {"InstanceId"},
		"MetricData.member.2.Dimensions.member.1.Value": {"i-1234"},
		"MetricData.member.2.Timestamp":                 {base.Add(-40 * time.Second).Format(time.RFC3339)},
		"MetricData.member.2.Value":                     {"60"},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	request := map[string]any{
		"Namespace":  "CborNS",
		"MetricName": "CPUUtilization",
		"StartTime":  float64(base.Add(-1 * time.Minute).Unix()),
		"EndTime":    float64(base.Add(1 * time.Minute).Unix()),
		"Period":     60,
		"Statistics": []string{"Average", "Sum", "SampleCount"},
		"Dimensions": []map[string]any{{"Name": "InstanceId", "Value": "i-1234"}},
	}

	// When: the identical request is issued over awsJson1_0 and rpcv2Cbor.
	jsonResp := cwTargetCall(t, srv, "GetMetricStatistics", request)
	defer jsonResp.Body.Close()
	helpers.AssertStatus(t, jsonResp, http.StatusOK)
	jsonBytes, err := io.ReadAll(jsonResp.Body)
	if err != nil {
		t.Fatalf("read json body: %v", err)
	}
	var fromJSON map[string]any
	if err := json.Unmarshal(jsonBytes, &fromJSON); err != nil {
		t.Fatalf("unmarshal json body: %v; body: %s", err, jsonBytes)
	}

	cborResp := cwCBORCall(t, srv, "GetMetricStatistics", request)
	defer cborResp.Body.Close()
	cborBytes, err := io.ReadAll(cborResp.Body)
	if err != nil {
		t.Fatalf("read cbor body: %v", err)
	}
	if cborResp.StatusCode != http.StatusOK {
		t.Fatalf("GetMetricStatistics over rpcv2Cbor: status = %d, want 200; body = %x", cborResp.StatusCode, cborBytes)
	}
	var fromCBOR map[string]any
	if err := cborlib.Unmarshal(cborBytes, &fromCBOR); err != nil {
		t.Fatalf("unmarshal cbor body: %v; body: %x", err, cborBytes)
	}

	// Then: the two answers are the same value.
	if !reflect.DeepEqual(normaliseShape(fromCBOR), normaliseShape(fromJSON)) {
		t.Fatalf("rpcv2Cbor and awsJson1_0 answers differ:\n cbor = %#v\n json = %#v", normaliseShape(fromCBOR), normaliseShape(fromJSON))
	}

	// And: it is the aggregate the datapoints imply, so parity is not two
	// protocols agreeing on an empty answer.
	datapoints, ok := normaliseShape(fromCBOR).(map[string]any)["Datapoints"].([]any)
	if !ok || len(datapoints) != 1 {
		t.Fatalf("expected exactly one datapoint, got: %#v", normaliseShape(fromCBOR))
	}
	if average := datapoints[0].(map[string]any)["Average"]; average != float64(50) {
		t.Fatalf("Average = %v, want 50", average)
	}
}

// normaliseShape rewrites a decoded response so two protocols' encodings of
// the same value compare equal: CBOR keys decode as any, and CBOR writes whole
// numbers as integers where JSON has only float64. Nothing else is touched —
// a missing member, a different string, or a different list order still fails.
func normaliseShape(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, member := range value {
			out[key] = normaliseShape(member)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(value))
		keys := make([]string, 0, len(value))
		for key := range value {
			name, ok := key.(string)
			if !ok {
				continue
			}
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out[key] = normaliseShape(value[key])
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, member := range value {
			out[i] = normaliseShape(member)
		}
		return out
	case int64:
		return float64(value)
	case uint64:
		return float64(value)
	case int:
		return float64(value)
	case float32:
		return float64(value)
	default:
		return v
	}
}
