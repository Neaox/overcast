package cloudwatch

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/internal/protocol/codec"
)

// TestTypedOps_CoverEveryDispatchedOperation is the CBOR counterpart of
// TestDispatchJSON_CoversEveryQueryOperation, and the guard #1280's fix needs:
// the typed registry is what claims rpcv2Cbor for the service, so an operation
// present in s.ops and absent from typedOp is reachable over Query and awsJson
// and silently 501s over CBOR — the #794 fault class in a third protocol.
//
// The check runs in both directions. An operation typed but not dispatched is
// just as wrong: Operations() is what the router's smithyRPCService consults,
// so it would claim an RPC v2 binding for something the other two protocols
// cannot serve.
func TestTypedOps_CoverEveryDispatchedOperation(t *testing.T) {
	svc := newTestService(t)

	var missingTyped, missingRaw []string
	for action := range svc.ops {
		if _, ok := svc.typedOp[action]; !ok {
			missingTyped = append(missingTyped, action)
		}
	}
	for action := range svc.typedOp {
		if _, ok := svc.ops[action]; !ok {
			missingRaw = append(missingRaw, action)
		}
	}
	sort.Strings(missingTyped)
	sort.Strings(missingRaw)

	if len(missingTyped) > 0 {
		t.Errorf("dispatched over Query/JSON but not typed, so unreachable over rpcv2Cbor: %v", missingTyped)
	}
	if len(missingRaw) > 0 {
		t.Errorf("typed but not dispatched, so Operations() claims an RPC v2 binding nothing else serves: %v", missingRaw)
	}
}

// TestOperations_NamesMatchTheirKeys pins the other half of what the router
// reads: smithyRPCService indexes Operations() by op.Name(), not by the key it
// was registered under, so a copy-paste name mismatch would leave the
// operation registered under one spelling and dispatched under another.
func TestOperations_NamesMatchTheirKeys(t *testing.T) {
	svc := newTestService(t)

	for key, operation := range svc.typedOp {
		if operation.Name() != key {
			t.Errorf("typedOp[%q].Name() = %q — the router indexes by Name(), so this operation is unreachable", key, operation.Name())
		}
	}
	if got, want := len(svc.Operations()), len(svc.typedOp); got != want {
		t.Errorf("Operations() returned %d operations, want %d", got, want)
	}
}

// TestSupportedProtocols_ClaimsRPCv2CBOR is the declaration itself. Without
// rpcv2Cbor in this list, smithyRPCService.supports returns false for every
// CloudWatch operation and the router answers 501 before Dispatch is reached
// — which is exactly the state #1280 records.
func TestSupportedProtocols_ClaimsRPCv2CBOR(t *testing.T) {
	svc := newTestService(t)

	var names []string
	for _, c := range svc.SupportedProtocols() {
		names = append(names, c.Name())
	}
	if !codec.Supports(svc.SupportedProtocols(), codec.RPCv2CBOR) {
		t.Fatalf("SupportedProtocols() = %v, want it to include %s", names, codec.NameRPCv2CBOR)
	}
	if !codec.Supports(svc.SupportedProtocols(), codec.JSON10) {
		t.Errorf("SupportedProtocols() = %v, want awsJson still claimed", names)
	}
}

// TestDispatch_CBORRoutesToTypedOperation checks the arm Dispatch grew: a
// request whose context carries the CBOR codec is answered by the typed
// operation, in CBOR, rather than falling into dispatchJSON and answering an
// AWS-JSON body under a CBOR content type.
func TestDispatch_CBORRoutesToTypedOperation(t *testing.T) {
	svc := newTestService(t)

	body, err := cborlib.Marshal(map[string]any{"Namespace": "AWS/EC2"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/service/GraniteServiceVersion20100801/operation/ListMetrics", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/cbor")
	r.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	r = r.WithContext(codec.WithDispatch(r.Context(), codec.RPCv2CBOR, "ListMetrics"))

	w := httptest.NewRecorder()
	svc.Dispatch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Dispatch: status = %d, want 200; body = %x", w.Code, w.Body.Bytes())
	}
	if got := w.Header().Get("Content-Type"); got != "application/cbor" {
		t.Fatalf("Content-Type = %q, want application/cbor", got)
	}
	var out map[string]any
	if err := cborlib.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not CBOR: %v; body = %x", err, w.Body.Bytes())
	}
	if _, ok := out["Metrics"]; !ok {
		t.Fatalf("expected a Metrics member, got %#v", out)
	}
}

// TestDispatch_CBORCarriesEmulationLimitation pins the one typed operation
// that has something to say beyond its body: PutMetricAlarm creates an alarm
// the evaluator will not evaluate, and says so in a header. A typed Fn is
// never handed the ResponseWriter, so that reaches the wire through
// op.Limitationer or not at all.
func TestDispatch_CBORCarriesEmulationLimitation(t *testing.T) {
	svc := newTestService(t)

	// A metric-math alarm: created, never evaluated — see alarm_input.go.
	body, err := cborlib.Marshal(map[string]any{
		"AlarmName":          "cbor-math-alarm",
		"ComparisonOperator": "GreaterThanThreshold",
		"EvaluationPeriods":  1,
		"Threshold":          1.0,
		"Metrics": []map[string]any{
			{"Id": "e1", "Expression": "m1 + m2"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/service/GraniteServiceVersion20100801/operation/PutMetricAlarm", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/cbor")
	r.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	r = r.WithContext(codec.WithDispatch(r.Context(), codec.RPCv2CBOR, "PutMetricAlarm"))

	w := httptest.NewRecorder()
	svc.Dispatch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Dispatch: status = %d, want 200; body = %x", w.Code, w.Body.Bytes())
	}
	if got := w.Header().Values("x-overcast-emulation-limitation"); len(got) == 0 {
		t.Fatalf("expected an x-overcast-emulation-limitation header on an alarm Overcast does not evaluate; headers = %v", w.Header())
	}
}
