// cloudfront_account_test.go covers the account ID CloudFront embeds in the
// ARNs it hands back.
//
// This exists because the harness account-ID default (000000000000) is also the
// literal several handlers hardcode, so an assertion built from either side
// agrees with the other whether or not the value was ever read from config. Only
// a server configured with a different account ID can tell them apart.
package cloudfront_test

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// functionSummaryWithARN parses the FunctionMetadata the existing
// parsedFunctionSummary in cloudfront_test.go deliberately ignores.
type functionSummaryWithARN struct {
	XMLName          xml.Name `xml:"FunctionSummary"`
	Name             string   `xml:"Name"`
	FunctionMetadata struct {
		FunctionARN string `xml:"FunctionARN"`
	} `xml:"FunctionMetadata"`
}

// TestCreateFunction_arnUsesConfiguredAccountID asserts the function ARN carries
// the configured account ID.
//
// CreateFunction assigns accountID := "000000000000" as a local literal
// (internal/services/cloudfront/handler_functions.go:45) instead of reading
// h.cfg.AccountID. The same service reads config correctly for distribution ARNs
// (internal/services/cloudfront/handler.go:93 and :643, via
// protocol.DistributionARN(h.cfg.AccountID, id)), so this is an oversight in one
// handler rather than a service-wide convention — a CloudFront function and a
// CloudFront distribution created on the same server report different accounts.
//
// Nothing catches it today on two counts: the harness account ID equals the
// hardcoded literal, and no test parses FunctionMetadata at all.
func TestCreateFunction_arnUsesConfiguredAccountID(t *testing.T) {
	// Given: a server configured with an account ID that is not the default.
	const accountID = "111122223333"
	srv := helpers.NewTestServer(t, helpers.WithAccountID(accountID))

	// When: a CloudFront function is created.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/2020-05-31/function",
		bytes.NewReader([]byte(cfFunctionCreateXML("account-func"))))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)

	var fn functionSummaryWithARN
	if err := xml.Unmarshal(readBody(t, resp), &fn); err != nil {
		t.Fatalf("unmarshal FunctionSummary: %v", err)
	}

	// Then: the ARN names the configured account, not the default one.
	want := "arn:aws:cloudfront::" + accountID + ":function/account-func"
	if fn.FunctionMetadata.FunctionARN != want {
		t.Errorf("FunctionARN = %q, want %q", fn.FunctionMetadata.FunctionARN, want)
	}
	if strings.Contains(fn.FunctionMetadata.FunctionARN, "000000000000") {
		t.Errorf("FunctionARN %q carries the default account ID on a server "+
			"configured with %s — the value is hardcoded, not read from config",
			fn.FunctionMetadata.FunctionARN, accountID)
	}
}
