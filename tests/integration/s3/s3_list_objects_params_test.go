// Optional-parameter coverage for the bucket listing operations
// (ListObjectsV2, ListObjects and ListObjectVersions).
//
// The behaviour asserted here comes from the ListObjectsV2 API reference
// (https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html)
// and, where that page is silent, from the AWS-verified expectations in the
// Ceph s3-tests suite (`test_bucket_listv2_*`) and Versity's AWS conformance
// suite — the two suites whose assertions are checked against real S3.
// Each test names its source.
package s3_test

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// listV2Result is the full ListBucketResult envelope, so a test can assert on
// the elements that only appear for a particular optional parameter
// (EncodingType, Owner, RestoreStatus) as well as the always-present ones.
// Owner and RestoreStatus are pointers so "element absent" is distinguishable
// from "element present but empty".
type listV2Result struct {
	XMLName               xml.Name       `xml:"ListBucketResult"`
	Name                  string         `xml:"Name"`
	Prefix                string         `xml:"Prefix"`
	Delimiter             string         `xml:"Delimiter"`
	EncodingType          string         `xml:"EncodingType"`
	KeyCount              int            `xml:"KeyCount"`
	MaxKeys               int            `xml:"MaxKeys"`
	IsTruncated           bool           `xml:"IsTruncated"`
	ContinuationToken     string         `xml:"ContinuationToken"`
	NextContinuationToken string         `xml:"NextContinuationToken"`
	StartAfter            string         `xml:"StartAfter"`
	Contents              []listV2Object `xml:"Contents"`
	CommonPrefixes        []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

type listV2Object struct {
	Key          string `xml:"Key"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
	Owner        *struct {
		ID          string `xml:"ID"`
		DisplayName string `xml:"DisplayName"`
	} `xml:"Owner"`
	RestoreStatus *struct {
		IsRestoreInProgress bool `xml:"IsRestoreInProgress"`
	} `xml:"RestoreStatus"`
}

// listV2 issues a ListObjectsV2 request with the supplied raw query string
// (without the leading "list-type=2") and decodes the successful envelope.
func listV2(t *testing.T, srv *helpers.TestServer, bucket, query string) listV2Result {
	t.Helper()
	resp := listV2Raw(t, srv, bucket, query, nil)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out listV2Result
	helpers.DecodeXML(t, resp, &out)
	return out
}

// listV2Raw issues a ListObjectsV2 request and returns the undecoded
// response, for tests that assert on status, headers or an error body. The
// caller closes the body.
func listV2Raw(t *testing.T, srv *helpers.TestServer, bucket, query string, headers map[string]string) *http.Response {
	t.Helper()
	path := "/" + bucket + "?list-type=2"
	if query != "" {
		path += "&" + query
	}
	req := get(srv, path)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ListObjectsV2 %s: %v", path, err)
	}
	return resp
}

// keysOf returns the Contents keys in response order.
func keysOf(r listV2Result) []string {
	out := make([]string, 0, len(r.Contents))
	for _, c := range r.Contents {
		out = append(out, c.Key)
	}
	return out
}

// prefixesOf returns the CommonPrefixes values in response order.
func prefixesOf(r listV2Result) []string {
	out := make([]string, 0, len(r.CommonPrefixes))
	for _, p := range r.CommonPrefixes {
		out = append(out, p.Prefix)
	}
	return out
}

// ---- max-keys boundary and malformed values (#172) -------------------------

// AWS returns an empty, untruncated page for max-keys=0 — "some S3 clients
// explicitly send max-keys=0 to detect if the bucket is empty without
// listing any items". Asserted by s3-tests' test_bucket_listv2_maxkeys_zero
// (IsTruncated false, no keys).
func TestListObjectsV2_maxKeysZero_returnsEmptyUntruncatedPage(t *testing.T) {
	// Given: a bucket with objects in it
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-bucket")
	for _, k := range []string{"bar", "baz", "foo", "quxx"} {
		putObject(t, srv, "maxkeys-bucket", k, []byte("x"), "text/plain")
	}

	// When: the client asks for zero keys
	got := listV2(t, srv, "maxkeys-bucket", "max-keys=0")

	// Then: an empty page that does not claim to be truncated
	if len(got.Contents) != 0 {
		t.Errorf("Contents = %v, want none", keysOf(got))
	}
	if got.KeyCount != 0 {
		t.Errorf("KeyCount = %d, want 0", got.KeyCount)
	}
	if got.IsTruncated {
		t.Error("IsTruncated = true, want false for max-keys=0")
	}
	if got.NextContinuationToken != "" {
		t.Errorf("NextContinuationToken = %q, want empty", got.NextContinuationToken)
	}
	if got.MaxKeys != 0 {
		t.Errorf("MaxKeys = %d, want 0", got.MaxKeys)
	}
}

// A negative max-keys is rejected: S3 answers InvalidArgument / 400 with
// "Argument max-keys must be an integer between 0 and 2147483647" rather
// than silently substituting the default.
func TestListObjectsV2_maxKeysNegative_returnsInvalidArgument(t *testing.T) {
	// Given: a bucket with an object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-bucket")
	putObject(t, srv, "maxkeys-bucket", "a.txt", []byte("x"), "text/plain")

	// When: max-keys is negative
	resp := listV2Raw(t, srv, "maxkeys-bucket", "max-keys=-1", nil)
	defer resp.Body.Close()

	// Then: 400 InvalidArgument
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// A max-keys that is not an integer is rejected the same way — s3-tests'
// test_bucket_list_maxkeys_invalid asserts status 400 / InvalidArgument for
// "&max-keys=blah".
func TestListObjectsV2_maxKeysNonNumeric_returnsInvalidArgument(t *testing.T) {
	// Given: a bucket with an object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-bucket")
	putObject(t, srv, "maxkeys-bucket", "a.txt", []byte("x"), "text/plain")

	// When: max-keys is not a number
	resp := listV2Raw(t, srv, "maxkeys-bucket", "max-keys=blah", nil)
	defer resp.Body.Close()

	// Then: 400 InvalidArgument
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// Above the signed 32-bit range max-keys is "not an integer or within
// integer range" — the same InvalidArgument, not a clamp.
func TestListObjectsV2_maxKeysAboveInt32Range_returnsInvalidArgument(t *testing.T) {
	// Given: a bucket with an object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-bucket")
	putObject(t, srv, "maxkeys-bucket", "a.txt", []byte("x"), "text/plain")

	// When: max-keys exceeds 2147483647
	resp := listV2Raw(t, srv, "maxkeys-bucket", "max-keys=2147483648", nil)
	defer resp.Body.Close()

	// Then: 400 InvalidArgument
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// Within the integer range but above the 1,000-key page cap, S3 clamps
// instead of erroring, and the response echoes the clamped value — Versity's
// AWS conformance test ListObjectsV2_exceeding_max_keys sends 233453333 and
// asserts MaxKeys == 1000, and Ceph's RGW emits the bounded value too.
func TestListObjectsV2_maxKeysAbovePageLimit_clampsToOneThousand(t *testing.T) {
	// Given: a bucket with a handful of objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-bucket")
	for _, k := range []string{"bar", "baz", "foo"} {
		putObject(t, srv, "maxkeys-bucket", k, []byte("x"), "text/plain")
	}

	// When: the client asks for far more than one page
	got := listV2(t, srv, "maxkeys-bucket", "max-keys=233453333")

	// Then: MaxKeys reports the effective cap, and the listing is complete
	if got.MaxKeys != 1000 {
		t.Errorf("MaxKeys = %d, want 1000", got.MaxKeys)
	}
	if got.IsTruncated {
		t.Error("IsTruncated = true, want false")
	}
	if got.KeyCount != 3 {
		t.Errorf("KeyCount = %d, want 3", got.KeyCount)
	}
}

// The default when max-keys is omitted is 1,000, echoed in the response —
// s3-tests' test_bucket_listv2_maxkeys_none.
func TestListObjectsV2_maxKeysOmitted_defaultsToOneThousand(t *testing.T) {
	// Given: a bucket with objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-bucket")
	for _, k := range []string{"bar", "baz", "foo", "quxx"} {
		putObject(t, srv, "maxkeys-bucket", k, []byte("x"), "text/plain")
	}

	// When: max-keys is not supplied
	got := listV2(t, srv, "maxkeys-bucket", "")

	// Then: MaxKeys is 1000 and everything is returned
	if got.MaxKeys != 1000 {
		t.Errorf("MaxKeys = %d, want 1000", got.MaxKeys)
	}
	if got.IsTruncated {
		t.Error("IsTruncated = true, want false")
	}
	if got := keysOf(got); strings.Join(got, ",") != "bar,baz,foo,quxx" {
		t.Errorf("keys = %v, want [bar baz foo quxx]", got)
	}
}

// The v1 listing shares max-keys semantics with v2 — s3-tests covers both
// (test_bucket_list_maxkeys_zero, test_bucket_list_maxkeys_invalid).
func TestListObjects_maxKeysZero_returnsEmptyUntruncatedPage(t *testing.T) {
	// Given: a bucket with objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-v1-bucket")
	for _, k := range []string{"bar", "baz", "foo"} {
		putObject(t, srv, "maxkeys-v1-bucket", k, []byte("x"), "text/plain")
	}

	// When: the v1 listing asks for zero keys
	resp, err := http.DefaultClient.Do(get(srv, "/maxkeys-v1-bucket?max-keys=0"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: an empty, untruncated page
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got struct {
		MaxKeys     int  `xml:"MaxKeys"`
		IsTruncated bool `xml:"IsTruncated"`
		Contents    []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}
	helpers.DecodeXML(t, resp, &got)
	if len(got.Contents) != 0 {
		t.Errorf("Contents = %d entries, want 0", len(got.Contents))
	}
	if got.IsTruncated {
		t.Error("IsTruncated = true, want false for max-keys=0")
	}
}

func TestListObjects_maxKeysInvalid_returnsInvalidArgument(t *testing.T) {
	// Given: a bucket with objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-v1-bucket")
	putObject(t, srv, "maxkeys-v1-bucket", "a.txt", []byte("x"), "text/plain")

	// When: the v1 listing supplies a non-numeric max-keys
	resp, err := http.DefaultClient.Do(get(srv, "/maxkeys-v1-bucket?max-keys=blah"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: 400 InvalidArgument
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// ListObjectVersions takes the same max-keys parameter and must validate it
// the same way rather than slicing with a negative bound.
func TestListObjectVersions_maxKeysNegative_returnsInvalidArgument(t *testing.T) {
	// Given: a bucket with an object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "maxkeys-versions-bucket")
	putObject(t, srv, "maxkeys-versions-bucket", "a.txt", []byte("x"), "text/plain")

	// When: the versions listing supplies a negative max-keys
	resp, err := http.DefaultClient.Do(get(srv, "/maxkeys-versions-bucket?versions&max-keys=-1"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: 400 InvalidArgument
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// ---- encoding-type (#173) --------------------------------------------------

// s3-tests' test_bucket_listv2_encoding_basic, verified against real S3:
// with encoding-type=url the keys and common prefixes are percent-encoded
// (`+` → %2B, space → %20) while `/` stays literal, so the delimiter comes
// back unchanged.
func TestListObjectsV2_encodingTypeURL_percentEncodesKeysAndPrefixes(t *testing.T) {
	// Given: keys containing characters that need encoding
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "encoding-bucket")
	for _, k := range []string{"foo+1/bar", "foo/bar/xyzzy", "quux ab/thud", "asdf+b"} {
		putObject(t, srv, "encoding-bucket", k, []byte("x"), "text/plain")
	}

	// When: listing with a delimiter and encoding-type=url
	got := listV2(t, srv, "encoding-bucket", "delimiter=/&encoding-type=url")

	// Then: the response advertises the encoding and encodes Key and
	// CommonPrefixes, leaving "/" literal
	if got.EncodingType != "url" {
		t.Errorf("EncodingType = %q, want %q", got.EncodingType, "url")
	}
	if got.Delimiter != "/" {
		t.Errorf("Delimiter = %q, want %q", got.Delimiter, "/")
	}
	if keys := keysOf(got); strings.Join(keys, ",") != "asdf%2Bb" {
		t.Errorf("keys = %v, want [asdf%%2Bb]", keys)
	}
	want := "foo%2B1/,foo/,quux%20ab/"
	if prefixes := prefixesOf(got); strings.Join(prefixes, ",") != want {
		t.Errorf("CommonPrefixes = %v, want [foo%%2B1/ foo/ quux%%20ab/]", prefixes)
	}
}

// The API reference names the encoded response elements exactly:
// "Delimiter, Prefix, Key, and StartAfter".
func TestListObjectsV2_encodingTypeURL_percentEncodesPrefixDelimiterAndStartAfter(t *testing.T) {
	// Given: a bucket whose keys live under a prefix that needs encoding
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "encoding-bucket")
	for _, k := range []string{"my dir/a.txt", "my dir/b.txt"} {
		putObject(t, srv, "encoding-bucket", k, []byte("x"), "text/plain")
	}

	// When: prefix, delimiter and start-after all carry encodable characters
	got := listV2(t, srv, "encoding-bucket", "prefix=my%20dir/&delimiter=%20&start-after=my%20dir/a.txt&encoding-type=url")

	// Then: each is echoed percent-encoded
	if got.Prefix != "my%20dir/" {
		t.Errorf("Prefix = %q, want %q", got.Prefix, "my%20dir/")
	}
	if got.Delimiter != "%20" {
		t.Errorf("Delimiter = %q, want %q", got.Delimiter, "%20")
	}
	if got.StartAfter != "my%20dir/a.txt" {
		t.Errorf("StartAfter = %q, want %q", got.StartAfter, "my%20dir/a.txt")
	}
}

// Without the parameter nothing is encoded and the EncodingType element is
// absent — it is emitted only "if you specify the encoding-type request
// parameter".
func TestListObjectsV2_encodingTypeOmitted_returnsRawKeysAndNoEncodingType(t *testing.T) {
	// Given: a key that would be encoded if encoding were on
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "encoding-bucket")
	putObject(t, srv, "encoding-bucket", "asdf+b", []byte("x"), "text/plain")

	// When: listing without encoding-type
	got := listV2(t, srv, "encoding-bucket", "")

	// Then: the key is raw and EncodingType is omitted
	if got.EncodingType != "" {
		t.Errorf("EncodingType = %q, want it omitted", got.EncodingType)
	}
	if keys := keysOf(got); strings.Join(keys, ",") != "asdf+b" {
		t.Errorf("keys = %v, want [asdf+b]", keys)
	}
}

// "Valid Values: url". Anything else is rejected with InvalidArgument and
// S3's "Invalid Encoding Method specified in Request" message.
func TestListObjectsV2_encodingTypeInvalid_returnsInvalidArgument(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "encoding-bucket")
	putObject(t, srv, "encoding-bucket", "a.txt", []byte("x"), "text/plain")

	// When: an unsupported encoding-type is requested
	resp := listV2Raw(t, srv, "encoding-bucket", "encoding-type=base64", nil)
	defer resp.Body.Close()

	// Then: 400 InvalidArgument
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// ---- fetch-owner (#173) ----------------------------------------------------

// "The owner field is not present in ListObjectsV2 by default. If you want
// to return the owner field with each key in the result, then set the
// FetchOwner field to true." s3-tests covers all three states
// (test_bucket_listv2_fetchowner_notempty / _defaultempty / _empty).
func TestListObjectsV2_fetchOwnerTrue_includesOwnerPerKey(t *testing.T) {
	// Given: a bucket with objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "owner-bucket")
	for _, k := range []string{"foo/bar", "quux"} {
		putObject(t, srv, "owner-bucket", k, []byte("x"), "text/plain")
	}

	// When: fetch-owner=true
	got := listV2(t, srv, "owner-bucket", "fetch-owner=true")

	// Then: every entry carries an Owner with a non-empty ID
	if len(got.Contents) == 0 {
		t.Fatal("expected Contents")
	}
	for _, c := range got.Contents {
		if c.Owner == nil {
			t.Errorf("key %q: Owner element absent, want present", c.Key)
			continue
		}
		if c.Owner.ID == "" {
			t.Errorf("key %q: Owner.ID empty", c.Key)
		}
	}
}

func TestListObjectsV2_fetchOwnerOmitted_omitsOwner(t *testing.T) {
	// Given: a bucket with an object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "owner-bucket")
	putObject(t, srv, "owner-bucket", "quux", []byte("x"), "text/plain")

	// When: fetch-owner is not supplied
	got := listV2(t, srv, "owner-bucket", "")

	// Then: no Owner element
	if len(got.Contents) != 1 {
		t.Fatalf("Contents = %d entries, want 1", len(got.Contents))
	}
	if got.Contents[0].Owner != nil {
		t.Errorf("Owner = %+v, want absent by default", got.Contents[0].Owner)
	}
}

func TestListObjectsV2_fetchOwnerFalse_omitsOwner(t *testing.T) {
	// Given: a bucket with an object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "owner-bucket")
	putObject(t, srv, "owner-bucket", "quux", []byte("x"), "text/plain")

	// When: fetch-owner=false
	got := listV2(t, srv, "owner-bucket", "fetch-owner=false")

	// Then: no Owner element
	if len(got.Contents) != 1 {
		t.Fatalf("Contents = %d entries, want 1", len(got.Contents))
	}
	if got.Contents[0].Owner != nil {
		t.Errorf("Owner = %+v, want absent for fetch-owner=false", got.Contents[0].Owner)
	}
}

// ---- x-amz-request-payer (#173) --------------------------------------------

// "Bucket owners need not specify this parameter in their requests" — the
// header is accepted and ignored. x-amz-request-charged comes back only when
// the requester was actually charged, which needs a Requester Pays bucket;
// Overcast has no Requester Pays support (Get/PutBucketRequestPayment answer
// 501), so no bucket here is one, and echoing the header would be a claim
// Overcast cannot back.
func TestListObjectsV2_requestPayerHeader_isAcceptedAndNotCharged(t *testing.T) {
	// Given: an ordinary (non Requester Pays) bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "payer-bucket")
	putObject(t, srv, "payer-bucket", "a.txt", []byte("x"), "text/plain")

	// When: the requester confirms they accept the charge
	resp := listV2Raw(t, srv, "payer-bucket", "", map[string]string{
		"x-amz-request-payer": "requester",
	})
	defer resp.Body.Close()

	// Then: the listing succeeds and nothing claims a charge was made
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("x-amz-request-charged"); got != "" {
		t.Errorf("x-amz-request-charged = %q, want it absent on a bucket that is not Requester Pays", got)
	}
}

// ---- x-amz-optional-object-attributes (#173) -------------------------------

// "Specifies the optional fields that you want returned in the response.
// Fields that you do not specify are not returned." RestoreStatus only
// appears for an object with an archive restore in progress or completed;
// Overcast implements no archive restore, so the header is accepted and no
// RestoreStatus element is produced — which is also what real S3 returns for
// an object that was never restored.
func TestListObjectsV2_optionalObjectAttributes_isAcceptedAndOmitsRestoreStatus(t *testing.T) {
	// Given: a bucket with an ordinary object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "attrs-bucket")
	putObject(t, srv, "attrs-bucket", "a.txt", []byte("x"), "text/plain")

	// When: RestoreStatus is requested
	resp := listV2Raw(t, srv, "attrs-bucket", "", map[string]string{
		"x-amz-optional-object-attributes": "RestoreStatus",
	})
	defer resp.Body.Close()

	// Then: the listing succeeds with no RestoreStatus element
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got listV2Result
	helpers.DecodeXML(t, resp, &got)
	if len(got.Contents) != 1 {
		t.Fatalf("Contents = %d entries, want 1", len(got.Contents))
	}
	if got.Contents[0].RestoreStatus != nil {
		t.Errorf("RestoreStatus = %+v, want absent for a never-restored object", got.Contents[0].RestoreStatus)
	}
}

// ---- continuation-token / start-after precedence (#174) --------------------

// s3-tests' test_bucket_listv2_both_continuationtoken_startafter: when both
// are sent the token decides where the page starts, and StartAfter is still
// echoed back.
func TestListObjectsV2_continuationTokenWithStartAfter_tokenSetsPosition(t *testing.T) {
	// Given: four keys, and a first page of one taken after start-after=bar
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "both-bucket")
	for _, k := range []string{"bar", "baz", "foo", "quxx"} {
		putObject(t, srv, "both-bucket", k, []byte("x"), "text/plain")
	}
	first := listV2(t, srv, "both-bucket", "start-after=bar&max-keys=1")
	if first.NextContinuationToken == "" {
		t.Fatalf("first page: NextContinuationToken empty (keys %v)", keysOf(first))
	}

	// When: the next page is requested with both the token and start-after
	second := listV2(t, srv, "both-bucket",
		"start-after=bar&continuation-token="+url.QueryEscape(first.NextContinuationToken))

	// Then: the token wins for positioning and start-after is echoed
	if second.ContinuationToken != first.NextContinuationToken {
		t.Errorf("ContinuationToken = %q, want %q", second.ContinuationToken, first.NextContinuationToken)
	}
	if second.StartAfter != "bar" {
		t.Errorf("StartAfter = %q, want %q", second.StartAfter, "bar")
	}
	if second.IsTruncated {
		t.Error("IsTruncated = true, want false on the last page")
	}
	if keys := keysOf(second); strings.Join(keys, ",") != "foo,quxx" {
		t.Errorf("keys = %v, want [foo quxx]", keys)
	}
}

// The v1 listing takes the same encoding-type parameter and encodes the same
// values (plus Marker/NextMarker in place of StartAfter) — s3-tests'
// test_bucket_list_encoding_basic asserts the identical keys and prefixes as
// its v2 twin. The AWS CLI reaches this path through `aws s3api
// list-objects`, which sets encoding-type=url for the caller.
func TestListObjects_encodingTypeURL_percentEncodesKeysAndPrefixes(t *testing.T) {
	// Given: keys containing characters that need encoding
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "encoding-v1-bucket")
	for _, k := range []string{"foo+1/bar", "foo/bar/xyzzy", "quux ab/thud", "asdf+b"} {
		putObject(t, srv, "encoding-v1-bucket", k, []byte("x"), "text/plain")
	}

	// When: listing with a delimiter and encoding-type=url
	resp, err := http.DefaultClient.Do(get(srv, "/encoding-v1-bucket?delimiter=/&encoding-type=url"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: keys and common prefixes are percent-encoded, "/" stays literal
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got struct {
		Delimiter    string `xml:"Delimiter"`
		EncodingType string `xml:"EncodingType"`
		Contents     []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
	}
	helpers.DecodeXML(t, resp, &got)

	if got.EncodingType != "url" {
		t.Errorf("EncodingType = %q, want %q", got.EncodingType, "url")
	}
	if got.Delimiter != "/" {
		t.Errorf("Delimiter = %q, want %q", got.Delimiter, "/")
	}
	if len(got.Contents) != 1 || got.Contents[0].Key != "asdf%2Bb" {
		t.Errorf("Contents = %+v, want a single key asdf%%2Bb", got.Contents)
	}
	prefixes := make([]string, 0, len(got.CommonPrefixes))
	for _, p := range got.CommonPrefixes {
		prefixes = append(prefixes, p.Prefix)
	}
	if want := "foo%2B1/,foo/,quux%20ab/"; strings.Join(prefixes, ",") != want {
		t.Errorf("CommonPrefixes = %v, want [foo%%2B1/ foo/ quux%%20ab/]", prefixes)
	}
}

func TestListObjects_encodingTypeInvalid_returnsInvalidArgument(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "encoding-v1-bucket")
	putObject(t, srv, "encoding-v1-bucket", "a.txt", []byte("x"), "text/plain")

	// When: an unsupported encoding-type is requested
	resp, err := http.DefaultClient.Do(get(srv, "/encoding-v1-bucket?encoding-type=base64"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: 400 InvalidArgument
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}
