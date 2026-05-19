package codec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func req(method, url string, body string, headers map[string]string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, url, nil)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestIdentifyJSON10_Match(t *testing.T) {
	r := req("POST", "/", `{}`, map[string]string{
		"Content-Type": "application/x-amz-json-1.0",
		"X-Amz-Target": "AWSSimpleQueueService.SendMessage",
	})
	c, op, ok := (identifyJSON10{}).Claim(r)
	if !ok || c != JSON10 || op != "SendMessage" {
		t.Fatalf("got (%v, %q, %v)", c, op, ok)
	}
}

func TestIdentifyJSON10_WrongContentType(t *testing.T) {
	r := req("POST", "/", `{}`, map[string]string{
		"Content-Type": "application/x-amz-json-1.1",
		"X-Amz-Target": "X.Op",
	})
	id := identifyJSON10{}
	if _, _, ok := id.Claim(r); ok {
		t.Fatal("should not claim 1.1 content-type")
	}
}

func TestIdentifyJSON10_MissingTarget(t *testing.T) {
	r := req("POST", "/", `{}`, map[string]string{
		"Content-Type": "application/x-amz-json-1.0",
	})
	id := identifyJSON10{}
	if _, _, ok := id.Claim(r); ok {
		t.Fatal("should not claim without X-Amz-Target")
	}
}

func TestIdentifyJSON11_Match(t *testing.T) {
	r := req("POST", "/", `{}`, map[string]string{
		"Content-Type": "application/x-amz-json-1.1; charset=utf-8",
		"X-Amz-Target": "AmazonDynamoDBv2_20120810.GetItem",
	})
	c, op, ok := (identifyJSON11{}).Claim(r)
	if !ok || c != JSON11 || op != "GetItem" {
		t.Fatalf("got (%v, %q, %v)", c, op, ok)
	}
}

func TestIdentifyQuery_ActionInURL(t *testing.T) {
	r := req("POST", "/?Action=ListTopics&Version=2010-03-31", "", map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	c, op, ok := (identifyQuery{}).Claim(r)
	if !ok || c != QueryXML || op != "ListTopics" {
		t.Fatalf("got (%v, %q, %v)", c, op, ok)
	}
}

func TestIdentifyQuery_BodyOnlyAction(t *testing.T) {
	// Operation lives in the form body — identifier returns the codec
	// but no operation name; resolver runs later (Phase 6).
	r := req("POST", "/", "Action=ListTopics", map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	c, op, ok := (identifyQuery{}).Claim(r)
	if !ok || c != QueryXML || op != "" {
		t.Fatalf("got (%v, %q, %v); want (QueryXML, \"\", true)", c, op, ok)
	}
}

func TestIdentifyQuery_WrongContentType(t *testing.T) {
	r := req("POST", "/?Action=X", "", map[string]string{
		"Content-Type": "application/json",
	})
	id := identifyQuery{}
	if _, _, ok := id.Claim(r); ok {
		t.Fatal("should not claim non-form content-type")
	}
}

func TestIdentifyRPCv2CBOR_Match(t *testing.T) {
	r := req("POST", "/service/AmazonSQS/operation/ListQueues", "", map[string]string{
		"Smithy-Protocol": "rpc-v2-cbor",
	})
	c, op, ok := (identifyRPCv2CBOR{}).Claim(r)
	if !ok || c != RPCv2CBOR || op != "ListQueues" {
		t.Fatalf("got (%v, %q, %v)", c, op, ok)
	}
}

func TestIdentifyRPCv2CBOR_WrongHeader(t *testing.T) {
	r := req("POST", "/service/AmazonSQS/operation/ListQueues", "", map[string]string{
		"Smithy-Protocol": "other",
	})
	if _, _, ok := (identifyRPCv2CBOR{}).Claim(r); ok {
		t.Fatal("should not claim non-rpc-v2-cbor protocol")
	}
}

func TestIdentifyRPCv2CBOR_WrongPath(t *testing.T) {
	r := req("POST", "/service/AmazonSQS/ListQueues", "", map[string]string{
		"Smithy-Protocol": "rpc-v2-cbor",
	})
	if _, _, ok := (identifyRPCv2CBOR{}).Claim(r); ok {
		t.Fatal("should not claim malformed rpc-v2-cbor path")
	}
}

func TestDefaultIdentifiers_PrecisionOrder(t *testing.T) {
	ids := DefaultIdentifiers()
	if len(ids) != 4 {
		t.Fatalf("len = %d, want 4", len(ids))
	}
	// Order: RPCv2 CBOR, JSON10, JSON11, Query
	if _, ok := ids[0].(identifyRPCv2CBOR); !ok {
		t.Errorf("ids[0] is %T, want identifyRPCv2CBOR", ids[0])
	}
	if _, ok := ids[1].(identifyJSON10); !ok {
		t.Errorf("ids[1] is %T, want identifyJSON10", ids[1])
	}
	if _, ok := ids[2].(identifyJSON11); !ok {
		t.Errorf("ids[2] is %T, want identifyJSON11", ids[2])
	}
	if _, ok := ids[3].(identifyQuery); !ok {
		t.Errorf("ids[3] is %T, want identifyQuery", ids[3])
	}
}

func TestFromContext_EmptyByDefault(t *testing.T) {
	r := req("POST", "/", "", nil)
	if c, op := FromContext(r.Context()); c != nil || op != "" {
		t.Errorf("got (%v, %q), want (nil, \"\")", c, op)
	}
}

func TestWithDispatch_RoundTrip(t *testing.T) {
	r := req("POST", "/", "", nil)
	ctx := WithDispatch(r.Context(), JSON11, "GetItem")
	c, op := FromContext(ctx)
	if c != JSON11 || op != "GetItem" {
		t.Errorf("got (%v, %q), want (JSON11, GetItem)", c, op)
	}
}
