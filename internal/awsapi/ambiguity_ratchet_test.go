package awsapi

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The ambiguity ratchet.
//
// A binding several modeled services declare is marked Ambiguous, and the
// registry names no owner for it. That is the honest answer, and the router
// handles it: claimAnswersCaller returns true unconditionally for an ambiguous
// claim, so the request still reaches a 501 rather than the S3 fallback.
//
// Read the scope of this before reading the rest, because it is easy to
// overstate. restFallback is registered after every explicit service route, so
// it sees a request only when no implementation claimed it. An operation
// Overcast implements is answered by its own route, and ambiguity in the models
// cannot reach it — ownership there never came from the manifest. RESTOperation
// also keeps resolving per service through the candidate arrays, so operation
// naming for IAM and logging survives ambiguity intact. **A refresh cannot make
// an implemented operation behave differently by making its binding shared.**
//
// What does change is narrower, and worth recording rather than fixing:
//
//   - For an operation Overcast does *not* implement, claimAnswersCaller stops
//     comparing the caller's credential scope with the binding's signing name,
//     there being no single name left. A caller signed for the wrong service
//     used to receive InvalidSignatureException from writeScopeMismatch and now
//     receives a 501. One error becomes a different error; nothing succeeds
//     that should not.
//   - The drift guards step aside. detectservice_signed_test.go skips a claim
//     the moment it reads Ambiguous, and the router's corpus ownership sweep
//     signs ambiguous operations with the service's own name rather than
//     asserting attribution. So the day one of our bindings becomes shared,
//     those tests quietly cover one binding less and stay green — coverage
//     narrowing with nothing to announce it. That is the part worth a ratchet.
//
// So this is a visibility ratchet, not a regression gate. A refresh diff
// reports "N path bindings changed which services share them" without saying
// whether any of the N were ours, and this ledger is the missing half: every
// operation of an implemented service that today reaches callers only through
// a shared binding. Growth fails here by name, so it is reviewed and recorded
// deliberately rather than absorbed.
//
// Being on the list is not a defect. Almost every entry is a tagging operation
// on a path a dozen services declare, which is AWS's doing and nothing Overcast
// can take back. The list exists so growth is a decision.
//
// The reverse direction matters too: an entry that no longer describes an
// ambiguous binding means upstream disambiguated it, and leaving it recorded
// would let the next real one hide behind a stale name.
var implementedServiceOperationsOnAmbiguousBindings = []string{
	"apigateway/CreateApi",
	"apigateway/DeleteApi",
	"apigateway/GetApi",
	"apigateway/GetApis",
	"apigateway/GetTags",
	"apigateway/TagResource",
	"apigateway/UntagResource",
	"appconfig/CreateApplication",
	"appconfig/DeleteApplication",
	"appconfig/GetAccountSettings",
	"appconfig/GetApplication",
	"appconfig/ListApplications",
	"appconfig/ListTagsForResource",
	"appconfig/TagResource",
	"appconfig/UntagResource",
	"appconfig/UpdateApplication",
	"appconfigdata/GetLatestConfiguration",
	"appregistry/CreateApplication",
	"appregistry/DeleteApplication",
	"appregistry/GetApplication",
	"appregistry/GetConfiguration",
	"appregistry/ListApplications",
	"appregistry/ListTagsForResource",
	"appregistry/TagResource",
	"appregistry/UntagResource",
	"appregistry/UpdateApplication",
	"appsync/CreateApi",
	"appsync/DeleteApi",
	"appsync/GetApi",
	"appsync/ListApis",
	"appsync/ListTagsForResource",
	"appsync/TagResource",
	"appsync/UntagResource",
	"backup/DescribeGlobalSettings",
	"backup/ListTags",
	"backup/TagResource",
	"bedrock/DeleteResourcePolicy",
	"bedrock/GetResourcePolicy",
	"bedrock/ListTagsForResource",
	"bedrock/TagResource",
	"bedrock/UntagResource",
	"eks/ListAccessPolicies",
	"eks/ListClusters",
	"eks/ListTagsForResource",
	"eks/TagResource",
	"eks/UntagResource",
	"lambda/ListTags",
	"lambda/TagResource",
	"lambda/UntagResource",
	"msk/CreateConfiguration",
	"msk/DeleteConfiguration",
	"msk/DescribeConfiguration",
	"msk/DescribeConfigurationRevision",
	"msk/ListConfigurationRevisions",
	"msk/ListConfigurations",
	"msk/ListTagsForResource",
	"msk/TagResource",
	"msk/UntagResource",
	"msk/UpdateConfiguration",
	"pipes/ListTagsForResource",
	"pipes/TagResource",
	"pipes/UntagResource",
	"scheduler/DeleteSchedule",
	"scheduler/GetSchedule",
	"scheduler/ListSchedules",
	"scheduler/ListTagsForResource",
	"scheduler/TagResource",
	"scheduler/UntagResource",
	"scheduler/UpdateSchedule",
}

func TestImplementedServiceOperationsOnAmbiguousBindings_matchTheLedger(t *testing.T) {
	got := implementedOperationsOnAmbiguousBindings(t)

	recorded := make(map[string]bool, len(implementedServiceOperationsOnAmbiguousBindings))
	for _, entry := range implementedServiceOperationsOnAmbiguousBindings {
		recorded[entry] = true
	}

	var added []string
	for _, entry := range got {
		if !recorded[entry] {
			added = append(added, entry)
		}
	}
	if len(added) > 0 {
		t.Errorf("%d operation(s) of implemented services newly reach callers only through a shared binding:\n  %s\n\n"+
			"A model refresh gave another service the same method and URI shape. For each one, check what the\n"+
			"binding now answers — writeScopeMismatch no longer fires for it, so a caller signed for the wrong\n"+
			"service gets a 501 instead of InvalidSignatureException — then add it to\n"+
			"implementedServiceOperationsOnAmbiguousBindings in this file to record the decision.",
			len(added), strings.Join(added, "\n  "))
	}

	present := make(map[string]bool, len(got))
	for _, entry := range got {
		present[entry] = true
	}
	var stale []string
	for _, entry := range implementedServiceOperationsOnAmbiguousBindings {
		if !present[entry] {
			stale = append(stale, entry)
		}
	}
	if len(stale) > 0 {
		t.Errorf("%d ledger entry/entries no longer name an ambiguous binding:\n  %s\n\n"+
			"Upstream disambiguated these, which is good news. Remove them, so the next binding that really\n"+
			"does become shared cannot hide behind a stale name.",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// implementedOperationsOnAmbiguousBindings returns "<service>/<Operation>" for
// every operation of a service Overcast implements that is reachable only
// through a binding the models share between services, sorted.
//
// It reads the candidate arrays rather than the binding's own ModelService,
// which is blank precisely because the binding is ambiguous. RESTOperation
// resolves through the same arrays, so this asks the question the way the
// router answers it.
func implementedOperationsOnAmbiguousBindings(t *testing.T) []string {
	t.Helper()
	implemented := implementedServiceKeys(t)

	seen := map[string]bool{}
	for _, op := range restOperations {
		if !op.Ambiguous {
			continue
		}
		for _, candidate := range restCandidates[op.CandidateStart:op.CandidateEnd] {
			if key := overcastService(candidate.ModelService); implemented[key] {
				seen[key+"/"+candidate.Operation] = true
			}
		}
	}
	entries := make([]string, 0, len(seen))
	for entry := range seen {
		entries = append(entries, entry)
	}
	sort.Strings(entries)
	return entries
}

// implementedServiceKeys reads the service packages rather than taking a list,
// so a service added to the tree is covered the day its package appears.
// "events" and "logs" are subpackages of eventbridge and cloudwatch that carry
// their own Overcast keys.
//
// internal/middleware's implementedServices reads the same directory the same
// way. The twelve lines are duplicated deliberately: the two live in different
// packages, both are test-only, and there is no shared test-support package to
// put them in — one would cost more than it saves, and a directory listing is
// not a behaviour the two could drift on meaningfully.
func implementedServiceKeys(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "services"))
	if err != nil {
		t.Fatalf("reading the service packages: %v", err)
	}
	keys := map[string]bool{"events": true, "logs": true}
	for _, entry := range entries {
		if entry.IsDir() {
			keys[entry.Name()] = true
		}
	}
	if len(keys) < 40 {
		t.Fatalf("found only %d service packages; the walk is looking in the wrong place", len(keys))
	}
	return keys
}
