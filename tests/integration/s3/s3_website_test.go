// Bucket website configuration — the full AWS WebsiteConfiguration document,
// not just IndexDocument/ErrorDocument.
//
// Wire shapes are taken from the pinned model (models/aws/VERSION):
// com.amazonaws.s3#WebsiteConfiguration carries ErrorDocument, IndexDocument,
// RedirectAllRequestsTo and RoutingRules; #RoutingRules is an unflattened list
// of #RoutingRule elements, each with an optional #Condition
// (HttpErrorCodeReturnedEquals, KeyPrefixEquals) and a required #Redirect
// (HostName, HttpRedirectCode, Protocol, ReplaceKeyPrefixWith,
// ReplaceKeyWith). #Protocol is the enum http | https, and
// #RedirectAllRequestsTo requires HostName.
package s3_test

import (
	"encoding/xml"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ---- Wire shapes -----------------------------------------------------------
//
// Declared here rather than reused from the handler so the tests assert the
// wire format independently of the implementation's own structs.

type websiteConfigurationXMLT struct {
	XMLName               xml.Name                   `xml:"WebsiteConfiguration"`
	IndexDocument         *indexDocumentXMLT         `xml:"IndexDocument"`
	ErrorDocument         *errorDocumentXMLT         `xml:"ErrorDocument"`
	RedirectAllRequestsTo *redirectAllRequestsToXMLT `xml:"RedirectAllRequestsTo"`
	RoutingRules          *routingRulesXMLT          `xml:"RoutingRules"`
}

type indexDocumentXMLT struct {
	Suffix string `xml:"Suffix"`
}

type errorDocumentXMLT struct {
	Key string `xml:"Key"`
}

type redirectAllRequestsToXMLT struct {
	HostName string `xml:"HostName"`
	Protocol string `xml:"Protocol"`
}

type routingRulesXMLT struct {
	Rules []routingRuleXMLT `xml:"RoutingRule"`
}

type routingRuleXMLT struct {
	Condition *routingConditionXMLT `xml:"Condition"`
	Redirect  routingRedirectXMLT   `xml:"Redirect"`
}

type routingConditionXMLT struct {
	HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals"`
	KeyPrefixEquals             string `xml:"KeyPrefixEquals"`
}

type routingRedirectXMLT struct {
	HostName             string `xml:"HostName"`
	HTTPRedirectCode     string `xml:"HttpRedirectCode"`
	Protocol             string `xml:"Protocol"`
	ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith"`
	ReplaceKeyWith       string `xml:"ReplaceKeyWith"`
}

// ---- Local helpers ---------------------------------------------------------

func putWebsite(t *testing.T, srv *helpers.TestServer, bucket, body string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"?website", []byte(body), map[string]string{
		"Content-Type": "application/xml",
	}))
	if err != nil {
		t.Fatalf("PutBucketWebsite %q: %v", bucket, err)
	}
	return resp
}

func putWebsiteOK(t *testing.T, srv *helpers.TestServer, bucket, body string) {
	t.Helper()
	resp := putWebsite(t, srv, bucket, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutBucketWebsite %q: status %d, body %s", bucket, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}

func getWebsite(t *testing.T, srv *helpers.TestServer, bucket string) websiteConfigurationXMLT {
	t.Helper()
	resp, err := http.DefaultClient.Do(get(srv, "/"+bucket+"?website"))
	if err != nil {
		t.Fatalf("GetBucketWebsite %q: %v", bucket, err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var cfg websiteConfigurationXMLT
	helpers.DecodeXML(t, resp, &cfg)
	return cfg
}

func assertPutWebsiteRejected(t *testing.T, srv *helpers.TestServer, bucket, body, wantCode string) {
	t.Helper()
	resp := putWebsite(t, srv, bucket, body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, wantCode)
}

// ---- Redirect-all ----------------------------------------------------------

func TestPutBucketWebsite_redirectAllRequestsToRoundTrip(t *testing.T) {
	// Given: a bucket and the redirect-only website configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-redirect-all")

	// When: RedirectAllRequestsTo is stored
	putWebsiteOK(t, srv, "web-redirect-all", `<?xml version="1.0" encoding="UTF-8"?>
<WebsiteConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <RedirectAllRequestsTo>
    <HostName>example.test</HostName>
    <Protocol>https</Protocol>
  </RedirectAllRequestsTo>
</WebsiteConfiguration>`)

	// Then: Get returns both fields, and no index/error document it never had
	cfg := getWebsite(t, srv, "web-redirect-all")
	if cfg.RedirectAllRequestsTo == nil {
		t.Fatalf("RedirectAllRequestsTo missing from the round trip: %+v", cfg)
	}
	if cfg.RedirectAllRequestsTo.HostName != "example.test" {
		t.Errorf("HostName = %q, want example.test", cfg.RedirectAllRequestsTo.HostName)
	}
	if cfg.RedirectAllRequestsTo.Protocol != "https" {
		t.Errorf("Protocol = %q, want https", cfg.RedirectAllRequestsTo.Protocol)
	}
	if cfg.IndexDocument != nil || cfg.ErrorDocument != nil || cfg.RoutingRules != nil {
		t.Errorf("redirect-all configuration invented other elements: %+v", cfg)
	}
}

func TestPutBucketWebsite_redirectAllProtocolIsOptional(t *testing.T) {
	// Given: com.amazonaws.s3#RedirectAllRequestsTo requires only HostName
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-redirect-all-host-only")

	// When: only the host name is supplied
	putWebsiteOK(t, srv, "web-redirect-all-host-only",
		`<WebsiteConfiguration><RedirectAllRequestsTo><HostName>example.test</HostName></RedirectAllRequestsTo></WebsiteConfiguration>`)

	// Then: it round-trips without an invented Protocol, which an SDK would
	// otherwise have to parse as an empty enum value
	cfg := getWebsite(t, srv, "web-redirect-all-host-only")
	if cfg.RedirectAllRequestsTo == nil || cfg.RedirectAllRequestsTo.HostName != "example.test" {
		t.Fatalf("RedirectAllRequestsTo = %+v, want HostName example.test", cfg.RedirectAllRequestsTo)
	}
	if cfg.RedirectAllRequestsTo.Protocol != "" {
		t.Errorf("Protocol = %q, want it omitted", cfg.RedirectAllRequestsTo.Protocol)
	}
}

// ---- Routing rules ---------------------------------------------------------

func TestPutBucketWebsite_routingRulesRoundTrip(t *testing.T) {
	// Given: a bucket and both documented routing-rule shapes — a prefix
	// rewrite and an error-code redirect to another host
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-routing")

	// When: the configuration is stored
	putWebsiteOK(t, srv, "web-routing", `<?xml version="1.0" encoding="UTF-8"?>
<WebsiteConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <IndexDocument><Suffix>index.html</Suffix></IndexDocument>
  <ErrorDocument><Key>error.html</Key></ErrorDocument>
  <RoutingRules>
    <RoutingRule>
      <Condition><KeyPrefixEquals>docs/</KeyPrefixEquals></Condition>
      <Redirect><ReplaceKeyPrefixWith>documents/</ReplaceKeyPrefixWith></Redirect>
    </RoutingRule>
    <RoutingRule>
      <Condition><HttpErrorCodeReturnedEquals>404</HttpErrorCodeReturnedEquals></Condition>
      <Redirect>
        <HostName>errors.example.test</HostName>
        <Protocol>https</Protocol>
        <HttpRedirectCode>301</HttpRedirectCode>
        <ReplaceKeyWith>report.html</ReplaceKeyWith>
      </Redirect>
    </RoutingRule>
  </RoutingRules>
</WebsiteConfiguration>`)

	// Then: every condition and redirect field survives, in AWS's nested
	// RoutingRules/RoutingRule shape
	cfg := getWebsite(t, srv, "web-routing")
	if cfg.IndexDocument == nil || cfg.IndexDocument.Suffix != "index.html" {
		t.Errorf("IndexDocument = %+v, want index.html", cfg.IndexDocument)
	}
	if cfg.ErrorDocument == nil || cfg.ErrorDocument.Key != "error.html" {
		t.Errorf("ErrorDocument = %+v, want error.html", cfg.ErrorDocument)
	}
	if cfg.RoutingRules == nil || len(cfg.RoutingRules.Rules) != 2 {
		t.Fatalf("RoutingRules = %+v, want 2 rules", cfg.RoutingRules)
	}

	first := cfg.RoutingRules.Rules[0]
	if first.Condition == nil || first.Condition.KeyPrefixEquals != "docs/" {
		t.Errorf("rule 1 Condition = %+v, want KeyPrefixEquals docs/", first.Condition)
	}
	if first.Redirect.ReplaceKeyPrefixWith != "documents/" {
		t.Errorf("rule 1 ReplaceKeyPrefixWith = %q, want documents/", first.Redirect.ReplaceKeyPrefixWith)
	}

	second := cfg.RoutingRules.Rules[1]
	if second.Condition == nil || second.Condition.HTTPErrorCodeReturnedEquals != "404" {
		t.Errorf("rule 2 Condition = %+v, want HttpErrorCodeReturnedEquals 404", second.Condition)
	}
	want := routingRedirectXMLT{
		HostName:         "errors.example.test",
		HTTPRedirectCode: "301",
		Protocol:         "https",
		ReplaceKeyWith:   "report.html",
	}
	if second.Redirect != want {
		t.Errorf("rule 2 Redirect = %+v, want %+v", second.Redirect, want)
	}
}

func TestPutBucketWebsite_routingRuleConditionIsOptional(t *testing.T) {
	// Given: com.amazonaws.s3#RoutingRule requires only Redirect
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-routing-no-condition")

	// When: an unconditional rule is stored
	putWebsiteOK(t, srv, "web-routing-no-condition",
		`<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument>`+
			`<RoutingRules><RoutingRule><Redirect><HostName>example.test</HostName></Redirect></RoutingRule></RoutingRules>`+
			`</WebsiteConfiguration>`)

	// Then: no empty Condition element is invented
	cfg := getWebsite(t, srv, "web-routing-no-condition")
	if cfg.RoutingRules == nil || len(cfg.RoutingRules.Rules) != 1 {
		t.Fatalf("RoutingRules = %+v, want 1 rule", cfg.RoutingRules)
	}
	if cfg.RoutingRules.Rules[0].Condition != nil {
		t.Errorf("Condition = %+v, want it omitted", cfg.RoutingRules.Rules[0].Condition)
	}
}

// ---- Replacement and deletion ----------------------------------------------

func TestPutBucketWebsite_replacesTheWholeConfiguration(t *testing.T) {
	// Given: a bucket configured with index, error and routing rules
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-replace")
	putWebsiteOK(t, srv, "web-replace",
		`<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument>`+
			`<ErrorDocument><Key>error.html</Key></ErrorDocument>`+
			`<RoutingRules><RoutingRule><Condition><KeyPrefixEquals>docs/</KeyPrefixEquals></Condition>`+
			`<Redirect><ReplaceKeyPrefixWith>documents/</ReplaceKeyPrefixWith></Redirect></RoutingRule></RoutingRules>`+
			`</WebsiteConfiguration>`)

	// When: a redirect-only configuration replaces it
	putWebsiteOK(t, srv, "web-replace",
		`<WebsiteConfiguration><RedirectAllRequestsTo><HostName>example.test</HostName></RedirectAllRequestsTo></WebsiteConfiguration>`)

	// Then: nothing from the previous configuration survives the replacement
	cfg := getWebsite(t, srv, "web-replace")
	if cfg.RedirectAllRequestsTo == nil || cfg.RedirectAllRequestsTo.HostName != "example.test" {
		t.Fatalf("RedirectAllRequestsTo = %+v, want HostName example.test", cfg.RedirectAllRequestsTo)
	}
	if cfg.IndexDocument != nil || cfg.ErrorDocument != nil || cfg.RoutingRules != nil {
		t.Errorf("the replaced configuration leaked into the new one: %+v", cfg)
	}
}

func TestDeleteBucketWebsite_removesRedirectAndRoutingRules(t *testing.T) {
	// Given: a bucket with a redirect-all configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-delete-redirect")
	putWebsiteOK(t, srv, "web-delete-redirect",
		`<WebsiteConfiguration><RedirectAllRequestsTo><HostName>example.test</HostName></RedirectAllRequestsTo></WebsiteConfiguration>`)

	// When: the configuration is deleted
	resp, err := http.DefaultClient.Do(bucketConfigurationDeleteRequest(t, srv, "/web-delete-redirect?website"))
	if err != nil {
		t.Fatalf("DeleteBucketWebsite: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// Then: Get reports the AWS not-found error rather than a stale redirect
	getResp, err := http.DefaultClient.Do(get(srv, "/web-delete-redirect?website"))
	if err != nil {
		t.Fatalf("GetBucketWebsite: %v", err)
	}
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
	helpers.AssertXMLError(t, getResp, "NoSuchWebsiteConfiguration")
}

// ---- Validation ------------------------------------------------------------

func TestPutBucketWebsite_validation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-validation")

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			// "If you specify this property, you can't specify any other
			// property." — com.amazonaws.s3#WebsiteConfiguration
			// $RedirectAllRequestsTo.
			name: "redirect-all with an index document",
			body: `<WebsiteConfiguration><RedirectAllRequestsTo><HostName>example.test</HostName></RedirectAllRequestsTo>` +
				`<IndexDocument><Suffix>index.html</Suffix></IndexDocument></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			name: "redirect-all with routing rules",
			body: `<WebsiteConfiguration><RedirectAllRequestsTo><HostName>example.test</HostName></RedirectAllRequestsTo>` +
				`<RoutingRules><RoutingRule><Redirect><HostName>other.test</HostName></Redirect></RoutingRule></RoutingRules></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			name: "redirect-all without a host name",
			body: `<WebsiteConfiguration><RedirectAllRequestsTo><Protocol>https</Protocol></RedirectAllRequestsTo></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			name: "protocol outside the enum",
			body: `<WebsiteConfiguration><RedirectAllRequestsTo><HostName>example.test</HostName><Protocol>ftp</Protocol></RedirectAllRequestsTo></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			// Neither of the two documented top-level forms.
			name: "empty configuration",
			body: `<WebsiteConfiguration></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			name: "routing rules without an index document",
			body: `<WebsiteConfiguration><RoutingRules><RoutingRule><Redirect><HostName>example.test</HostName></Redirect></RoutingRule></RoutingRules></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			// IndexDocument.Suffix "must not be empty and must not include a
			// slash character" — com.amazonaws.s3#IndexDocument $Suffix.
			name: "index suffix containing a slash",
			body: `<WebsiteConfiguration><IndexDocument><Suffix>pages/index.html</Suffix></IndexDocument></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			name: "empty index suffix",
			body: `<WebsiteConfiguration><IndexDocument><Suffix></Suffix></IndexDocument></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			name: "empty error document key",
			body: `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument><ErrorDocument><Key></Key></ErrorDocument></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			// "Not required if one of the siblings is present" —
			// com.amazonaws.s3#Redirect. A Redirect with no sibling at all
			// names no destination.
			name: "routing rule with an empty redirect",
			body: `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument>` +
				`<RoutingRules><RoutingRule><Redirect></Redirect></RoutingRule></RoutingRules></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			// "Can be present only if ReplaceKeyWith is not provided." —
			// com.amazonaws.s3#Redirect $ReplaceKeyPrefixWith.
			name: "routing rule replacing both key and prefix",
			body: `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument>` +
				`<RoutingRules><RoutingRule><Redirect><ReplaceKeyWith>a.html</ReplaceKeyWith>` +
				`<ReplaceKeyPrefixWith>b/</ReplaceKeyPrefixWith></Redirect></RoutingRule></RoutingRules></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
		{
			name: "routing rule with an empty condition",
			body: `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument>` +
				`<RoutingRules><RoutingRule><Condition></Condition><Redirect><HostName>example.test</HostName></Redirect>` +
				`</RoutingRule></RoutingRules></WebsiteConfiguration>`,
			want: "InvalidArgument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPutWebsiteRejected(t, srv, "web-validation", tt.body, tt.want)
		})
	}
}

func TestPutBucketWebsite_rejectedConfigurationIsNotStored(t *testing.T) {
	// Given: a bucket with a valid configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "web-reject-keeps-previous")
	putWebsiteOK(t, srv, "web-reject-keeps-previous",
		`<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument></WebsiteConfiguration>`)

	// When: an invalid replacement is refused
	assertPutWebsiteRejected(t, srv, "web-reject-keeps-previous",
		`<WebsiteConfiguration><RedirectAllRequestsTo><HostName>a.test</HostName></RedirectAllRequestsTo>`+
			`<IndexDocument><Suffix>home.html</Suffix></IndexDocument></WebsiteConfiguration>`, "InvalidArgument")

	// Then: the previous configuration is untouched
	cfg := getWebsite(t, srv, "web-reject-keeps-previous")
	if cfg.IndexDocument == nil || cfg.IndexDocument.Suffix != "index.html" {
		t.Fatalf("IndexDocument = %+v, want the unchanged index.html", cfg.IndexDocument)
	}
	if cfg.RedirectAllRequestsTo != nil {
		t.Errorf("a refused configuration was partially stored: %+v", cfg.RedirectAllRequestsTo)
	}
}

func TestPutBucketWebsite_noSuchBucket(t *testing.T) {
	// Given: no bucket
	srv := helpers.NewTestServer(t)

	// When: a website configuration is put
	resp := putWebsite(t, srv, "web-missing",
		`<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument></WebsiteConfiguration>`)
	defer resp.Body.Close()

	// Then: the missing bucket outranks the body, as it does on AWS
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}
