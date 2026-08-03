package codec

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestQueryXML_DecodeNamedListMembers(t *testing.T) {
	// Given: the Query protocol's renamed-member list form. When a model gives
	// a list's member a locationName, every AWS SDK serialises
	// Field.<locationName>.N instead of Field.member.N — RDS SubnetIds is
	// SubnetIds.SubnetIdentifier.N. This is the wire shape real SDKs send, and
	// dropping it made every compat suite's CreateDBSubnetGroup fail with
	// "At least one SubnetId is required".
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		"DBSubnetGroupName=grp"+
			"&SubnetIds.SubnetIdentifier.1=subnet-00000000"+
			"&SubnetIds.SubnetIdentifier.2=subnet-00000001"))
	var in struct {
		DBSubnetGroupName string   `json:"DBSubnetGroupName"`
		SubnetIds         []string `json:"SubnetIds"`
	}

	// When: it is decoded
	if aerr := QueryXML.Decode(r, &in); aerr != nil {
		t.Fatalf("unexpected error: %v", aerr)
	}

	// Then: the list arrives intact and ordered
	if in.DBSubnetGroupName != "grp" {
		t.Errorf("DBSubnetGroupName = %q, want grp", in.DBSubnetGroupName)
	}
	if len(in.SubnetIds) != 2 || in.SubnetIds[0] != "subnet-00000000" || in.SubnetIds[1] != "subnet-00000001" {
		t.Errorf("SubnetIds = %#v, want the two subnets in order", in.SubnetIds)
	}
}

func TestQueryXML_DecodeNamedListMembersOfStructs(t *testing.T) {
	// Given: the same renamed-member form carrying struct members
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		"Tags.Tag.1.Key=env&Tags.Tag.1.Value=prod&Tags.Tag.2.Key=team"))
	var in struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}

	// When/Then: members decode with their fields
	if aerr := QueryXML.Decode(r, &in); aerr != nil {
		t.Fatalf("unexpected error: %v", aerr)
	}
	if len(in.Tags) != 2 || in.Tags[0].Key != "env" || in.Tags[0].Value != "prod" || in.Tags[1].Key != "team" {
		t.Errorf("Tags = %#v", in.Tags)
	}
}

func TestQueryXML_NamedMemberFormDoesNotSwallowNestedStructs(t *testing.T) {
	// Given: a nested struct field whose key shape (Foo.Bar) resembles the
	// named-member form but has no numeric index, plus a map in entry form
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		"Config.Name=n&Attributes.entry.1.key=K&Attributes.entry.1.value=V"))
	var in struct {
		Config struct {
			Name string `json:"Name"`
		} `json:"Config"`
		Attributes map[string]string `json:"Attributes"`
	}

	// When/Then: both keep decoding through their own paths — the new list
	// branch only claims keys whose target field is a slice
	if aerr := QueryXML.Decode(r, &in); aerr != nil {
		t.Fatalf("unexpected error: %v", aerr)
	}
	if in.Config.Name != "n" {
		t.Errorf("Config.Name = %q, want n", in.Config.Name)
	}
	if in.Attributes["K"] != "V" {
		t.Errorf("Attributes = %#v, want K:V", in.Attributes)
	}
}

// TestQueryXML_DecodeDeeplyNestedStructs pins that a struct nested more than
// two levels deep decodes. The recursion strips the prefix from the full key
// set, so the prefix handed down has to be the whole path — passing only the
// current segment left every third level looking for keys that do not exist.
// Auto Scaling's MixedInstancesPolicy is the shape that found this.
func TestQueryXML_DecodeDeeplyNestedStructs(t *testing.T) {
	// Given: a four-segment Query key
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		"MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateName=my-template"+
			"&MixedInstancesPolicy.InstancesDistribution.OnDemandBaseCapacity=2"))
	var in struct {
		MixedInstancesPolicy struct {
			LaunchTemplate struct {
				LaunchTemplateSpecification struct {
					LaunchTemplateName string `json:"LaunchTemplateName"`
				} `json:"LaunchTemplateSpecification"`
			} `json:"LaunchTemplate"`
			InstancesDistribution struct {
				OnDemandBaseCapacity int `json:"OnDemandBaseCapacity"`
			} `json:"InstancesDistribution"`
		} `json:"MixedInstancesPolicy"`
	}

	// When: it is decoded
	if aerr := QueryXML.Decode(r, &in); aerr != nil {
		t.Fatalf("unexpected error: %v", aerr)
	}

	// Then: the innermost fields are populated
	if got := in.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateName; got != "my-template" {
		t.Errorf("LaunchTemplateName = %q, want my-template", got)
	}
	if got := in.MixedInstancesPolicy.InstancesDistribution.OnDemandBaseCapacity; got != 2 {
		t.Errorf("OnDemandBaseCapacity = %d, want 2", got)
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

func TestRPCv2JSON_WriteNotImplemented(t *testing.T) {
	w := httptest.NewRecorder()
	RPCv2JSON.WriteError(w, httptest.NewRequest(http.MethodPost, "/", nil), protocol.ErrNotImplemented)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != contentTypeRPCv2JSON {
		t.Errorf("content-type = %q, want %q", got, contentTypeRPCv2JSON)
	}
	if got := w.Header().Get("Smithy-Protocol"); got != smithyProtocolRPCv2JSON {
		t.Errorf("Smithy-Protocol = %q, want %q", got, smithyProtocolRPCv2JSON)
	}
	if got := w.Header().Get("x-emulator-unsupported"); got != "true" {
		t.Errorf("x-emulator-unsupported = %q, want true", got)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode JSON error: %v", err)
	}
	if body["__type"] != "NotImplemented" {
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

// TestQueryXML_DecodeRequest_nestedListInsideListMember covers IAM's
// ContextEntries shape: a list whose members carry a list of their own
// (ContextEntries.member.N.ContextKeyValues.member.M). Decoding a member key
// at a time used to keep only the last nested value.
func TestQueryXML_DecodeRequest_nestedListInsideListMember(t *testing.T) {
	// Given: a Query body with two context entries, one carrying two values
	type contextEntry struct {
		ContextKeyName   string   `json:"ContextKeyName"`
		ContextKeyValues []string `json:"ContextKeyValues"`
		ContextKeyType   string   `json:"ContextKeyType"`
	}
	type request struct {
		ContextEntries []contextEntry `json:"ContextEntries"`
	}
	form := url.Values{
		"ContextEntries.member.1.ContextKeyName":            {"aws:RequestedRegion"},
		"ContextEntries.member.1.ContextKeyType":            {"string"},
		"ContextEntries.member.1.ContextKeyValues.member.1": {"eu-west-1"},
		"ContextEntries.member.1.ContextKeyValues.member.2": {"us-east-1"},
		"ContextEntries.member.2.ContextKeyName":            {"aws:SourceIp"},
		"ContextEntries.member.2.ContextKeyValues.member.1": {"10.0.0.1"},
	}

	// When: it is decoded
	var got request
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if aerr := QueryXML.Decode(req, &got); aerr != nil {
		t.Fatalf("DecodeRequest: %v", aerr)
	}

	// Then: both entries and every nested value survive
	if len(got.ContextEntries) != 2 {
		t.Fatalf("ContextEntries = %#v, want 2", got.ContextEntries)
	}
	first := got.ContextEntries[0]
	if first.ContextKeyName != "aws:RequestedRegion" || first.ContextKeyType != "string" {
		t.Fatalf("first entry = %#v, want the region key", first)
	}
	if len(first.ContextKeyValues) != 2 ||
		first.ContextKeyValues[0] != "eu-west-1" || first.ContextKeyValues[1] != "us-east-1" {
		t.Fatalf("first entry values = %#v, want both values in order", first.ContextKeyValues)
	}
	if len(got.ContextEntries[1].ContextKeyValues) != 1 {
		t.Fatalf("second entry values = %#v, want one", got.ContextEntries[1].ContextKeyValues)
	}
}
