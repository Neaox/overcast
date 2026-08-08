// Bucket lifecycle configuration — Put/Get/Delete round trip, the constructs
// Overcast refuses rather than storing-and-ignoring, the clock-driven
// expiration sweeper, and the x-amz-expiration hint AWS puts on objects a rule
// will expire.
//
// TDD contract: these tests were written against the AWS documented shapes
// before internal/services/s3/handler_lifecycle.go existed, and failed with
// 501 until it did.
package s3_test

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ---- Wire shapes -----------------------------------------------------------

// lifecycleConfigurationXML mirrors AWS's LifecycleConfiguration element. It is
// declared here rather than reused from the handler so the test asserts the
// wire format independently of the implementation's own structs.
type lifecycleConfigurationXML struct {
	XMLName xml.Name            `xml:"LifecycleConfiguration"`
	Rules   []lifecycleRuleXMLT `xml:"Rule"`
}

type lifecycleRuleXMLT struct {
	ID                   string                             `xml:"ID"`
	Prefix               string                             `xml:"Prefix"`
	Status               string                             `xml:"Status"`
	Filter               *lifecycleFilterXMLT               `xml:"Filter"`
	Expiration           *lifecycleExpiryXMLT               `xml:"Expiration"`
	NoncurrentExpiration *lifecycleNoncurrentExpirationXMLT `xml:"NoncurrentVersionExpiration"`
	Transition           []lifecycleTransXMLT               `xml:"Transition"`
	AbortMPU             *lifecycleAbortMPUXMLT             `xml:"AbortIncompleteMultipartUpload"`
}

type lifecycleFilterXMLT struct {
	Prefix                string            `xml:"Prefix"`
	Tag                   *lifecycleTagXMLT `xml:"Tag"`
	ObjectSizeGreaterThan *int64            `xml:"ObjectSizeGreaterThan"`
	ObjectSizeLessThan    *int64            `xml:"ObjectSizeLessThan"`
	And                   *lifecycleAndXMLT `xml:"And"`
}

type lifecycleAndXMLT struct {
	Prefix                string             `xml:"Prefix"`
	Tags                  []lifecycleTagXMLT `xml:"Tag"`
	ObjectSizeGreaterThan *int64             `xml:"ObjectSizeGreaterThan"`
	ObjectSizeLessThan    *int64             `xml:"ObjectSizeLessThan"`
}

type lifecycleTagXMLT struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type lifecycleExpiryXMLT struct {
	Days                      int    `xml:"Days"`
	Date                      string `xml:"Date"`
	ExpiredObjectDeleteMarker *bool  `xml:"ExpiredObjectDeleteMarker"`
}

type lifecycleTransXMLT struct {
	Days         int    `xml:"Days"`
	Date         string `xml:"Date"`
	StorageClass string `xml:"StorageClass"`
}

type lifecycleAbortMPUXMLT struct {
	DaysAfterInitiation int `xml:"DaysAfterInitiation"`
}

type lifecycleNoncurrentExpirationXMLT struct {
	NoncurrentDays          int  `xml:"NoncurrentDays"`
	NewerNoncurrentVersions *int `xml:"NewerNoncurrentVersions"`
}

// ---- Local helpers ---------------------------------------------------------

func putLifecycle(t *testing.T, srv *helpers.TestServer, bucket, body string) *http.Response {
	t.Helper()
	return putLifecycleWithHeaders(t, srv, bucket, body, nil)
}

// putLifecycleWithHeaders is putLifecycle plus request headers, for the
// x-amz-transition-default-minimum-object-size parameter, which
// PutBucketLifecycleConfiguration takes as a header rather than in the body.
func putLifecycleWithHeaders(t *testing.T, srv *helpers.TestServer, bucket, body string, extra map[string]string) *http.Response {
	t.Helper()
	headers := map[string]string{"Content-Type": "application/xml"}
	for name, value := range extra {
		headers[name] = value
	}
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"?lifecycle", []byte(body), headers))
	if err != nil {
		t.Fatalf("PutBucketLifecycleConfiguration %q: %v", bucket, err)
	}
	return resp
}

func putLifecycleOK(t *testing.T, srv *helpers.TestServer, bucket, body string) {
	t.Helper()
	resp := putLifecycle(t, srv, bucket, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutBucketLifecycleConfiguration %q: status %d, body %s",
			bucket, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}

func getLifecycle(t *testing.T, srv *helpers.TestServer, bucket string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(get(srv, "/"+bucket+"?lifecycle"))
	if err != nil {
		t.Fatalf("GetBucketLifecycleConfiguration %q: %v", bucket, err)
	}
	return resp
}

// assertPutLifecycleRejected asserts the configuration was refused with the
// given AWS error code. §2.1 of docs/plans/full-emulation-priority.md: a rule
// construct the sweeper cannot act on must be refused here, never stored.
func assertPutLifecycleRejected(t *testing.T, srv *helpers.TestServer, bucket, body, wantCode string) {
	t.Helper()
	resp := putLifecycle(t, srv, bucket, body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, wantCode)
}

// ---- Response shape --------------------------------------------------------

// TestGetBucketLifecycleConfiguration_omitsUnsetOptionalElements asserts on the
// raw XML rather than a decoded struct, because Go's decoder is forgiving where
// every real SDK is not.
//
// Regression: the response used to serialise an empty <Date></Date> alongside a
// Days rule. Go read it back as "", but the Node, Python, Go, Java, .NET and
// Rust SDKs all parse that element as a timestamp and reject "" with "Invalid
// RFC3339 timestamp format" — so the whole response failed to deserialise for
// every real client while the Go-level round-trip test stayed green. The compat
// suites caught it; this test is what would have caught it here.
func TestGetBucketLifecycleConfiguration_omitsUnsetOptionalElements(t *testing.T) {
	// Given: a bucket whose rule uses Days, not Date
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-shape")
	putLifecycleOK(t, srv, "lc-shape", `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration>
  <Rule><ID>days-only</ID><Filter/><Status>Enabled</Status>
    <Expiration><Days>30</Days></Expiration>
    <Transition><Days>7</Days><StorageClass>GLACIER</StorageClass></Transition></Rule>
</LifecycleConfiguration>`)

	// When: the configuration is read back
	resp := getLifecycle(t, srv, "lc-shape")
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	resp.Body.Close()

	// Then: no empty optional element is emitted — an SDK would fail to parse it
	for _, element := range []string{"<Date>", "<ExpiredObjectDeleteMarker>", "<Tag>", "<And>", "<ObjectSize"} {
		if strings.Contains(body, element) {
			t.Errorf("response emits an unset %s element, which real SDKs reject: %s", element, body)
		}
	}

	// And: the elements that were set are all there
	for _, element := range []string{"<Days>30</Days>", "<Days>7</Days>", "<StorageClass>GLACIER</StorageClass>"} {
		if !strings.Contains(body, element) {
			t.Errorf("response is missing %s: %s", element, body)
		}
	}
}

// ---- Put/Get/Delete round trip ---------------------------------------------

func TestPutBucketLifecycleConfiguration_roundTrip(t *testing.T) {
	// Given: a bucket with no lifecycle configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-round-trip")

	// When: a configuration using the modern Filter form is stored
	putLifecycleOK(t, srv, "lc-round-trip", `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Rule>
    <ID>expire-logs</ID>
    <Filter><Prefix>logs/</Prefix></Filter>
    <Status>Enabled</Status>
    <Expiration><Days>30</Days></Expiration>
    <Transition><Days>7</Days><StorageClass>GLACIER</StorageClass></Transition>
  </Rule>
</LifecycleConfiguration>`)

	// Then: Get returns it in AWS's LifecycleConfiguration shape
	resp := getLifecycle(t, srv, "lc-round-trip")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)

	var cfg lifecycleConfigurationXML
	helpers.DecodeXML(t, resp, &cfg)
	if len(cfg.Rules) != 1 {
		t.Fatalf("Rules = %d, want 1 (%+v)", len(cfg.Rules), cfg.Rules)
	}
	rule := cfg.Rules[0]
	if rule.ID != "expire-logs" {
		t.Errorf("ID = %q, want expire-logs", rule.ID)
	}
	if rule.Status != "Enabled" {
		t.Errorf("Status = %q, want Enabled", rule.Status)
	}
	if rule.Filter == nil || rule.Filter.Prefix != "logs/" {
		t.Errorf("Filter = %+v, want Prefix logs/", rule.Filter)
	}
	if rule.Expiration == nil || rule.Expiration.Days != 30 {
		t.Errorf("Expiration = %+v, want Days 30", rule.Expiration)
	}
	if len(rule.Transition) != 1 || rule.Transition[0].StorageClass != "GLACIER" || rule.Transition[0].Days != 7 {
		t.Errorf("Transition = %+v, want Days 7 / GLACIER", rule.Transition)
	}
}

func TestPutBucketLifecycleConfiguration_noncurrentExpirationRoundTrip(t *testing.T) {
	// Given: a bucket and an AWS-valid noncurrent-version expiration action.
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-noncurrent-round-trip")
	putLifecycleOK(t, srv, "lc-noncurrent-round-trip", `<LifecycleConfiguration>`+
		`<Rule><ID>noncurrent</ID><Filter><Prefix>assets/</Prefix></Filter><Status>Enabled</Status>`+
		`<NoncurrentVersionExpiration><NoncurrentDays>30</NoncurrentDays>`+
		`<NewerNoncurrentVersions>5</NewerNoncurrentVersions></NoncurrentVersionExpiration>`+
		`</Rule></LifecycleConfiguration>`)

	// When: the service-owned configuration is read back.
	resp := getLifecycle(t, srv, "lc-noncurrent-round-trip")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got lifecycleConfigurationXML
	if err := xml.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode lifecycle configuration: %v", err)
	}

	// Then: S3 preserves both fields rather than silently dropping the action.
	if len(got.Rules) != 1 || got.Rules[0].NoncurrentExpiration == nil {
		t.Fatalf("noncurrent expiration missing from round trip: %#v", got.Rules)
	}
	action := got.Rules[0].NoncurrentExpiration
	if action.NoncurrentDays != 30 || action.NewerNoncurrentVersions == nil || *action.NewerNoncurrentVersions != 5 {
		t.Fatalf("unexpected noncurrent expiration: %#v", action)
	}
}

func TestPutBucketLifecycleConfiguration_legacyPrefixForm(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-legacy-prefix")

	// When: the deprecated rule-level Prefix form is used (no Filter element)
	putLifecycleOK(t, srv, "lc-legacy-prefix", `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration>
  <Rule>
    <ID>legacy</ID>
    <Prefix>tmp/</Prefix>
    <Status>Enabled</Status>
    <Expiration><Days>1</Days></Expiration>
  </Rule>
</LifecycleConfiguration>`)

	// Then: it is echoed back in the same legacy form — AWS does not rewrite
	// a Prefix rule into a Filter rule.
	resp := getLifecycle(t, srv, "lc-legacy-prefix")
	defer resp.Body.Close()
	var cfg lifecycleConfigurationXML
	helpers.DecodeXML(t, resp, &cfg)
	if len(cfg.Rules) != 1 {
		t.Fatalf("Rules = %d, want 1", len(cfg.Rules))
	}
	if cfg.Rules[0].Prefix != "tmp/" {
		t.Errorf("Prefix = %q, want tmp/", cfg.Rules[0].Prefix)
	}
	if cfg.Rules[0].Filter != nil {
		t.Errorf("Filter = %+v, want nil for a legacy Prefix rule", cfg.Rules[0].Filter)
	}
}

func TestGetBucketLifecycleConfiguration_noConfiguration(t *testing.T) {
	// Given: a bucket that has never had a lifecycle configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-absent")

	// When: the configuration is fetched
	resp := getLifecycle(t, srv, "lc-absent")
	defer resp.Body.Close()

	// Then: AWS's NoSuchLifecycleConfiguration, not an empty 200
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchLifecycleConfiguration")
	helpers.AssertRequestID(t, resp)
}

func TestGetBucketLifecycleConfiguration_noSuchBucket(t *testing.T) {
	// Given: no bucket
	srv := helpers.NewTestServer(t)

	// When: the configuration is fetched
	resp := getLifecycle(t, srv, "lc-missing-bucket")
	defer resp.Body.Close()

	// Then: NoSuchBucket wins over NoSuchLifecycleConfiguration
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

func TestDeleteBucketLifecycle_removesConfiguration(t *testing.T) {
	// Given: a bucket with a stored configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-delete")
	putLifecycleOK(t, srv, "lc-delete", oneDayExpiryConfig)

	// When: the configuration is deleted
	resp, err := http.DefaultClient.Do(del(srv, "/lc-delete?lifecycle"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Then: 204, and Get now reports it absent
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	getResp := getLifecycle(t, srv, "lc-delete")
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
	helpers.AssertXMLError(t, getResp, "NoSuchLifecycleConfiguration")
}

func TestDeleteBucketLifecycle_noConfigurationIsIdempotent(t *testing.T) {
	// Given: a bucket with no configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-delete-idem")

	// When: it is deleted anyway
	resp, err := http.DefaultClient.Do(del(srv, "/lc-delete-idem?lifecycle"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: 204 — AWS treats DeleteBucketLifecycle as idempotent
	helpers.AssertStatus(t, resp, http.StatusNoContent)
}

func TestPutBucketLifecycleConfiguration_replacesPreviousConfiguration(t *testing.T) {
	// Given: a bucket with one rule
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-replace")
	putLifecycleOK(t, srv, "lc-replace", oneDayExpiryConfig)

	// When: a different configuration is put
	putLifecycleOK(t, srv, "lc-replace", `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration>
  <Rule><ID>second</ID><Filter/><Status>Disabled</Status><Expiration><Days>5</Days></Expiration></Rule>
</LifecycleConfiguration>`)

	// Then: Put replaces rather than merges
	resp := getLifecycle(t, srv, "lc-replace")
	defer resp.Body.Close()
	var cfg lifecycleConfigurationXML
	helpers.DecodeXML(t, resp, &cfg)
	if len(cfg.Rules) != 1 || cfg.Rules[0].ID != "second" || cfg.Rules[0].Status != "Disabled" {
		t.Fatalf("rules = %+v, want the single replacement rule", cfg.Rules)
	}
}

// ---- Transition default minimum object size --------------------------------
//
// x-amz-transition-default-minimum-object-size is a request header on
// PutBucketLifecycleConfiguration and a response header on both that operation
// and GetBucketLifecycleConfiguration
// (com.amazonaws.s3#PutBucketLifecycleConfigurationRequest,
// #PutBucketLifecycleConfigurationOutput and
// #GetBucketLifecycleConfigurationOutput in the pinned model). Its enum is
// varies_by_storage_class | all_storage_classes_128K
// (com.amazonaws.s3#TransitionDefaultMinimumObjectSize).

const transitionMinimumHeader = "x-amz-transition-default-minimum-object-size"

func TestPutBucketLifecycleConfiguration_transitionDefaultMinimumObjectSizeRoundTrip(t *testing.T) {
	// Given: a bucket and a configuration sent with the non-default behaviour
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-min-size")

	// When: the header selects varies_by_storage_class
	putResp := putLifecycleWithHeaders(t, srv, "lc-min-size", oneDayExpiryConfig, map[string]string{
		transitionMinimumHeader: "varies_by_storage_class",
	})
	defer putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// Then: Put echoes it back, as PutBucketLifecycleConfigurationOutput does
	if got := putResp.Header.Get(transitionMinimumHeader); got != "varies_by_storage_class" {
		t.Errorf("Put %s = %q, want varies_by_storage_class", transitionMinimumHeader, got)
	}

	// And: Get reports the stored setting on the same header
	getResp := getLifecycle(t, srv, "lc-min-size")
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	if got := getResp.Header.Get(transitionMinimumHeader); got != "varies_by_storage_class" {
		t.Errorf("Get %s = %q, want varies_by_storage_class", transitionMinimumHeader, got)
	}
}

func TestPutBucketLifecycleConfiguration_transitionDefaultMinimumObjectSizeDefaults(t *testing.T) {
	// Given: a bucket and a configuration sent without the header
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-min-size-default")

	// When: the configuration is stored
	putResp := putLifecycle(t, srv, "lc-min-size-default", oneDayExpiryConfig)
	defer putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// Then: both operations report AWS's default behaviour rather than nothing
	if got := putResp.Header.Get(transitionMinimumHeader); got != "all_storage_classes_128K" {
		t.Errorf("Put %s = %q, want all_storage_classes_128K", transitionMinimumHeader, got)
	}
	getResp := getLifecycle(t, srv, "lc-min-size-default")
	defer getResp.Body.Close()
	if got := getResp.Header.Get(transitionMinimumHeader); got != "all_storage_classes_128K" {
		t.Errorf("Get %s = %q, want all_storage_classes_128K", transitionMinimumHeader, got)
	}
}

func TestPutBucketLifecycleConfiguration_transitionDefaultMinimumObjectSizeIsReplaced(t *testing.T) {
	// Given: a bucket configured with the non-default behaviour
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-min-size-replace")
	first := putLifecycleWithHeaders(t, srv, "lc-min-size-replace", oneDayExpiryConfig, map[string]string{
		transitionMinimumHeader: "varies_by_storage_class",
	})
	first.Body.Close()
	helpers.AssertStatus(t, first, http.StatusOK)

	// When: a later Put omits the header
	second := putLifecycle(t, srv, "lc-min-size-replace", oneDayExpiryConfig)
	defer second.Body.Close()
	helpers.AssertStatus(t, second, http.StatusOK)

	// Then: the configuration is replaced wholesale, so the setting reverts to
	// the default rather than surviving from the previous configuration
	getResp := getLifecycle(t, srv, "lc-min-size-replace")
	defer getResp.Body.Close()
	if got := getResp.Header.Get(transitionMinimumHeader); got != "all_storage_classes_128K" {
		t.Errorf("Get %s = %q, want all_storage_classes_128K after a Put without the header",
			transitionMinimumHeader, got)
	}
}

func TestPutBucketLifecycleConfiguration_rejectsUnknownTransitionDefaultMinimumObjectSize(t *testing.T) {
	// Given: a bucket and a value outside the model's enum
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-min-size-invalid")

	// When: the configuration is put with it
	resp := putLifecycleWithHeaders(t, srv, "lc-min-size-invalid", oneDayExpiryConfig, map[string]string{
		transitionMinimumHeader: "all_storage_classes_1M",
	})
	defer resp.Body.Close()

	// Then: S3 refuses the request rather than storing an unmodelled behaviour
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")

	// And: nothing was stored
	getResp := getLifecycle(t, srv, "lc-min-size-invalid")
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
}

// ---- Validation ------------------------------------------------------------

func TestPutBucketLifecycleConfiguration_validation(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{
			name:     "no rules",
			body:     `<LifecycleConfiguration></LifecycleConfiguration>`,
			wantCode: "MalformedXML",
		},
		{
			name:     "malformed body",
			body:     `<LifecycleConfiguration><Rule>`,
			wantCode: "MalformedXML",
		},
		{
			name: "rule with no action",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status></Rule></LifecycleConfiguration>`,
			// AWS: "At least one action needs to be specified in a rule."
			wantCode: "InvalidRequest",
		},
		{
			name: "bad status",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>enabled</Status>` +
				`<Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`,
			wantCode: "MalformedXML",
		},
		{
			name: "duplicate rule ids",
			body: `<LifecycleConfiguration>` +
				`<Rule><ID>dup</ID><Filter/><Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule>` +
				`<Rule><ID>dup</ID><Filter><Prefix>x/</Prefix></Filter><Status>Enabled</Status><Expiration><Days>2</Days></Expiration></Rule>` +
				`</LifecycleConfiguration>`,
			wantCode: "InvalidArgument",
		},
		{
			name: "expiration with both days and date",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<Expiration><Days>1</Days><Date>2030-01-01T00:00:00.000Z</Date></Expiration></Rule></LifecycleConfiguration>`,
			wantCode: "MalformedXML",
		},
		{
			name: "expiration days not positive",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<Expiration><Days>0</Days></Expiration></Rule></LifecycleConfiguration>`,
			wantCode: "InvalidArgument",
		},
		{
			name: "expiration date not midnight utc",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<Expiration><Date>2030-01-01T09:30:00.000Z</Date></Expiration></Rule></LifecycleConfiguration>`,
			wantCode: "InvalidArgument",
		},
		{
			name: "unknown transition storage class",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<Transition><Days>1</Days><StorageClass>MAGNETIC_TAPE</StorageClass></Transition></Rule></LifecycleConfiguration>`,
			wantCode: "MalformedXML",
		},
		{
			name: "both filter and rule-level prefix",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Prefix>x/</Prefix><Filter><Prefix>y/</Prefix></Filter>` +
				`<Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`,
			wantCode: "MalformedXML",
		},
		{
			name: "neither filter nor prefix",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Status>Enabled</Status>` +
				`<Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`,
			wantCode: "MalformedXML",
		},
		{
			name: "rule id too long",
			body: `<LifecycleConfiguration><Rule><ID>` + strings.Repeat("a", 256) + `</ID><Filter/>` +
				`<Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`,
			wantCode: "InvalidArgument",
		},
		{
			name: "noncurrent expiration days missing",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<NoncurrentVersionExpiration/></Rule></LifecycleConfiguration>`,
			wantCode: "InvalidArgument",
		},
		{
			name: "newer noncurrent versions out of range",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<NoncurrentVersionExpiration><NoncurrentDays>3</NoncurrentDays>` +
				`<NewerNoncurrentVersions>101</NewerNoncurrentVersions></NoncurrentVersionExpiration>` +
				`</Rule></LifecycleConfiguration>`,
			wantCode: "InvalidArgument",
		},
		{
			name: "newer noncurrent versions requires filter",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Prefix/><Status>Enabled</Status>` +
				`<NoncurrentVersionExpiration><NoncurrentDays>3</NoncurrentDays>` +
				`<NewerNoncurrentVersions>2</NewerNoncurrentVersions></NoncurrentVersionExpiration>` +
				`</Rule></LifecycleConfiguration>`,
			wantCode: "InvalidRequest",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a bucket
			srv := helpers.NewTestServer(t)
			createBucket(t, srv, "lc-validate")

			// When + Then: the configuration is refused with the AWS code
			assertPutLifecycleRejected(t, srv, "lc-validate", tc.body, tc.wantCode)

			// And: nothing was stored
			resp := getLifecycle(t, srv, "lc-validate")
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusNotFound)
		})
	}
}

// TestPutBucketLifecycleConfiguration_refusesUnevaluatedConstructs covers the
// §2.1 fidelity-risk veto: Overcast's object store keeps exactly one live
// object per key and has no version history or delete markers, so the two
// remaining unrepresented version-dependent constructs cannot be acted on.
// They are refused at Put time rather than stored and silently ignored.
func TestPutBucketLifecycleConfiguration_refusesUnevaluatedConstructs(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "NoncurrentVersionTransition",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<NoncurrentVersionTransition><NoncurrentDays>3</NoncurrentDays><StorageClass>GLACIER</StorageClass></NoncurrentVersionTransition>` +
				`</Rule></LifecycleConfiguration>`,
		},
		{
			name: "ExpiredObjectDeleteMarker",
			body: `<LifecycleConfiguration><Rule><ID>a</ID><Filter/><Status>Enabled</Status>` +
				`<Expiration><ExpiredObjectDeleteMarker>true</ExpiredObjectDeleteMarker></Expiration>` +
				`</Rule></LifecycleConfiguration>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a bucket
			srv := helpers.NewTestServer(t)
			createBucket(t, srv, "lc-refuse")

			// When + Then: refused, with a message naming the construct.
			// The body is read once and asserted on directly — the shared
			// AssertXMLError helper consumes it.
			resp := putLifecycle(t, srv, "lc-refuse", tc.body)
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			body := helpers.ReadBody(t, resp)
			resp.Body.Close()
			if !strings.Contains(body, "<Code>InvalidArgument</Code>") {
				t.Errorf("error code is not InvalidArgument: %s", body)
			}
			if !strings.Contains(body, tc.name) {
				t.Errorf("error message does not name %s: %s", tc.name, body)
			}
		})
	}
}

func TestPutBucketLifecycleConfiguration_noSuchBucket(t *testing.T) {
	// Given: no bucket
	srv := helpers.NewTestServer(t)

	// When: a configuration is put
	resp := putLifecycle(t, srv, "lc-no-bucket", oneDayExpiryConfig)
	defer resp.Body.Close()

	// Then: NoSuchBucket
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

// ---- Filters that ARE evaluated -------------------------------------------

func TestPutBucketLifecycleConfiguration_tagAndSizeFilters(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-filters")

	// When: a rule combines a prefix, a tag and a size bound through And
	putLifecycleOK(t, srv, "lc-filters", `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration>
  <Rule>
    <ID>combined</ID>
    <Filter>
      <And>
        <Prefix>data/</Prefix>
        <Tag><Key>temp</Key><Value>yes</Value></Tag>
        <ObjectSizeGreaterThan>10</ObjectSizeGreaterThan>
      </And>
    </Filter>
    <Status>Enabled</Status>
    <Expiration><Days>1</Days></Expiration>
  </Rule>
</LifecycleConfiguration>`)

	// Then: the whole filter round-trips — these are evaluated by the sweeper,
	// so they are accepted rather than refused.
	resp := getLifecycle(t, srv, "lc-filters")
	defer resp.Body.Close()
	var cfg lifecycleConfigurationXML
	helpers.DecodeXML(t, resp, &cfg)
	if len(cfg.Rules) != 1 || cfg.Rules[0].Filter == nil || cfg.Rules[0].Filter.And == nil {
		t.Fatalf("rules = %+v, want one rule with an And filter", cfg.Rules)
	}
	and := cfg.Rules[0].Filter.And
	if and.Prefix != "data/" {
		t.Errorf("And.Prefix = %q, want data/", and.Prefix)
	}
	if len(and.Tags) != 1 || and.Tags[0].Key != "temp" || and.Tags[0].Value != "yes" {
		t.Errorf("And.Tags = %+v, want temp=yes", and.Tags)
	}
	if and.ObjectSizeGreaterThan == nil || *and.ObjectSizeGreaterThan != 10 {
		t.Errorf("And.ObjectSizeGreaterThan = %v, want 10", and.ObjectSizeGreaterThan)
	}
}

// ---- x-amz-expiration ------------------------------------------------------

func TestHeadObject_expirationHeader(t *testing.T) {
	// Given: a bucket with a 1-day expiration rule and a matching object
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createBucket(t, srv, "lc-hdr")
	putLifecycleOK(t, srv, "lc-hdr", `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration>
  <Rule><ID>expire-tmp</ID><Filter><Prefix>tmp/</Prefix></Filter><Status>Enabled</Status>
    <Expiration><Days>1</Days></Expiration></Rule>
</LifecycleConfiguration>`)
	putObject(t, srv, "lc-hdr", "tmp/a.txt", []byte("x"), "text/plain")
	putObject(t, srv, "lc-hdr", "keep/b.txt", []byte("x"), "text/plain")

	// When: the matching object is HEADed
	resp, err := http.DefaultClient.Do(head(srv, "/lc-hdr/tmp/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Then: AWS's x-amz-expiration hint names the rule and the rounded date
	helpers.AssertStatus(t, resp, http.StatusOK)
	got := resp.Header.Get("x-amz-expiration")
	if !strings.Contains(got, `rule-id="expire-tmp"`) || !strings.Contains(got, `expiry-date="`) {
		t.Errorf("x-amz-expiration = %q, want expiry-date=… and rule-id=\"expire-tmp\"", got)
	}
	// AWS rounds the expiry to the following UTC midnight.
	if !strings.Contains(got, "00:00:00 GMT") {
		t.Errorf("x-amz-expiration = %q, want a UTC-midnight expiry-date", got)
	}

	// And: an object no rule matches carries no hint
	resp2, err := http.DefaultClient.Do(head(srv, "/lc-hdr/keep/b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if got := resp2.Header.Get("x-amz-expiration"); got != "" {
		t.Errorf("x-amz-expiration on an unmatched object = %q, want empty", got)
	}
}

func TestGetObject_noExpirationHeaderWithoutLifecycle(t *testing.T) {
	// Given: a bucket with no lifecycle configuration at all
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "lc-none")
	putObject(t, srv, "lc-none", "a.txt", []byte("x"), "text/plain")

	// When: the object is fetched
	resp, err := http.DefaultClient.Do(get(srv, "/lc-none/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: no hint — the object path does no lifecycle work at all
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("x-amz-expiration"); got != "" {
		t.Errorf("x-amz-expiration = %q, want empty", got)
	}
}

// ---- The sweeper -----------------------------------------------------------

// TestLifecycleSweeper_endToEnd is the one integration-level sweep: it proves
// the ticker is wired to the injected clock and that a configuration stored
// over HTTP really removes objects.
//
// The rule uses an absolute Date rather than Days so the clock only has to
// advance past one sweep interval. Every service on a test server shares the
// mock clock, and several tick once a second, so a day-scale advance here
// would fire tens of thousands of unrelated timers. The day-scale semantics —
// Days rounding, transitions, aborts, disabled rules, paging — are unit-tested
// against a handler that owns its clock in
// internal/services/s3/lifecycle_test.go.
func TestLifecycleSweeper_endToEnd(t *testing.T) {
	// Given: a bucket holding a matching and a non-matching object, with a
	// prefix-scoped rule whose expiry date has already arrived
	srv := helpers.NewTestServer(t, helpers.WithMockClock(), helpers.WithServiceSubset("s3"))
	createBucket(t, srv, "lc-sweep")
	putObject(t, srv, "lc-sweep", "tmp/gone.txt", []byte("bye"), "text/plain")
	putObject(t, srv, "lc-sweep", "keep/stays.txt", []byte("hi"), "text/plain")

	due := srv.Clock.Now().UTC().Truncate(24 * time.Hour)
	putLifecycleOK(t, srv, "lc-sweep", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration>
  <Rule><ID>tmp</ID><Filter><Prefix>tmp/</Prefix></Filter><Status>Enabled</Status>
    <Expiration><Date>%s</Date></Expiration></Rule>
</LifecycleConfiguration>`, due.Format("2006-01-02T15:04:05.000Z")))

	// When: the clock reaches the first sweep tick. Advancing a shared mock
	// clock is not free — every timer in the emulator fires on the way — so
	// this test buys the end-to-end proof with exactly one sweep interval and
	// no more.
	srv.Clock.Add(time.Hour)

	// Then: the matching object is deleted and the other is untouched
	waitForObjectGone(t, srv, "lc-sweep", "tmp/gone.txt")
	assertObjectPresent(t, srv, "lc-sweep", "keep/stays.txt")
}

// ---- Sweeper helpers -------------------------------------------------------

const oneDayExpiryConfig = `<?xml version="1.0" encoding="UTF-8"?>
<LifecycleConfiguration>
  <Rule><ID>one-day</ID><Filter/><Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule>
</LifecycleConfiguration>`

// waitForObjectGone polls HeadObject until the sweeper has removed the key.
// The sweep itself is triggered by the mock clock; this loop only waits for
// the sweeper goroutine to be scheduled, so the bound is milliseconds.
func waitForObjectGone(t *testing.T, srv *helpers.TestServer, bucket, key string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.DefaultClient.Do(head(srv, "/"+bucket+"/"+key))
		if err != nil {
			t.Fatal(err)
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusNotFound {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("object %s/%s was not expired by the lifecycle sweeper", bucket, key)
}

func assertObjectPresent(t *testing.T, srv *helpers.TestServer, bucket, key string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(head(srv, "/"+bucket+"/"+key))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("object %s/%s: status %d, want 200 (it should not have been expired)", bucket, key, resp.StatusCode)
	}
}
