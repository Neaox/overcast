//go:build dev

package router

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/awsapi"
)

// This file closes one of the three limits docs/plans/manifest-enforcement.md
// states the binding gate cannot reach.
//
// 139 modeled URIs pin a query parameter — /2020-05-31/tagging?Operation=Tag,
// /apikeys?mode=import, /2019-09-30/functions/{n}/provisioned-concurrency?List=ALL.
// chi routes on the path alone, so two operations that differ only in the query
// arrive at one route and something downstream has to branch. The binding gate
// drops the query before comparing (uriSegments), which is why it can say the
// path is served and cannot say the branch exists.
//
// The branch that *can* be proven from the model is the classifier's.
// Registry.ClaimRESTQuery matches the literal query components Smithy declares,
// and its answer is what names the operation for IAM authorisation, for the
// request log, and for the 501 a fallback returns. If it cannot tell
// TagResource from UntagResource, the emulator is authorising and reporting the
// wrong operation whatever the handler then does.
//
// What is still not proven here is the *handler's* branch: that s3's
// GET /{Bucket}?acl runs GetBucketAcl rather than ListObjectsV2. That needs a
// request/response assertion per operation, not a model, and it is why the
// entry below is a recorded carve-out rather than a silent skip.

// queryBranchNotProvable names the services whose query-discriminated bindings
// this gate cannot judge, with the reason.
//
// It is asserted in both directions: a service listed here that has no
// query-pinned declared binding fails, and so does a service with one that is
// not listed and not provable.
var queryBranchNotProvable = map[string]string{
	"s3": "S3 is deliberately absent from the generated REST indexes — it is the wildcard owner every unclaimed path falls to, so classifying it would make the registry claim the whole path space. Its query-discriminated bindings are exercised by the s3 package's own request tests (?versioning, ?list-type=2, ?acl and the rest) rather than from the model.",
}

// TestQueryDiscriminatedBindings_areClaimedDistinctly asserts that every
// declared operation AWS binds behind a query parameter is identified by the
// generated registry from method, path and query together.
func TestQueryDiscriminatedBindings_areClaimedDistinctly(t *testing.T) {
	// Given: the generated registry, and every declared capability whose model
	// pins a query on the URI.
	registry := awsapi.NewRegistry()

	// When: each is looked up at the concrete request it models.
	var misclaimed []string
	carvedOut := map[string]bool{}
	proven := 0
	for _, cap := range dispatchedCapabilities() {
		for _, op := range awsapi.Operations(cap.Service, modeledOperationName(cap)) {
			if !strings.Contains(op.URI, "?") {
				continue
			}
			if _, skipped := queryBranchNotProvable[cap.Service]; skipped {
				carvedOut[cap.Service] = true
				continue
			}
			path, query := concreteRequest(op.URI)
			claim, ok := registry.ClaimRESTQuery(op.HTTPMethod, path, query)
			switch {
			case !ok:
				misclaimed = append(misclaimed, fmt.Sprintf("%s/%s: %s %s?%s is claimed by nothing",
					cap.Service, cap.Operation, op.HTTPMethod, path, query))
			case claim.Service != cap.Service || claim.Operation != op.Name:
				misclaimed = append(misclaimed, fmt.Sprintf("%s/%s: %s %s?%s is claimed as %s/%s",
					cap.Service, cap.Operation, op.HTTPMethod, path, query, claim.Service, claim.Operation))
			default:
				proven++
			}
		}
	}

	// Then: every one resolves to the operation that declares it.
	sort.Strings(misclaimed)
	if len(misclaimed) > 0 {
		t.Errorf("%d query-discriminated binding(s) are not identified from the wire:\n\t%s\n\n"+
			"The query is part of the binding. An operation the registry cannot name is authorised, logged and "+
			"reported as whichever operation shares its path.", len(misclaimed), strings.Join(misclaimed, "\n\t"))
	}
	if proven == 0 {
		t.Error("no query-discriminated binding was proven — the gate is passing without asserting anything")
	}

	// And: the carve-out describes services that really do have such bindings.
	for service, reason := range queryBranchNotProvable {
		if !carvedOut[service] {
			t.Errorf("queryBranchNotProvable names %q, which declares no query-discriminated binding any more — delete the entry\n\trecorded reason: %s",
				service, reason)
		}
	}
}

// TestQueryDiscriminatedBindings_needTheQuery is the half that keeps the gate
// above from passing on the path alone.
//
// A claim that resolves identically with the query removed proves nothing about
// discrimination: the path was unique and the query was decoration. This asserts
// the opposite for every binding the gate certifies — that dropping the query
// changes the answer — which is what makes "claimed distinctly" mean it.
//
// Lambda is the clearest case in the tree. GET on
// /2019-09-30/functions/{name}/provisioned-concurrency is
// GetProvisionedConcurrencyConfig; the same path with ?List=ALL is
// ListProvisionedConcurrencyConfigs. One route, two operations, and only the
// query tells them apart.
func TestQueryDiscriminatedBindings_needTheQuery(t *testing.T) {
	registry := awsapi.NewRegistry()

	var undiscriminated []string
	for _, cap := range dispatchedCapabilities() {
		for _, op := range awsapi.Operations(cap.Service, modeledOperationName(cap)) {
			if !strings.Contains(op.URI, "?") {
				continue
			}
			if _, skipped := queryBranchNotProvable[cap.Service]; skipped {
				continue
			}
			path, query := concreteRequest(op.URI)
			withQuery, ok := registry.ClaimRESTQuery(op.HTTPMethod, path, query)
			if !ok || withQuery.Operation != op.Name {
				// Reported by the sibling test; not this one's finding.
				continue
			}
			if bare, ok := registry.ClaimRESTQuery(op.HTTPMethod, path, ""); ok && bare.Operation == op.Name {
				undiscriminated = append(undiscriminated, fmt.Sprintf("%s/%s: %s %s resolves to the same operation without %q",
					cap.Service, cap.Operation, op.HTTPMethod, path, query))
			}
		}
	}

	sort.Strings(undiscriminated)
	if len(undiscriminated) > 0 {
		t.Errorf("%d binding(s) the model pins a query on are claimed without it:\n\t%s\n\n"+
			"Either the generated index has dropped the query component, or the model no longer pins one. "+
			"Both make the sibling gate's proof empty.", len(undiscriminated), strings.Join(undiscriminated, "\n\t"))
	}
}

// concreteRequest turns a modeled URI template into the path and raw query a
// client would send. Labels become an arbitrary literal, because the registry
// matches them structurally and the value is never read; a greedy label spans
// more than one segment, so it becomes two.
func concreteRequest(uri string) (path, query string) {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri, query = uri[:i], uri[i+1:]
	}
	segments := strings.Split(uri, "/")
	for i, segment := range segments {
		switch {
		case isGreedyLabel(segment):
			segments[i] = "resource/child"
		case isLabel(segment):
			segments[i] = "resource"
		}
	}
	return strings.Join(segments, "/"), query
}
