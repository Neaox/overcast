package codec

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/internal/protocol"
)

// --- shared fixtures ---------------------------------------------------

type smallIn struct {
	QueueURL string `json:"QueueUrl" xml:"QueueUrl"`
	Body     string `json:"MessageBody" xml:"MessageBody"`
}

type smallOut struct {
	MessageID string `json:"MessageId" xml:"MessageId"`
}

// newJSONReq builds a POST request with a JSON body suitable for either
// JSON 1.0 or JSON 1.1 codecs (body format is identical between them).
func newJSONReq(t *testing.T, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r = r.WithContext(context.Background())
	return r
}

// --- JSON 1.0 ----------------------------------------------------------

func TestJSON10_RoundTrip(t *testing.T) {
	r := newJSONReq(t, `{"QueueUrl":"http://q","MessageBody":"hi"}`)
	var in smallIn
	if aerr := JSON10.Decode(r, &in); aerr != nil {
		t.Fatalf("decode: %v", aerr)
	}
	if in.QueueURL != "http://q" || in.Body != "hi" {
		t.Fatalf("decoded mismatch: %+v", in)
	}

	w := httptest.NewRecorder()
	JSON10.WriteResponse(w, r, http.StatusOK, &smallOut{MessageID: "abc"})

	if got := w.Header().Get("Content-Type"); got != contentTypeJSON10 {
		t.Errorf("content-type = %q, want %q", got, contentTypeJSON10)
	}
	if !strings.Contains(w.Body.String(), `"MessageId":"abc"`) {
		t.Errorf("body missing field: %s", w.Body.String())
	}
}

func TestJSON10_DecodeMalformed(t *testing.T) {
	r := newJSONReq(t, `{not json`)
	var in smallIn
	aerr := JSON10.Decode(r, &in)
	if aerr == nil {
		t.Fatal("expected error on malformed JSON")
	}
	if aerr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", aerr.HTTPStatus)
	}
}

func TestJSON10_DecodeNilBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Body = nil
	var in smallIn
	if aerr := JSON10.Decode(r, &in); aerr != nil {
		t.Fatalf("nil body should be no-op, got %v", aerr)
	}
}

func TestJSON10_DrainsBody(t *testing.T) {
	rdr := &countingReader{Reader: strings.NewReader(`{"QueueUrl":"q"}` + strings.Repeat(" ", 1024))}
	r := httptest.NewRequest(http.MethodPost, "/", rdr)
	var in smallIn
	if aerr := JSON10.Decode(r, &in); aerr != nil {
		t.Fatalf("decode: %v", aerr)
	}
	if !rdr.fullyDrained() {
		t.Errorf("body not fully drained: %d bytes remaining", rdr.remaining())
	}
}

func TestJSON10_WriteError(t *testing.T) {
	w := httptest.NewRecorder()
	JSON10.WriteError(w, httptest.NewRequest(http.MethodPost, "/", nil), &protocol.AWSError{
		Code: "InvalidParameterValue", Message: "bad", HTTPStatus: 400,
	})
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"InvalidParameterValue"`) {
		t.Errorf("body missing code: %s", w.Body.String())
	}
}

func TestJSON10_WriteResponseNil(t *testing.T) {
	w := httptest.NewRecorder()
	JSON10.WriteResponse(w, httptest.NewRequest(http.MethodPost, "/", nil), http.StatusOK, nil)
	if w.Body.String() != "{}" {
		t.Errorf("nil should render {}, got %q", w.Body.String())
	}
}

// --- JSON 1.1 ----------------------------------------------------------

func TestJSON11_ResponseContentType(t *testing.T) {
	w := httptest.NewRecorder()
	JSON11.WriteResponse(w, httptest.NewRequest(http.MethodPost, "/", nil), http.StatusOK, &smallOut{MessageID: "x"})
	if got := w.Header().Get("Content-Type"); got != contentTypeJSON11 {
		t.Errorf("content-type = %q, want %q", got, contentTypeJSON11)
	}
}

func TestJSON11_DecodeSameAsJSON10(t *testing.T) {
	r := newJSONReq(t, `{"QueueUrl":"u","MessageBody":"b"}`)
	var in smallIn
	if aerr := JSON11.Decode(r, &in); aerr != nil {
		t.Fatalf("decode: %v", aerr)
	}
	if in.QueueURL != "u" || in.Body != "b" {
		t.Errorf("decoded mismatch: %+v", in)
	}
}

// --- Query XML ---------------------------------------------------------

func TestQueryXML_DecodeFormValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("Name=test-topic&DurationSeconds=1234"))
	var in struct {
		Name            string `json:"Name"`
		DurationSeconds int    `json:"DurationSeconds"`
	}
	aerr := QueryXML.Decode(r, &in)
	if aerr != nil {
		t.Fatalf("unexpected error: %v", aerr)
	}
	if in.Name != "test-topic" {
		t.Errorf("Name = %q, want test-topic", in.Name)
	}
	if in.DurationSeconds != 1234 {
		t.Errorf("DurationSeconds = %d, want 1234", in.DurationSeconds)
	}
}

type queryResp struct {
	XMLName any    `xml:"ListQueuesResponse"`
	Result  string `xml:"ListQueuesResult"`
}

func TestQueryXML_WriteResponse(t *testing.T) {
	w := httptest.NewRecorder()
	QueryXML.WriteResponse(w, httptest.NewRequest(http.MethodPost, "/", nil), http.StatusOK, &queryResp{Result: "ok"})
	body := w.Body.String()
	if !strings.Contains(body, "<ListQueuesResult>ok</ListQueuesResult>") {
		t.Errorf("body missing result element: %s", body)
	}
	// WriteQueryXML does not auto-embed ResponseMetadata; the caller's
	// struct must include it. The XML header and content-type are what
	// the codec is responsible for here.
	if got := w.Header().Get("Content-Type"); got != "text/xml" {
		t.Errorf("content-type = %q, want text/xml", got)
	}
}

func TestQueryXML_WriteResponseNil(t *testing.T) {
	w := httptest.NewRecorder()
	QueryXML.WriteResponse(w, httptest.NewRequest(http.MethodPost, "/", nil), http.StatusOK, nil)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("nil should produce 500 (refuses to fabricate envelope), got %d", w.Code)
	}
}

func TestQueryXML_WriteError(t *testing.T) {
	w := httptest.NewRecorder()
	QueryXML.WriteError(w, httptest.NewRequest(http.MethodPost, "/", nil), &protocol.AWSError{
		Code: "Throttling", Message: "slow down", HTTPStatus: 400,
	})
	body := w.Body.String()
	if !strings.Contains(body, "<Code>Throttling</Code>") {
		t.Errorf("body missing code: %s", body)
	}
	if !strings.Contains(body, "<ErrorResponse") {
		t.Errorf("body missing ErrorResponse envelope: %s", body)
	}
}

// --- REST-XML ----------------------------------------------------------

type s3Result struct {
	XMLName any    `xml:"Result"`
	Bucket  string `xml:"Bucket"`
}

func TestRESTXML_RoundTrip(t *testing.T) {
	body := `<smallIn><QueueUrl>q</QueueUrl><MessageBody>m</MessageBody></smallIn>`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var in smallIn
	if aerr := RESTXML.Decode(r, &in); aerr != nil {
		t.Fatalf("decode: %v", aerr)
	}
	if in.QueueURL != "q" || in.Body != "m" {
		t.Errorf("decoded mismatch: %+v", in)
	}

	w := httptest.NewRecorder()
	RESTXML.WriteResponse(w, r, http.StatusOK, &s3Result{Bucket: "b"})
	if got := w.Header().Get("Content-Type"); got != "application/xml" {
		t.Errorf("content-type = %q, want application/xml", got)
	}
	if !strings.Contains(w.Body.String(), "<Bucket>b</Bucket>") {
		t.Errorf("body missing bucket: %s", w.Body.String())
	}
}

func TestRESTXML_WriteResponseNilIsEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	RESTXML.WriteResponse(w, httptest.NewRequest(http.MethodPost, "/", nil), http.StatusNoContent, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("nil should produce empty body, got %q", w.Body.String())
	}
}

func TestRESTXML_DecodeMalformed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`<not closed`))
	var in smallIn
	aerr := RESTXML.Decode(r, &in)
	if aerr == nil {
		t.Fatal("expected error on malformed XML")
	}
	if aerr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", aerr.HTTPStatus)
	}
}

func TestRESTXML_DecodeEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	var in smallIn
	if aerr := RESTXML.Decode(r, &in); aerr != nil {
		t.Fatalf("empty body should be no-op, got %v", aerr)
	}
}

// --- RPC v2 CBOR ------------------------------------------------------

func TestRPCv2CBOR_RoundTrip(t *testing.T) {
	body, err := cborlib.Marshal(map[string]any{
		"QueueUrl":    "http://q",
		"MessageBody": "hi",
	})
	if err != nil {
		t.Fatalf("marshal cbor request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/service/AmazonSQS/operation/SendMessage", bytes.NewReader(body))
	var in smallIn
	if aerr := RPCv2CBOR.Decode(r, &in); aerr != nil {
		t.Fatalf("decode: %v", aerr)
	}
	if in.QueueURL != "http://q" || in.Body != "hi" {
		t.Fatalf("decoded mismatch: %+v", in)
	}

	w := httptest.NewRecorder()
	RPCv2CBOR.WriteResponse(w, r, http.StatusOK, &smallOut{MessageID: "abc"})

	if got := w.Header().Get("Content-Type"); got != contentTypeCBOR {
		t.Errorf("content-type = %q, want %q", got, contentTypeCBOR)
	}
	if got := w.Header().Get("Smithy-Protocol"); got != "rpc-v2-cbor" {
		t.Errorf("Smithy-Protocol = %q, want rpc-v2-cbor", got)
	}

	var out smallOut
	if err := cborlib.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode cbor response: %v", err)
	}
	if out.MessageID != "abc" {
		t.Fatalf("decoded response mismatch: %+v", out)
	}
}

func TestRPCv2CBOR_DecodeMalformed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not cbor"))
	var in smallIn
	aerr := RPCv2CBOR.Decode(r, &in)
	if aerr == nil {
		t.Fatal("expected error on malformed CBOR")
	}
	if aerr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", aerr.HTTPStatus)
	}
}

func TestRPCv2CBOR_WriteError(t *testing.T) {
	w := httptest.NewRecorder()
	RPCv2CBOR.WriteError(w, httptest.NewRequest(http.MethodPost, "/", nil), &protocol.AWSError{
		Code: "InvalidParameterValue", Message: "bad", HTTPStatus: 400,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != contentTypeCBOR {
		t.Errorf("content-type = %q, want %q", got, contentTypeCBOR)
	}
	var body map[string]string
	if err := cborlib.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cbor error: %v", err)
	}
	if body["__type"] != "InvalidParameterValue" || body["message"] != "bad" {
		t.Fatalf("unexpected error body: %#v", body)
	}
}

// --- helpers -----------------------------------------------------------

type countingReader struct {
	io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += n
	return n, err
}

func (c *countingReader) remaining() int {
	rem, _ := io.Copy(io.Discard, c.Reader)
	return int(rem)
}

func (c *countingReader) fullyDrained() bool {
	return c.remaining() == 0
}

// --- benchmarks (Phase 0 baselines, see docs/plans/smithy.md §9.4) -----

var benchSmallReq = []byte(`{"QueueUrl":"http://q","MessageBody":"hello world"}`)

func BenchmarkCodec_JSON10_Decode_SmallStruct(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(benchSmallReq))
		var in smallIn
		_ = JSON10.Decode(r, &in)
	}
}

func BenchmarkCodec_JSON10_Encode_SmallStruct(b *testing.B) {
	b.ReportAllocs()
	out := &smallOut{MessageID: "abc-123"}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		JSON10.WriteResponse(w, r, http.StatusOK, out)
	}
}

func BenchmarkCodec_JSON11_Encode_SmallStruct(b *testing.B) {
	b.ReportAllocs()
	out := &smallOut{MessageID: "abc-123"}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		JSON11.WriteResponse(w, r, http.StatusOK, out)
	}
}

func BenchmarkCodec_QueryXML_Encode_Nested(b *testing.B) {
	b.ReportAllocs()
	out := &queryResp{Result: "ok"}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		QueryXML.WriteResponse(w, r, http.StatusOK, out)
	}
}

func BenchmarkCodec_RESTXML_Encode_SmallStruct(b *testing.B) {
	b.ReportAllocs()
	out := &s3Result{Bucket: "my-bucket"}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		RESTXML.WriteResponse(w, r, http.StatusOK, out)
	}
}
