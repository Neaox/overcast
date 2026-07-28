package router_test

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/tests/helpers"
)

// knownUnownedOperations counts the modeled operations that still reach S3's
// catch-all instead of a protocol-correct 501, grouped by the structural reason
// they cannot be claimed. Each entry is a debt, not a target: the numbers may
// only shrink. Lowering one means deleting or reducing its line here, so a fix
// cannot land silently and a regression cannot hide behind an aggregate.
//
//   - s3-family signing name: S3 Control signs as "s3", indistinguishable from
//     S3 itself by credential scope alone. Needs host-based routing
//     (*.s3-control.*) or the x-amz-account-id header S3 Control requires.
//   - no modeled sigv4 name: CodeCatalyst authenticates with a bearer token and
//     carries no aws.auth#sigv4 trait, so there is no credential scope to match.
//     Needs bearer-token presence accepted as non-S3 evidence.
var knownUnownedOperations = map[string]int{
	"s3-family signing name": 89,
	"no modeled sigv4 name":  28,
}

// TestGeneratedCorpus_noModeledOperationReachesS3 drives every modeled non-S3
// operation through the real router and asserts the response did not come from
// S3's catch-all.
//
// It deliberately exercises the router rather than the registry. The registry
// gate in internal/awsapi proves a claim *exists*; only the router proves the
// claim is *acted on*, because restFallback applies credential-scope rules that
// the registry knows nothing about. Both gates are needed.
//
// Only S3 is enabled, which makes the assertion exact: with no other service
// registered, any response that is not a 501 or a ServiceDisabled 503 can only
// have come from S3.
func TestGeneratedCorpus_noModeledOperationReachesS3(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServices("s3"))
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

	for _, reason := range sortedKeys(unowned) {
		allowed, known := knownUnownedOperations[reason]
		switch {
		case !known:
			t.Errorf("%d modeled operations reach S3 for an unrecognised reason %q:\n      %s",
				unowned[reason], reason, strings.Join(examples[reason], "\n      "))
		case unowned[reason] > allowed:
			t.Errorf("%q now leaks %d operations to S3, was %d — a fallback regressed:\n      %s",
				reason, unowned[reason], allowed, strings.Join(examples[reason], "\n      "))
		case unowned[reason] < allowed:
			t.Errorf("%q leaks %d operations to S3, fewer than the recorded %d. "+
				"Lower knownUnownedOperations[%q] to %d to lock the improvement in.",
				reason, unowned[reason], allowed, reason, unowned[reason])
		}
	}
	for _, reason := range sortedKeys(knownUnownedOperations) {
		if _, seen := unowned[reason]; !seen {
			t.Errorf("%q no longer leaks any operation to S3; remove it from knownUnownedOperations", reason)
		}
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
		// Every AWS SDK, CLI, and CDK request is signed. An ambiguous binding
		// exposes no single modeled signing name, so fall back to the service
		// identity — the scope a caller for that service would actually send.
		scope := claim.SigningName
		if scope == "" {
			scope = op.Service
		}
		req.Header.Set("Authorization", corpusSigV4(scope))
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

func corpusSigV4(service string) string {
	return "AWS4-HMAC-SHA256 Credential=test/20260729/us-east-1/" + service +
		"/aws4_request, SignedHeaders=host, Signature=test"
}

func unownedReason(signingName string) string {
	switch {
	case signingName == "":
		return "no modeled sigv4 name"
	case isS3FamilySigningName(signingName):
		return "s3-family signing name"
	default:
		return "unexpected: signed as " + signingName
	}
}

func isS3FamilySigningName(name string) bool {
	switch strings.ToLower(name) {
	case "s3", "s3-object-lambda", "s3-outposts", "s3express":
		return true
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
