package router_test

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/tests/helpers"
)

// TestGeneratedCorpus_noModeledOperationReachesS3 drives every modeled non-S3
// operation through the real router and asserts the response did not come from
// S3's catch-all.
//
// It deliberately exercises the router rather than the registry. The registry
// gate in internal/awsapi proves a claim *exists*; only the router proves the
// claim is *acted on*, because restFallback applies credential-scope rules that
// the registry knows nothing about. Both gates are needed.
//
// Only S3 is registered, which makes the assertion exact: every other service
// answers 501 on its own path prefixes, so any response that is not a 501 can
// only have come from S3.
func TestGeneratedCorpus_noModeledOperationReachesS3(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServiceSubset("s3"))
	registry := awsapi.NewRegistry()
	client := &http.Client{}

	unowned := map[string]int{}
	examples := map[string][]string{}

	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		if op.Service == "s3" {
			return true
		}
		req, signingName, ok := corpusRequest(t, srv.URL, registry, op)
		if !ok {
			return true
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s/%s: %v", op.Service, op.Name, err)
		}
		// Drain before closing so the transport reuses the connection. A
		// close with unread body tears the connection down, and this loop
		// makes one request per modeled operation — thousands of sockets in
		// TIME_WAIT, which exhausts the Windows ephemeral port range and
		// fails every later dial in the package (and its -p neighbours).
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotImplemented || resp.StatusCode == http.StatusServiceUnavailable {
			return true
		}

		reason := unownedReason(signingName)
		unowned[reason]++
		if len(examples[reason]) < 5 {
			examples[reason] = append(examples[reason],
				fmt.Sprintf("%s/%s %s %s -> %d", op.Service, op.Name, op.HTTPMethod, op.URI, resp.StatusCode))
		}
		return true
	})

	// No modeled non-S3 operation may rely on S3 as its owner. The reason is
	// reported alongside the count so a regression names the evidence rule that
	// stopped holding rather than just the total that moved.
	for _, reason := range sortedKeys(unowned) {
		t.Errorf("%d modeled operations reach S3 instead of a 501 (%s):\n      %s",
			unowned[reason], reason, strings.Join(examples[reason], "\n      "))
	}
}

// corpusRequest builds the smallest request that identifies an operation on the
// wire. REST bindings are signed with their modeled service, which is what an
// SDK, the CLI, and CDK all send.
func corpusRequest(t *testing.T, base string, registry *awsapi.Registry, op awsapi.Operation) (*http.Request, string, bool) {
	t.Helper()
	switch {
	case op.TargetPrefix != "" && (op.Protocol == awsapi.ProtocolAWSJSON10 || op.Protocol == awsapi.ProtocolAWSJSON11):
		req, err := http.NewRequest(http.MethodPost, base+"/", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("X-Amz-Target", op.TargetPrefix+op.Name)
		return req, "", true

	case op.Protocol == awsapi.ProtocolAWSQuery || op.Protocol == awsapi.ProtocolEC2Query:
		body := "Action=" + op.Name + "&Version=" + op.APIVersion
		req, err := http.NewRequest(http.MethodPost, base+"/", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, "", true

	case op.Protocol == awsapi.ProtocolRESTJSON || op.Protocol == awsapi.ProtocolRESTXML:
		if op.HTTPMethod == "" || op.URI == "" {
			return nil, "", false
		}
		path, query := corpusPath(op.URI)
		claim, ok := registry.ClaimRESTQuery(op.HTTPMethod, path, query)
		if !ok {
			t.Errorf("registry does not claim modeled binding %s %s (%s/%s)",
				op.HTTPMethod, op.URI, op.Service, op.Name)
			return nil, "", false
		}
		target := base + path
		if query != "" {
			target += "?" + query
		}
		req, err := http.NewRequest(op.HTTPMethod, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		applyCorpusAuth(req, op, claim)
		// Classification uses the *modeled* signing name, not the scope we had
		// to invent, so a category always names a property of the model.
		return req, claim.SigningName, true
	}
	return nil, "", false
}

// corpusPath turns a Smithy URI template into a concrete routable path.
func corpusPath(uri string) (path, query string) {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri, query = uri[:i], uri[i+1:]
	}
	var b strings.Builder
	for _, segment := range strings.Split(uri, "/") {
		if segment == "" {
			continue
		}
		b.WriteByte('/')
		if strings.HasPrefix(segment, "{") {
			b.WriteString("corpusvalue")
		} else {
			b.WriteString(segment)
		}
	}
	if b.Len() == 0 {
		return "/", query
	}
	return b.String(), query
}

// applyCorpusAuth attaches the credentials a real caller for this operation
// would send, which is what the router's evidence rules are written against.
//
// X-Amz-Account-Id goes on every request rather than only the S3 Control ones.
// S3 Control is the only API the router reads it for, so setting it uniformly
// keeps this test from carrying a second copy of the router's S3-family list
// that could silently drift out of step with it.
func applyCorpusAuth(req *http.Request, op awsapi.Operation, claim awsapi.Claim) {
	req.Header.Set("X-Amz-Account-Id", "000000000000")
	switch {
	case claim.SigningName != "":
		req.Header.Set("Authorization", corpusSigV4(claim.SigningName))
	case claim.Ambiguous:
		// No single modeled name; send the scope a caller for this service
		// would use.
		req.Header.Set("Authorization", corpusSigV4(op.Service))
	default:
		// Modeled with no aws.auth#sigv4 name: a bearer-token API.
		req.Header.Set("Authorization", "Bearer corpus-token")
	}
}

// corpusSigV4 builds a syntactically valid Authorization header with a dummy
// signature. Ownership is decided from the credential scope alone, so no real
// signing is needed to drive these paths — which is what makes a sweep of the
// whole corpus cheap, here and against a running binary. It does depend on
// signature validation being off, as it is by default; a fixture that enables
// cfg.SigV4Validate would need genuinely signed requests.
func corpusSigV4(service string) string {
	return "AWS4-HMAC-SHA256 Credential=test/20260729/us-east-1/" + service +
		"/aws4_request, SignedHeaders=host, Signature=test"
}

// unownedReason names the modeled property that left an operation unowned, so a
// failure points at the evidence rule to look at rather than just a count.
func unownedReason(signingName string) string {
	if signingName == "" {
		return "no modeled sigv4 name"
	}
	return "signing name " + signingName
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
